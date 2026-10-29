package main

import (
	"encoding/json"
	"time"
)

// opencodePayload contains only the bridge identifiers; tool inputs and results are never decoded.
type opencodePayload struct {
	Type      string     `json:"type"`
	SessionID string     `json:"session_id"`
	Directory string     `json:"directory"`
	SkillID   string     `json:"skill_id"`
	Server    string     `json:"server"`
	Tool      string     `json:"tool"`
	Outcome   MCPOutcome `json:"outcome"`
}

func opencodeAdapter(stdin []byte, remote remoteResolver, now time.Time) ([]TelemetryEvent, error) {
	var p opencodePayload
	if json.Unmarshal(stdin, &p) != nil || !validIdentifier(p.SessionID, sessionIdentifier) {
		return nil, nil
	}
	var event TelemetryEvent
	var err error
	switch p.Type {
	case "skill":
		event, err = newSkillEvent("opencode", p.SessionID, resolveRemote(remote, p.Directory), p.Directory, p.SkillID, now)
	case "mcp":
		if !validIdentifier(p.Server, mcpIdentifier) || (p.Outcome != mcpSucceeded && p.Outcome != mcpFailed) {
			return nil, nil
		}
		event, err = newMCPEvent("opencode", p.SessionID, resolveRemote(remote, p.Directory), p.Directory, MCPPayload{ServerName: p.Server, ToolName: p.Tool, Outcome: p.Outcome}, now)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, nil
	}
	return []TelemetryEvent{event}, nil
}
