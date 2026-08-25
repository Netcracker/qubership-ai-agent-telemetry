package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

func scanCursorSkills(r io.Reader, startOffset int64) ([]string, int64) {
	result := scanCursorTranscriptEvidence(r, startOffset)
	return result.Skills, result.End
}

func TestScanCursorTranscriptReadToolUse(t *testing.T) {
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"text","text":"reading skill"},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/adr-authoring/SKILL.md"}}` +
		`]}}` + "\n"
	skills, end := scanCursorSkills(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "adr-authoring" {
		t.Fatalf("skills = %v", skills)
	}
	if end != int64(len(line)) {
		t.Fatalf("end = %d, want %d", end, len(line))
	}
}

func TestScanCursorTranscriptReadFileSkillUse(t *testing.T) {
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"ReadFile","input":{"path":"/repo/.agents/skills/review/SKILL.md"}}` +
		`]}}` + "\n"

	skills, _ := scanCursorSkills(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "review" {
		t.Fatalf("skills = %v, want [review]", skills)
	}
}

func TestScanCursorTranscriptNestedSkillRead(t *testing.T) {
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/shipping/land-it/SKILL.md"}}` +
		`]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "land-it" {
		t.Fatalf("skills = %v, want [land-it]", skills)
	}
}

func TestScanCursorTranscriptManualAttach(t *testing.T) {
	line := `{"role":"user","message":{"content":[{"type":"text","text":` +
		`"<manually_attached_skills>\nSkill Name: telemetry-probe\nPath: /x/SKILL.md\n"}]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(line), 0)
	if len(skills) != 1 || skills[0] != "telemetry-probe" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptIgnoresNonSkillReadsAndDedups(t *testing.T) {
	lines := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/src/main.go"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(lines), 0)
	if len(skills) != 1 || skills[0] != "a" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptCollectsOperationPaths(t *testing.T) {
	lines := `{"role":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"Read","input":{"path":"/aggregate/repo-a/src/main.go"}},` +
		`{"type":"tool_use","name":"ApplyPatch","input":{"target_file":"/aggregate/repo-a/src/config.go"}},` +
		`{"type":"tool_use","name":"Shell","input":{"working_directory":"/aggregate/repo-a"}},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/aggregate/.cursor/skills/review/SKILL.md"}}` +
		`]}}` + "\n"

	result := scanCursorTranscriptEvidence(strings.NewReader(lines), 0)
	if len(result.Skills) != 1 || result.Skills[0] != "review" {
		t.Fatalf("skills = %v, want [review]", result.Skills)
	}
	want := []string{
		"/aggregate/repo-a/src/main.go",
		"/aggregate/repo-a/src/config.go",
		"/aggregate/repo-a",
	}
	if strings.Join(result.Paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %v, want %v", result.Paths, want)
	}
}

func TestScanCursorTranscriptBoundsOperationEvidence(t *testing.T) {
	var lines strings.Builder
	for i := 0; i <= maxCursorEvidencePaths; i++ {
		line, err := json.Marshal(map[string]any{
			"message": map[string]any{
				"content": []map[string]any{
					{"type": "tool_use", "name": "Read", "input": map[string]any{
						"path": filepath.Join("/workspace/repo", fmt.Sprintf("file-%d.go", i)),
					}},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(append(line, '\n'))
	}
	result := scanCursorTranscriptEvidence(strings.NewReader(lines.String()), 0)
	if len(result.Paths) != maxCursorEvidencePaths || !result.PathsTruncated {
		t.Fatalf("paths = %d, truncated = %v", len(result.Paths), result.PathsTruncated)
	}
}

func TestScanCursorTranscriptParsesOperationPathFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "cursor", "operation-paths.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	result := scanCursorTranscriptEvidence(strings.NewReader(string(body)), 0)
	if strings.Join(result.Skills, ",") != "review" {
		t.Fatalf("skills = %v, want [review]", result.Skills)
	}
	want := []string{
		"/workspace/repo/src/main.go",
		"/workspace/repo",
	}
	if strings.Join(result.Paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %v, want %v", result.Paths, want)
	}
}

func TestScanCursorTranscriptOnlyDetectsSkillsReadByReadTool(t *testing.T) {
	line := `{"role":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"ApplyPatch","input":{"path":"/aggregate/.cursor/skills/review/SKILL.md"}},` +
		`{"type":"tool_use","name":"Write","input":{"file_path":"/aggregate/.agents/skills/format/SKILL.md"}},` +
		`{"type":"tool_use","name":"Read","input":{"path":"/aggregate/.cursor/skills/validate/SKILL.md"}}` +
		`]}}` + "\n"

	result := scanCursorTranscriptEvidence(strings.NewReader(line), 0)
	if strings.Join(result.Skills, ",") != "validate" {
		t.Fatalf("skills = %v, want [validate]", result.Skills)
	}
	if len(result.Paths) != 0 {
		t.Fatalf("paths = %v, want no skill-body paths as operation evidence", result.Paths)
	}
}

func TestScanCursorTranscriptOffsetSkipsEarlyLines(t *testing.T) {
	first := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/old/SKILL.md"}}]}}` + "\n"
	second := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(first+second), int64(len(first)))
	if len(skills) != 1 || skills[0] != "new" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptSkipsMalformedLine(t *testing.T) {
	lines := "{not json\n" +
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/a/SKILL.md"}}]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(lines), 0)
	if len(skills) != 1 || skills[0] != "a" {
		t.Fatalf("skills = %v", skills)
	}
}

func TestScanCursorTranscriptManualAttachMultiple(t *testing.T) {
	line := `{"role":"user","message":{"content":[{"type":"text","text":` +
		`"<manually_attached_skills>\nSkill Name: alpha\nPath: /a\nSkill Name: beta\nPath: /b\n"}]}}` + "\n"
	skills, _ := scanCursorSkills(strings.NewReader(line), 0)
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
	skills, _ := scanCursorSkills(strings.NewReader(string(line)+"\n"), 0)
	if len(skills) != 1 || skills[0] != "ai-agent-telemetry-configure" {
		t.Fatalf("skills = %v, want [ai-agent-telemetry-configure]", skills)
	}
}

func TestCursorTranscriptEventsWithoutOperationEvidenceIsUnattributed(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/adr-authoring/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})
	events := cursorTranscriptEvents(stdin, nil, func(string) string {
		t.Fatal("remote resolver must not receive the aggregate workspace without operation evidence")
		return ""
	}, fixedTime)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "cursor" || e.SessionID != "c1" || skillName(t, e) != "adr-authoring" ||
		e.RepoRemote != "" || e.RepoDir != "" {
		t.Fatalf("event = %+v", e)
	}
	if !reflect.DeepEqual(e.PolicyPaths, []string{"/repo"}) {
		t.Fatalf("policy paths = %#v, want workspace root", e.PolicyPaths)
	}
}

func TestCursorTranscriptEventsInfersNestedRepository(t *testing.T) {
	aggregate := t.TempDir()
	repo := filepath.Join(aggregate, "repo-a")
	initCursorTestRepo(t, repo)

	transcript := filepath.Join(aggregate, "session.jsonl")
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(aggregate, ".cursor", "skills", "review", "SKILL.md"),
				}},
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(repo, "src", "main.go"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{aggregate}, "transcript_path": transcript,
	})
	events := cursorTranscriptEvents(stdin, nil, func(dir string) string {
		if dir != repo {
			t.Fatalf("remote resolved from %q, want %q", dir, repo)
		}
		return "git@github.com:Netcracker/repo-a.git"
	}, fixedTime)

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].RepoDir != repo {
		t.Fatalf("repo dir = %q, want %q", events[0].RepoDir, repo)
	}
	if events[0].RepoRemote != "git@github.com:Netcracker/repo-a.git" {
		t.Fatalf("repo remote = %q", events[0].RepoRemote)
	}
	filtered := filterEventsByPolicy(events, telemetryPolicy{
		RepoAllowList: []string{"github.com/Netcracker/*"},
	}, nil)
	if len(filtered) != 1 {
		t.Fatalf("filtered events = %d, want 1", len(filtered))
	}
	body, err := json.Marshal(filtered[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), aggregate) || strings.Contains(string(body), repo) {
		t.Fatalf("serialized event leaked local path: %s", body)
	}
}

