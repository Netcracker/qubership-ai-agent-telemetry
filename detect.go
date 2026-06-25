package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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

// detect routes a raw hook payload to the per-harness detector. Claude Code
// emits a native Skill-tool event; Codex and Cursor are detected from the
// session transcript.
func detect(agent string, stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error) {
	stdin = bytes.TrimPrefix(stdin, utf8BOM)
	switch agent {
	case "claude":
		return claudeAdapter(stdin, remote, now)
	case "codex":
		return codexTranscriptEventsAuto(stdin, now), nil
	case "cursor":
		return cursorTranscriptEventsAuto(stdin, remote, now), nil
	default:
		return nil, fmt.Errorf("no detector for agent %q", agent)
	}
}

type codexPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

// claudePayload is the Claude Code PreToolUse hook envelope. Only the fields
// the adapter needs are decoded; the rest (permission_mode, effort,
// tool_use_id, transcript_path) are ignored.
type claudePayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Skill string `json:"skill"`
	} `json:"tool_input"`
}

// claudeAdapter turns a Claude Code PreToolUse hook payload into a single
// SkillEvent. The hook is registered on the Skill tool, so it fires once per
// skill activation; tool_input.skill is the skill name (namespace-prefixed for
// plugin skills, bare for project skills).
func claudeAdapter(stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error) {
	var p claudePayload
	if len(stdin) > 0 {
		// Malformed JSON yields no events rather than an error: a broken turn
		// must never fail the hook.
		_ = json.Unmarshal(stdin, &p)
	}
	// Defensive: the matcher already scopes the hook to the Skill tool, but
	// only emit when the payload confirms it and names a skill.
	if p.ToolName != "Skill" || p.ToolInput.Skill == "" {
		return nil, nil
	}
	// p.Cwd resolves the git remote only; the local path never leaves the
	// process, since it leaks the user's home directory and username.
	var rem string
	if remote != nil && p.Cwd != "" {
		rem = remote(p.Cwd)
	}
	return []SkillEvent{{
		Agent:      "claude",
		SessionID:  p.SessionID,
		RepoRemote: rem,
		Skill:      p.ToolInput.Skill,
		TS:         now,
	}}, nil
}

// cursorPayload is the Cursor afterAgentResponse hook envelope. Only the fields
// the adapter needs are decoded; the rest (conversation_id, generation_id,
// model, token counts, cursor_version, user_email) are ignored. user_email is
// deliberately not collected: it is PII, and the project drops repo.path and
// turn.id for the same reason.
type cursorPayload struct {
	SessionID      string   `json:"session_id"`
	WorkspaceRoots []string `json:"workspace_roots"`
	TranscriptPath string   `json:"transcript_path"`
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

// skillPathRe matches the tail of a path to a skill body: skills/<name>/SKILL.md.
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
var skillPathRe = regexp.MustCompile(`(?i)(?:^|[\s"'=/\\])skills[\\/]+([^\\/\s"']+)[\\/]+SKILL\.md`)

// skillNameInPath returns the skill name carried by a single filesystem path, or
// ("", false) when the path is not a skill body. Use it for a clean path such as
// a Cursor Read tool's input.path.
func skillNameInPath(s string) (string, bool) {
	if m := skillPathRe.FindStringSubmatch(s); m != nil {
		return m[1], true
	}
	return "", false
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
		names = append(names, m[1])
	}
	return names
}
