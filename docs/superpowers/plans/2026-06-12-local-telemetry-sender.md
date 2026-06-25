# Local Telemetry Sender Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a tiny Go CLI that the skill-call hook invokes per event; it normalizes the hook input into an OpenTelemetry log event, buffers it in a machine-global spool directory, and opportunistically flushes the buffer to an OTLP/HTTP collector.

**Architecture:** Single short-lived CLI binary (no daemon). Each `ingest` run parses stdin via a per-agent adapter, writes one event file to a spool dir (atomic temp+rename, no shared-file contention), then opportunistically flushes the whole spool over OTLP/HTTP under a single advisory lock — deleting files only on a confirmed successful export. Two tiny bootstrap scripts (`sh` + PowerShell) locate-or-download the binary into a per-machine cache and `exec` it.

**Tech Stack:** Go 1.26 (single static binary, `package main`, flat layout). Library reuse: `go.opentelemetry.io/otel` log SDK + `otlploghttp` exporter for OTLP; `github.com/gofrs/flock` for cross-platform file locking; Go stdlib for everything else (`flag`, `encoding/json`, `os.UserCacheDir`, `regexp`, `os/exec`).

**Scope:** This plan delivers the full generic machinery (spool, rotation, flush, CLI, bootstrap, build/release) plus the **Codex** adapter only — Codex is the only harness whose hook is fully implemented today. The `Adapter` interface makes Claude / Cursor / OpenCode adapters drop-in follow-up work; they are out of scope here.

**Minimalism rules for this build:** flat single `package main`; prefer stdlib over a dependency; no subcommand framework (stdlib `flag` + a switch); one responsibility per file; no feature the hook does not need yet (YAGNI).

---

## File Structure

All Go source lives at repo-root `sender/` (built and released as an artifact — **not** shipped inside the APM package). Bootstrap scripts and hook templates live inside the package (shipped per-repo).

| File | Responsibility |
|---|---|
| `sender/go.mod` | Module `skills-telemetry`, Go 1.26, pinned deps |
| `sender/main.go` | CLI entry: dispatch `ingest`/`flush`/`status`/`version`; `version` built-in |
| `sender/event.go` | `SkillEvent` normalized model + JSON marshal/unmarshal |
| `sender/adapter.go` | `Adapter` interface, `Dispatch(agent, stdin)`, Codex adapter (marker parse) |
| `sender/spool.go` | Machine-global paths, atomic `Enqueue`, `List`, `Remove`, `Rotate(cap)` |
| `sender/flush.go` | `Flush` under flock: OTLP/HTTP export via SDK, delete-on-success, throttle marker |
| `sender/*_test.go` | Tests colocated per file |
| `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh` | POSIX locate-or-download-or-exec |
| `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1` | Windows locate-or-download-or-exec |
| `agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-codex-hooks.json` | Modify: call bootstrap with `--agent=codex --endpoint=…` |
| `sender/Makefile` | Cross-compile matrix + SHA-256 checksums |

The current `detect_skill_call.py` is removed in Task 9 (its marker logic is ported into the Codex adapter).

---

## Task 1: Go module and `version` command

**Files:**
- Create: `sender/go.mod`
- Create: `sender/main.go`
- Test: `sender/main_test.go`

- [ ] **Step 1: Create the module**

Run from repo root:

```bash
mkdir -p sender && cd sender && go mod init skills-telemetry && go mod edit -go=1.26
```

- [ ] **Step 2: Write the failing test**

`sender/main_test.go`:

```go
package main

import "testing"

func TestRunVersion(t *testing.T) {
	var out string
	code := run([]string{"version"}, func(s string) { out = s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if out != version+"\n" {
		t.Fatalf("output = %q, want %q", out, version+"\n")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	code := run([]string{"bogus"}, func(string) {})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd sender && go test ./... -run TestRun -v`
Expected: FAIL — `undefined: run` / `undefined: version`.

- [ ] **Step 4: Write minimal implementation**

`sender/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], func(s string) { fmt.Print(s) }))
}

// run dispatches a subcommand. stdout is injected for testability.
func run(args []string, stdout func(string)) int {
	if len(args) == 0 {
		stdout("usage: skills-telemetry <ingest|flush|status|version>\n")
		return 2
	}
	switch args[0] {
	case "version":
		stdout(version + "\n")
		return 0
	default:
		stdout("unknown command: " + args[0] + "\n")
		return 2
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd sender && go test ./... -run TestRun -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd .. && git add sender/go.mod sender/main.go sender/main_test.go
git commit -m "feat(sender): scaffold Go CLI with version command"
```

---

## Task 2: `SkillEvent` model

**Files:**
- Create: `sender/event.go`
- Test: `sender/event_test.go`

- [ ] **Step 1: Write the failing test**