func TestCursorTranscriptEventsWithMultipleRepositoriesIsUnattributed(t *testing.T) {
	aggregate := t.TempDir()
	repoA := filepath.Join(aggregate, "repo-a")
	repoB := filepath.Join(aggregate, "repo-b")
	initCursorTestRepo(t, repoA)
	initCursorTestRepo(t, repoB)

	transcript := filepath.Join(aggregate, "session.jsonl")
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(aggregate, ".cursor", "skills", "review", "SKILL.md"),
				}},
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(repoA, "src", "a.go"),
				}},
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(repoB, "src", "b.go"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{aggregate}, "transcript_path": transcript,
	})
	events := cursorTranscriptEvents(stdin, nil, func(directory string) string {
		switch directory {
		case repoA:
			return "git@github.com:Netcracker/repo-a.git"
		case repoB:
			return "git@github.com:Netcracker/repo-b.git"
		default:
			t.Fatalf("remote resolved from %q, want one of the repositories", directory)
			return ""
		}
	}, fixedTime)

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].RepoDir != "" || events[0].RepoRemote != "" {
		t.Fatalf("event repository = (%q, %q), want empty", events[0].RepoDir, events[0].RepoRemote)
	}
}

func TestCursorEvidenceRepoDistinguishesNestedRepositoriesInEitherOrder(t *testing.T) {
	outer := t.TempDir()
	nested := filepath.Join(outer, "nested")
	initCursorTestRepo(t, outer)
	initCursorTestRepo(t, nested)
	outerPath := filepath.Join(outer, "src", "outer.go")
	nestedPath := filepath.Join(nested, "src", "nested.go")

	for _, paths := range [][]string{
		{outerPath, nestedPath},
		{nestedPath, outerPath},
	} {
		if got := cursorEvidenceRepo(paths, []string{outer}, func(directory string) string {
			switch directory {
			case outer:
				return "git@github.com:Netcracker/outer.git"
			case nested:
				return "git@github.com:Netcracker/nested.git"
			default:
				t.Fatalf("remote resolved from %q", directory)
				return ""
			}
		}); got != "" {
			t.Fatalf("paths %v resolved to %q, want unattributed", paths, got)
		}
	}
}

