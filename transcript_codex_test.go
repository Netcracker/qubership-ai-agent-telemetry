package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexExecLine(cmd string) string {
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	payload := map[string]any{"type": "function_call", "name": "exec_command", "arguments": string(args)}
	line, _ := json.Marshal(map[string]any{"type": "response_item", "payload": payload})
	return string(line) + "\n"
}

func codexShellCommandLine(command string) string {
	args, _ := json.Marshal(map[string]string{"command": command})
	payload := map[string]any{"type": "function_call", "name": "shell_command", "arguments": string(args)}
	line, _ := json.Marshal(map[string]any{"type": "response_item", "payload": payload})
	return string(line) + "\n"
}

func codexCustomExecLine(input string) string {
	payload := map[string]any{"type": "custom_tool_call", "name": "exec", "input": input}
	line, _ := json.Marshal(map[string]any{"type": "response_item", "payload": payload})
	return string(line) + "\n"
}

func codexMetaLine(repo string) string {
	payload := map[string]any{"id": "s1", "cwd": "/repo", "git": map[string]string{"repository_url": repo}}
	line, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
	return string(line) + "\n"
}

func codexMsgLine() string {
	payload := map[string]any{"type": "message", "role": "assistant"}
	line, _ := json.Marshal(map[string]any{"type": "response_item", "payload": payload})
	return string(line) + "\n"
}

func TestScanCodexRolloutFindsSkillRead(t *testing.T) {
	roll := codexMetaLine("https://github.com/o/r") +
		codexExecLine("sed -n '1,220p' .agents/skills/adr-authoring/SKILL.md") +
		codexExecLine("rg --files docs/adr") +
		codexMsgLine()
	scan, end := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 1 || scan.skills[0] != "adr-authoring" {
		t.Fatalf("skills = %v, want [adr-authoring]", scan.skills)
	}
	if scan.repoRemote != "https://github.com/o/r" {
		t.Fatalf("repoRemote = %q", scan.repoRemote)
	}
	if end != int64(len(roll)) {
		t.Fatalf("end = %d, want %d", end, len(roll))
	}
}

func TestScanCodexRolloutFindsDesktopCustomExecSkillRead(t *testing.T) {
	roll := codexCustomExecLine(`const r = await tools.shell_command({"command":"Get-Content -Raw '.agents\\skills\\ai-agent-telemetry-configure\\SKILL.md'"}); text(r)`)
	scan, _ := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 1 || scan.skills[0] != "ai-agent-telemetry-configure" {
		t.Fatalf("skills = %v, want [ai-agent-telemetry-configure]", scan.skills)
	}
}

func TestScanCodexRolloutFindsDesktopShellCommandSkillRead(t *testing.T) {
	roll := codexShellCommandLine(`Get-Content -LiteralPath 'C:\Users\denif\repo\.agents\skills\ai-agent-telemetry-configure\SKILL.md'`)
	scan, _ := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 1 || scan.skills[0] != "ai-agent-telemetry-configure" {
		t.Fatalf("skills = %v, want [ai-agent-telemetry-configure]", scan.skills)
	}
}

func TestScanCodexRolloutAbsoluteAndRelativePaths(t *testing.T) {
	roll := codexExecLine("cat /Users/me/repo/.agents/skills/english-us-developer-style/SKILL.md") +
		codexExecLine("head -n 50 .agents/skills/adr-authoring/SKILL.md")
	scan, _ := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 2 {
		t.Fatalf("skills = %v, want 2", scan.skills)
	}
	if scan.skills[0] != "english-us-developer-style" || scan.skills[1] != "adr-authoring" {
		t.Fatalf("skills = %v", scan.skills)
	}
}

func TestScanCodexRolloutDedupsWithinFile(t *testing.T) {
	roll := codexExecLine("sed -n '1,10p' .agents/skills/adr-authoring/SKILL.md") +
		codexExecLine("sed -n '11,20p' .agents/skills/adr-authoring/SKILL.md")
	scan, _ := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 1 {
		t.Fatalf("skills = %v, want 1 (deduped)", scan.skills)
	}
}