`sender/event_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestSkillEventJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	in := SkillEvent{
		Agent:      "codex",
		SessionID:  "s1",
		TurnID:     "t1",
		RepoPath:   "/repo",
		RepoRemote: "git@host:org/repo.git",
		Skill:      "ops:deploy",
		Source:     "Netcracker/qubership-ai-packages",
		TS:         ts,
	}
	b, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SkillEvent
	if err := out.UnmarshalJSON(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sender && go test ./... -run TestSkillEvent -v`
Expected: FAIL — `undefined: SkillEvent`.

- [ ] **Step 3: Write minimal implementation**

`sender/event.go`:

```go
package main

import (
	"encoding/json"
	"time"
)

// SkillEvent is the normalized, agent-independent record produced by an adapter
// and persisted in the spool. It is the only shape that leaves this process.
type SkillEvent struct {
	Agent      string    `json:"agent"`
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id,omitempty"`
	RepoPath   string    `json:"repo_path,omitempty"`
	RepoRemote string    `json:"repo_remote,omitempty"`
	Skill      string    `json:"skill"`
	Source     string    `json:"source,omitempty"`
	TS         time.Time `json:"ts"`
}

func (e SkillEvent) MarshalJSON() ([]byte, error) {
	type alias SkillEvent
	return json.Marshal(alias(e))
}

