# CLI refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drop the retired `[skill-called]` marker from the Go CLI, consolidate its 17 source files into 8 by domain, and rename the component off "sender".

**Architecture:** Three coupled changes done in order. Removing the marker makes the session transcript the sole skill-detection signal for Codex and Cursor, which collapses the marker adapters, the merge step, and the per-agent branching in `ingest`. The file consolidation then groups the now-smaller surface into eight domain files in a flat `package main`. The rename moves `sender/` to `cli/`, renames the `service.name` resource attribute, and renames the `Spool` type to `Outbox`.

**Tech Stack:** Go (`package main`, module `skills-telemetry`), `go test`, OpenTelemetry OTLP/HTTP log export.

## Global Constraints

- Stay in a flat `package main`. No sub-packages.
- All committed text is American English (per the repo and user style rules).
- The on-disk config dir name `qubership-skills-telemetry` (the `pkgName` constant) must not change — it is the install path.
- Never edit files under `docs/superpowers/` — they are dated archive snapshots.
- A hook must never fail the agent turn: `ingest` returns 0 even on error.
- Run every Go command from the CLI directory (`sender/` until Task 8 renames it to `cli/`).
- After each task: `go test ./...` and `go vet ./...` are clean before commit.

---

### Task 1: Stop the adr-authoring fixture from emitting the marker

The instrumented test skill emits `[skill-called]` on activation. After the marker is gone, the fixture must stop emitting it. This is a docs/fixture-only change with no Go build.

**Files:**
- Modify: `agent-packages/adr-authoring/.apm/skills/adr-authoring/SKILL.md:8-13`
- Modify: `agent-packages/adr-authoring/README.md:15-20`

- [ ] **Step 1: Remove the marker block from SKILL.md**

Delete lines 8–13 (the "Mandatory! Emit this line first…" instruction and its fenced marker block):

```
Mandatory! Emit this line first, before any other text:

```
[skill-called] skill=adr-authoring source=denifilatoff/skills-telemetry/agent-packages/adr-authoring
```

```

The file should flow from the `# Authoring an ADR` heading straight into "You already know the ADR genre…".

- [ ] **Step 2: Rewrite the README "Note" section**

Replace the `## Note` section (lines 15–20) with a description that no longer claims a marker is emitted:

```markdown
## Note

This package doubles as the instrumented test skill for `qubership-skills-telemetry`.
The CLI detects its `SKILL.md` activation from the session transcript — so the
package also proves the pipeline detects an arbitrary third-party skill, not only
the CLI's own self-test.
```

- [ ] **Step 3: Verify no marker remains in the fixture**

