package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTelemetryIdentifiers(t *testing.T) {
	tests := []struct {
		name, value string
		profile     identifierProfile
		want        bool
	}{
		{"session minimum", "s", sessionIdentifier, true},
		{"session maximum", strings.Repeat("a", 128), sessionIdentifier, true},
		{"session oversized", strings.Repeat("a", 129), sessionIdentifier, false},
		{"session first character", "-session", sessionIdentifier, false},
		{"session colon", "session:child", sessionIdentifier, true},
		{"namespaced skill", "plugin-name:skill-name", nameIdentifier, true},
		{"name maximum", strings.Repeat("a", 255), nameIdentifier, true},
		{"name oversized", strings.Repeat("a", 256), nameIdentifier, false},
		{"name first character", ":skill", nameIdentifier, false},
		{"source maximum", strings.Repeat("a", 64), sourceIdentifier, true},
		{"source oversized", strings.Repeat("a", 65), sourceIdentifier, false},
		{"source punctuation first", "_source", sourceIdentifier, true},
		{"source colon", "plugin:source", sourceIdentifier, false},
		{"MCP maximum", strings.Repeat("a", 128), mcpIdentifier, true},
		{"MCP oversized", strings.Repeat("a", 129), mcpIdentifier, false},
		{"MCP punctuation first", ".github", mcpIdentifier, true},
		{"MCP colon", "github:tool", mcpIdentifier, false},
		{"control character", "skill\nname", nameIdentifier, false},
		{"unicode", "skіll", nameIdentifier, false},
		{"path", "skills/demo", nameIdentifier, false},
		{"shell", "demo$(id)", nameIdentifier, false},
		{"MCP punctuation", "github.get-issue_v2", mcpIdentifier, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validIdentifier(tt.value, tt.profile); got != tt.want {
				t.Fatalf("validIdentifier(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestTelemetryEventValidation(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	duration := int64(42)

	validSkill := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         "codex",
		SessionID:     "session-1",
		RepoRemote:    "git@github.com:Netcracker/Project.git",
		RepoDir:       "/repo",
		TS:            ts,
		Payload:       SkillPayload{SkillName: "plugin-name:skill-name"},
	}
	validCommand := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventCommandInvoked,
		Agent:         "claude",
		SessionID:     "session-1",
		TS:            ts,
		Payload: CommandPayload{
			CommandName:   "review-pr",
			CommandSource: "plugin",
			ExpansionType: "slash_command",
		},
	}
	validMCP := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventMCPExecuted,
		Agent:         "cursor",
		SessionID:     "session-1",
		TS:            ts,
		Payload: MCPPayload{
			ServerName: "github",
			ToolName:   "get-issue_v2",
			Outcome:    mcpSucceeded,
			DurationMS: &duration,
		},
	}

	tests := []struct {
		name string
		ev   TelemetryEvent
	}{
		{"unknown schema version", withEvent(validSkill, func(ev *TelemetryEvent) { ev.SchemaVersion = 2 })},
		{"invalid event name", withEvent(validSkill, func(ev *TelemetryEvent) { ev.EventName = "other" })},
		{"invalid agent", withEvent(validSkill, func(ev *TelemetryEvent) { ev.Agent = "other" })},
		{"missing timestamp", withEvent(validSkill, func(ev *TelemetryEvent) { ev.TS = time.Time{} })},
		{"invalid session", withEvent(validSkill, func(ev *TelemetryEvent) { ev.SessionID = "bad session" })},
		{"missing payload", withEvent(validSkill, func(ev *TelemetryEvent) { ev.Payload = nil })},
		{"typed nil payload", withEvent(validSkill, func(ev *TelemetryEvent) {
			ev.Payload = (*SkillPayload)(nil)
		})},
		{"pointer payload", withEvent(validSkill, func(ev *TelemetryEvent) {
			payload := ev.Payload.(SkillPayload)
			ev.Payload = &payload
		})},
		{"skill payload mismatch", withEvent(validSkill, func(ev *TelemetryEvent) { ev.Payload = validCommand.Payload })},
		{"invalid skill", withEvent(validSkill, func(ev *TelemetryEvent) { ev.Payload = SkillPayload{SkillName: "bad/skill"} })},
		{"command payload mismatch", withEvent(validCommand, func(ev *TelemetryEvent) { ev.Payload = validMCP.Payload })},
		{"invalid command source", withEvent(validCommand, func(ev *TelemetryEvent) {
			payload := ev.Payload.(CommandPayload)
			payload.CommandSource = "bad source"
			ev.Payload = payload
		})},
		{"invalid expansion type", withEvent(validCommand, func(ev *TelemetryEvent) {
			payload := ev.Payload.(CommandPayload)
			payload.ExpansionType = "other"
			ev.Payload = payload
		})},
		{"MCP payload mismatch", withEvent(validMCP, func(ev *TelemetryEvent) { ev.Payload = validSkill.Payload })},
		{"invalid MCP server", withEvent(validMCP, func(ev *TelemetryEvent) {
			payload := ev.Payload.(MCPPayload)
			payload.ServerName = "bad/server"
			ev.Payload = payload
		})},
		{"invalid MCP outcome", withEvent(validMCP, func(ev *TelemetryEvent) {
			payload := ev.Payload.(MCPPayload)
			payload.Outcome = "other"
			ev.Payload = payload
		})},
		{"negative duration", withEvent(validMCP, func(ev *TelemetryEvent) {
			negative := int64(-1)
			payload := ev.Payload.(MCPPayload)
			payload.DurationMS = &negative
			ev.Payload = payload
		})},
		{"reserved agent with ordinary skill", withEvent(validSkill, func(ev *TelemetryEvent) {
			ev.Agent = selftestAgent
		})},
		{"reserved skill with ordinary agent", withEvent(validSkill, func(ev *TelemetryEvent) {
			ev.Payload = SkillPayload{SkillName: selftestSkill}
		})},
	}

	for _, ev := range []TelemetryEvent{validSkill, validCommand, validMCP} {
		if err := validateTelemetryEvent(ev); err != nil {
			t.Fatalf("validateTelemetryEvent(%q): %v", ev.EventName, err)
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateTelemetryEvent(tt.ev); err == nil {
				t.Fatal("validateTelemetryEvent() succeeded, want error")
			}
		})
	}

	for _, agent := range []string{"claude", "codex", "cursor"} {
		if _, err := newSkillEvent(agent, "session-1", "", "/repo", "skill", ts); err != nil {
			t.Fatalf("newSkillEvent(%q): %v", agent, err)
		}
	}
	if _, err := newSkillEvent(selftestAgent, "session-1", "", "", selftestSkill, ts); err == nil {
		t.Fatal("ordinary constructor accepted reserved selftest event")
	}

	probe := newSelftestProbe(ts)
	if err := validateTelemetryEvent(probe); err != nil {
		t.Fatalf("newSelftestProbe validation: %v", err)
	}
	if probe.Agent != selftestAgent || probe.EventName != eventSkillExecuted ||
		probe.Payload != (SkillPayload{SkillName: selftestSkill}) ||
		probe.SessionID == "" || probe.RepoRemote != "" || probe.RepoDir != "" {
		t.Fatalf("newSelftestProbe() = %#v", probe)
	}

	if err := validateSerializableEvent(withEvent(validSkill, func(ev *TelemetryEvent) {
		ev.RepoRemote = "github.com/netcracker/project"
	})); err != nil {
		t.Fatalf("validateSerializableEvent(normalized): %v", err)
	}
	if err := validateSerializableEvent(withEvent(validSkill, func(ev *TelemetryEvent) {
		ev.RepoRemote = ""
	})); err != nil {
		t.Fatalf("validateSerializableEvent(unscoped): %v", err)
	}
	if err := validateSerializableEvent(validSkill); err == nil {
		t.Fatal("validateSerializableEvent accepted raw remote")
	}
}

