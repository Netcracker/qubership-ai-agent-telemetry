# Cursor Skill-Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `cursor` adapter to the sender so skill use inside Cursor turns reaches the collector, hung off the `afterAgentResponse` hook with two signals (marker + transcript parse).

**Architecture:** A new `cursorAdapter` scans the hook payload `text` for the `[skill-called]` marker. A new `transcript_cursor.go` parses the JSONL at `transcript_path` for the two ways Cursor loads a skill: an automatic `Read` of `<root>/.cursor/skills/<name>/SKILL.md`, and a manual `<manually_attached_skills>` block. The `ingest` path merges both for `agent == "cursor"`, deduping by skill name. The repo remote comes from `workspace_roots[0]`, not the transcript. The spool, flush, `gitRemote`, `markerRe`, `mergeBySkill`, and `OffsetStore` are reused unchanged.

**Tech Stack:** Go (stdlib `encoding/json`, `regexp`, `bufio`), the existing sender in `sender/`, APM for hook deployment.

**Spec:** `docs/superpowers/specs/2026-06-16-cursor-skill-detection.md`

---

## File structure

| File | Responsibility | Change |
|---|---|---|
| `sender/adapter.go` | `cursorPayload`, `cursorAdapter`, `cursorRemote`, register `"cursor"` | Modify |
| `sender/transcript_cursor.go` | Parse the Cursor transcript for skill reads and manual attach blocks | Create |
| `sender/adapter_test.go` | Tests for `cursorAdapter` | Modify |
| `sender/transcript_cursor_test.go` | Tests for the transcript parser and offset behavior | Create |
| `sender/main.go` | Merge transcript events for `agent == "cursor"` in `ingest` | Modify |
| `agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-cursor-hooks.json` | The `afterAgentResponse` hook | Modify |
| `agent-packages/qubership-skills-telemetry/apm.yml` | Package version bump | Modify |
| `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh` and `.ps1` | `BINARY_VERSION` bump | Modify |
| `INSTALL.md` | Note the third harness | Modify |

---

## Task 1: Cursor adapter — marker scan

Decode the `afterAgentResponse` payload and emit one event per marker found in `text`. The repo remote is resolved from `workspace_roots[0]`.