Run: `grep -rn "skill-called" agent-packages/`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add agent-packages/adr-authoring/
git commit -m "refactor(fixture): stop adr-authoring emitting the [skill-called] marker"
```

---

### Task 2: Remove the marker from the CLI code

Delete the marker regex and the Codex/Cursor marker adapters, route both harnesses to their transcript parsers, drop the merge step and the `Source` field, and rename the unrelated flush-throttle "marker" so the two concepts no longer collide. The marker removal is compile-coupled across several files, so the code and its tests change together and the task verifies with one green `go test ./...`.

**Files:**
- Modify → rename: `sender/adapter.go` → `sender/detect.go`
- Modify → rename: `sender/adapter_test.go` → `sender/detect_test.go`
- Modify: `sender/transcript_codex.go` (delete `mergeBySkill`)
- Modify: `sender/event.go` (drop `Source`)
- Modify: `sender/event_test.go` (drop `Source`)
- Modify: `sender/flush.go:123` (drop `skill.source` attribute)
- Modify: `sender/main.go` (simplify `ingest`; rename throttle helpers)
- Modify: `sender/spool.go:15` (rename `markerName` → `flushStampName`)
- Modify: `sender/ingest_test.go` (transcript payloads; renamed throttle)

**Interfaces:**
- Produces: `detect(agent string, stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error)` — the single detection entry point, replacing `Dispatch`.
- Produces: `flushStampName` (const, was `markerName`), `touchFlushStamp(s *Spool)` (was `touchMarker`).
- Consumes: existing `claudeAdapter`, `codexTranscriptEventsAuto(stdin []byte, now time.Time) []SkillEvent`, `cursorTranscriptEventsAuto(stdin []byte, remote remoteResolver, now time.Time) []SkillEvent`.

- [ ] **Step 1: Rewrite the detection tests for the new behavior**

`git mv sender/adapter_test.go sender/detect_test.go`, then replace its contents. Keep the Claude tests (drop the `Source` assertion — the field is gone), replace `Dispatch` with `detect(..., time.Now().UTC())`, drop every marker test, and add transcript-routing tests for Codex and Cursor:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectClaudeParsesSkillTool(t *testing.T) {
	stdin := []byte(`{"session_id":"6a35f862","cwd":"/repo","tool_name":"Skill","tool_input":{"skill":"superpowers:brainstorming"}}`)
	events, err := detect("claude", stdin, func(cwd string) string {
		if cwd != "/repo" {
			t.Fatalf("resolver got cwd %q", cwd)
		}
		return "git@host:org/repo.git"
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Agent != "claude" || e.SessionID != "6a35f862" || e.Skill != "superpowers:brainstorming" {
		t.Fatalf("event = %+v", e)
	}
	if e.RepoRemote != "git@host:org/repo.git" {
		t.Fatalf("remote = %q", e.RepoRemote)
	}
}

func TestDetectClaudeIgnoresOtherTools(t *testing.T) {
	events, err := detect("claude", []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestDetectClaudeMalformedJSON(t *testing.T) {
	events, err := detect("claude", []byte(`{not json`), func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestDetectUnknownAgent(t *testing.T) {
	if _, err := detect("nope", []byte(`{}`), func(string) string { return "" }, time.Now().UTC()); err == nil {
		t.Fatal("want error for unknown agent")
	}
}

func TestDetectCodexFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@host:o/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := []byte(`{"session_id":"s1","transcript_path":"` + tp + `"}`)
	events, err := detect("codex", stdin, func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || events[0].Agent != "codex" || events[0].Skill != "demo" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].RepoRemote != "git@host:o/r.git" {
		t.Fatalf("remote = %q", events[0].RepoRemote)
	}
}

func TestDetectCursorFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tp := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/demo/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := []byte(`{"session_id":"c1","workspace_roots":["/repo"],"transcript_path":"` + tp + `"}`)
	events, err := detect("cursor", stdin, func(string) string { return "git@host:o/r.git" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || events[0].Agent != "cursor" || events[0].Skill != "demo" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].RepoRemote != "git@host:o/r.git" {
		t.Fatalf("remote = %q", events[0].RepoRemote)
	}
}
```

- [ ] **Step 2: Rewrite adapter.go as detect.go**

`git mv sender/adapter.go sender/detect.go`, then replace its contents. Drop `markerRe`, `codexAdapter`, `cursorAdapter`, the `Adapter` type, the `adapters` map, and `Dispatch`. Drop `regexp` from the imports and the now-unused `LastAssistantMessage` (codex) and `Text` (cursor) payload fields. Keep `claudeAdapter`, `cursorRemote`, and the three payload structs. Add `detect`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// remoteResolver returns the git remote URL for a working dir, or "" if unknown.
// Injected so detectors stay pure and testable.
type remoteResolver func(cwd string) string

// detect routes a raw hook payload to the per-harness detector. Claude Code
// emits a native Skill-tool event; Codex and Cursor are detected from the
// session transcript.
func detect(agent string, stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error) {
	switch agent {
	case "claude":
		return claudeAdapter(stdin, remote, now)
	case "codex":
		return codexTranscriptEventsAuto(stdin, now), nil
	case "cursor":
		return cursorTranscriptEventsAuto(stdin, remote, now), nil
	default:
		return nil, fmt.Errorf("no detector for agent %q", agent)
	}
}
```

Keep the rest of the file as: `codexPayload` (fields `SessionID`, `Cwd`, `TranscriptPath`), `claudePayload` and `claudeAdapter` unchanged, `cursorPayload` (fields `SessionID`, `WorkspaceRoots`, `TranscriptPath`) and `cursorRemote` unchanged.

- [ ] **Step 3: Delete mergeBySkill from transcript_codex.go**

Remove the `mergeBySkill` function (the last function in `sender/transcript_codex.go`, with its doc comment). Nothing else in that file changes.

- [ ] **Step 4: Drop the Source field from the event**

In `sender/event.go`, delete the `Source string` line from `SkillEvent`. In `sender/event_test.go`, delete the `Source: "Netcracker/qubership-ai-packages",` line from the round-trip fixture.

- [ ] **Step 5: Drop the skill.source attribute from flush**

In `sender/flush.go`, delete line 123: `otellog.String("skill.source", ev.Source),`.

- [ ] **Step 6: Simplify ingest and rename the throttle marker**

In `sender/main.go`, replace the body of `ingest` so it calls `detect` once and drops the per-agent branches and the merge:

```go
func ingest(s *Spool, agent, endpoint string, stdin []byte, remote remoteResolver) int {
	events, err := detect(agent, stdin, remote, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 0
	}
	for _, ev := range events {
		if err := s.Enqueue(ev); err != nil {
			fmt.Fprintln(os.Stderr, "enqueue:", err)
		}
	}
	if _, err := s.Rotate(bufferCap); err != nil {
		fmt.Fprintln(os.Stderr, "rotate:", err)
	}
	if shouldFlush(s, flushCountN, flushIntervalT) {
		touchFlushStamp(s)
		tlsCfg, cerr := caTLSConfig(pkgConfigDir())
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "ca:", cerr)
		}
		if _, err := Flush(s, endpoint, resolveToken(), tlsCfg, flushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
	}
	return 0
}
```

In the same file, rename `touchMarker` to `touchFlushStamp` and update its doc comment and the `markerName` references inside it and inside `shouldFlush`. In `sender/spool.go`, rename the const: `const flushStampName = ".lastflush"` and update the comment on `List` that mentions "the throttle marker".

- [ ] **Step 7: Update ingest_test.go for transcript payloads and the renamed throttle**

In `sender/ingest_test.go`:
- `TestIngestEnqueuesAndFlushes`: replace the `last_assistant_message` marker stdin with a Codex transcript payload. Write a rollout file and pass its path:

```go
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@host:o/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := []byte(`{"session_id":"s1","transcript_path":"` + tp + `"}`)
```

  and change the final stat to `flushStampName`.
- `TestIngestCursorMergesMarkerAndTranscript`: rename to `TestIngestCursorFromTranscript`, drop the marker from the stdin (keep only `session_id`, `workspace_roots`, `transcript_path`), and assert `len(files) == 1` (one skill, transcript only).
- `TestShouldFlushThrottle`: replace `touchMarker(s)` with `touchFlushStamp(s)`.

- [ ] **Step 8: Run the full test suite**

Run: `cd sender && go test ./... && go vet ./...`
Expected: PASS, vet clean.

- [ ] **Step 9: Commit**

```bash
git add sender/ agent-packages/
git commit -m "refactor(cli): drop the [skill-called] marker, transcript is the sole Codex/Cursor signal"
```

---

### Task 3: Consolidate config resolution into config.go

Merge the six smallest source files — endpoint, env file, token, CA, paths, machine — into one `config.go`, and their tests into `config_test.go`. Pure move: no behavior change.

**Files:**
- Create: `sender/config.go` (from `endpoint.go` + `envfile.go` + `token.go` + `ca.go` + `paths.go` + `machine.go`)
- Create: `sender/config_test.go` (from `endpoint_test.go` + `envfile_test.go` + `token_test.go` + `ca_test.go` + `machine_test.go`)
- Delete: the twelve source/test files above.

- [ ] **Step 1: Build config.go from the six files**

Assemble manually: start `config.go` with `package main`, a single merged `import (...)` block (the union is `crypto/rand`, `crypto/tls`, `crypto/x509`, `fmt`, `os`, `path/filepath`, `strings` — confirm by reading the `import` block of each of the six files before pasting), then paste the function/const/type bodies in this order: `pkgName`, `pkgConfigDir`, `pkgConfigPath`, `pkgEnv` (paths.go); `resolveEndpoint`, `resolveEndpointFrom` (endpoint.go); `parseEnv`, `loadEnvFile` (envfile.go); `resolveToken`, `resolveTokenFrom` (token.go); `caFileName`, `copyCAFile`, `caTLSConfig` (ca.go); `resolveMachineID`, `resolveMachineIDFrom`, `newUUID` (machine.go). Drop each file's own `package main` and `import` lines. While pasting `ca.go`, fix its comment "the well-known name the sender auto-discovers" → "the well-known name the CLI auto-discovers".

- [ ] **Step 2: Build config_test.go from the five test files**

Same mechanic: `package main`, one merged import block, then the bodies of `endpoint_test.go`, `envfile_test.go`, `token_test.go`, `ca_test.go`, `machine_test.go`. (There is no `paths_test.go`.)

- [ ] **Step 3: Delete the originals**

```bash
cd sender
git rm endpoint.go envfile.go token.go ca.go paths.go machine.go \
       endpoint_test.go envfile_test.go token_test.go ca_test.go machine_test.go
