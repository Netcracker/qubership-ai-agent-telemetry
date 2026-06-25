package main

import (
	"encoding/json"
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

func TestDetectStripsLeadingUTF8BOM(t *testing.T) {
	// PowerShell 5.1 prepends a UTF-8 BOM when piping stdin to a native command
	// (Cursor on Windows). The payload must still parse.
	stdin := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"session_id":"s","cwd":"/repo","tool_name":"Skill","tool_input":{"skill":"demo"}}`)...)
	events, err := detect("claude", stdin, func(string) string { return "" }, time.Now().UTC())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(events) != 1 || events[0].Skill != "demo" {
		t.Fatalf("got %d events (%+v), want 1 for demo", len(events), events)
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
	stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tp})
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
	stdin, _ := json.Marshal(map[string]any{"session_id": "c1", "workspace_roots": []string{"/repo"}, "transcript_path": tp})
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

func TestSanitizeRemote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"https clean", "https://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https user", "https://username@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https user+token", "https://username:ghp_xxxx@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https oauth gitlab", "https://oauth2:glpat-xxxx@gitlab.com/org/repo.git", "https://gitlab.com/org/repo.git"},
		{"http user+pass", "http://user:pass@example.com/repo.git", "http://example.com/repo.git"},
		{"ssh url clean", "ssh://git@host/org/repo.git", "ssh://host/org/repo.git"},
		{"ssh url with port", "ssh://deploy@host:2222/repo.git", "ssh://host:2222/repo.git"},
		{"scp-like", "git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"git protocol", "git://host/repo.git", "git://host/repo.git"},
		{"file url", "file:///path/to/repo.git", "file:///path/to/repo.git"},
		{"local path", "/path/to/repo.git", "/path/to/repo.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeRemote(c.in); got != c.want {
				t.Fatalf("sanitizeRemote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSkillNameInPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means no match expected
	}{
		{"cursor apm windows", `C:\Users\u\repo\.agents\skills\ai-agent-telemetry-configure\SKILL.md`, "ai-agent-telemetry-configure"},
		{"cursor legacy unix", "/repo/.cursor/skills/foo/SKILL.md", "foo"},
		{"cursor legacy windows", `C:\repo\.cursor\skills\foo\SKILL.md`, "foo"},
		{"global plugin", `C:\Users\u\.claude\plugins\cache\p\6.0.3\skills\brainstorming\SKILL.md`, "brainstorming"},
		{"global user", "/home/u/.claude/skills/foo/SKILL.md", "foo"},
		{"case-insensitive fs keeps name case", "/x/skills/Foo/skill.md", "Foo"},
		{"my-skills boundary", "my-skills/foo/SKILL.md", ""},
		{"no skills segment", "/repo/src/main.go", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := skillNameInPath(c.in)
			if c.want == "" {
				if ok {
					t.Fatalf("got %q, want no match", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got (%q, %v), want %q", got, ok, c.want)
			}
		})
	}
}

func TestSkillNamesInText(t *testing.T) {
	// Two real reads plus noise, including a Codex doubled-backslash path.
	text := `cat /Users/me/repo/.agents/skills/alpha/SKILL.md && rg foo && ls skills\\beta\\SKILL.md`
	got := skillNamesInText(text)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got %v, want [alpha beta]", got)
	}
	if n := skillNamesInText("cat README.md"); n != nil {
		t.Fatalf("got %v, want nil", n)
	}
}