**Files:**
- Modify: `sender/adapter.go`
- Test: `sender/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `sender/adapter_test.go`:

```go
func TestCursorAdapterParsesMarker(t *testing.T) {
	stdin := []byte(`{
		"session_id": "c1",
		"text": "ok\n[skill-called] skill=ops:deploy source=Netcracker/x\n",
		"workspace_roots": ["/repo"],
		"transcript_path": "/nope.jsonl"
	}`)
	events, err := Dispatch("cursor", stdin, func(cwd string) string {
		if cwd != "/repo" {
			t.Fatalf("resolver got cwd %q", cwd)
		}
		return "git@host:org/repo.git"
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "cursor" || e.SessionID != "c1" || e.Skill != "ops:deploy" ||
		e.Source != "Netcracker/x" || e.RepoRemote != "git@host:org/repo.git" {
		t.Fatalf("event = %+v", e)
	}
}

func TestCursorAdapterNoMarker(t *testing.T) {
	events, err := Dispatch("cursor", []byte(`{"text":"nothing here"}`), func(string) string { return "" })
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestCursorAdapterMalformedJSON(t *testing.T) {
	events, err := Dispatch("cursor", []byte(`{not json`), func(string) string { return "" })
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd sender && go test -run TestCursorAdapter ./...`
Expected: FAIL — `no adapter for agent "cursor"`.

- [ ] **Step 3: Add the payload, adapter, and registration**

In `sender/adapter.go`, add `"cursor": cursorAdapter,` to the `adapters` map:

```go
var adapters = map[string]Adapter{
	"codex":  codexAdapter,
	"claude": claudeAdapter,
	"cursor": cursorAdapter,
}
```

Add the payload type after `claudePayload`:

```go
// cursorPayload is the Cursor afterAgentResponse hook envelope. Only the fields
// the adapter needs are decoded; the rest (conversation_id, generation_id,
// model, token counts, cursor_version, user_email) are ignored. user_email is
// deliberately not collected: it is PII, and the project drops repo.path and
// turn.id for the same reason.
type cursorPayload struct {
	SessionID      string   `json:"session_id"`
	Text           string   `json:"text"`
	WorkspaceRoots []string `json:"workspace_roots"`
	TranscriptPath string   `json:"transcript_path"`
}
```

Add the adapter and the shared remote helper at the end of the file:

```go
// cursorAdapter scans the afterAgentResponse text for the marker. The hook fires
// once per agent response, so it may carry several markers. The transcript parse
// (transcript_cursor.go) is the second, deterministic signal merged in by ingest.
func cursorAdapter(stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error) {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	matches := markerRe.FindAllStringSubmatch(p.Text, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	rem := cursorRemote(p, remote)
	events := make([]SkillEvent, 0, len(matches))
	for _, m := range matches {
		events = append(events, SkillEvent{
			Agent:      "cursor",
			SessionID:  p.SessionID,
			RepoRemote: rem,
			Skill:      m[1],
			Source:     m[2],
			TS:         now,
		})
	}
	return events, nil
}

// cursorRemote resolves the git remote from the first workspace root. Cursor
// gives no git data in the transcript, so the remote always comes from the hook
// payload, for both the marker and the transcript signals.
func cursorRemote(p cursorPayload, remote remoteResolver) string {
	if remote == nil || len(p.WorkspaceRoots) == 0 || p.WorkspaceRoots[0] == "" {
		return ""
	}
	return remote(p.WorkspaceRoots[0])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd sender && go test -run TestCursorAdapter ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sender/adapter.go sender/adapter_test.go
git commit -m "feat(sender): add Cursor marker adapter

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Cursor transcript scanner — pure line parsing

Parse a single Cursor transcript line into skill names. Two shapes: an assistant `tool_use` named `Read` whose `input.path` ends in `.cursor/skills/<name>/SKILL.md`, and a user `text` content holding a `<manually_attached_skills>` block with `Skill Name: <name>` lines. This task builds the pure scanner with no file or offset I/O.

**Files:**
- Create: `sender/transcript_cursor.go`
- Create: `sender/transcript_cursor_test.go`

- [ ] **Step 1: Write the failing tests**

Create `sender/transcript_cursor_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd sender && go test -run TestScanCursorTranscript ./...`
Expected: FAIL — `undefined: scanCursorTranscript`.

- [ ] **Step 3: Write the scanner**

Create `sender/transcript_cursor.go`:

```go
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

// cursorSkillReadRe matches the Read tool input.path of a skill body. The path
// is absolute, ending in .cursor/skills/<name>/SKILL.md. The character before
// `.cursor` must be a separator (or start of string) so a path like
// `my.cursor/...` does not match.
var cursorSkillReadRe = regexp.MustCompile(`(?:^|/)\.cursor/skills/([^/]+)/SKILL\.md`)

// cursorManualSkillRe matches a `Skill Name: <name>` line inside the
// <manually_attached_skills> block Cursor inlines on a manual /skill-name call.
var cursorManualSkillRe = regexp.MustCompile(`(?m)^Skill Name:\s*(\S+)`)

// scanCursorTranscript streams a Cursor transcript and returns the skill names
// read at or beyond startOffset, in order and deduped, plus the end-of-file byte
// offset to persist as the next offset. Unlike Codex, the repo remote is not in
// the transcript, so it is resolved from the hook payload instead.
func scanCursorTranscript(r io.Reader, startOffset int64) ([]string, int64) {
	var skills []string
	seen := map[string]bool{}
	br := bufio.NewReader(r)
	var pos int64
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			lineStart := pos
			pos += int64(len(line))
			if lineStart >= startOffset {
				processCursorLine(line, &skills, seen)
			}
		}
		if err != nil {
			break
		}
	}
	return skills, pos
}

func processCursorLine(line string, skills *[]string, seen map[string]bool) {
	var env struct {
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Text  string `json:"text"`
				Input struct {
					Path string `json:"path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return
	}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			*skills = append(*skills, name)
		}
	}
	for _, c := range env.Message.Content {
		switch c.Type {
		case "tool_use":
			if c.Name != "Read" {
				continue
			}
			for _, m := range cursorSkillReadRe.FindAllStringSubmatch(c.Input.Path, -1) {
				add(m[1])
			}
		case "text":
			if !strings.Contains(c.Text, "<manually_attached_skills>") {
				continue
			}
			for _, m := range cursorManualSkillRe.FindAllStringSubmatch(c.Text, -1) {
				add(m[1])
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd sender && go test -run TestScanCursorTranscript ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sender/transcript_cursor.go sender/transcript_cursor_test.go
git commit -m "feat(sender): parse Cursor transcript for skill reads

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Cursor transcript events — file open, offset, and remote

Wrap the scanner so it opens `transcript_path`, applies the per-session offset (reused `OffsetStore`, key `cursor:<session>`), resolves the remote from `workspace_roots[0]`, and returns events. Mirror `codexTranscriptEvents` / `codexTranscriptEventsAuto`.

**Files:**
- Modify: `sender/transcript_cursor.go`
- Modify: `sender/transcript_cursor_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `sender/transcript_cursor_test.go`:

```go
import (
	"os"            // add to the existing import block
	"path/filepath" // add to the existing import block
)

func TestCursorTranscriptEventsReadsFileAndResolvesRemote(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/adr-authoring/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := []byte(`{"session_id":"c1","workspace_roots":["/repo"],"transcript_path":"` + tp + `"}`)
	events := cursorTranscriptEvents(stdin, nil, func(string) string { return "git@host:org/repo.git" }, fixedTime)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "cursor" || e.SessionID != "c1" || e.Skill != "adr-authoring" ||
		e.RepoRemote != "git@host:org/repo.git" || e.Source != "" {
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
	stdin := []byte(`{"session_id":"c1","workspace_roots":["/repo"],"transcript_path":"` + tp + `"}`)

	first1 := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(first1) != 1 || first1[0].Skill != "old" {
		t.Fatalf("first pass = %v", first1)
	}
	// Second run with no new bytes returns nothing.
	if again := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime); len(again) != 0 {
		t.Fatalf("second pass = %v, want 0", again)
	}
	// Append a new skill read; only the new one is emitted.
	f, _ := os.OpenFile(tp, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/new/SKILL.md"}}]}}` + "\n")
	f.Close()
	third := cursorTranscriptEvents(stdin, store, func(string) string { return "" }, fixedTime)
	if len(third) != 1 || third[0].Skill != "new" {
		t.Fatalf("third pass = %v", third)
	}
}
```

Note: `fixedTime` is the shared test timestamp. If it is not already declared in the package's test files, add this once at the top of `sender/transcript_cursor_test.go`:

```go
var fixedTime = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC) // add "time" to imports
```

Before adding it, check it is not already declared elsewhere in the `sender` test files:

Run: `cd sender && grep -rn "fixedTime" *_test.go`
If it already exists, reuse it and do not redeclare.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd sender && go test -run TestCursorTranscriptEvents ./...`
Expected: FAIL — `undefined: cursorTranscriptEvents`.

- [ ] **Step 3: Add the file/offset/remote wrapper**

Append to `sender/transcript_cursor.go` (add `"fmt"`, `"os"`, and `"time"` to the import block):

```go
// cursorTranscriptEvents reads the transcript named by transcript_path and
// returns one event per skill read since the last run. It never fails the
// caller: any problem yields zero events. When offsets is non-nil and the
// payload carries a session id, only reads beyond the stored byte offset are
// emitted, and the offset advances to the end of the file. The remote is
// resolved from workspace_roots, since the transcript carries no git data.
func cursorTranscriptEvents(stdin []byte, offsets *OffsetStore, remote remoteResolver, now time.Time) []SkillEvent {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	if p.TranscriptPath == "" {
		return nil
	}
	f, err := os.Open(p.TranscriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var offset int64
	key := "cursor:" + p.SessionID
	useOffset := offsets != nil && p.SessionID != ""
	if useOffset {
		offset = offsets.Load(key)
		if fi, serr := f.Stat(); serr == nil && offset > fi.Size() {
			offset = 0 // file rotated or truncated since the last run
		}
	}

	skills, end := scanCursorTranscript(f, offset)

	if useOffset {
		_ = offsets.Save(key, end)
	}

	rem := cursorRemote(p, remote)
	events := make([]SkillEvent, 0, len(skills))
	for _, name := range skills {
		events = append(events, SkillEvent{
			Agent:      "cursor",
			SessionID:  p.SessionID,
			RepoRemote: rem,
			Skill:      name,
			TS:         now,
		})
	}
	return events
}

// cursorTranscriptEventsAuto wires cursorTranscriptEvents to the default offset
// store. It skips building the store unless the payload names a transcript.
func cursorTranscriptEventsAuto(stdin []byte, remote remoteResolver, now time.Time) []SkillEvent {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	if p.TranscriptPath == "" {
		return nil
	}
	store, err := DefaultOffsetStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "offset:", err)
		store = nil
	}
	return cursorTranscriptEvents(stdin, store, remote, now)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd sender && go test -run TestCursorTranscript ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sender/transcript_cursor.go sender/transcript_cursor_test.go
git commit -m "feat(sender): wire Cursor transcript offset and remote

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Merge both signals in ingest

For `agent == "cursor"`, merge the transcript events with the marker events, deduping by skill name. Mirror the existing Codex branch.

**Files:**
- Modify: `sender/main.go:179-184`
- Test: `sender/ingest_test.go`

- [ ] **Step 1: Write the failing test**

First read the existing `ingest_test.go` to match its setup helpers:

Run: `cd sender && grep -n "func Test\|func newTestSpool\|t.Setenv\|ingest(" ingest_test.go`

Then append a test that mirrors the Codex two-signal ingest test (use the same spool/temp-dir helpers the file already uses — substitute `<spool-setup>` with the existing pattern, e.g. a `Spool` rooted in `t.TempDir()`):

```go
func TestIngestCursorMergesMarkerAndTranscript(t *testing.T) {
	// Isolate config/cache dirs so the real machine state is untouched.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	// Transcript reads skill "from-transcript"; marker reports "from-marker".
	body := `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/from-transcript/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := []byte(`{"session_id":"c1","workspace_roots":["/repo"],` +
		`"text":"[skill-called] skill=from-marker source=Netcracker/x",` +
		`"transcript_path":"` + tp + `"}`)

	s := <spool-setup>            // reuse the helper this file already uses
	ingest(s, "cursor", "", stdin, func(string) string { return "" })

	names := s.List()             // adapt to however the file inspects spooled events
	// Expect two distinct skills: from-marker (signal 1) and from-transcript (signal 2).
	if len(names) != 2 {
		t.Fatalf("spooled %d events, want 2", len(names))
	}
}
```

Note: match the exact spool construction and inspection the existing tests use. The behavioral assertion that matters: a marker skill and a transcript-only skill both land, i.e. `ingest` calls `mergeBySkill` for `cursor`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd sender && go test -run TestIngestCursorMerges ./...`
Expected: FAIL — only the marker skill is spooled (one event), because the cursor branch does not yet merge the transcript.

- [ ] **Step 3: Add the cursor merge branch**

In `sender/main.go`, after the existing Codex branch (around line 182-184), add:

```go
	if agent == "codex" {
		events = mergeBySkill(events, codexTranscriptEventsAuto(stdin, time.Now().UTC()))
	}
	if agent == "cursor" {
		events = mergeBySkill(events, cursorTranscriptEventsAuto(stdin, remote, time.Now().UTC()))
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd sender && go test -run TestIngestCursorMerges ./...`
Expected: PASS.

- [ ] **Step 5: Full suite, vet, fmt, build**

Run:
```bash
cd sender && gofmt -l . && go vet ./... && go test -race ./... && go build ./...
```
Expected: `gofmt -l` prints nothing; vet clean; all tests PASS; build succeeds.

- [ ] **Step 6: Commit**

```bash
git add sender/main.go sender/ingest_test.go
git commit -m "feat(sender): merge Cursor marker and transcript signals

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Fill the Cursor hook, after confirming the APM target

The hook fires `ingest --agent=cursor` once per agent response. The open question is how APM maps `skill-call-cursor-hooks.json` onto Cursor's native `.cursor/hooks.json` (which needs `version: 1` and `{ "command": "..." }` entries). Resolve this before writing the final file.

**Files:**
- Modify: `agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-cursor-hooks.json`

- [ ] **Step 1: Check whether APM supports a Cursor target**

Run:
```bash
apm --version
apm install --help 2>&1 | grep -i target
apm install --target cursor --help 2>&1 | head -20
```
Determine: does `--target cursor` exist, and what native file does it write (expected `<root>/.cursor/hooks.json`)?

- [ ] **Step 2: Branch on the result**

**Branch A — APM supports `--target cursor` and uses the same `skill-call-<target>-hooks.json` convention as Codex/Claude.** Write the hook in the same APM wrapper shape the other two use, with the Cursor event name:

```json
{
  "hooks": {
    "afterAgentResponse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "sh ./scripts/bootstrap.sh ingest --agent=cursor",
            "timeout": 30,
            "statusMessage": "Recording skill telemetry"
          }
        ]
      }
    ]
  }
}
```

Then run a local install into this repo and confirm the generated `.cursor/hooks.json` has `version: 1`, the `afterAgentResponse` event, and an absolute path to the deployed bootstrap. If APM does not inject `version: 1` or flattens the shape wrongly, fall back to Branch B.

**Branch B — APM does not support Cursor, or does not produce a valid `.cursor/hooks.json`.** Ship the native file directly in the package and document a manual copy step (the consent boundary is still committing it into the target repo). Create `agent-packages/qubership-skills-telemetry/.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "afterAgentResponse": [
      { "command": "sh ./scripts/bootstrap.sh ingest --agent=cursor" }
    ]
  }
}
```

Record which branch was taken in the spec's "Open questions" section, replacing the open item with the resolved fact.

- [ ] **Step 3: Commit**

```bash
git add agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-cursor-hooks.json docs/superpowers/specs/2026-06-16-cursor-skill-detection.md
# Branch B also: git add agent-packages/qubership-skills-telemetry/.cursor/hooks.json
git commit -m "feat(package): wire Cursor afterAgentResponse hook

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Version bumps and INSTALL.md

A new harness adapter is a new feature, so bump the binary minor version and the package version, and note the third harness in the install doc.

**Files:**
- Modify: `agent-packages/qubership-skills-telemetry/apm.yml`
- Modify: `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh`
- Modify: `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1`
- Modify: `INSTALL.md`

- [ ] **Step 1: Read the current versions**

Run:
```bash
grep -n "version" agent-packages/qubership-skills-telemetry/apm.yml | head
grep -n "BINARY_VERSION\|BinaryVersion" agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1
```
Expected current: package `1.4.0`, `BINARY_VERSION=v0.4.0`.

- [ ] **Step 2: Bump**

- `apm.yml`: `1.4.0` → `1.5.0`.
- `bootstrap.sh`: `BINARY_VERSION="v0.4.0"` → `BINARY_VERSION="v0.5.0"`.
- `bootstrap.ps1`: `$BinaryVersion = "v0.4.0"` → `"v0.5.0"`.

- [ ] **Step 3: Update INSTALL.md**

Add Cursor to the harness list and the `ingest` row so it covers all three harnesses (Codex, Claude, Cursor). Mirror the existing wording for the other two; one line for the Cursor `--agent=cursor` hook.

- [ ] **Step 4: Commit**

```bash
git add agent-packages/qubership-skills-telemetry/apm.yml \
        agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh \
        agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1 \
        INSTALL.md
git commit -m "chore(release): bump to v0.5.0 for the Cursor adapter

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Live verification (manual, after release)

The Go path is unit-tested; this confirms the real Cursor delivery path and two assumptions the unit tests cannot: that the transcript is append-only (so byte offsets are valid), and that the deployed hook fires.

- [ ] **Step 1: Confirm the transcript is append-only**

In a real Cursor session in a repo with the package installed, invoke a skill, note the transcript size, invoke another skill, and confirm the file only grew and the earlier bytes are unchanged:

```bash
TP=~/.cursor/projects/<project>/agent-transcripts/<id>/<id>.jsonl
wc -c "$TP"           # before second skill
# ...invoke another skill in Cursor...
wc -c "$TP"           # after — must be larger, never smaller
```
If the file is rewritten rather than appended (size shrinks or early bytes change), the offset approach over-emits; switch the offset key strategy to a persisted `(session, skill)` dedup set as a follow-up and note it in the spec.

- [ ] **Step 2: Confirm the event reaches the collector**

After a real skill invocation in Cursor, query the collector (reuse the field-filter query from the other harnesses):
```
_time:10m agent:=cursor
```
Expected: a record with `agent=cursor`, the skill name, `repo.remote` resolved from the workspace root, a UUID `session.id`, and `service.version=v0.5.0`. Confirm `user_email` is absent.

- [ ] **Step 3: Record the outcome**

Update the spec's "Open questions" and "Limitations" with the verified append-only result and the confirmed APM/hook behavior.

---

## Self-review notes

- **Spec coverage:** marker signal (Task 1), transcript parse for both activation modes (Task 2), offset and remote (Task 3), two-signal merge (Task 4), the `afterAgentResponse` hook and APM mapping (Task 5), `user_email` dropped (Task 1 payload comment + Task 7 check), reuse of `OffsetStore`/`mergeBySkill`/`markerRe` (Tasks 2-4), open questions on APM target and append-only (Tasks 5, 7).
- **Type consistency:** `cursorPayload`, `cursorAdapter`, `cursorRemote`, `scanCursorTranscript`, `processCursorLine`, `cursorTranscriptEvents`, `cursorTranscriptEventsAuto` are named identically across all tasks; the transcript reads `input.path` (transcript field), distinct from the hook payload's `tool_input.file_path`.
- **Known soft spot:** the offset strategy assumes an append-only transcript; Task 7 verifies it and names the fallback. The `ingest` test (Task 4) must adapt to the existing `ingest_test.go` spool helpers rather than invent new ones.