func TestScanCodexRolloutIgnoresNonSkillReads(t *testing.T) {
	roll := codexExecLine("cat README.md") +
		codexExecLine("ls my-skills/foo/SKILL.md") // not a `skills/` dir boundary
	scan, _ := scanCodexRollout(strings.NewReader(roll), 0)
	if len(scan.skills) != 0 {
		t.Fatalf("skills = %v, want 0", scan.skills)
	}
}

func TestScanCodexRolloutHonorsOffset(t *testing.T) {
	meta := codexMetaLine("https://github.com/o/r")
	first := codexExecLine("sed -n '1,10p' .agents/skills/adr-authoring/SKILL.md")
	roll := meta + first
	// Start scanning after meta+first: the skill read is below the offset.
	scan, _ := scanCodexRollout(strings.NewReader(roll), int64(len(roll)))
	if len(scan.skills) != 0 {
		t.Fatalf("skills = %v, want 0 (below offset)", scan.skills)
	}
	if scan.repoRemote != "https://github.com/o/r" {
		t.Fatalf("repoRemote = %q, want it read despite the offset", scan.repoRemote)
	}
}

func writeRollout(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return p
}

func TestCodexTranscriptEventsReadsFile(t *testing.T) {
	roll := codexMetaLine("https://github.com/o/r") +
		codexExecLine("sed -n '1,220p' .agents/skills/adr-authoring/SKILL.md")
	path := writeRollout(t, roll)
	stdin, _ := json.Marshal(map[string]string{"session_id": "s1", "transcript_path": path})
	store := &OffsetStore{Dir: t.TempDir()}
	now := time.Unix(100, 0).UTC()

	events := codexTranscriptEvents(stdin, store, now)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "codex" || e.SessionID != "s1" || e.Skill != "adr-authoring" {
		t.Fatalf("event = %+v", e)
	}
	if e.RepoRemote != "https://github.com/o/r" {
		t.Fatalf("repoRemote = %q", e.RepoRemote)
	}
}

func TestCodexTranscriptEventsOffsetSkipsSeenReads(t *testing.T) {
	roll := codexMetaLine("https://github.com/o/r") +
		codexExecLine("sed -n '1,220p' .agents/skills/adr-authoring/SKILL.md")
	path := writeRollout(t, roll)
	stdin, _ := json.Marshal(map[string]string{"session_id": "s1", "transcript_path": path})
	store := &OffsetStore{Dir: t.TempDir()}

	first := codexTranscriptEvents(stdin, store, time.Unix(1, 0).UTC())
	if len(first) != 1 {
		t.Fatalf("first run got %d, want 1", len(first))
	}
	// Second run with no new lines: nothing re-emitted.
	second := codexTranscriptEvents(stdin, store, time.Unix(2, 0).UTC())
	if len(second) != 0 {
		t.Fatalf("second run got %d, want 0", len(second))
	}
	// Append a new skill read; only that one is emitted.
	extra := codexExecLine("cat .agents/skills/english-us-developer-style/SKILL.md")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(extra)
	_ = f.Close()

	third := codexTranscriptEvents(stdin, store, time.Unix(3, 0).UTC())
	if len(third) != 1 || third[0].Skill != "english-us-developer-style" {
		t.Fatalf("third run = %+v, want one english-us-developer-style", third)
	}
}

func TestCodexTranscriptEventsNoTranscriptPath(t *testing.T) {
	stdin := []byte(`{"session_id":"s1","last_assistant_message":"nothing"}`)
	if got := codexTranscriptEvents(stdin, &OffsetStore{Dir: t.TempDir()}, time.Now()); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestCodexTranscriptEventsMissingFile(t *testing.T) {
	stdin, _ := json.Marshal(map[string]string{"session_id": "s1", "transcript_path": "/no/such/rollout.jsonl"})
	if got := codexTranscriptEvents(stdin, &OffsetStore{Dir: t.TempDir()}, time.Now()); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