git add config.go config_test.go
```

- [ ] **Step 4: Verify gofmt, vet, and tests**

Run: `cd sender && gofmt -l config.go config_test.go && go vet ./... && go test ./...`
Expected: `gofmt -l` prints nothing (files are formatted), vet clean, tests PASS.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(cli): consolidate endpoint, envfile, token, ca, paths, machine into config.go"
```

---

### Task 4: Consolidate storage into outbox.go and rename Spool → Outbox

Merge the event type, the disk-backed queue, and the offset store into `outbox.go`, and rename the `Spool` type, its constructor, and its on-disk directory to `Outbox` / `outbox`. The rename ripples to every file that references `*Spool` (`flush.go`, `main.go`, `selftest.go`, `status.go`), so it lands in this task as a global rename followed by the merge.

**Files:**
- Create: `sender/outbox.go` (from `event.go` + `spool.go` + `offset.go`, with the rename applied)
- Create: `sender/outbox_test.go` (from `event_test.go` + `spool_test.go` + `offset_test.go`)
- Modify: `sender/flush.go`, `sender/main.go`, `sender/selftest.go`, `sender/status.go` (and their tests) — `Spool` → `Outbox`, `DefaultSpool` → `DefaultOutbox`, `lockSpool` → `lockOutbox`
- Delete: `event.go`, `spool.go`, `offset.go` and their tests.