func TestCursorTranscriptEventsGroupsRepositoriesWithSameNormalizedRemote(t *testing.T) {
	aggregate := t.TempDir()
	repoA := filepath.Join(aggregate, "repo-a")
	repoB := filepath.Join(aggregate, "repo-b")
	initCursorTestRepo(t, repoA)
	initCursorTestRepo(t, repoB)

	transcript := filepath.Join(aggregate, "session.jsonl")
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(aggregate, ".cursor", "skills", "review", "SKILL.md"),
				}},
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(repoA, "src", "a.go"),
				}},
				{"type": "tool_use", "name": "Read", "input": map[string]any{
					"path": filepath.Join(repoB, "src", "b.go"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{aggregate}, "transcript_path": transcript,
	})

	events := cursorTranscriptEvents(stdin, nil, func(directory string) string {
		switch directory {
		case repoA:
			return "git@github.com:Netcracker/repo.git"
		case repoB:
			return "https://github.com/netcracker/repo.git"
		default:
			t.Fatalf("remote resolved from %q, want either repository", directory)
			return ""
		}
	}, fixedTime)

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].RepoDir != repoA {
		t.Fatalf("repo dir = %q, want %q", events[0].RepoDir, repoA)
	}
	if events[0].RepoRemote != "git@github.com:Netcracker/repo.git" {
		t.Fatalf("repo remote = %q", events[0].RepoRemote)
	}
}

func initCursorTestRepo(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", directory).CombinedOutput(); err != nil {
		t.Fatalf("git init %q: %v\n%s", directory, err, output)
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
	if len(first1) != 1 || skillName(t, first1[0]) != "old" {
		t.Fatalf("first pass = %v", first1)
	}
	if again := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime); len(again) != 0 {
		t.Fatalf("second pass = %v, want 0", again)
	}
	f, err := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	third := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(third) != 1 || skillName(t, third[0]) != "new" {
		t.Fatalf("third pass = %v", third)
	}
}

func TestCursorTranscriptEventsMigratesLegacyParentOffset(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.jsonl")
	first := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/old/SKILL.md"}}]}}` + "\n"
	second := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(parent, []byte(first+second), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &OffsetStore{Dir: t.TempDir()}
	if err := store.Save("cursor:c1", int64(len(first))); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": parent,
	})

	events := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(events) != 1 || skillName(t, events[0]) != "new" {
		t.Fatalf("events = %#v, want only new skill", events)
	}
	if got := store.Load(cursorTranscriptOffsetKey("c1", parent)); got != int64(len(first+second)) {
		t.Fatalf("new offset = %d, want %d", got, len(first+second))
	}
}