func withEvent(ev TelemetryEvent, change func(*TelemetryEvent)) TelemetryEvent {
	change(&ev)
	return ev
}

func TestTelemetryEventCanonicalJSON(t *testing.T) {
	ts := time.Date(2026, 7, 16, 14, 34, 56, 123456789, time.FixedZone("test", 2*60*60))
	canonicalTS := time.Date(2026, 7, 16, 12, 34, 56, 123456789, time.UTC)
	duration := int64(42)

	tests := []struct {
		name    string
		fixture string
		event   TelemetryEvent
	}{
		{
			name:    "skill",
			fixture: "skill-v1.json",
			event: TelemetryEvent{
				SchemaVersion: eventSchemaVersion,
				EventName:     eventSkillExecuted,
				Agent:         "codex",
				SessionID:     "session-123",
				RepoRemote:    "github.com/netcracker/project",
				RepoDir:       "/must/not/be/serialized",
				TS:            ts,
				Payload:       SkillPayload{SkillName: "superpowers:brainstorming"},
			},
		},
		{
			name:    "command",
			fixture: "command-v1.json",
			event: TelemetryEvent{
				SchemaVersion: eventSchemaVersion,
				EventName:     eventCommandInvoked,
				Agent:         "claude",
				SessionID:     "session-123",
				RepoRemote:    "github.com/netcracker/project",
				TS:            ts,
				Payload: CommandPayload{
					CommandName:   "review-pr",
					CommandSource: "plugin",
					ExpansionType: "slash_command",
				},
			},
		},
		{
			name:    "MCP",
			fixture: "mcp-v1.json",
			event: TelemetryEvent{
				SchemaVersion: eventSchemaVersion,
				EventName:     eventMCPExecuted,
				Agent:         "claude",
				SessionID:     "session-123",
				RepoRemote:    "github.com/netcracker/project",
				TS:            ts,
				Payload: MCPPayload{
					ServerName: "github",
					ToolName:   "get_issue",
					Outcome:    mcpSucceeded,
					DurationMS: &duration,
				},
			},
		},
		{
			name:    "selftest",
			fixture: "selftest-v1.json",
			event: TelemetryEvent{
				SchemaVersion: eventSchemaVersion,
				EventName:     eventSkillExecuted,
				Agent:         selftestAgent,
				SessionID:     "65ee3934-032f-4b3a-a440-089ae5c053b9",
				TS:            ts,
				Payload:       SkillPayload{SkillName: selftestSkill},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := readEventFixture(t, tt.fixture)
			got, err := json.MarshalIndent(tt.event, "", "  ")
			if err != nil {
				t.Fatalf("json.MarshalIndent(): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("canonical JSON mismatch\n got: %s\nwant: %s", got, want)
			}

			var decoded TelemetryEvent
			if err := json.Unmarshal(want, &decoded); err != nil {
				t.Fatalf("json.Unmarshal(): %v", err)
			}
			expected := tt.event
			expected.RepoDir = ""
			expected.TS = canonicalTS
			if !eventsEqual(decoded, expected) {
				t.Fatalf("decoded event = %#v, want %#v", decoded, expected)
			}
		})
	}

	withoutRemote := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         "codex",
		SessionID:     "session-123",
		TS:            canonicalTS,
		Payload:       SkillPayload{SkillName: "skill"},
	}
	got, err := json.Marshal(withoutRemote)
	if err != nil {
		t.Fatalf("json.Marshal(without remote): %v", err)
	}
	if bytes.Contains(got, []byte(`"repo_remote"`)) {
		t.Fatalf("json.Marshal(without remote) included repo_remote: %s", got)
	}
	var decoded TelemetryEvent
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(without remote): %v", err)
	}
	if !eventsEqual(decoded, withoutRemote) {
		t.Fatalf("decoded event without remote = %#v, want %#v", decoded, withoutRemote)
	}
}

