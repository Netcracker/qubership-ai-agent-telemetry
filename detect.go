package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// sanitizeRemote strips the userinfo component (username, password, token)
// from a git remote URL to prevent PII and credential leakage.
func sanitizeRemote(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// utf8BOM is the UTF-8 byte-order mark. PowerShell 5.1 prepends it when it pipes
// a string to a native command's stdin, so a hook payload arriving through a
// PowerShell-piped shell (e.g. Cursor on Windows: `Get-Content tmp | cmd`) is
// preceded by these bytes. They are not valid JSON, so json.Unmarshal fails and
// no skill is detected. Strip a single leading BOM before routing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// remoteResolver returns the git remote URL for a working dir, or "" if unknown.
// Injected so detectors stay pure and testable.
type remoteResolver func(cwd string) string

// detect routes a raw hook payload to the per-harness adapter.
func detect(agent string, stdin []byte, remote remoteResolver, now time.Time) ([]TelemetryEvent, error) {
	stdin = bytes.TrimPrefix(stdin, utf8BOM)
	switch agent {
	case "claude":
		return claudeAdapter(stdin, remote, now)
	case "codex":
		return codexAdapter(stdin, now)
	case "cursor":
		return cursorAdapter(stdin, remote, now)
	default:
		return nil, fmt.Errorf("no detector for agent %q", agent)
	}
}

type codexPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

type codexHookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
}

// codexMCPPayload excludes tool_response because MCP results are unbounded
// private content and must never enter telemetry.
type codexMCPPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
}

func codexAdapter(stdin []byte, now time.Time) ([]TelemetryEvent, error) {
	var envelope codexHookEnvelope
	if len(stdin) == 0 || json.Unmarshal(stdin, &envelope) != nil {
		return nil, nil
	}
	switch envelope.HookEventName {
	case "Stop":
		return codexTranscriptEventsAuto(stdin, now), nil
	case "PostToolUse":
		// Continue below.
	default:
		return nil, nil
	}

	var p codexMCPPayload
	if json.Unmarshal(stdin, &p) != nil {
		return nil, nil
	}
	server, tool, ok := normalizeMCPToolName(p.ToolName)
	if !ok {
		return nil, nil
	}
	ev, err := newMCPEvent("codex", p.SessionID, "", p.Cwd, MCPPayload{
		ServerName: server,
		ToolName:   tool,
		Outcome:    mcpUnknown,
	}, now)
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{ev}, nil
}

type claudeHookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
}

// claudeSkillPayload is the allowlist for Claude Code PreToolUse hooks. Fields
// such as permission_mode, effort, tool_use_id, and transcript_path are never
// decoded.
type claudeSkillPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
}

// claudeCommandPayload excludes command_args and prompt because both may
// contain source code, credentials, or other private user content.
type claudeCommandPayload struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	CommandName   string `json:"command_name"`
	CommandSource string `json:"command_source"`
	ExpansionType string `json:"expansion_type"`
}

// claudeMCPPayload excludes tool_input, tool_response, and error. MCP payloads
// and results are unbounded private content and must never enter telemetry.
type claudeMCPPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	Duration  *int64 `json:"duration_ms"`
}

// claudeAdapter routes Claude Code lifecycle hooks to their normalized
// telemetry events. Malformed or invalid payloads yield no events so a broken
// turn never fails the hook.
func claudeAdapter(stdin []byte, remote remoteResolver, now time.Time) ([]TelemetryEvent, error) {
	var envelope claudeHookEnvelope
	if len(stdin) == 0 || json.Unmarshal(stdin, &envelope) != nil {
		return nil, nil
	}

	switch envelope.HookEventName {
	case "":
		// Older hook registrations predate hook_event_name. Preserve their
		// PreToolUse Skill payloads while refusing to infer any other event.
		if envelope.ToolName != "Skill" {
			return nil, nil
		}
		return claudeSkillEvent(stdin, remote, now)
	case "PreToolUse":
		return claudeSkillEvent(stdin, remote, now)
	case "UserPromptExpansion":
		return claudeCommandEvent(stdin, remote, now)
	case "PostToolUse":
		return claudeMCPEvent(stdin, remote, now, mcpSucceeded)
	case "PostToolUseFailure":
		return claudeMCPEvent(stdin, remote, now, mcpFailed)
	default:
		return nil, nil
	}
}