func TestCursorTranscriptEventsRetriesPartialFinalLine(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	line := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/review/SKILL.md"}}]}}` + "\n"
	split := len(line) / 2
	if err := os.WriteFile(tp, []byte(line[:split]), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &OffsetStore{Dir: t.TempDir()}
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})

	if events := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime); len(events) != 0 {
		t.Fatalf("partial line events = %#v, want none", events)
	}
	f, err := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line[split:]); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	events := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(events) != 1 || skillName(t, events[0]) != "review" {
		t.Fatalf("completed line events = %#v, want one review skill", events)
	}
}

func TestCursorTranscriptEventsUsesIndependentOffsetsPerTranscript(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	for _, transcript := range []struct {
		path  string
		skill string
	}{
		{path: parent, skill: "skill-a"},
		{path: child, skill: "skill-b"},
	} {
		line := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/` + transcript.skill + `/SKILL.md"}}]}}` + "\n"
		if err := os.WriteFile(transcript.path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store := &OffsetStore{Dir: t.TempDir()}
	for _, path := range []string{parent, child} {
		stdin, err := json.Marshal(map[string]any{
			"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		events := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
		if len(events) != 1 {
			t.Fatalf("events for %s = %v, want one", path, events)
		}
	}
}

func TestCursorTranscriptEventsScansDirectSubagentTranscripts(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.jsonl")
	subagents := filepath.Join(dir, "subagents")
	child := filepath.Join(subagents, "worker.jsonl")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte(
		`{"message":{"content":[{"type":"tool_use","name":"Subagent","input":{"description":"review"}}]}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(
		`{"message":{"content":[{"type":"tool_use","name":"ReadFile","input":{"path":"/repo/.agents/skills/review/SKILL.md"}}]}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	stdin, err := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := cursorTranscriptEvents(stdin, &OffsetStore{Dir: t.TempDir()}, func(string) string { return "" }, fixedTime)
	if len(events) != 1 || skillName(t, events[0]) != "review" {
		t.Fatalf("events = %#v, want one review skill", events)
	}
}

func TestCursorTranscriptEventsKeepsSameSkillFromSeparateTranscripts(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.jsonl")
	subagents := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, transcript := range []string{
		parent,
		filepath.Join(subagents, "worker-a.jsonl"),
		filepath.Join(subagents, "worker-b.jsonl"),
	} {
		line := `{"message":{"content":[{"type":"tool_use","name":"ReadFile","input":{"path":"/repo/.agents/skills/review/SKILL.md"}}]}}` + "\n"
		if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stdin, err := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &OffsetStore{Dir: t.TempDir()}
	events := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(events) != 3 {
		t.Fatalf("events = %#v, want three review skill events", events)
	}
	if replay := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime); len(replay) != 0 {
		t.Fatalf("replay = %#v, want none", replay)
	}
}

func TestCursorSubagentTranscriptInfersNestedRepository(t *testing.T) {
	aggregate := t.TempDir()
	repo := filepath.Join(aggregate, "repo-a")
	initCursorTestRepo(t, repo)
	parent := filepath.Join(aggregate, "parent.jsonl")
	child := filepath.Join(aggregate, "subagents", "worker.jsonl")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "ReadFile", "input": map[string]any{
					"path": filepath.Join(aggregate, ".agents", "skills", "review", "SKILL.md"),
				}},
				{"type": "tool_use", "name": "ReadFile", "input": map[string]any{
					"path": filepath.Join(repo, "src", "main.go"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	stdin, err := json.Marshal(map[string]any{
		"session_id": "c1", "workspace_roots": []string{aggregate}, "transcript_path": parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := cursorTranscriptEvents(stdin, nil, func(directory string) string {
		if directory != repo {
			t.Fatalf("remote resolved from %q, want %q", directory, repo)
		}
		return "git@github.com:Netcracker/repo-a.git"
	}, fixedTime)
	if len(events) != 1 || events[0].RepoDir != repo || events[0].RepoRemote == "" {
		t.Fatalf("events = %#v, want attributed child skill", events)
	}
}
