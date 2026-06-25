package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestIngestEnqueuesAndFlushes(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Isolate the config dir so a CA configured on the dev machine does not
	// force TLS onto the plain-HTTP test server (caTLSConfig reads pkgConfigDir).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	s := &Outbox{Dir: t.TempDir()}
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@host:o/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tp})

	code := ingest(s, "codex", srv.URL, stdin, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("expected a flush on first ingest")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("buffer should be drained: %d files", len(files))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, flushStampName)); err != nil {
		t.Fatalf("flush stamp missing: %v", err)
	}
}

func TestIngestBadJSONStillSucceeds(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	code := ingest(s, "codex", "", []byte("not json"), func(string) string { return "" })
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
}

func TestIngestCursorFromTranscript(t *testing.T) {
	// Isolate config/cache dirs so the real machine state and any configured CA
	// are untouched (DefaultOffsetStore uses the cache dir; caTLSConfig the config dir).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	body := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/from-transcript/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})

	// Empty endpoint => Flush is a no-op, so events stay in the outbox to inspect.
	s := &Outbox{Dir: t.TempDir()}
	if code := ingest(s, "cursor", "", stdin, func(string) string { return "" }); code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}

	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("buffered %d events, want 1", len(files))
	}
}

func TestShouldFlushThrottle(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should not flush with empty buffer")
	}
	_ = s.Enqueue(SkillEvent{Skill: "x", TS: time.Now().UTC()})
	if !shouldFlush(s, 10, time.Hour) {
		t.Fatal("should flush when no prior attempt")
	}
	touchFlushStamp(s)
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should skip: stamp fresh and count below N")
	}
}