func (e *SkillEvent) UnmarshalJSON(b []byte) error {
	type alias SkillEvent
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = SkillEvent(a)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sender && go test ./... -run TestSkillEvent -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd .. && git add sender/event.go sender/event_test.go
git commit -m "feat(sender): add normalized SkillEvent model"
```

---

## Task 3: Codex adapter (marker parsing)

Ports the marker logic from `detect_skill_call.py`. The Codex `Stop` payload carries the assistant text in `last_assistant_message`; markers have the shape `[skill-called] skill=<name> source=<src>`. One payload may contain several markers → several events.

**Files:**
- Create: `sender/adapter.go`
- Test: `sender/adapter_test.go`

- [ ] **Step 1: Write the failing test**

`sender/adapter_test.go`:

```go
package main

import "testing"

func TestCodexAdapterParsesMarkers(t *testing.T) {
	stdin := []byte(`{
		"hook_event_name": "Stop",
		"session_id": "s1",
		"turn_id": "t1",
		"cwd": "/repo",
		"last_assistant_message": "done.\n[skill-called] skill=ops:deploy source=Netcracker/x\n[skill-called] skill=english-us-developer-style source=Netcracker/x\n"
	}`)
	events, err := Dispatch("codex", stdin, func(string) string { return "" })
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Skill != "ops:deploy" || events[0].Source != "Netcracker/x" {
		t.Fatalf("event[0] = %+v", events[0])
	}
	if events[0].Agent != "codex" || events[0].SessionID != "s1" || events[0].TurnID != "t1" || events[0].RepoPath != "/repo" {
		t.Fatalf("event[0] common fields = %+v", events[0])
	}
	if events[1].Skill != "english-us-developer-style" {
		t.Fatalf("event[1] = %+v", events[1])
	}
}

func TestCodexAdapterNoMarkers(t *testing.T) {
	events, err := Dispatch("codex", []byte(`{"last_assistant_message":"nothing here"}`), func(string) string { return "" })
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestCodexAdapterUsesRemoteResolver(t *testing.T) {
	stdin := []byte(`{"cwd":"/repo","last_assistant_message":"[skill-called] skill=a source=b"}`)
	events, err := Dispatch("codex", stdin, func(cwd string) string {
		if cwd != "/repo" {
			t.Fatalf("resolver got cwd %q", cwd)
		}
		return "git@host:org/repo.git"
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if events[0].RepoRemote != "git@host:org/repo.git" {
		t.Fatalf("remote = %q", events[0].RepoRemote)
	}
}

func TestDispatchUnknownAgent(t *testing.T) {
	if _, err := Dispatch("nope", []byte(`{}`), func(string) string { return "" }); err == nil {
		t.Fatal("want error for unknown agent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sender && go test ./... -run "Codex|Dispatch" -v`
Expected: FAIL — `undefined: Dispatch`.

- [ ] **Step 3: Write minimal implementation**

`sender/adapter.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// remoteResolver returns the git remote URL for a working dir, or "" if unknown.
// Injected so adapters stay pure and testable.
type remoteResolver func(cwd string) string

// Adapter turns a harness-specific hook payload into normalized events.
type Adapter func(stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error)

var adapters = map[string]Adapter{
	"codex": codexAdapter,
}

// Dispatch routes raw stdin to the adapter for the named agent.
func Dispatch(agent string, stdin []byte, remote remoteResolver) ([]SkillEvent, error) {
	a, ok := adapters[agent]
	if !ok {
		return nil, fmt.Errorf("no adapter for agent %q", agent)
	}
	return a(stdin, remote, time.Now().UTC())
}

var markerRe = regexp.MustCompile(`(?m)^\[skill-called\]\s+skill=(\S+)\s+source=(\S+)\s*$`)

type codexPayload struct {
	SessionID            string `json:"session_id"`
	TurnID               string `json:"turn_id"`
	Cwd                  string `json:"cwd"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

func codexAdapter(stdin []byte, remote remoteResolver, now time.Time) ([]SkillEvent, error) {
	var p codexPayload
	if len(stdin) > 0 {
		// Malformed JSON yields no events rather than an error: a broken turn
		// must never fail the hook.
		_ = json.Unmarshal(stdin, &p)
	}
	matches := markerRe.FindAllStringSubmatch(p.LastAssistantMessage, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	var rem string
	if remote != nil && p.Cwd != "" {
		rem = remote(p.Cwd)
	}
	events := make([]SkillEvent, 0, len(matches))
	for _, m := range matches {
		events = append(events, SkillEvent{
			Agent:      "codex",
			SessionID:  p.SessionID,
			TurnID:     p.TurnID,
			RepoPath:   p.Cwd,
			RepoRemote: rem,
			Skill:      m[1],
			Source:     m[2],
			TS:         now,
		})
	}
	return events, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sender && go test ./... -run "Codex|Dispatch" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd .. && git add sender/adapter.go sender/adapter_test.go
git commit -m "feat(sender): add Codex marker adapter"
```

---

## Task 4: Spool paths, atomic enqueue, list, remove

The spool is a per-machine directory under `os.UserCacheDir()` (cross-platform; the buffer is regenerable so cache semantics are correct, and it keeps us on a single stdlib call). Each event is written to its own file: `<unixNanoZeroPadded>-<pid>-<rand>.json`, written to a `.tmp` sibling then atomically renamed. `List` ignores `.tmp` files and the throttle marker.

**Files:**
- Create: `sender/spool.go`
- Test: `sender/spool_test.go`

- [ ] **Step 1: Write the failing test**

`sender/spool_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpoolEnqueueAndList(t *testing.T) {
	dir := t.TempDir()
	s := &Spool{Dir: dir}

	ev := SkillEvent{Agent: "codex", Skill: "a", TS: time.Unix(1, 0).UTC()}
	if err := s.Enqueue(ev); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}

	got, err := s.Read(files[0])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Skill != "a" {
		t.Fatalf("read skill = %q", got.Skill)
	}

	if err := s.Remove(files[0]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	files, _ = s.List()
	if len(files) != 0 {
		t.Fatalf("after remove got %d files, want 0", len(files))
	}
}

func TestSpoolListIgnoresTmpAndMarker(t *testing.T) {
	dir := t.TempDir()
	s := &Spool{Dir: dir}
	if err := os.WriteFile(filepath.Join(dir, "x.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sender && go test ./... -run TestSpool -v`
Expected: FAIL — `undefined: Spool`.

- [ ] **Step 3: Write minimal implementation**

`sender/spool.go`:

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const markerName = ".lastflush"

// Spool is a machine-global directory holding one JSON file per buffered event.
type Spool struct {
	Dir string
}

// DefaultSpool returns the per-machine spool rooted in the user cache dir.
func DefaultSpool() (*Spool, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "qubership-skills-telemetry", "spool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Spool{Dir: dir}, nil
}

// Enqueue writes one event atomically (temp file + rename).
func (s *Spool) Enqueue(ev SkillEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%d-%s.json", time.Now().UnixNano(), os.Getpid(), randHex())
	final := filepath.Join(s.Dir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// List returns event file names (not paths), oldest first, excluding temp files
// and the throttle marker.
func (s *Spool) List() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasSuffix(n, ".tmp") || n == markerName {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names) // filenames start with zero-padded nanos => chronological
	return names, nil
}

// Read decodes one event file by name.
func (s *Spool) Read(name string) (SkillEvent, error) {
	var ev SkillEvent
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return ev, err
	}
	err = json.Unmarshal(b, &ev)
	return ev, err
}

// Remove deletes one event file by name.
func (s *Spool) Remove(name string) error {
	return os.Remove(filepath.Join(s.Dir, name))
}

func randHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sender && go test ./... -run TestSpool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd .. && git add sender/spool.go sender/spool_test.go
git commit -m "feat(sender): add machine-global spool with atomic enqueue"
```

---

## Task 5: Ring-buffer rotation (cap)

When the buffer cannot drain (collector down), it must not grow without bound. `Rotate(cap)` deletes the oldest files until at most `cap` remain.

**Files:**
- Modify: `sender/spool.go` (add `Rotate`)
- Test: `sender/spool_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Append to `sender/spool_test.go`:

```go
func TestSpoolRotateDropsOldest(t *testing.T) {
	dir := t.TempDir()
	s := &Spool{Dir: dir}
	for i := 0; i < 5; i++ {
		if err := s.Enqueue(SkillEvent{Skill: "s", TS: time.Unix(int64(i), 0).UTC()}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond) // ensure distinct nanos in filenames
	}
	dropped, err := s.Rotate(3)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	files, _ := s.List()
	if len(files) != 3 {
		t.Fatalf("remaining = %d, want 3", len(files))
	}
}

func TestSpoolRotateUnderCapNoop(t *testing.T) {
	dir := t.TempDir()
	s := &Spool{Dir: dir}
	_ = s.Enqueue(SkillEvent{Skill: "s", TS: time.Unix(1, 0).UTC()})
	dropped, err := s.Rotate(100)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sender && go test ./... -run TestSpoolRotate -v`
Expected: FAIL — `s.Rotate undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `sender/spool.go`:

```go
// Rotate deletes the oldest event files until at most limit remain.
// Returns how many were dropped. (limit avoids shadowing the builtin cap/max.)
func (s *Spool) Rotate(limit int) (int, error) {
	names, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(names) <= limit {
		return 0, nil
	}
	drop := names[:len(names)-limit] // List is oldest-first
	for _, n := range drop {
		if err := s.Remove(n); err != nil {
			return 0, err
		}
	}
	return len(drop), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd sender && go test ./... -run TestSpoolRotate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd .. && git add sender/spool.go sender/spool_test.go
git commit -m "feat(sender): add ring-buffer rotation to spool"
```

---

## Task 6: Flush over OTLP/HTTP under an advisory lock

`Flush` is the only place that touches the network. It:
1. Skips silently if `endpoint == ""` or the spool is empty.
2. Takes a non-blocking advisory lock (one flusher per machine); if the lock is held, returns `(0, nil)` — the opportunistic model tolerates skipping.
3. Builds an OTLP/HTTP log pipeline (reuse: `otlploghttp` + `sdk/log`), emits one log record per buffered event, and detects export failure via a captured global error handler.
4. Deletes the exported files only on a confirmed clean export. On failure it leaves them for next time (at-least-once; duplicates are acceptable for telemetry).

**Files:**
- Create: `sender/flush.go`
- Test: `sender/flush_test.go`

- [ ] **Step 1: Add dependencies**

Run:

```bash
cd sender
go get github.com/gofrs/flock
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/log
go get go.opentelemetry.io/otel/sdk/log
go get go.opentelemetry.io/otel/exporters/otlp/otlploghttp
go get go.opentelemetry.io/otel/sdk/resource
go get go.opentelemetry.io/otel/attribute
```

- [ ] **Step 2: Write the failing test**

`sender/flush_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func seed(t *testing.T, s *Spool, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Enqueue(SkillEvent{Agent: "codex", Skill: "s", TS: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFlushSendsAndClearsOnSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Spool{Dir: t.TempDir()}
	seed(t, s, 3)

	sent, err := Flush(s, srv.URL, "", 2*time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent = %d, want 3", sent)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("collector received no requests")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("spool not cleared: %d files remain", len(files))
	}
}

func TestFlushKeepsBufferOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Spool{Dir: t.TempDir()}
	seed(t, s, 2)

	_, err := Flush(s, srv.URL, "", 2*time.Second)
	if err == nil {
		t.Fatal("want error on server 500")
	}
	files, _ := s.List()
	if len(files) != 2 {
		t.Fatalf("buffer should be intact: %d files remain, want 2", len(files))
	}
}

func TestFlushEmptyEndpointIsNoop(t *testing.T) {
	s := &Spool{Dir: t.TempDir()}
	seed(t, s, 1)
	sent, err := Flush(s, "", "", time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed: %d files", len(files))
	}
}

func TestFlushSkipsWhenLocked(t *testing.T) {
	s := &Spool{Dir: t.TempDir()}
	seed(t, s, 1)
	// Hold the lock from this test.
	release, err := lockSpool(s)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer release()

	sent, err := Flush(s, "http://127.0.0.1:0", "", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0 (should skip when locked)", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed while locked: %d files", len(files))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd sender && go test ./... -run TestFlush -v`
Expected: FAIL — `undefined: Flush` / `undefined: lockSpool`.

- [ ] **Step 4: Write minimal implementation**

`sender/flush.go`:

```go
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// lockSpool takes a non-blocking advisory lock for the spool. The returned
// func releases it. (release, nil) with a nil release means the lock was busy.
func lockSpool(s *Spool) (release func(), busy error) {
	fl := flock.New(filepath.Join(s.Dir, ".flush.lock"))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errLockBusy
	}
	return func() { _ = fl.Unlock() }, nil
}

var errLockBusy = fmt.Errorf("flush lock busy")

// Flush sends every buffered event to the OTLP/HTTP endpoint and removes the
// files that were sent. Returns the number of events sent.
// Skips (0, nil) when: endpoint is empty, buffer empty, or the lock is held.
func Flush(s *Spool, endpoint, token string, timeout time.Duration) (int, error) {
	if endpoint == "" {
		return 0, nil
	}
	names, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(names) == 0 {
		return 0, nil
	}

	release, lockErr := lockSpool(s)
	if lockErr == errLockBusy {
		return 0, nil
	}
	if lockErr != nil {
		return 0, lockErr
	}
	defer release()

	// Re-list under the lock to avoid sending files a concurrent flush already took.
	names, err = s.List()
	if err != nil || len(names) == 0 {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Capture export errors: SimpleProcessor routes them to the global handler.
	var exportErr error
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(e error) { exportErr = e }))

	opts := []otlploghttp.Option{otlploghttp.WithEndpointURL(endpoint)}
	if token != "" {
		opts = append(opts, otlploghttp.WithHeaders(map[string]string{"Authorization": "Bearer " + token}))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return 0, err
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", "skills-telemetry"),
		attribute.String("service.version", version),
	)
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)),
		sdklog.WithResource(res),
	)
	logger := provider.Logger("skills-telemetry")

	sentNames := make([]string, 0, len(names))
	for _, n := range names {
		ev, rerr := s.Read(n)
		if rerr != nil {
			continue // skip unreadable file; do not fail the whole batch
		}
		var rec otellog.Record
		rec.SetTimestamp(ev.TS)
		rec.SetObservedTimestamp(time.Now().UTC())
		rec.SetBody(otellog.StringValue("skill_executed"))
		rec.AddAttributes(
			otellog.String("agent", ev.Agent),
			otellog.String("session.id", ev.SessionID),
			otellog.String("turn.id", ev.TurnID),
			otellog.String("repo.path", ev.RepoPath),
			otellog.String("repo.remote", ev.RepoRemote),
			otellog.String("skill.name", ev.Skill),
			otellog.String("skill.source", ev.Source),
		)
		logger.Emit(ctx, rec)
		sentNames = append(sentNames, n)
	}

	// Shutdown flushes the exporter; export errors surface via exportErr.
	_ = provider.Shutdown(ctx)
	if exportErr != nil {
		return 0, exportErr
	}

	for _, n := range sentNames {
		_ = s.Remove(n)
	}
	return len(sentNames), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd sender && go test ./... -run TestFlush -v`
Expected: PASS. (If `go vet` flags the import order, run `gofmt -w .`.)

- [ ] **Step 6: Commit**

```bash
cd .. && git add sender/flush.go sender/flush_test.go sender/go.mod sender/go.sum
git commit -m "feat(sender): add OTLP/HTTP flush with locking and delete-on-success"
```

---

## Task 7: `ingest` command, throttle, `status`

`ingest --agent=<a> --endpoint=<url>` reads stdin, dispatches to the adapter, enqueues every event, rotates to the cap, then opportunistically flushes when the throttle allows (≥ `flushCountN` buffered events OR ≥ `flushIntervalT` since the last attempt). The throttle is a marker file whose mtime records the last attempt. `status` prints buffer depth and the last-attempt time. The token is read from `SKILLS_TELEMETRY_TOKEN`. **Any error is logged to stderr and returns 0** — the hook must never fail the turn.

**Files:**
- Modify: `sender/main.go` (dispatch `ingest`/`flush`/`status`, add helpers)
- Test: `sender/ingest_test.go`

- [ ] **Step 1: Write the failing test**

`sender/ingest_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIngestEnqueuesAndFlushes(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Spool{Dir: t.TempDir()}
	stdin := []byte(`{"session_id":"s1","cwd":"/repo","last_assistant_message":"[skill-called] skill=a source=b"}`)

	code := ingest(s, "codex", srv.URL, stdin, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
	// flushCountN is 1-friendly via the throttle: first ingest always attempts a flush.
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("expected a flush on first ingest")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("buffer should be drained: %d files", len(files))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, markerName)); err != nil {
		t.Fatalf("throttle marker missing: %v", err)
	}
}

func TestIngestBadJSONStillSucceeds(t *testing.T) {
	s := &Spool{Dir: t.TempDir()}
	code := ingest(s, "codex", "", []byte("not json"), func(string) string { return "" })
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
}

func TestShouldFlushThrottle(t *testing.T) {
	dir := t.TempDir()
	s := &Spool{Dir: dir}
	// 0 events => never flush.
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should not flush with empty buffer")
	}
	_ = s.Enqueue(SkillEvent{Skill: "x", TS: time.Now().UTC()})
	// No marker yet => treat as overdue => flush.
	if !shouldFlush(s, 10, time.Hour) {
		t.Fatal("should flush when no prior attempt")
	}
	// Fresh marker + below count threshold => skip.
	touchMarker(s)
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should skip: marker fresh and count below N")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd sender && go test ./... -run "TestIngest|TestShouldFlush" -v`
Expected: FAIL — `undefined: ingest` / `undefined: shouldFlush` / `undefined: touchMarker`.

- [ ] **Step 3: Write minimal implementation**

Replace the body of `sender/main.go` with:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

const (
	bufferCap      = 100
	flushCountN    = 10
	flushIntervalT = 60 * time.Second
	flushTimeout   = 2 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], func(s string) { fmt.Print(s) }))
}

func run(args []string, stdout func(string)) int {
	if len(args) == 0 {
		stdout("usage: skills-telemetry <ingest|flush|status|version>\n")
		return 2
	}
	switch args[0] {
	case "version":
		stdout(version + "\n")
		return 0
	case "ingest":
		agent, endpoint := parseFlags(args[1:])
		s, err := DefaultSpool()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spool:", err)
			return 0 // never fail the hook
		}
		raw, _ := io.ReadAll(os.Stdin)
		return ingest(s, agent, endpoint, raw, gitRemote)
	case "flush":
		_, endpoint := parseFlags(args[1:])
		s, err := DefaultSpool()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spool:", err)
			return 0
		}
		if _, err := Flush(s, endpoint, os.Getenv("SKILLS_TELEMETRY_TOKEN"), flushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
		return 0
	case "status":
		s, err := DefaultSpool()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spool:", err)
			return 0
		}
		names, _ := s.List()
		last := "never"
		if fi, err := os.Stat(filepath.Join(s.Dir, markerName)); err == nil {
			last = fi.ModTime().UTC().Format(time.RFC3339)
		}
		stdout(fmt.Sprintf("spool: %s\nbuffered: %d\nlast_flush_attempt: %s\n", s.Dir, len(names), last))
		return 0
	default:
		stdout("unknown command: " + args[0] + "\n")
		return 2
	}
}

// parseFlags reads --agent= and --endpoint= without a flag framework (minimal).
func parseFlags(args []string) (agent, endpoint string) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--agent="):
			agent = strings.TrimPrefix(a, "--agent=")
		case strings.HasPrefix(a, "--endpoint="):
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		}
	}
	return agent, endpoint
}

// ingest is the per-event path: parse, enqueue, rotate, opportunistic flush.
// It returns 0 even on error — a hook must never fail the agent turn.
func ingest(s *Spool, agent, endpoint string, stdin []byte, remote remoteResolver) int {
	events, err := Dispatch(agent, stdin, remote)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
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
		touchMarker(s)
		if _, err := Flush(s, endpoint, os.Getenv("SKILLS_TELEMETRY_TOKEN"), flushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
	}
	return 0
}

// shouldFlush is true when there is something to send AND either enough has
// piled up or enough time has passed since the last attempt.
func shouldFlush(s *Spool, countN int, intervalT time.Duration) bool {
	names, err := s.List()
	if err != nil || len(names) == 0 {
		return false
	}
	if len(names) >= countN {
		return true
	}
	fi, err := os.Stat(filepath.Join(s.Dir, markerName))
	if err != nil {
		return true // no prior attempt recorded
	}
	return time.Since(fi.ModTime()) >= intervalT
}

// touchMarker records the time of a flush attempt (success or failure) so the
// throttle bounds retry frequency against a dead collector.
func touchMarker(s *Spool) {
	p := filepath.Join(s.Dir, markerName)
	now := time.Now()
	if err := os.WriteFile(p, []byte(now.UTC().Format(time.RFC3339)), 0o600); err != nil {
		return
	}
	_ = os.Chtimes(p, now, now)
}

// gitRemote best-effort resolves origin URL for a working dir; "" on failure.
func gitRemote(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Delete the now-duplicated `main_test.go` assertion that referenced the old single-file `run`? No — `TestRunVersion`/`TestRunUnknownCommand` still pass against this `run`. Keep them.

- [ ] **Step 4: Run the full test suite**

Run: `cd sender && go test ./... -v`
Expected: PASS (all tasks 1–7).

- [ ] **Step 5: Build the binary**

Run: `cd sender && go build -ldflags "-s -w -X main.version=0.1.0" -o /tmp/skills-telemetry . && /tmp/skills-telemetry version`
Expected: prints `0.1.0`.

- [ ] **Step 6: Commit**

```bash
cd .. && git add sender/main.go sender/main_test.go sender/ingest_test.go
git commit -m "feat(sender): add ingest/flush/status commands with throttle"
```

---

## Task 8: Bootstrap scripts (`sh` + PowerShell)

Each script: resolve the per-machine cache dir, ensure the pinned binary version is present (download + SHA-256 verify if missing), then `exec` it forwarding all args and stdin. Only OS-built-in tools. Values `BINARY_VERSION`, `BASE_URL`, and the per-platform checksums are filled in by the release process (Task 10) — here they are declared as variables at the top.

**Files:**
- Create: `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh`
- Create: `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1`

- [ ] **Step 1: Write `bootstrap.sh`**

```sh
#!/bin/sh
# Locate-or-download the skills-telemetry binary into a per-machine cache,
# then exec it forwarding all args and stdin. POSIX sh; only built-in tools.
set -eu

BINARY_VERSION="0.1.0"
BASE_URL="https://REPLACE_ME/skills-telemetry"  # set by release

# Per-OS cache base (mirrors Go os.UserCacheDir).
case "$(uname -s)" in
  Darwin) CACHE_BASE="$HOME/Library/Caches" ;;
  *)      CACHE_BASE="${XDG_CACHE_HOME:-$HOME/.cache}" ;;
esac

case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *) echo "skills-telemetry: unsupported OS $(uname -s)" >&2; exit 0 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64|amd64)  ARCH="amd64" ;;
  *) echo "skills-telemetry: unsupported arch $(uname -m)" >&2; exit 0 ;;
esac

DIR="$CACHE_BASE/qubership-skills-telemetry/bin/$BINARY_VERSION"
BIN="$DIR/skills-telemetry-$OS-$ARCH"

if [ ! -x "$BIN" ]; then
  mkdir -p "$DIR"
  TMP="$BIN.tmp.$$"
  if ! curl -fsSL "$BASE_URL/$BINARY_VERSION/skills-telemetry-$OS-$ARCH" -o "$TMP"; then
    echo "skills-telemetry: download failed" >&2; exit 0
  fi
  chmod +x "$TMP"
  mv "$TMP" "$BIN"
fi

exec "$BIN" "$@"
```

> Note: checksum verification is added in Task 10 once real artifacts and sums exist; the variable hook is the `BASE_URL`/`BINARY_VERSION` block above. A failed bootstrap exits 0 so it never breaks the agent turn.

- [ ] **Step 2: Write `bootstrap.ps1`**

```powershell
# Locate-or-download the skills-telemetry binary into LOCALAPPDATA, then run it
# forwarding all args and stdin. Windows PowerShell 5.1 built-ins only.
$ErrorActionPreference = 'Stop'

$BinaryVersion = '0.1.0'
$BaseUrl = 'https://REPLACE_ME/skills-telemetry'  # set by release

$cacheBase = $env:LOCALAPPDATA
$arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { 'amd64' }
$dir = Join-Path $cacheBase "qubership-skills-telemetry\bin\$BinaryVersion"
$bin = Join-Path $dir "skills-telemetry-windows-$arch.exe"

try {
  if (-not (Test-Path $bin)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $tmp = "$bin.tmp"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$BinaryVersion/skills-telemetry-windows-$arch.exe" -OutFile $tmp
    Move-Item -Force $tmp $bin
  }
  # Forward stdin and all args.
  $input | & $bin @args
  exit $LASTEXITCODE
} catch {
  Write-Error "skills-telemetry bootstrap failed: $_"
  exit 0  # never fail the agent turn
}
```

- [ ] **Step 3: Lint the shell script**

Run: `sh -n agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh && echo OK`
Expected: `OK` (syntax check passes).

- [ ] **Step 4: Commit**

```bash
git add agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh \
        agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1
git commit -m "feat(package): add sh/powershell bootstrap launchers"
```

---

## Task 9: Wire the Codex hook to the bootstrap; remove the Python script

The Codex `Stop` hook must now call the bootstrap with `--agent=codex --endpoint=…` instead of the Python detector. The bootstrap forwards stdin (the hook payload) and the flags to the binary's `ingest` command.

**Files:**
- Modify: `agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-codex-hooks.json`
- Delete: `agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/detect_skill_call.py`
- Modify: `agent-packages/qubership-skills-telemetry/README.md`

- [ ] **Step 1: Rewrite the hook template**

Replace the contents of `skill-call-codex-hooks.json` with:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "sh \"$(git rev-parse --show-toplevel)/apm_modules/_local/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh\" ingest --agent=codex --endpoint=https://REPLACE_ME/v1/logs",
            "commandWindows": "powershell -NoProfile -ExecutionPolicy Bypass -File \"%CD%\\apm_modules\\_local\\qubership-skills-telemetry\\.apm\\hooks\\scripts\\bootstrap.ps1\" ingest --agent=codex --endpoint=https://REPLACE_ME/v1/logs",
            "timeout": 30,
            "statusMessage": "Recording skill telemetry"
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 2: Delete the Python detector**

Run:

```bash
git rm agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/detect_skill_call.py
```

- [ ] **Step 3: Update the package README**

In `agent-packages/qubership-skills-telemetry/README.md`, replace the launcher/strategy paragraph with a short description of the new flow: the hook calls `bootstrap.sh`/`bootstrap.ps1`, which fetches the pinned Go `skills-telemetry` binary into a per-machine cache and runs `ingest`; events are buffered in a machine-global spool and flushed over OTLP/HTTP. State that Codex is implemented; Claude/Cursor/OpenCode adapters are follow-up work.

- [ ] **Step 4: Sanity-check JSON**

Run: `python3 -c "import json,sys; json.load(open('agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-codex-hooks.json'))" && echo OK`
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-codex-hooks.json \
        agent-packages/qubership-skills-telemetry/README.md
git commit -m "feat(package): wire Codex hook to bootstrap, drop python detector"
```

---

## Task 10: Build and release matrix

Cross-compile the four primary targets, emit SHA-256 checksums, and document how `BINARY_VERSION`/`BASE_URL`/checksums get filled into the bootstrap scripts.

**Files:**
- Create: `sender/Makefile`

- [ ] **Step 1: Write the Makefile**

`sender/Makefile`:

```makefile
VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64

.PHONY: build
build:
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(DIST)/skills-telemetry-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $$out . ; \
	done

.PHONY: checksums
checksums: build
	@cd $(DIST) && shasum -a 256 skills-telemetry-* > SHA256SUMS && cat SHA256SUMS

.PHONY: test
test:
	go test ./... -race

.PHONY: clean
clean:
	rm -rf $(DIST)
```

- [ ] **Step 2: Run the build and checksums**

Run: `cd sender && make checksums`
Expected: five binaries in `sender/dist/` and a printed `SHA256SUMS` table.

- [ ] **Step 3: Verify binary sizes are small**

Run: `cd sender && ls -lh dist/ | awk '{print $5, $9}'`
Expected: each binary in the single-digit MB range (confirms `-s -w` trimming).

- [ ] **Step 4: Document release wiring**

Append a `## Release` section to `agent-packages/qubership-skills-telemetry/README.md` explaining: bump `VERSION`, run `make checksums`, upload `dist/*` to the artifact store, then set `BINARY_VERSION`, `BASE_URL`, and the per-platform SHA-256 values in `bootstrap.sh`/`bootstrap.ps1`. (Checksum verification in the bootstrap is added when the first real artifacts exist.)

- [ ] **Step 5: Add `dist/` to gitignore and commit**

```bash
cd .. && printf "\n# Sender build output\nsender/dist/\n" >> .gitignore
git add sender/Makefile .gitignore agent-packages/qubership-skills-telemetry/README.md
git commit -m "build(sender): add cross-compile and checksum matrix"
```

---

## Self-Review

**Spec coverage:**
- Language Go, single static binary → Tasks 1, 10. ✓
- Hybrid hook→sender (enqueue + opportunistic flush, no daemon) → Task 7 (`ingest`, `shouldFlush`). ✓
- Decoupled delivery via per-machine cache + tiny bootstrap → Task 8. ✓
- Binary from release artifact, version pinned, checksum → Tasks 8, 10 (checksum verify deferred to first real artifacts, noted explicitly). ✓
- Bootstrap `sh`+PowerShell, OS built-ins, no Python → Task 8. ✓
- CLI shape (`ingest`/`flush`/`status`/`version`) → Tasks 1, 7. ✓
- Adapter layer in Go; port `detect_skill_call.py`; drop the script → Tasks 3, 9. ✓
- Spool dir, one file per event, atomic rename, machine-global → Task 4. ✓
- Concurrency: writers never share a file; flush serialized by one lock → Tasks 4, 6. ✓
- Ring buffer cap ~100, drop oldest → Task 5, wired in Task 7. ✓
- Emit OTLP logs, not raw input → Task 6. ✓
- Collector address via `--endpoint=` flag in hook, OTLP/HTTP, token separate (`SKILLS_TELEMETRY_TOKEN`) → Tasks 7, 9. ✓
- Open questions (token issuance, exact N/T) left as config constants / env → Task 7 constants, noted. ✓

**Scope note:** Claude/Cursor/OpenCode adapters are intentionally out of scope — the `Adapter` map in Task 3 makes each a single follow-up file + hook template. Stated in the header.

**Placeholder scan:** No "TBD"/"implement later". `REPLACE_ME` in bootstrap/hook is a deliberate release-time value, documented in Tasks 8/10, not a code gap.

**Type consistency:** `SkillEvent` fields (Task 2) are used identically in Tasks 3, 6. `Spool` methods `Enqueue`/`List`/`Read`/`Remove`/`Rotate` (Tasks 4–5) match their call sites in Tasks 6–7. `Dispatch`/`remoteResolver`/`Adapter` (Task 3) match `ingest` (Task 7). `Flush(s, endpoint, token, timeout)` signature (Task 6) matches all callers (Tasks 6–7). `markerName`, `shouldFlush`, `touchMarker`, `lockSpool` consistent across Tasks 4, 6, 7.
