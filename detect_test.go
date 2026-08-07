package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDetectClaudeParsesSkillTool(t *testing.T) {
	fixtures := []string{
		`{"session_id":"6a35f862","cwd":"/repo","tool_name":"Skill","tool_input":{"skill":"superpowers:brainstorming"}}`,
		`{"hook_event_name":"PreToolUse","session_id":"6a35f862","cwd":"/repo","tool_name":"Skill","tool_input":{"skill":"superpowers:brainstorming"}}`,
	}
	for _, fixture := range fixtures {
		events, err := detect("claude", []byte(fixture), func(cwd string) string {
			if cwd != "/repo" {
				t.Fatalf("resolver got cwd %q", cwd)
			}
			return "git@host:org/repo.git"
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("got %d events, want 1", len(events))
		}
		e := events[0]
		if e.Agent != "claude" || e.SessionID != "6a35f862" || skillName(t, e) != "superpowers:brainstorming" {
			t.Fatalf("event = %+v", e)
		}
		if e.RepoRemote != "git@host:org/repo.git" {
			t.Fatalf("remote = %q", e.RepoRemote)
		}
	}
}

func TestDetectClaudeCommand(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	stdin := []byte(`{"hook_event_name":"UserPromptExpansion","session_id":"s1","cwd":"/repo","command_name":"review-pr","command_source":"plugin","expansion_type":"slash_command","command_args":"secret","prompt":"private"}`)
	events, err := detect("claude", stdin, func(cwd string) string {
		if cwd != "/repo" {
			t.Fatalf("resolver got cwd %q", cwd)
		}
		return ""
	}, now)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	want := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventCommandInvoked,
		Agent:         "claude",
		SessionID:     "s1",
		RepoDir:       "/repo",
		TS:            now,
		Payload: CommandPayload{
			CommandName:   "review-pr",
			CommandSource: "plugin",
			ExpansionType: "slash_command",
		},
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("event = %#v, want %#v", events[0], want)
	}
}

func TestDetectClaudeMCP(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name     string
		stdin    string
		outcome  MCPOutcome
		tool     string
		duration *int64
	}{
		{
			name:     "success",
			stdin:    `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","duration_ms":42,"tool_input":{"token":"secret"},"tool_response":{"email":"person@example.com"}}`,
			outcome:  mcpSucceeded,
			tool:     "get_issue",
			duration: durationPtr(42),
		},
		{
			name:     "failure",
			stdin:    `{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","duration_ms":17,"tool_input":{"token":"secret"},"error":"private failure"}`,
			outcome:  mcpFailed,
			tool:     "get_issue",
			duration: durationPtr(17),
		},
		{
			name:     "tool keeps separator after server",
			stdin:    `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__issues__get","duration_ms":1}`,
			outcome:  mcpSucceeded,
			tool:     "issues__get",
			duration: durationPtr(1),
		},
		{
			name:     "negative duration omitted",
			stdin:    `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","duration_ms":-1}`,
			outcome:  mcpSucceeded,
			tool:     "get_issue",
			duration: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := detect("claude", []byte(tt.stdin), func(string) string { return "" }, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1", len(events))
			}
			payload, ok := events[0].Payload.(MCPPayload)
			if !ok {
				t.Fatalf("payload = %T, want MCPPayload", events[0].Payload)
			}
			if payload.ServerName != "github" || payload.ToolName != tt.tool ||
				payload.Outcome != tt.outcome || !reflect.DeepEqual(payload.DurationMS, tt.duration) {
				t.Fatalf("payload = %#v", payload)
			}
		})
	}
}

func TestDetectClaudePrivacy(t *testing.T) {
	fixtures := []string{
		`{"hook_event_name":"UserPromptExpansion","session_id":"s1","cwd":"/repo","command_name":"review-pr","command_source":"plugin","expansion_type":"slash_command","command_args":"secret-command-args","prompt":"private-prompt"}`,
		`{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","duration_ms":42,"tool_input":{"token":"secret-token"},"tool_response":{"email":"person@example.com"}}`,
		`{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","error":"private-error"}`,
	}
	for _, fixture := range fixtures {
		events, err := detect("claude", []byte(fixture), func(string) string { return "" }, time.Now().UTC())
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("got %d events, want 1", len(events))
		}
		encoded, err := json.Marshal(events[0])
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		for _, forbidden := range []string{
			"secret-command-args", "private-prompt", "secret-token",
			"person@example.com", "private-error", "mcp__github__get_issue",
		} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("serialized event retained %q: %s", forbidden, encoded)
			}
		}
	}
}