func claudeSkillEvent(stdin []byte, remote remoteResolver, now time.Time) ([]TelemetryEvent, error) {
	var p claudeSkillPayload
	if json.Unmarshal(stdin, &p) != nil {
		return nil, nil
	}
	if p.ToolName != "Skill" || p.ToolInput.Skill == "" {
		return nil, nil
	}
	ev, err := newSkillEvent("claude", p.SessionID, resolveRemote(remote, p.Cwd), p.Cwd, p.ToolInput.Skill, now)
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{ev}, nil
}

func claudeCommandEvent(stdin []byte, remote remoteResolver, now time.Time) ([]TelemetryEvent, error) {
	var p claudeCommandPayload
	if json.Unmarshal(stdin, &p) != nil {
		return nil, nil
	}
	ev, err := newCommandEvent("claude", p.SessionID, resolveRemote(remote, p.Cwd), p.Cwd, CommandPayload{
		CommandName:   p.CommandName,
		CommandSource: p.CommandSource,
		ExpansionType: p.ExpansionType,
	}, now)
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{ev}, nil
}

func claudeMCPEvent(
	stdin []byte, remote remoteResolver, now time.Time, outcome MCPOutcome,
) ([]TelemetryEvent, error) {
	var p claudeMCPPayload
	if json.Unmarshal(stdin, &p) != nil {
		return nil, nil
	}
	server, tool, ok := normalizeMCPToolName(p.ToolName)
	if !ok {
		return nil, nil
	}
	if p.Duration != nil && *p.Duration < 0 {
		p.Duration = nil
	}
	ev, err := newMCPEvent("claude", p.SessionID, resolveRemote(remote, p.Cwd), p.Cwd, MCPPayload{
		ServerName: server,
		ToolName:   tool,
		Outcome:    outcome,
		DurationMS: p.Duration,
	}, now)
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{ev}, nil
}