**Interfaces:**
- Produces: `type Outbox struct { Dir string }` (was `Spool`), `DefaultOutbox() (*Outbox, error)` (was `DefaultSpool`). All methods (`Enqueue`, `List`, `Read`, `Remove`, `Rotate`) keep their names with the receiver retyped to `*Outbox`.
- Note: the on-disk sub-directory changes from `spool` to `outbox`; the `.flush.lock` and `flushStampName` files move with it. Existing unsent events in the old `spool` dir are orphaned — acceptable for best-effort telemetry.

- [ ] **Step 1: Rename the identifiers across the package**

```bash
cd sender
grep -rl 'Spool\|DefaultSpool\|lockSpool' *.go | xargs sed -i '' \
  -e 's/DefaultSpool/DefaultOutbox/g' \
  -e 's/lockSpool/lockOutbox/g' \
  -e 's/\bSpool\b/Outbox/g'
```

Then change the on-disk dir in `spool.go`'s `DefaultOutbox`: `filepath.Join(base, "qubership-skills-telemetry", "spool")` → `"outbox"`. Update the `Outbox` type doc comment ("a machine-global directory holding one JSON file per buffered event") to read naturally. Fix `offset.go`'s comment "beside the spool" → "beside the outbox".

- [ ] **Step 2: Verify the rename compiles and is green**

Run: `cd sender && go test ./...`
Expected: PASS (rename only, no behavior change).

- [ ] **Step 3: Merge the three files into outbox.go**

Assemble `sender/outbox.go`: `package main`, one merged import block (union of `crypto/rand`, `encoding/hex`, `encoding/json`, `fmt`, `os`, `path/filepath`, `sort`, `strconv`, `strings`, `time` — verify against the three files), then the bodies in order: `SkillEvent` + its `MarshalJSON`/`UnmarshalJSON` (event.go); `flushStampName` const, `Outbox` type, `DefaultOutbox`, `Enqueue`, `List`, `Read`, `Remove`, `Rotate`, `randHex` (spool.go); `OffsetStore` type, `DefaultOffsetStore`, `path`, `Load`, `Save` (offset.go). Build `outbox_test.go` the same way from `event_test.go` + `spool_test.go` + `offset_test.go`.

- [ ] **Step 4: Delete the originals**

```bash
cd sender
git rm event.go spool.go offset.go event_test.go spool_test.go offset_test.go
git add outbox.go outbox_test.go flush.go main.go selftest.go status.go selftest_test.go status_test.go flush_test.go
```

- [ ] **Step 5: Verify gofmt, vet, and tests**

Run: `cd sender && gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l` prints nothing, vet clean, tests PASS.

- [ ] **Step 6: Commit**

```bash
git commit -m "refactor(cli): consolidate event, spool, offset into outbox.go; rename Spool to Outbox"
```

---

### Task 5: Consolidate the command handlers into commands.go

Merge the provision, selftest, and status handlers into one `commands.go`, and their tests into `commands_test.go`. Pure move.

**Files:**
- Create: `sender/commands.go` (from `provision.go` + `selftest.go` + `status.go`)
- Create: `sender/commands_test.go` (from `provision_test.go` + `selftest_test.go` + `status_test.go`)
- Delete: the six files above.

- [ ] **Step 1: Merge into commands.go**