func TestTelemetryEventRejects(t *testing.T) {
	valid := string(readEventFixture(t, "skill-v1.json"))
	tests := []struct {
		name string
		data string
	}{
		{"unknown envelope field", strings.Replace(valid, `"payload":`, `"unexpected":true,"payload":`, 1)},
		{"unknown payload field", strings.Replace(valid, `"skill_name": "superpowers:brainstorming"`, `"skill_name":"superpowers:brainstorming","unexpected":true`, 1)},
		{"null envelope field", strings.Replace(valid, `"repo_remote": "github.com/netcracker/project"`, `"repo_remote": null`, 1)},
		{"null required envelope field", strings.Replace(valid, `"agent": "codex"`, `"agent": null`, 1)},
		{"null payload", strings.Replace(
			valid,
			`"payload": {`+newline+`    "skill_name": "superpowers:brainstorming"`+newline+`  }`,
			`"payload": null`,
			1,
		)},
		{"null payload field", strings.Replace(valid, `"skill_name": "superpowers:brainstorming"`, `"skill_name": null`, 1)},
		{"unknown version", strings.Replace(valid, `"schema_version": 1`, `"schema_version": 2`, 1)},
		{"missing schema discriminator", strings.Replace(valid, `  "schema_version": 1,`+newline, "", 1)},
		{"missing event discriminator", strings.Replace(valid, `  "event_name": "skill_executed",`+newline, "", 1)},
		{"wrong payload", strings.Replace(valid, `"skill_name": "superpowers:brainstorming"`, `"tool_name":"get_issue","outcome":"succeeded"`, 1)},
		{"invalid timestamp", strings.Replace(valid, `"2026-07-16T12:34:56.123456789Z"`, `"not-a-timestamp"`, 1)},
		{"trailing JSON", valid + `{}`},
		{"unnormalized remote", strings.Replace(valid, `"github.com/netcracker/project"`, `"git@github.com:Netcracker/Project.git"`, 1)},
		{"entire document null", `null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ev TelemetryEvent
			if err := json.Unmarshal([]byte(tt.data), &ev); err == nil {
				t.Fatalf("json.Unmarshal() succeeded for %s", tt.data)
			}
		})
	}

	mcp := string(readEventFixture(t, "mcp-v1.json"))
	for _, field := range []string{"server_name", "duration_ms"} {
		t.Run("null optional MCP "+field, func(t *testing.T) {
			var ev TelemetryEvent
			data := strings.Replace(mcp, eventJSONFieldValue(mcp, field), "null", 1)
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				t.Fatalf("json.Unmarshal() accepted null %s", field)
			}
		})
	}
}

func TestTelemetryEventLegacy(t *testing.T) {
	fixture := readEventFixture(t, "skill-legacy.json")
	var decoded TelemetryEvent
	if err := json.Unmarshal(fixture, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(legacy): %v", err)
	}
	want := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         "codex",
		SessionID:     "session-123",
		RepoRemote:    "github.com/netcracker/project",
		TS:            time.Date(2026, 7, 16, 12, 34, 56, 123456789, time.UTC),
		Payload:       SkillPayload{SkillName: "superpowers:brainstorming"},
	}
	if !eventsEqual(decoded, want) {
		t.Fatalf("decoded legacy event = %#v, want %#v", decoded, want)
	}

	validSelftest := `{
		"agent":"selftest",
		"session_id":"65ee3934-032f-4b3a-a440-089ae5c053b9",
		"skill":"__selftest__",
		"ts":"2026-07-16T12:34:56.123456789Z"
	}`
	if err := json.Unmarshal([]byte(validSelftest), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(legacy selftest): %v", err)
	}

	invalid := []string{
		strings.Replace(string(fixture), `"session-123"`, `"bad session"`, 1),
		strings.Replace(string(fixture), `"superpowers:brainstorming"`, `"bad/skill"`, 1),
		strings.Replace(string(fixture), `"github.com/netcracker/project"`, `"git@github.com:Netcracker/Project.git"`, 1),
		strings.Replace(string(fixture), `"skill":`, `"unexpected":true,"skill":`, 1),
		strings.Replace(validSelftest, `"selftest"`, `"codex"`, 1),
		strings.Replace(validSelftest, `"__selftest__"`, `"ordinary-skill"`, 1),
		strings.Replace(validSelftest, `"65ee3934-032f-4b3a-a440-089ae5c053b9"`, `"session-123"`, 1),
		strings.Replace(validSelftest, `"skill":"__selftest__"`, `"repo_remote":"github.com/netcracker/project","skill":"__selftest__"`, 1),
		strings.Replace(validSelftest, `"skill":"__selftest__"`, `"skill":null`, 1),
		validSelftest + `{}`,
	}
	for i, data := range invalid {
		t.Run("invalid legacy "+string(rune('A'+i)), func(t *testing.T) {
			var ev TelemetryEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				t.Fatalf("json.Unmarshal() accepted invalid legacy event: %s", data)
			}
		})
	}

	withoutRemote := strings.Replace(
		string(fixture),
		`  "repo_remote": "github.com/netcracker/project",`+newline,
		"",
		1,
	)
	if err := json.Unmarshal([]byte(withoutRemote), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(legacy without remote): %v", err)
	}
}

const newline = "\n"

func readEventFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "events", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(data, []byte(newline))
}

func eventsEqual(got, want TelemetryEvent) bool {
	gotTS, wantTS := got.TS, want.TS
	got.TS, want.TS = time.Time{}, time.Time{}
	return reflect.DeepEqual(got, want) && gotTS.Equal(wantTS)
}

func eventJSONFieldValue(data, field string) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &object); err != nil {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(object["payload"], &payload); err != nil {
		return ""
	}
	return string(payload[field])
}
