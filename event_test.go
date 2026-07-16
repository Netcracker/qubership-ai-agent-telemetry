package main

import (
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
		{"namespaced skill", "plugin-name:skill-name", nameIdentifier, true},
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
		{"reserved agent with ordinary skill", withEvent(validSkill, func(ev *TelemetryEvent) { ev.Agent = "selftest" })},
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
	if _, err := newSkillEvent("selftest", "session-1", "", "", selftestSkill, ts); err == nil {
		t.Fatal("ordinary constructor accepted reserved selftest event")
	}

	probe := newSelftestProbe(ts)
	if err := validateTelemetryEvent(probe); err != nil {
		t.Fatalf("newSelftestProbe validation: %v", err)
	}
	if probe.Agent != "selftest" || probe.EventName != eventSkillExecuted ||
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
