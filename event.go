package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

type eventEnvelope struct {
	SchemaVersion int              `json:"schema_version"`
	EventName     EventName        `json:"event_name"`
	Agent         string           `json:"agent"`
	SessionID     string           `json:"session_id"`
	RepoRemote    string           `json:"repo_remote,omitempty"`
	TS            time.Time        `json:"ts"`
	Payload       telemetryPayload `json:"payload"`
}

type encodedEventEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	EventName     EventName       `json:"event_name"`
	Agent         string          `json:"agent"`
	SessionID     string          `json:"session_id"`
	RepoRemote    string          `json:"repo_remote,omitempty"`
	TS            time.Time       `json:"ts"`
	Payload       json.RawMessage `json:"payload"`
}

type legacySkillEvent struct {
	Agent      string    `json:"agent"`
	SessionID  string    `json:"session_id"`
	RepoRemote string    `json:"repo_remote,omitempty"`
	Skill      string    `json:"skill"`
	TS         time.Time `json:"ts"`
}

func (ev TelemetryEvent) MarshalJSON() ([]byte, error) {
	if err := validateSerializableEvent(ev); err != nil {
		return nil, err
	}
	return json.Marshal(eventEnvelope{
		SchemaVersion: ev.SchemaVersion,
		EventName:     ev.EventName,
		Agent:         ev.Agent,
		SessionID:     ev.SessionID,
		RepoRemote:    ev.RepoRemote,
		TS:            ev.TS.UTC(),
		Payload:       ev.Payload,
	})
}

func (ev *TelemetryEvent) UnmarshalJSON(data []byte) error {
	if err := rejectExplicitNulls(data); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(data, &fields); err != nil {
		return err
	}
	_, hasVersion := fields["schema_version"]
	_, hasEventName := fields["event_name"]
	if hasVersion != hasEventName {
		return fmt.Errorf("partially versioned telemetry event")
	}
	if !hasVersion {
		return ev.unmarshalLegacyJSON(data)
	}

	var envelope encodedEventEnvelope
	if err := decodeStrictJSON(data, &envelope); err != nil {
		return err
	}
	if err := rejectExplicitNulls(envelope.Payload); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	var payload telemetryPayload
	switch envelope.EventName {
	case eventSkillExecuted:
		var decoded SkillPayload
		if err := decodeStrictJSON(envelope.Payload, &decoded); err != nil {
			return fmt.Errorf("invalid skill payload: %w", err)
		}
		payload = decoded
	case eventCommandInvoked:
		var decoded CommandPayload
		if err := decodeStrictJSON(envelope.Payload, &decoded); err != nil {
			return fmt.Errorf("invalid command payload: %w", err)
		}
		payload = decoded
	case eventMCPExecuted:
		var decoded MCPPayload
		if err := decodeStrictJSON(envelope.Payload, &decoded); err != nil {
			return fmt.Errorf("invalid MCP payload: %w", err)
		}
		payload = decoded
	default:
		return fmt.Errorf("unknown event name %q", envelope.EventName)
	}

	decoded := TelemetryEvent{
		SchemaVersion: envelope.SchemaVersion,
		EventName:     envelope.EventName,
		Agent:         envelope.Agent,
		SessionID:     envelope.SessionID,
		RepoRemote:    envelope.RepoRemote,
		TS:            envelope.TS,
		Payload:       payload,
	}
	if err := validateSerializableEvent(decoded); err != nil {
		return err
	}
	*ev = decoded
	return nil
}

func (ev *TelemetryEvent) unmarshalLegacyJSON(data []byte) error {
	var legacy legacySkillEvent
	if err := decodeStrictJSON(data, &legacy); err != nil {
		return err
	}
	decoded := TelemetryEvent{
		SchemaVersion: eventSchemaVersion,
		EventName:     eventSkillExecuted,
		Agent:         legacy.Agent,
		SessionID:     legacy.SessionID,
		RepoRemote:    legacy.RepoRemote,
		TS:            legacy.TS,
		Payload:       SkillPayload{SkillName: legacy.Skill},
	}
	if err := validateSerializableEvent(decoded); err != nil {
		return err
	}
	*ev = decoded
	return nil
}

func decodeStrictJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func rejectExplicitNulls(data []byte) error {
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("JSON object cannot be null")
	}
	for name, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("field %q cannot be null", name)
		}
	}
	return nil
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
