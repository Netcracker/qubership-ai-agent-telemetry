package main

import (
	"fmt"
	"time"
)

type EventName string

const (
	eventSkillExecuted  EventName = "skill_executed"
	eventCommandInvoked EventName = "command_invoked"
	eventMCPExecuted    EventName = "mcp_tool_executed"
	eventSchemaVersion            = 1
	selftestAgent                 = "selftest"
)

type MCPOutcome string

const (
	mcpSucceeded MCPOutcome = "succeeded"
	mcpFailed    MCPOutcome = "failed"
	mcpUnknown   MCPOutcome = "unknown"
)

type telemetryPayload interface {
	eventName() EventName
}

type SkillPayload struct {
	SkillName string `json:"skill_name"`
}

func (SkillPayload) eventName() EventName {
	return eventSkillExecuted
}

type CommandPayload struct {
	CommandName   string `json:"command_name"`
	CommandSource string `json:"command_source"`
	ExpansionType string `json:"expansion_type"`
}

func (CommandPayload) eventName() EventName {
	return eventCommandInvoked
}

type MCPPayload struct {
	ServerName string     `json:"server_name,omitempty"`
	ToolName   string     `json:"tool_name"`
	Outcome    MCPOutcome `json:"outcome"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
}

func (MCPPayload) eventName() EventName {
	return eventMCPExecuted
}

type TelemetryEvent struct {
	SchemaVersion int
	EventName     EventName
	Agent         string
	SessionID     string
	RepoRemote    string
	RepoDir       string
	TS            time.Time
	Payload       telemetryPayload
}

type identifierProfile struct {
	max        int
	allowColon bool
	firstAlnum bool
}

var (
	sessionIdentifier = identifierProfile{
		max: 128, allowColon: true, firstAlnum: true,
	}
	nameIdentifier = identifierProfile{
		max: 255, allowColon: true, firstAlnum: true,
	}
	sourceIdentifier = identifierProfile{max: 64}
	mcpIdentifier    = identifierProfile{max: 128}
)

func validIdentifier(value string, profile identifierProfile) bool {
	if len(value) == 0 || len(value) > profile.max {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		alnum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if i == 0 && profile.firstAlnum && !alnum {
			return false
		}
		if alnum || c == '_' || c == '.' || c == '-' || profile.allowColon && c == ':' {
			continue
		}
		return false
	}
	return true
}

func newSkillEvent(
	agent, sessionID, repoRemote, repoDir, skillName string,
	ts time.Time,
) (TelemetryEvent, error) {
	ev := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         agent,
		SessionID:     sessionID,
		RepoRemote:    repoRemote,
		RepoDir:       repoDir,
		TS:            ts,
		Payload:       SkillPayload{SkillName: skillName},
	}
	if !validHarnessAgent(agent) {
		return TelemetryEvent{}, fmt.Errorf("invalid agent %q", agent)
	}
	if err := validateTelemetryEvent(ev); err != nil {
		return TelemetryEvent{}, err
	}
	return ev, nil
}

func newCommandEvent(
	agent, sessionID, repoRemote, repoDir string,
	payload CommandPayload, ts time.Time,
) (TelemetryEvent, error) {
	ev := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventCommandInvoked,
		Agent:         agent,
		SessionID:     sessionID,
		RepoRemote:    repoRemote,
		RepoDir:       repoDir,
		TS:            ts,
		Payload:       payload,
	}
	if !validHarnessAgent(agent) {
		return TelemetryEvent{}, fmt.Errorf("invalid agent %q", agent)
	}
	if err := validateTelemetryEvent(ev); err != nil {
		return TelemetryEvent{}, err
	}
	return ev, nil
}

func newMCPEvent(
	agent, sessionID, repoRemote, repoDir string,
	payload MCPPayload, ts time.Time,
) (TelemetryEvent, error) {
	ev := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventMCPExecuted,
		Agent:         agent,
		SessionID:     sessionID,
		RepoRemote:    repoRemote,
		RepoDir:       repoDir,
		TS:            ts,
		Payload:       payload,
	}
	if !validHarnessAgent(agent) {
		return TelemetryEvent{}, fmt.Errorf("invalid agent %q", agent)
	}
	if err := validateTelemetryEvent(ev); err != nil {
		return TelemetryEvent{}, err
	}
	return ev, nil
}

func newSelftestProbe(ts time.Time) TelemetryEvent {
	return TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         selftestAgent,
		SessionID:     newUUID(),
		TS:            ts,
		Payload:       SkillPayload{SkillName: selftestSkill},
	}
}

func validateTelemetryEvent(ev TelemetryEvent) error {
	if ev.SchemaVersion != eventSchemaVersion {
		return fmt.Errorf("invalid schema version %d", ev.SchemaVersion)
	}
	if ev.Payload == nil {
		return fmt.Errorf("missing payload")
	}
	switch ev.Payload.(type) {
	case SkillPayload, CommandPayload, MCPPayload:
	default:
		return fmt.Errorf("unknown payload type")
	}
	if ev.EventName != ev.Payload.eventName() {
		return fmt.Errorf("payload does not match event name %q", ev.EventName)
	}
	if ev.TS.IsZero() {
		return fmt.Errorf("missing timestamp")
	}
	if !validIdentifier(ev.SessionID, sessionIdentifier) {
		return fmt.Errorf("invalid session ID")
	}

	if ev.Agent == selftestAgent {
		payload, ok := ev.Payload.(SkillPayload)
		if !ok || ev.EventName != eventSkillExecuted || payload.SkillName != selftestSkill ||
			!validUUIDv4(ev.SessionID) || ev.RepoRemote != "" || ev.RepoDir != "" {
			return fmt.Errorf("invalid selftest event")
		}
		return nil
	}
	if !validHarnessAgent(ev.Agent) {
		return fmt.Errorf("invalid agent %q", ev.Agent)
	}

	switch payload := ev.Payload.(type) {
	case SkillPayload:
		if payload.SkillName == selftestSkill || !validIdentifier(payload.SkillName, nameIdentifier) {
			return fmt.Errorf("invalid skill name")
		}
	case CommandPayload:
		if !validIdentifier(payload.CommandName, nameIdentifier) {
			return fmt.Errorf("invalid command name")
		}
		if !validIdentifier(payload.CommandSource, sourceIdentifier) {
			return fmt.Errorf("invalid command source")
		}
		if payload.ExpansionType != "slash_command" && payload.ExpansionType != "mcp_prompt" {
			return fmt.Errorf("invalid expansion type")
		}
	case MCPPayload:
		if payload.ServerName != "" && !validIdentifier(payload.ServerName, mcpIdentifier) {
			return fmt.Errorf("invalid MCP server name")
		}
		if !validIdentifier(payload.ToolName, mcpIdentifier) {
			return fmt.Errorf("invalid MCP tool name")
		}
		if payload.Outcome != mcpSucceeded && payload.Outcome != mcpFailed && payload.Outcome != mcpUnknown {
			return fmt.Errorf("invalid MCP outcome")
		}
		if payload.DurationMS != nil && *payload.DurationMS < 0 {
			return fmt.Errorf("negative MCP duration")
		}
	default:
		return fmt.Errorf("unknown payload type")
	}
	return nil
}

func validateSerializableEvent(ev TelemetryEvent) error {
	if err := validateTelemetryEvent(ev); err != nil {
		return err
	}
	if ev.RepoRemote != "" && ev.RepoRemote != remoteIdentity(ev.RepoRemote) {
		return fmt.Errorf("repository remote is not normalized")
	}
	return nil
}

func validHarnessAgent(agent string) bool {
	return agent == "claude" || agent == "codex" || agent == "cursor"
}

func validUUIDv4(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' ||
		value[14] != '4' || value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'b' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