func normalizeMCPToolName(name string) (server, tool string, ok bool) {
	const prefix = "mcp__"
	if !strings.HasPrefix(name, prefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(name, prefix)
	separator := strings.Index(remainder, "__")
	if separator < 0 {
		return "", "", false
	}
	server, tool = remainder[:separator], remainder[separator+2:]
	if !validIdentifier(server, mcpIdentifier) || !validIdentifier(tool, mcpIdentifier) {
		return "", "", false
	}
	return server, tool, true
}

// resolveRemote uses the local working directory only to discover repository
// identity. RepoDir is retained in memory for policy checks but is never
// serialized.
func resolveRemote(remote remoteResolver, cwd string) string {
	if remote == nil || cwd == "" {
		return ""
	}
	return remote(cwd)
}

// cursorPayload is the Cursor afterAgentResponse hook envelope. Only the fields
// the adapter needs are decoded; the rest (conversation_id, generation_id,
// model, token counts, cursor_version, user_email) are ignored. user_email is
// deliberately not collected: it is PII, and the project drops repo.path and
// turn.id for the same reason.
type cursorPayload struct {
	SessionID           string   `json:"session_id"`
	WorkspaceRoots      []string `json:"workspace_roots"`
	TranscriptPath      string   `json:"transcript_path"`
	AgentTranscriptPath string   `json:"agent_transcript_path"`
}

type cursorHookEnvelope struct {
	HookEventName string `json:"hook_event_name"`
}

// cursorMCPPayload excludes result_json because MCP results are unbounded
// private content and must never enter telemetry.
type cursorMCPPayload struct {
	SessionID      string   `json:"session_id"`
	WorkspaceRoots []string `json:"workspace_roots"`
	ToolName       string   `json:"tool_name"`
	Duration       *int64   `json:"duration"`
}

func cursorAdapter(
	stdin []byte, remote remoteResolver, now time.Time,
) ([]TelemetryEvent, error) {
	var envelope cursorHookEnvelope
	if len(stdin) == 0 || json.Unmarshal(stdin, &envelope) != nil {
		return nil, nil
	}
	switch envelope.HookEventName {
	case "afterAgentResponse":
		return cursorTranscriptEventsAuto(stdin, remote, now), nil
	case "subagentStop":
		var p cursorPayload
		if json.Unmarshal(stdin, &p) != nil || p.AgentTranscriptPath == "" || !cursorHasWorkspaceRoot(p.WorkspaceRoots) {
			return nil, nil
		}
		p.TranscriptPath = p.AgentTranscriptPath
		normalized, err := json.Marshal(p)
		if err != nil {
			return nil, nil
		}
		return cursorTranscriptEventsAuto(normalized, remote, now), nil
	case "afterMCPExecution":
		// Continue below.
	default:
		return nil, nil
	}

	var p cursorMCPPayload
	if json.Unmarshal(stdin, &p) != nil || !validIdentifier(p.ToolName, mcpIdentifier) {
		return nil, nil
	}
	if p.Duration != nil && *p.Duration < 0 {
		p.Duration = nil
	}
	var repoDir string
	if len(p.WorkspaceRoots) > 0 {
		repoDir = p.WorkspaceRoots[0]
	}
	ev, err := newMCPEvent("cursor", p.SessionID, cursorRemote(cursorPayload{
		WorkspaceRoots: p.WorkspaceRoots,
	}, remote), repoDir, MCPPayload{
		ToolName:   p.ToolName,
		Outcome:    mcpUnknown,
		DurationMS: p.Duration,
	}, now)
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{ev}, nil
}

func cursorHasWorkspaceRoot(roots []string) bool {
	for _, root := range roots {
		if strings.TrimSpace(root) != "" {
			return true
		}
	}
	return false
}

// cursorRemote resolves the git remote from the first workspace root. Cursor
// gives no git data in the transcript, so the remote always comes from the hook
// payload.
func cursorRemote(p cursorPayload, remote remoteResolver) string {
	if remote == nil || len(p.WorkspaceRoots) == 0 || p.WorkspaceRoots[0] == "" {
		return ""
	}
	return remote(p.WorkspaceRoots[0])
}

// skillPathRe matches a skill body below a skills directory. Organization and
// implementation directories may appear between skills and the skill itself;
// the final directory before SKILL.md is the skill name.
// It is the single source of truth shared by every transcript-scraped harness
// (Codex, Cursor); Claude gets a structured skill name and never parses a path.
//
// No location anchor. The tail is matched under any parent, because global and
// plugin skills live outside the project under arbitrary parents — e.g.
// ~/.claude/plugins/cache/<plugin>/<version>/skills/<name>/SKILL.md, where the
// segment before `skills` is a version number, not a dot-config dir. Any
// folder-based anchor would miss them.
//
// Separators repeat ([\\/]+) so the same pattern matches `/` (Unix), a single
// `\` (a Windows path in Cursor's input.path after JSON decode), and a doubled
// `\\` (a Windows path embedded in a JS string literal inside Codex's
// custom_tool_call input, where each backslash arrives doubled).
//
// The boundary before `skills` ((?:^|[\s"'=/\\])) requires a separator, quote,
// `=`, whitespace, or start-of-string, so `my-skills/...` does not match while a
// clean path and a path embedded in a shell command both do.
//
// (?i) lets the structural literals `skills` and `SKILL.md` match in any case,
// for the case-insensitive filesystems on Windows (NTFS) and macOS (APFS). The
// capture group still preserves the skill name's original case, since (?i)
// affects matching, not the captured substring.
var skillPathRe = regexp.MustCompile(`(?i)(?:^|[\s"'=/\\])skills[\\/]+(?:[^\\/\s"']+[\\/]+)*([a-z0-9][a-z0-9-]{0,63})[\\/]+SKILL\.md`)

// skillNameInPath returns the skill name carried by a single filesystem path, or
// ("", false) when the path is not a skill body. Use it for a clean path such as
// a Cursor Read tool's input.path.
func skillNameInPath(s string) (string, bool) {
	if m := skillPathRe.FindStringSubmatch(s); m != nil && validDetectedSkillName(m[1]) {
		return m[1], true
	}
	return "", false
}

// validDetectedSkillName completes the constraints that are awkward to express
// in Go's RE2 syntax. Uppercase remains accepted for compatibility with the
// case-insensitive path matching used by earlier releases.
func validDetectedSkillName(name string) bool {
	if name == "" || len(name) > 64 || name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// skillNamesInText returns every skill name matched in a free-text string, in
// order, with duplicates kept. Use it for text that may embed several paths,
// such as a Codex shell command. It returns nil when there are no matches.
func skillNamesInText(s string) []string {
	matches := skillPathRe.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if validDetectedSkillName(m[1]) {
			names = append(names, m[1])
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}
