package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

func TestScanCursorTranscriptReadToolUse(t *testing.T) {
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"text","text":"reading skill"},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/adr-authoring/SKILL.md"}}` +
		`]}}` + "\n"
	skills, end := scanCursorTranscript(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "adr-authoring" {
		t.Fatalf("skills = %v", skills)
	}
	if end != int64(len(line)) {
		t.Fatalf("end = %d, want %d", end, len(line))
	}
}

func TestScanCursorTranscriptManualAttach(t *testing.T) {
	line := `{"role":"user","message":{"content":[{"type":"text","text":` +
		`"<manually_attached_skills>\nSkill Name: telemetry-probe\nPath: /x/SKILL.md\n"}]}}` + "\n"
	skills, _ := scanCursorTranscript(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "telemetry-probe" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptIgnoresNonSkillReadsAndDedups(t *testing.T) {
	lines := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/src/main.go"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorTranscript(strings.NewReader(lines), 0)
	if len(skills) != 1 || skills[0] != "a" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptOffsetSkipsEarlyLines(t *testing.T) {
	first := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/old/SKILL.md"}}]}}` + "\n"
	second := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorTranscript(strings.NewReader(first+second), int64(len(first)))
	if len(skills) != 1 || skills[0] != "new" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptSkipsMalformedLine(t *testing.T) {
	lines := "{not json\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorTranscript(strings.NewReader(lines), 0)
	if len(skills) != 1 || skills[0] != "a" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptManualAttachMultiple(t *testing.T) {
	line := `{"role":"user","message":{"content":[{"type":"text","text":` +
		`"<manually_attached_skills>\nSkill Name: alpha\nPath: /a\nSkill Name: beta\nPath: /b\n"}]}}` + "\n"
	skills, _ := scanCursorTranscript(strings.NewReader(line), 0)
	if len(skills) != 2 || skills[0] != "alpha" || skills[1] != "beta" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptWindowsAgentsPath(t *testing.T) {
	path := `C:\Users\denif\repo\.agents\skills\ai-agent-telemetry-configure\SKILL.md`
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "Read", "input": map[string]any{"path": path}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	skills, _ := scanCursorTranscript(strings.NewReader(string(line)+"\n"), 0)
	if len(skills) != 1 || skills[0] != "ai-agent-telemetry-configure" {
		t.Fatalf("skills = %v, want [ai-agent-telemetry-configure]", skills)
	}
}

func TestCursorTranscriptEventsReadsFileAndResolvesRemote(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/adr-authoring/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})
	events := cursorTranscriptEvents(stdin, nil, func(string) string { return "git@host:org/repo.git" }, fixedTime)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "cursor" || e.SessionID != "c1" || e.Skill != "adr-authoring" ||
		e.RepoRemote != "git@host:org/repo.git" {
		t.Fatalf("event = %+v", e)
	}
}

func TestCursorTranscriptEventsNoPath(t *testing.T) {
	events := cursorTranscriptEvents([]byte(`{"session_id":"c1"}`), nil, func(string) string { return "" }, fixedTime)
	if events != nil {
		t.Fatalf("want nil, got %v", events)
	}
}

func TestCursorTranscriptEventsHonorsOffset(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	first := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/old/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &OffsetStore{Dir: t.TempDir()}
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})

	first1 := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(first1) != 1 || first1[0].Skill != "old" {
		t.Fatalf("first pass = %v", first1)
	}
	if again := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime); len(again) != 0 {
		t.Fatalf("second pass = %v, want 0", again)
	}
	f, _ := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n")
	f.Close()
	third := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(third) != 1 || third[0].Skill != "new" {
		t.Fatalf("third pass = %v", third)
	}
}
