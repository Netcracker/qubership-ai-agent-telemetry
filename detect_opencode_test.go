package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeBridgeUsesStableSkillID(t *testing.T) {
	events, err := detect("opencode", []byte(`{"type":"skill","session_id":"ses_test","directory":"/fixture","skill_id":"git-release","name":"Private Display","text":"PRIVATE_BODY"}`), func(string) string { return "https://host/org/repo.git" }, time.Now())
	if err != nil || len(events) != 1 {
		t.Fatalf("detect(opencode skill) = %v, %v, want one event", events, err)
	}
	event := events[0]
	if event.Agent != "opencode" || skillName(t, event) != "git-release" || event.RepoDir != "/fixture" {
		t.Fatalf("event = %#v", event)
	}
	event.RepoRemote = normalizeRawRemote(event.RepoRemote)
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "/fixture") {
		t.Fatalf("private data serialized: %s", raw)
	}
}

func TestOpenCodeBridgePreservesMCPOutcomesWithoutNestedDuration(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			raw := `{"type":"mcp","session_id":"ses_test","directory":"/fixture","server":"docs","tool":"search","outcome":"` + outcome + `","duration_ms":999,"arguments":{"secret":"PRIVATE"}}`
			events, err := detect("opencode", []byte(raw), nil, time.Now())
			if err != nil || len(events) != 1 {
				t.Fatalf("detect() = %v, %v", events, err)
			}
			want := MCPPayload{ServerName: "docs", ToolName: "search", Outcome: MCPOutcome(outcome)}
			if got := events[0].Payload; got != want {
				t.Errorf("payload = %#v, want %#v", got, want)
			}
		})
	}
}

func TestOpenCodeBridgeDropsInvalidEnvelopes(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"type":"read","session_id":"s","skill_id":"x"}`, `{"type":"skill","session_id":"s","skill_id":"Display Name"}`, `{"type":"skill","session_id":"s","skill_id":"\u0442\u0435\u0441\u0442"}`, `{"type":"mcp","session_id":"s","server":"docs","tool":"search","outcome":"maybe"}`} {
		t.Run(raw, func(t *testing.T) {
			events, err := detect("opencode", []byte(raw), nil, time.Now())
			if err != nil || len(events) != 0 {
				t.Errorf("detect(%s) = %v, %v, want no events", raw, events, err)
			}
		})
	}
}

func TestOpenCodeEventsUseSharedPathAndRepositoryPolicy(t *testing.T) {
	directory := t.TempDir()
	events, err := detect("opencode", []byte(`{"type":"skill","session_id":"s","directory":`+strconv.Quote(directory)+`,"skill_id":"git-release"}`), func(string) string { return "https://github.com/personal/project.git" }, time.Now())
	if err != nil || len(events) != 1 {
		t.Fatalf("detect() = %v, %v", events, err)
	}
	cases := []struct {
		name   string
		policy telemetryPolicy
		want   int
	}{
		{"path allows denied remote", telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}, PathAllowList: []string{directory}}, 1},
		{"both denied", telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}, 0},
		{"disabled overrides path", telemetryPolicy{Disabled: true, PathAllowList: []string{directory}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterEventsByPolicy(events, tc.policy, nil)
			if len(got) != tc.want {
				t.Errorf("filtered events = %d, want %d", len(got), tc.want)
			}
		})
	}
}
