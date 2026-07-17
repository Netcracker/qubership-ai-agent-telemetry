package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var forbiddenSentinels = []string{
	"PROMPT_SECRET_7f1",
	"ARG_SECRET_7f2",
	"INPUT_SECRET_7f3",
	"RESULT_SECRET_7f4",
	"ERROR_SECRET_7f5",
	"CALL_SECRET_7f6",
	"TURN_SECRET_7f7",
	"MODEL_SECRET_7f8",
	"person@example.com",
	"/home/private-user/project",
	"https://mcp.internal/token",
}

func TestPrivacyRawHooksExcludePrivateFieldsFromOutboxAndOTLP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	now := time.Unix(1_750_000_000, 0).UTC()

	tests := []struct {
		name  string
		agent string
		input func(t *testing.T) []byte
	}{
		{name: "claude skill", agent: "claude", input: claudePrivacySkillHook},
		{name: "claude command", agent: "claude", input: claudePrivacyCommandHook},
		{name: "claude MCP success", agent: "claude", input: func(t *testing.T) []byte {
			return claudePrivacyMCPHook(t, "PostToolUse")
		}},
		{name: "claude MCP failure", agent: "claude", input: func(t *testing.T) []byte {
			return claudePrivacyMCPHook(t, "PostToolUseFailure")
		}},
		{name: "codex skill", agent: "codex", input: codexPrivacySkillHook},
		{name: "codex MCP", agent: "codex", input: codexPrivacyMCPHook},
		{name: "cursor skill", agent: "cursor", input: cursorPrivacySkillHook},
		{name: "cursor MCP", agent: "cursor", input: cursorPrivacyMCPHook},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := detect(tt.agent, tt.input(t), privacyRemoteResolver, now)
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			events = filterEventsByPolicy(events, telemetryPolicy{
				RepoAllowList: []string{"github.com/netcracker/*"},
			}, func(string) []string { return []string{privacyRemoteResolver("")} })
			if len(events) != 1 {
				t.Fatalf("policy retained %d events, want 1", len(events))
			}

			outbox := &Outbox{Dir: t.TempDir()}
			if err := outbox.Enqueue(events[0]); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			files, err := outbox.List()
			if err != nil || len(files) != 1 {
				t.Fatalf("outbox files = %v, err = %v; want one", files, err)
			}
			outboxJSON, err := os.ReadFile(filepath.Join(outbox.Dir, files[0]))
			if err != nil {
				t.Fatal(err)
			}
			assertNoForbiddenSentinels(t, "outbox JSON", outboxJSON)
			if _, err := outbox.Read(files[0]); err != nil {
				t.Fatalf("read outbox event: %v", err)
			}

			capture := newOTLPCapture(t)
			defer capture.server.Close()
			sent, err := Flush(outbox, capture.server.URL, "", nil, 2*time.Second)
			if err != nil {
				t.Fatalf("flush: %v", err)
			}
			if sent != 1 || len(capture.requests) != 1 {
				t.Fatalf("sent %d events in %d requests, want 1 and 1", sent, len(capture.requests))
			}
			for _, body := range capture.bodies {
				assertNoForbiddenSentinels(t, "OTLP request", body)
			}
		})
	}
}

func TestPrivacyInvalidIdentifiersProduceNoOutboxOrCollectorData(t *testing.T) {
	invalid := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "maximum plus one", value: strings.Repeat("a", sessionIdentifier.max+1)},
		{name: "Unicode", value: "session-é"},
		{name: "whitespace", value: "session id"},
		{name: "newline", value: "session\nid"},
		{name: "tab", value: "session\tid"},
		{name: "slash", value: "session/id"},
		{name: "backslash", value: `session\id`},
		{name: "shell metacharacters", value: `session;$(touch-pwned)`},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			input := privacyJSON(t, privacyHookFields(map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      tt.value,
				"cwd":             "/repo",
				"tool_name":       "Skill",
				"tool_input":      map[string]any{"skill": "privacy-skill"},
			}))
			assertRejectedHookLeavesNoData(t, "claude", input)
		})
	}

	t.Run("malformed optional MCP server", func(t *testing.T) {
		input := privacyJSON(t, privacyHookFields(map[string]any{
			"hook_event_name": "PostToolUse",
			"session_id":      "session-valid",
			"cwd":             "/repo",
			"tool_name":       "mcp__bad/server__get_issue",
		}))
		assertRejectedHookLeavesNoData(t, "claude", input)
	})
}

