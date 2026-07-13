package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRunHooksInstallAcceptsTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out string
	code := run([]string{"hooks", "install", "--target=codex,claude"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	if !strings.Contains(out, "claude: installed") || !strings.Contains(out, "codex: installed") {
		t.Fatalf("output = %q, want install results", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("unrequested Cursor hook exists: %v", err)
	}
}

func TestRunConfigureInstallsHooks(t *testing.T) {
	home, configHome := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed: %v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	if !strings.Contains(out, "restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted") {
		t.Fatalf("output = %q, want Codex restart reminder", out)
	}
}

func TestRunConfigureInstallsHooksNone(t *testing.T) {
	home, _ := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks=none"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists with --hooks=none: %v", target, err)
		}
	}
}

func TestRunConfigureInstallsHooksSubset(t *testing.T) {
	home, _ := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks=claude,cursor"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range []hookTarget{hookClaude, hookCursor} {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed: %v", target, err)
		}
	}
	if _, err := os.Stat(hookPath(home, hookCodex)); !os.IsNotExist(err) {
		t.Fatalf("unrequested Codex hook exists: %v", err)
	}
}

func TestRunConfigureInstallsHooksContinuesAfterMalformedClaudeFile(t *testing.T) {
	home, configHome := isolateRunConfigure(t)
	claudePath := hookPath(home, hookClaude)
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs"}, func(s string) { out += s })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, target := range []hookTarget{hookCodex, hookCursor} {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed after Claude failure: %v", target, err)
		}
	}
	if !strings.Contains(out, "claude: invalid") || !strings.Contains(out, "codex: installed") || !strings.Contains(out, "cursor: installed") {
		t.Fatalf("output = %q, want aggregate hook status", out)
	}
}

func TestRunConfigureRejectsUnavailableHomeWithoutRelativeHooks(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	configHome := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")

	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs"}, func(s string) { out += s })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, relativeDir := range []string{".claude", ".codex", ".cursor"} {
		if _, err := os.Stat(filepath.Join(workingDir, relativeDir)); !os.IsNotExist(err) {
			t.Fatalf("configure created relative hook directory %s: %v", relativeDir, err)
		}
	}
	if !strings.Contains(out, "claude: invalid") || !strings.Contains(out, "codex: invalid") || !strings.Contains(out, "cursor: invalid") {
		t.Fatalf("output = %q, want unavailable-home hook status", out)
	}
}

func isolateRunConfigure(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")
	return home, configHome
}

func TestRunHooksReportsUnavailableHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	var out string
	code := run([]string{"hooks", "install"}, func(s string) { out += s })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out, "no user home directory") {
		t.Fatalf("output = %q, want home error", out)
	}
}

func TestRunHooksRejectsMissingAction(t *testing.T) {
	var out string
	code := run([]string{"hooks"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "action") {
		t.Fatalf("output = %q, want missing action error", out)
	}
}

func TestRunHooksRejectsUnknownAction(t *testing.T) {
	var out string
	code := run([]string{"hooks", "remove"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "remove") {
		t.Fatalf("output = %q, want unknown action", out)
	}
}

func TestRunHooksRejectsUnknownTarget(t *testing.T) {
	var out string
	code := run([]string{"hooks", "install", "--target=windsurf"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "windsurf") {
		t.Fatalf("output = %q, want invalid target", out)
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
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@github.com:Netcracker/r.git"}}}` + "\n" +
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
	if code := ingest(s, "cursor", "", stdin, func(string) string { return "git@github.com:Netcracker/repo.git" }); code != 0 {
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

func TestRepoRemoteCacheMemoizesByRepoDir(t *testing.T) {
	var remotes int
	cache := newRepoRemoteCache(func(cwd string) []string {
		remotes++
		return []string{"git@github.com:Netcracker/repo.git"}
	})

	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	events := []SkillEvent{
		{RepoDir: "/repo"},
		{RepoDir: "/repo"},
	}
	got := filterEventsByPolicy(events, policy, cache.remotesFor)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if remotes != 1 {
		t.Fatalf("remote calls = %d, want 1", remotes)
	}
}