Assemble `sender/commands.go`: `package main`, one merged import block (union of the three files' imports — verify), then the bodies in order: `applyProvision`, `writeEnvFile` (provision.go); `selftestSkill` const, `selftestResult` type, `runSelftest`, `probesRemaining` (selftest.go); `statusReport` type, `gatherStatus`, `formatStatus`, `caState` (status.go). The `Outbox` rename from Task 4 is already applied in these files.

- [ ] **Step 2: Merge the tests into commands_test.go**

Same mechanic from `provision_test.go` + `selftest_test.go` + `status_test.go`.

- [ ] **Step 3: Delete the originals**

```bash
cd sender
git rm provision.go selftest.go status.go provision_test.go selftest_test.go status_test.go
git add commands.go commands_test.go
```

- [ ] **Step 4: Verify gofmt, vet, and tests**

Run: `cd sender && gofmt -l commands.go commands_test.go && go vet ./... && go test ./...`
Expected: clean, PASS. Confirm the final layout is eight source files:

Run: `ls sender/*.go | grep -v _test.go`
Expected: `commands.go config.go detect.go flush.go main.go outbox.go transcript_codex.go transcript_cursor.go`.

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor(cli): consolidate provision, selftest, status into commands.go"
```

---

### Task 6: Rename the service.name resource attribute

Drop the `-sender` suffix from the OTLP `service.name`. This is backend-visible: Grafana dashboards key on it.

**Files:**
- Modify: `sender/flush.go:90` and `sender/flush.go:104`

- [ ] **Step 1: Rename both occurrences**

In `sender/flush.go`, change the `service.name` attribute value (line ~90) and the logger instrumentation-scope name (line ~104) from `"qubership-skills-telemetry-sender"` to `"qubership-skills-telemetry"`.

- [ ] **Step 2: Verify nothing else references the old value**

Run: `grep -rn "qubership-skills-telemetry-sender" sender/`
Expected: no output.

- [ ] **Step 3: Verify tests**

Run: `cd sender && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(cli)!: rename service.name off -sender to qubership-skills-telemetry

Backend-visible: Grafana dashboards key on service.name and must be
updated to the new value."
```

---

### Task 7: Move sender/ to cli/

Rename the source directory and re-point the release workflow. The Go module is already named `skills-telemetry`, so no module rename is needed.

**Files:**
- Rename: `sender/` → `cli/`
- Modify: `.github/workflows/release.yml:3,31,32,34,55,56,58`

- [ ] **Step 1: Move the directory**

```bash
git mv sender cli
```

- [ ] **Step 2: Re-point the release workflow**

In `.github/workflows/release.yml`, update every `sender` path: the header comment on line 3 ("Build the sender binary…" → "Build the skills-telemetry CLI binary…"), and the four pairs of `go-version-file: sender/go.mod`, `cache-dependency-path: sender/go.sum`, `working-directory: sender` → `cli/go.mod`, `cli/go.sum`, `cli`.

- [ ] **Step 3: Verify no stale sender path remains**

Run: `grep -rn "sender" .github/workflows/release.yml`
Expected: no output.

- [ ] **Step 4: Verify the build and tests from the new directory**

Run: `cd cli && go build ./... && go test ./... && go vet ./...`
Expected: builds, PASS, vet clean.

- [ ] **Step 5: Verify the APM install still finds the binary**

Run: `apm install --target codex && apm compile`
Expected: completes without error and builds the CLI. Then clean up the generated artifacts per the repo's testing-and-cleanup note (`apm_modules/`, `.agents/`, `.codex/`, `apm.lock.yaml`, `cli/dist/`, `cli/skills-telemetry`).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(cli): rename sender/ directory to cli/"
```

---

## Notes for the implementer

- Tasks 1–2 are the marker removal; 3–5 are the file consolidation; 6–7 are the rename. Within a step, when a `sed` or merge touches a file another task also edits, do it in task order — the plan is sequenced so earlier renames land before later merges move the renamed code.
- macOS `sed` needs the empty backup arg: `sed -i '' -e ...`. On Linux use `sed -i -e ...`.
- After the directory move (Task 7), all later work happens in `cli/`, not `sender/`.
- Out of scope here: the OpenCode adapter, token auth, offset-file garbage collection, and any change to `pkgName` / the config directory.
- Follow-up after merge: update `docs/cli.md`, `README.md`, and `CLAUDE.md` to refer to `cli/` instead of `sender/`, and tell the backend owner to update the Grafana `service.name` key. These are docs, tracked separately from this code plan.
