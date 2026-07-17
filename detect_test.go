package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestDetectCodexFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@host:o/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tp})
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
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})
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