func assertRejectedHookLeavesNoData(t *testing.T, agent string, input []byte) {
	t.Helper()
	outbox := &Outbox{Dir: t.TempDir()}
	events, err := detect(agent, input, privacyRemoteResolver, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	events = filterEventsByPolicy(events, telemetryPolicy{}, nil)
	for _, event := range events {
		if err := outbox.Enqueue(event); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	capture := newOTLPCapture(t)
	defer capture.server.Close()
	sent, err := Flush(outbox, capture.server.URL, "", nil, time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	files, err := outbox.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || len(files) != 0 || sent != 0 || len(capture.requests) != 0 {
		t.Fatalf("events=%d files=%d sent=%d requests=%d; want all zero",
			len(events), len(files), sent, len(capture.requests))
	}
}

func assertNoForbiddenSentinels(t *testing.T, form string, serialized []byte) {
	t.Helper()
	for _, sentinel := range forbiddenSentinels {
		if bytes.Contains(serialized, []byte(sentinel)) {
			t.Errorf("%s contains forbidden sentinel %q", form, sentinel)
		}
	}
}

func privacyRemoteResolver(string) string {
	return "git@github.com:Netcracker/privacy-safe.git"
}

func privacyHookFields(fields map[string]any) map[string]any {
	private := map[string]any{
		"prompt":          forbiddenSentinels[0],
		"arguments":       forbiddenSentinels[1],
		"command_args":    forbiddenSentinels[1],
		"input":           forbiddenSentinels[2],
		"tool_input":      forbiddenSentinels[2],
		"result":          forbiddenSentinels[3],
		"result_json":     forbiddenSentinels[3],
		"tool_response":   forbiddenSentinels[3],
		"error":           forbiddenSentinels[4],
		"call_id":         forbiddenSentinels[5],
		"tool_use_id":     forbiddenSentinels[5],
		"turn_id":         forbiddenSentinels[6],
		"conversation_id": forbiddenSentinels[6],
		"model":           forbiddenSentinels[7],
		"user_email":      forbiddenSentinels[8],
		"local_path":      forbiddenSentinels[9],
		"url":             forbiddenSentinels[10],
	}
	for key, value := range fields {
		private[key] = value
	}
	return private
}

func privacyJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func claudePrivacySkillHook(t *testing.T) []byte {
	fields := privacyHookFields(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "claude-skill-session",
		"cwd":             forbiddenSentinels[9],
		"tool_name":       "Skill",
		"tool_input": map[string]any{
			"skill": "privacy-skill",
			"input": forbiddenSentinels[2],
		},
	})
	return privacyJSON(t, fields)
}

func claudePrivacyCommandHook(t *testing.T) []byte {
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": "UserPromptExpansion",
		"session_id":      "claude-command-session",
		"cwd":             forbiddenSentinels[9],
		"command_name":    "review-pr",
		"command_source":  "plugin",
		"expansion_type":  "slash_command",
	}))
}

func claudePrivacyMCPHook(t *testing.T, hook string) []byte {
	duration := int64(42)
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": hook,
		"session_id":      "claude-mcp-session",
		"cwd":             forbiddenSentinels[9],
		"tool_name":       "mcp__github__get_issue",
		"duration_ms":     duration,
	}))
}

func codexPrivacySkillHook(t *testing.T) []byte {
	transcript := codexMetaLine("git@github.com:Netcracker/privacy-safe.git") +
		codexExecLine("cat /repo/skills/privacy-skill/SKILL.md "+strings.Join(forbiddenSentinels, " "))
	path := writeRollout(t, transcript)
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "codex-skill-session",
		"cwd":             forbiddenSentinels[9],
		"transcript_path": path,
	}))
}

func codexPrivacyMCPHook(t *testing.T) []byte {
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": "PostToolUse",
		"session_id":      "codex-mcp-session",
		"cwd":             forbiddenSentinels[9],
		"tool_name":       "mcp__github__get_issue",
	}))
}

func cursorPrivacySkillHook(t *testing.T) []byte {
	line := privacyJSON(t, map[string]any{
		"role": "assistant",
		"message": map[string]any{"content": []map[string]any{
			{"type": "text", "text": strings.Join(forbiddenSentinels, " ")},
			{"type": "tool_use", "name": "Read", "input": map[string]any{
				"path": "/repo/skills/privacy-skill/SKILL.md",
			}},
		}},
	})
	path := filepath.Join(t.TempDir(), "cursor.jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": "afterAgentResponse",
		"session_id":      "cursor-skill-session",
		"workspace_roots": []string{forbiddenSentinels[9]},
		"transcript_path": path,
	}))
}

func cursorPrivacyMCPHook(t *testing.T) []byte {
	duration := int64(42)
	return privacyJSON(t, privacyHookFields(map[string]any{
		"hook_event_name": "afterMCPExecution",
		"session_id":      "cursor-mcp-session",
		"workspace_roots": []string{forbiddenSentinels[9]},
		"tool_name":       "get_issue",
		"duration":        duration,
	}))
}