func TestDetectClaudeRejects(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
	}{
		{"built-in tool", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash"}`},
		{"missing MCP server", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp____get_issue"}`},
		{"missing MCP tool", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__github__"}`},
		{"missing MCP separator", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__github"}`},
		{"invalid MCP server", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__git hub__get_issue"}`},
		{"invalid MCP tool", `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"mcp__github__get/issue"}`},
		{"unsupported expansion", `{"hook_event_name":"UserPromptExpansion","session_id":"s1","command_name":"review-pr","command_source":"plugin","expansion_type":"other"}`},
		{"invalid command name", `{"hook_event_name":"UserPromptExpansion","session_id":"s1","command_name":"review pr","command_source":"plugin","expansion_type":"slash_command"}`},
		{"invalid command source", `{"hook_event_name":"UserPromptExpansion","session_id":"s1","command_name":"review-pr","command_source":"plugin:source","expansion_type":"slash_command"}`},
		{"invalid session", `{"hook_event_name":"UserPromptExpansion","session_id":"bad session","command_name":"review-pr","command_source":"plugin","expansion_type":"slash_command"}`},
		{"unsupported hook", `{"hook_event_name":"Notification","session_id":"s1","tool_name":"mcp__github__get_issue"}`},
		{"unsupported hook with skill", `{"hook_event_name":"Notification","session_id":"s1","tool_name":"Skill","tool_input":{"skill":"demo"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := detect("claude", []byte(tt.stdin), func(string) string { return "" }, time.Now().UTC())
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("got %d events (%#v), want 0", len(events), events)
			}
		})
	}
}

func durationPtr(duration int64) *int64 {
	return &duration
}

func TestDetectStripsLeadingUTF8BOM(t *testing.T) {
	// PowerShell 5.1 prepends a UTF-8 BOM when piping stdin to a native command
	// (Cursor on Windows). The payload must still parse.
	stdin := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"session_id":"s","cwd":"/repo","tool_name":"Skill","tool_input":{"skill":"demo"}}`)...)
	events, err := detect("claude", stdin, func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || skillName(t, events[0]) != "demo" {
		t.Fatalf("got %d events (%+v), want 1 for demo", len(events), events)
	}
}

func TestDetectClaudeIgnoresOtherTools(t *testing.T) {
	events, err := detect("claude", []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestDetectClaudeMalformedJSON(t *testing.T) {
	events, err := detect("claude", []byte(`{not json`), func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestDetectUnknownAgent(t *testing.T) {
	if _, err := detect("nope", []byte(`{}`), func(string) string { return "" }, time.Now().UTC()); err == nil {
		t.Fatal("want error for unknown agent")
	}
}

func TestDetectCodexMCP(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	stdin := []byte(`{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","tool_response":{"token":"secret"}}`)
	events, err := detect("codex", stdin, func(string) string { return "" }, now)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	want := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventMCPExecuted,
		Agent:         "codex",
		SessionID:     "s1",
		RepoDir:       "/repo",
		TS:            now,
		Payload: MCPPayload{
			ServerName: "github",
			ToolName:   "get_issue",
			Outcome:    mcpUnknown,
		},
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("event = %#v, want %#v", events[0], want)
	}
	encoded, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "mcp__github__get_issue") {
		t.Fatalf("serialized event retained private MCP content: %s", encoded)
	}

	for _, toolName := range []string{"get_issue", "mcp____get_issue", "mcp__github__", "mcp__git hub__get_issue"} {
		t.Run("reject "+toolName, func(t *testing.T) {
			payload := `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":` +
				strconv.Quote(toolName) + `}`
			got, err := detect("codex", []byte(payload), nil, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("got %d events (%#v), want 0", len(got), got)
			}
		})
	}
}

func TestDetectCursorMCP(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name     string
		toolName string
		duration int64
		want     *int64
	}{
		{name: "duration", toolName: "get_issue", duration: 42, want: durationPtr(42)},
		{name: "negative duration omitted", toolName: "get_issue", duration: -1},
		{name: "invalid tool rejected", toolName: "get issue", duration: 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := []byte(fmt.Sprintf(
				`{"hook_event_name":"afterMCPExecution","session_id":"s1","workspace_roots":["/repo"],"tool_name":%q,"duration":%d,"result_json":{"email":"person@example.com"}}`,
				tt.toolName, tt.duration,
			))
			events, err := detect("cursor", stdin, func(cwd string) string {
				if cwd != "/repo" {
					t.Fatalf("resolver got cwd %q", cwd)
				}
				return ""
			}, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if tt.toolName == "get issue" {
				if len(events) != 0 {
					t.Fatalf("got %d events (%#v), want 0", len(events), events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1", len(events))
			}
			payload, ok := events[0].Payload.(MCPPayload)
			if !ok {
				t.Fatalf("payload = %T, want MCPPayload", events[0].Payload)
			}
			if payload.ServerName != "" || payload.ToolName != "get_issue" ||
				payload.Outcome != mcpUnknown || !reflect.DeepEqual(payload.DurationMS, tt.want) {
				t.Fatalf("payload = %#v", payload)
			}
			encoded, err := json.Marshal(events[0])
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if strings.Contains(string(encoded), "person@example.com") {
				t.Fatalf("serialized event retained private MCP content: %s", encoded)
			}
		})
	}
}

func TestDetectClineSkill(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name       string
		hookName   string
		toolField  string
		toolName   string
		parameters map[string]any
		success    bool
		wantSkill  string
	}{
		{
			name:       "VS Code use_skill",
			hookName:   "PostToolUse",
			toolName:   "use_skill",
			parameters: map[string]any{"skill_name": "cline-hook-probe"},
			success:    true,
			wantSkill:  "cline-hook-probe",
		},
		{
			name:       "tool field compatibility",
			hookName:   "PostToolUse",
			toolField:  "tool",
			toolName:   "use_skill",
			parameters: map[string]any{"skill_name": "cline-hook-probe"},
			success:    true,
			wantSkill:  "cline-hook-probe",
		},
		{
			name:       "CLI skills",
			hookName:   "tool_result",
			toolName:   "skills",
			parameters: map[string]any{"skill": "cline-hook-probe"},
			success:    true,
			wantSkill:  "cline-hook-probe",
		},
		{
			name:       "camel case compatibility",
			hookName:   "PostToolUse",
			toolName:   "skills",
			parameters: map[string]any{"skillName": "cline-hook-probe"},
			success:    true,
			wantSkill:  "cline-hook-probe",
		},
		{name: "unsuccessful", hookName: "PostToolUse", toolName: "use_skill", parameters: map[string]any{"skill_name": "cline-hook-probe"}},
		{name: "unrelated hook", hookName: "PreToolUse", toolName: "use_skill", parameters: map[string]any{"skill_name": "cline-hook-probe"}, success: true},
		{name: "unrelated tool", hookName: "PostToolUse", toolName: "read_file", parameters: map[string]any{"skill_name": "cline-hook-probe"}, success: true},
		{name: "missing skill", hookName: "PostToolUse", toolName: "use_skill", parameters: map[string]any{}, success: true},
		{name: "invalid skill", hookName: "PostToolUse", toolName: "use_skill", parameters: map[string]any{"skill_name": "not a skill"}, success: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolField := tt.toolField
			if toolField == "" {
				toolField = "toolName"
			}
			stdin, err := json.Marshal(map[string]any{
				"hookName":       tt.hookName,
				"taskId":         "cline-session-1",
				"workspaceRoots": []string{"", "/repo"},
				"postToolUse": map[string]any{
					toolField:    tt.toolName,
					"parameters": tt.parameters,
					"success":    tt.success,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resolverCalls := 0
			events, err := detect("cline", stdin, func(cwd string) string {
				resolverCalls++
				if cwd != "/repo" {
					t.Fatalf("resolver got cwd %q, want /repo", cwd)
				}
				return "git@github.com:Netcracker/project.git"
			}, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if tt.wantSkill == "" {
				if len(events) != 0 || resolverCalls != 0 {
					t.Fatalf("events = %#v, resolver calls = %d; want no event and no repository lookup", events, resolverCalls)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("events = %#v, want one", events)
			}
			event := events[0]
			if event.Agent != "cline" || event.SessionID != "cline-session-1" || event.RepoDir != "/repo" ||
				event.RepoRemote != "git@github.com:Netcracker/project.git" || skillName(t, event) != tt.wantSkill {
				t.Fatalf("event = %#v", event)
			}
			if resolverCalls != 1 {
				t.Fatalf("resolver calls = %d, want 1", resolverCalls)
			}
		})
	}
}

func TestDetectClineMCP(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name          string
		hookName      string
		toolField     string
		toolName      string
		parameters    map[string]any
		success       any
		durationField string
		duration      any
		wantOutcome   MCPOutcome
		wantDuration  *int64
		wantServer    string
		wantTool      string
	}{
		{
			name:          "classic success with execution time",
			hookName:      "PostToolUse",
			toolName:      "use_mcp_tool",
			parameters:    map[string]any{"server_name": "github", "tool_name": "get_issue"},
			success:       true,
			durationField: "executionTimeMs",
			duration:      42,
			wantOutcome:   mcpSucceeded,
			wantDuration:  durationPtr(42),
		},
		{
			name:          "CLI failure with duration alias",
			hookName:      "tool_result",
			toolField:     "tool",
			toolName:      "use_mcp_tool",
			parameters:    map[string]any{"server_name": "github", "tool_name": "get_issue"},
			success:       false,
			durationField: "durationMs",
			duration:      17,
			wantOutcome:   mcpFailed,
			wantDuration:  durationPtr(17),
		},
		{
			name:        "direct SDK success",
			hookName:    "PostToolUse",
			toolName:    "github__get_issue",
			success:     true,
			wantOutcome: mcpSucceeded,
		},
		{
			name:        "direct SDK failure",
			hookName:    "PostToolUse",
			toolName:    "github__get_issue",
			success:     false,
			wantOutcome: mcpFailed,
		},
		{
			name:          "zero duration retained",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "executionTimeMs",
			duration:      0,
			wantOutcome:   mcpSucceeded,
			wantDuration:  durationPtr(0),
		},
		{
			name:          "negative duration omitted",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "executionTimeMs",
			duration:      -1,
			wantOutcome:   mcpSucceeded,
		},
		{
			name:          "null execution time omitted",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "executionTimeMs",
			duration:      nil,
			wantOutcome:   mcpSucceeded,
		},
		{
			name:          "null duration alias omitted",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "durationMs",
			duration:      nil,
			wantOutcome:   mcpSucceeded,
		},
		{
			name:          "fractional duration omitted",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "executionTimeMs",
			duration:      1.5,
			wantOutcome:   mcpSucceeded,
		},
		{
			name:          "overflowing duration omitted",
			hookName:      "PostToolUse",
			toolName:      "github__get_issue",
			success:       true,
			durationField: "executionTimeMs",
			duration:      json.Number("9223372036854775808"),
			wantOutcome:   mcpSucceeded,
		},
		{name: "missing success", hookName: "PostToolUse", toolName: "github__get_issue"},
		{name: "non-boolean success", hookName: "PostToolUse", toolName: "github__get_issue", success: "true"},
		{
			name:       "missing classic server",
			hookName:   "PostToolUse",
			toolName:   "use_mcp_tool",
			parameters: map[string]any{"tool_name": "get_issue"},
			success:    true,
		},
		{
			name:       "invalid classic tool",
			hookName:   "PostToolUse",
			toolName:   "use_mcp_tool",
			parameters: map[string]any{"server_name": "github", "tool_name": "get issue"},
			success:    true,
		},
		{name: "direct name without separator", hookName: "PostToolUse", toolName: "get_issue", success: true},
		{name: "direct name with extra separator", hookName: "PostToolUse", toolName: "git__hub__get_issue", success: true},
		{
			name:        "direct name at transform limit",
			hookName:    "PostToolUse",
			toolName:    strings.Repeat("a", 53) + "__get_issue",
			success:     true,
			wantOutcome: mcpSucceeded,
			wantServer:  strings.Repeat("a", 53),
			wantTool:    "get_issue",
		},
		{
			name:     "direct name over transform limit",
			hookName: "PostToolUse",
			toolName: strings.Repeat("a", 54) + "__get_issue",
			success:  true,
		},
		{
			name:     "direct name with transform suffix",
			hookName: "PostToolUse",
			toolName: "github_0123abcd__get_issue",
			success:  true,
		},
		{name: "unrelated tool", hookName: "PostToolUse", toolName: "read_file", success: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolField := tt.toolField
			if toolField == "" {
				toolField = "toolName"
			}
			postToolUse := map[string]any{toolField: tt.toolName}
			if tt.parameters != nil {
				postToolUse["parameters"] = tt.parameters
			}
			if tt.success != nil {
				postToolUse["success"] = tt.success
			}
			if tt.durationField != "" {
				postToolUse[tt.durationField] = tt.duration
			}
			stdin, err := json.Marshal(map[string]any{
				"hookName":       tt.hookName,
				"taskId":         "cline-session-1",
				"workspaceRoots": []string{"/repo"},
				"postToolUse":    postToolUse,
			})
			if err != nil {
				t.Fatal(err)
			}
			resolverCalls := 0
			events, err := detect("cline", stdin, func(cwd string) string {
				resolverCalls++
				if cwd != "/repo" {
					t.Fatalf("resolver got cwd %q, want /repo", cwd)
				}
				return "git@github.com:Netcracker/project.git"
			}, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if tt.wantOutcome == "" {
				if len(events) != 0 || resolverCalls != 0 {
					t.Fatalf("events = %#v, resolver calls = %d; want no event and no lookup", events, resolverCalls)
				}
				return
			}
			if len(events) != 1 || resolverCalls != 1 {
				t.Fatalf("events = %#v, resolver calls = %d; want one event and one lookup", events, resolverCalls)
			}
			event := events[0]
			payload, ok := event.Payload.(MCPPayload)
			if !ok {
				t.Fatalf("payload = %T, want MCPPayload", event.Payload)
			}
			wantServer := firstNonEmpty(tt.wantServer, "github")
			wantTool := firstNonEmpty(tt.wantTool, "get_issue")
			if event.Agent != "cline" || event.SessionID != "cline-session-1" || event.RepoDir != "/repo" ||
				event.RepoRemote != "git@github.com:Netcracker/project.git" || payload.ServerName != wantServer ||
				payload.ToolName != wantTool || payload.Outcome != tt.wantOutcome ||
				!reflect.DeepEqual(payload.DurationMS, tt.wantDuration) {
				t.Fatalf("event = %#v", event)
			}
		})
	}
}

func TestDetectClineRejectsMalformedInput(t *testing.T) {
	for _, input := range [][]byte{nil, []byte("{not json"), []byte(`[]`)} {
		events, err := detect("cline", input, func(string) string {
			t.Fatal("repository resolver called for malformed input")
			return ""
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("events = %#v, want none", events)
		}
	}
}

func TestDetectClineMultiRootAttribution(t *testing.T) {
	tests := []struct {
		name    string
		remotes map[string]string
		want    bool
	}{
		{
			name: "same normalized repository",
			remotes: map[string]string{
				"/repo-a": "git@github.com:Netcracker/project.git",
				"/repo-b": "https://github.com/Netcracker/project.git",
			},
			want: true,
		},
		{
			name: "ambiguous repositories",
			remotes: map[string]string{
				"/repo-a": "git@github.com:Netcracker/project-a.git",
				"/repo-b": "git@github.com:Netcracker/project-b.git",
			},
		},
		{
			name: "one unresolved repository",
			remotes: map[string]string{
				"/repo-a": "git@github.com:Netcracker/project.git",
				"/repo-b": "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := []byte(`{"hookName":"PostToolUse","taskId":"cline-session","workspaceRoots":["/repo-a","/repo-b"],` +
				`"postToolUse":{"toolName":"skills","parameters":{"skill":"probe"},"success":true}}`)
			var resolved []string
			events, err := detect("cline", stdin, func(cwd string) string {
				resolved = append(resolved, cwd)
				return tt.remotes[cwd]
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(resolved, []string{"/repo-a", "/repo-b"}) {
				t.Fatalf("resolved roots = %v", resolved)
			}
			if tt.want {
				if len(events) != 1 || events[0].RepoDir != "/repo-a" ||
					events[0].RepoRemote != "git@github.com:Netcracker/project.git" {
					t.Fatalf("events = %#v", events)
				}
			} else if len(events) != 0 {
				t.Fatalf("events = %#v, want none", events)
			}
		})
	}
}

func TestDetectRoutesExistingSkill(t *testing.T) {
	tests := []struct {
		agent     string
		hookEvent string
		line      string
	}{
		{
			agent:     "codex",
			hookEvent: "Stop",
			line:      `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}`,
		},
		{
			agent:     "cursor",
			hookEvent: "afterAgentResponse",
			line:      `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/demo/SKILL.md"}}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			tp := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(tp, []byte(tt.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := json.Marshal(map[string]any{
				"hook_event_name": tt.hookEvent,
				"session_id":      "s1",
				"cwd":             "/repo",
				"workspace_roots": []string{"/repo"},
				"transcript_path": tp,
			})
			if err != nil {
				t.Fatal(err)
			}
			events, err := detect(tt.agent, stdin, func(string) string { return "" }, time.Now().UTC())
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(events) != 1 || skillName(t, events[0]) != "demo" {
				t.Fatalf("events = %#v, want one demo skill", events)
			}
			events, err = detect(tt.agent, stdin, func(string) string { return "" }, time.Now().UTC())
			if err != nil {
				t.Fatalf("second detect: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("second detect got %d events (%#v), want 0", len(events), events)
			}
		})
	}
}

func TestDetectRejectsUnsupportedTranscriptHooksWithoutAdvancingOffsets(t *testing.T) {
	tests := []struct {
		name     string
		agent    string
		rejected string
		accepted string
		line     string
	}{
		{
			name:     "Codex missing hook",
			agent:    "codex",
			accepted: "Stop",
			line:     `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}`,
		},
		{
			name:     "Codex unsupported hook",
			agent:    "codex",
			rejected: "Notification",
			accepted: "Stop",
			line:     `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}`,
		},
		{
			name:     "Cursor missing hook",
			agent:    "cursor",
			accepted: "afterAgentResponse",
			line:     `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/demo/SKILL.md"}}]}}`,
		},
		{
			name:     "Cursor unsupported hook",
			agent:    "cursor",
			rejected: "beforeSubmitPrompt",
			accepted: "afterAgentResponse",
			line:     `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/demo/SKILL.md"}}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			tp := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(tp, []byte(tt.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{
				"session_id":      "s1",
				"cwd":             "/repo",
				"workspace_roots": []string{"/repo"},
				"transcript_path": tp,
			}
			if tt.rejected != "" {
				payload["hook_event_name"] = tt.rejected
			}
			stdin, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			events, err := detect(tt.agent, stdin, func(string) string { return "" }, time.Now().UTC())
			if err != nil {
				t.Fatalf("detect rejected hook: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("rejected hook got %d events (%#v), want 0", len(events), events)
			}

			payload["hook_event_name"] = tt.accepted
			stdin, err = json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			events, err = detect(tt.agent, stdin, func(string) string { return "" }, time.Now().UTC())
			if err != nil {
				t.Fatalf("detect accepted hook: %v", err)
			}
			if len(events) != 1 || skillName(t, events[0]) != "demo" {
				t.Fatalf("accepted hook events = %#v, want one demo skill", events)
			}
		})
	}
}

func TestDetectCodexFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@host:o/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "s1",
		"transcript_path": tp,
	})
	events, err := detect("codex", stdin, func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || events[0].Agent != "codex" || skillName(t, events[0]) != "demo" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].RepoRemote != "git@host:o/r.git" {
		t.Fatalf("remote = %q", events[0].RepoRemote)
	}
}

func TestDetectCursorFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tp := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/demo/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "afterAgentResponse",
		"session_id":      "c1",
		"workspace_roots": []string{"/repo"},
		"transcript_path": tp,
	})
	events, err := detect("cursor", stdin, func(string) string { return "git@host:o/r.git" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || events[0].Agent != "cursor" || skillName(t, events[0]) != "demo" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].RepoRemote != "git@host:o/r.git" {
		t.Fatalf("remote = %q", events[0].RepoRemote)
	}
}

func TestSanitizeRemote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"https clean", "https://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https user", "https://username@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https user+token", "https://username:ghp_xxxx@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https oauth gitlab", "https://oauth2:glpat-xxxx@gitlab.com/org/repo.git", "https://gitlab.com/org/repo.git"},
		{"http user+pass", "http://user:pass@example.com/repo.git", "http://example.com/repo.git"},
		{"ssh url clean", "ssh://git@host/org/repo.git", "ssh://host/org/repo.git"},
		{"ssh url with port", "ssh://deploy@host:2222/repo.git", "ssh://host:2222/repo.git"},
		{"scp-like", "git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"git protocol", "git://host/repo.git", "git://host/repo.git"},
		{"file url", "file:///path/to/repo.git", "file:///path/to/repo.git"},
		{"local path", "/path/to/repo.git", "/path/to/repo.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeRemote(c.in); got != c.want {
				t.Fatalf("sanitizeRemote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSkillNameInPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means no match expected
	}{
		{"cursor apm windows", `C:\Users\u\repo\.agents\skills\ai-agent-telemetry-configure\SKILL.md`, "ai-agent-telemetry-configure"},
		{"cursor legacy unix", "/repo/.cursor/skills/foo/SKILL.md", "foo"},
		{"cursor legacy windows", `C:\repo\.cursor\skills\foo\SKILL.md`, "foo"},
		{"global plugin", `C:\Users\u\.claude\plugins\cache\p\6.0.3\skills\brainstorming\SKILL.md`, "brainstorming"},
		{"global user", "/home/u/.claude/skills/foo/SKILL.md", "foo"},
		{"codex bundled system", "/home/u/.codex/skills/.system/openai-docs/SKILL.md", "openai-docs"},
		{"cursor nested group", "/repo/.cursor/skills/shipping/land-it/SKILL.md", "land-it"},
		{"windows nested group", `C:\repo\.cursor\skills\shipping\land-it\SKILL.md`, "land-it"},
		{"case-insensitive fs keeps name case", "/x/skills/Foo/skill.md", "Foo"},
		{"regex fragment is not a skill", `/\/skills\/.+\/SKILL.md$/`, ""},
		{"glob fragment is not a skill", "/repo/skills/*/SKILL.md", ""},
		{"arbitrary intermediate directory", "/repo/skills/@team+platform/foo/SKILL.md", "foo"},
		{"invalid punctuation", "/repo/skills/not_a_skill/SKILL.md", ""},
		{"trailing hyphen", "/repo/skills/not-a-skill-/SKILL.md", ""},
		{"consecutive hyphens", "/repo/skills/not--a-skill/SKILL.md", ""},
		{"unicode case fold is not ASCII", "/repo/skills/ſkill/SKILL.md", ""},
		{"64 characters", "/repo/skills/" + strings.Repeat("a", 64) + "/SKILL.md", strings.Repeat("a", 64)},
		{"65 characters", "/repo/skills/" + strings.Repeat("a", 65) + "/SKILL.md", ""},
		{"my-skills boundary", "my-skills/foo/SKILL.md", ""},
		{"no skills segment", "/repo/src/main.go", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := skillNameInPath(c.in)
			if c.want == "" {
				if ok {
					t.Fatalf("got %q, want no match", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got (%q, %v), want %q", got, ok, c.want)
			}
		})
	}
}

func TestSkillNamesInText(t *testing.T) {
	// Two real reads plus noise, including a Codex doubled-backslash path.
	text := `cat /Users/me/repo/.agents/skills/alpha/SKILL.md && rg foo && ls skills\\beta\\SKILL.md`
	got := skillNamesInText(text)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got %v, want [alpha beta]", got)
	}
	if n := skillNamesInText("cat README.md"); n != nil {
		t.Fatalf("got %v, want nil", n)
	}
}
