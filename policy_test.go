package main

import (
	"strings"
	"testing"
	"time"
)

func TestPolicyAppliesToEveryHarnessEvent(t *testing.T) {
	ts := time.Unix(1, 0).UTC()
	skill, _ := newSkillEvent("codex", "s1", "git@github.com:Netcracker/project.git", "/repo", "brainstorming", ts)
	command, _ := newCommandEvent("claude", "s2", "git@github.com:Netcracker/project.git", "/repo", CommandPayload{
		CommandName: "review-pr", CommandSource: "plugin", ExpansionType: "slash_command",
	}, ts)
	mcp, _ := newMCPEvent("cursor", "s3", "git@github.com:Netcracker/project.git", "/repo", MCPPayload{
		ServerName: "github", ToolName: "get_issue", Outcome: mcpSucceeded,
	}, ts)
	got := filterEventsByPolicy(
		[]TelemetryEvent{skill, command, mcp},
		telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}},
		nil,
	)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for _, ev := range got {
		if ev.RepoRemote != "github.com/netcracker/project" {
			t.Fatalf("%s repo remote = %q", ev.EventName, ev.RepoRemote)
		}
	}
}

func TestRemoteIdentityNormalizesCommonGitURLs(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Netcracker/qubership-ai-agent-telemetry.git":         "github.com/netcracker/qubership-ai-agent-telemetry",
		"git@github.com:Netcracker/qubership-ai-agent-telemetry.git":             "github.com/netcracker/qubership-ai-agent-telemetry",
		"ssh://git@gitlab.example.com/qubership/platform/service.git":            "gitlab.example.com/qubership/platform/service",
		"https://oauth2:token@gitlab.example.com/qubership/platform/service.git": "gitlab.example.com/qubership/platform/service",
	}
	for in, want := range cases {
		if got := normalizeRawRemote(in); got != want {
			t.Fatalf("normalizeRawRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteIdentityRejectsLocalAndUnsafeValues(t *testing.T) {
	cases := map[string]string{
		"Unix absolute path":     "/home/private/project.git",
		"Unix relative path":     "../private/project.git",
		"two-part relative path": "private/project.git",
		"nested relative path":   "repos/private/project.git",
		"home relative path":     "~/private/project.git",
		"dot-directory path":     ".cache/project.git",
		"dotted directory path":  "repos.local/private.git",
		"dotted nested path":     "source.dir/private/project.git",
		"Windows drive path":     `C:\\Users\\private\\project.git`,
		"Windows slash drive":    `C:/Users/private/project.git`,
		"Windows UNC path":       `\\\\server\\share\\project.git`,
		"file URL":               "file:///home/private/project.git",
		"URL traversal":          "https://github.com/Netcracker/../private.git",
		"encoded URL traversal":  "https://github.com/Netcracker/%2e%2e/private.git",
		"scp traversal":          "git@github.com:Netcracker/../private.git",
		"canonical traversal":    "github.com/Netcracker/../private",
		"control character":      "github.com/Netcracker/pro\x00ject",
		"leading control":        "\ngithub.com/Netcracker/project",
		"unsupported raw value":  "private-project",
		"allowlist pattern":      "github.com/Netcracker/*",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got := normalizeRawRemote(raw); got != "" {
				t.Fatalf("normalizeRawRemote(%q) = %q, want empty", raw, got)
			}
		})
	}
}

func TestRemoteIdentityPreservesSupportedNetworkForms(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Netcracker/project.git":          "github.com/netcracker/project",
		"ssh://git@gitlab.example.com/group/project.git": "gitlab.example.com/group/project",
		"https://github.com/Netcracker/project.git":      "github.com/netcracker/project",
		"git://example.net/team/project.git":             "example.net/team/project",
	}
	for raw, want := range cases {
		if got := normalizeRawRemote(raw); got != want {
			t.Errorf("normalizeRawRemote(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRepoAllowedPreservesSingleLabelHostPatterns(t *testing.T) {
	if !repoAllowed("git@host:org/project.git", []string{"host/org/*"}) {
		t.Fatal("want single-label SCP host to match its canonical allowlist pattern")
	}
}

func TestNormalizeRepoPatternPreservesSupportedForms(t *testing.T) {
	cases := map[string]string{
		"host/org/*":                      "host/org/*",
		"github.com/Netcracker/**":        "github.com/netcracker/**",
		"https://github.com/Netcracker/*": "github.com/netcracker/*",
		"git@github.com:Netcracker/*.git": "github.com/netcracker/*",
	}
	for pattern, want := range cases {
		if got := normalizeRepoPattern(pattern); got != want {
			t.Errorf("normalizeRepoPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestUnscopedPolicyClearsUnsafeRepositoryRemote(t *testing.T) {
	unsafe := []string{
		"/home/private/project.git",
		"private/project.git",
		"repos/private/project.git",
		"~/private/project.git",
		".cache/project.git",
		"repos.local/private.git",
		"source.dir/private/project.git",
	}
	for _, remote := range unsafe {
		t.Run(remote, func(t *testing.T) {
			ev, err := newSkillEvent("codex", "s1", remote, "", "skill", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			got := filterEventsByPolicy([]TelemetryEvent{ev}, telemetryPolicy{}, nil)
			if len(got) != 1 {
				t.Fatalf("got %d events, want 1", len(got))
			}
			if got[0].RepoRemote != "" {
				t.Fatalf("repo remote = %q, want empty", got[0].RepoRemote)
			}
		})
	}
}

func TestRepoAllowedMatchesGithubOrgWithoutAllowingPersonalForksByOrigin(t *testing.T) {
	allow := []string{"github.com/Netcracker/*", "github.com/Qubership/*"}
	if !repoAllowed("git@github.com:Netcracker/qubership-ai-agent-telemetry.git", allow) {
		t.Fatal("want Netcracker repo allowed")
	}
	if !repoAllowed("git@github.com:netcracker/qubership-ai-agent-telemetry.git", allow) {
		t.Fatal("want GitHub org matching to ignore path case")
	}
	if repoAllowed("git@github.com:some-user/qubership-ai-agent-telemetry.git", allow) {
		t.Fatal("personal fork origin alone must not match an org allow pattern")
	}
}

func TestRepoAllowedAcceptsURLLikePattern(t *testing.T) {
	if !repoAllowed("git@github.com:Netcracker/project.git", []string{"https://github.com/Netcracker/*"}) {
		t.Fatal("want URL-like allow pattern to match")
	}
}

func TestRepoAllowedMatchesGitlabNestedGroups(t *testing.T) {
	allow := []string{"gitlab.example.com/qubership/**"}
	if !repoAllowed("ssh://git@gitlab.example.com/qubership/platform/service.git", allow) {
		t.Fatal("want nested GitLab group allowed")
	}
	if repoAllowed("ssh://git@gitlab.example.com/personal/platform/service.git", allow) {
		t.Fatal("want unrelated GitLab group denied")
	}
}

func TestPolicyAllowsPersonalForkWhenAnotherRemoteMatchesAllowlist(t *testing.T) {
	ev := testSkillEvent(t, "codex", "s1", "git@github.com:some-user/project.git", "/repo", "skill", time.Now())
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	allowed := eventAllowed(ev, policy, func(cwd string) []string {
		if cwd != "/repo" {
			t.Fatalf("cwd = %q", cwd)
		}
		return []string{
			"git@github.com:some-user/project.git",
			"git@github.com:Netcracker/project.git",
		}
	})
	if !allowed {
		t.Fatal("want fork allowed when upstream remote belongs to the org")
	}
}

func TestFilterEventsUsesMatchingAllowlistedRemoteForForks(t *testing.T) {
	ev := testSkillEvent(t, "codex", "s1", "git@github.com:some-user/project.git", "/repo", "skill", time.Now())
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	got := filterEventsByPolicy([]TelemetryEvent{ev}, policy, func(string) []string {
		return []string{
			"git@github.com:some-user/project.git",
			"git@github.com:Netcracker/project.git",
		}
	})
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "github.com/netcracker/project" {
		t.Fatalf("repo remote = %q", got[0].RepoRemote)
	}
}

func TestPolicyUsesGitRemoteWhenEventRemoteMissing(t *testing.T) {
	ev := testSkillEvent(t, "codex", "s1", "", "/repo", "skill", time.Now())
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	got := filterEventsByPolicy([]TelemetryEvent{ev}, policy, func(cwd string) []string {
		if cwd != "/repo" {
			t.Fatalf("cwd = %q", cwd)
		}
		return []string{"git@github.com:Netcracker/project.git"}
	})
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "github.com/netcracker/project" {
		t.Fatalf("repo remote = %q", got[0].RepoRemote)
	}
}

func TestPolicyWithAllowlistDropsUnknownRemote(t *testing.T) {
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	ev := testSkillEvent(t, "codex", "s1", "", "", "skill", time.Now())
	if eventAllowed(ev, policy, nil) {
		t.Fatal("empty remote should be denied when an allowlist is configured")
	}
}

func TestResolveRepoAllowListUsesEnvOverride(t *testing.T) {
	got, defaulted := resolveRepoAllowList("github.com/Env/*", []string{"github.com/File/*"})
	if defaulted {
		t.Fatal("env override should not be marked as default")
	}
	if strings.Join(got, ",") != "github.com/Env/*" {
		t.Fatalf("repo allow = %v", got)
	}
}

func TestResolveRepoAllowListUsesFile(t *testing.T) {
	got, defaulted := resolveRepoAllowList("", []string{"github.com/File/*"})
	if defaulted {
		t.Fatal("file policy should not be marked as default")
	}
	if strings.Join(got, ",") != "github.com/File/*" {
		t.Fatalf("repo allow = %v", got)
	}
}

func TestResolveRepoAllowListDefaultsToNetcrackerRepositories(t *testing.T) {
	got, defaulted := resolveRepoAllowList("", nil)
	if !defaulted {
		t.Fatal("missing policy should be marked as default")
	}
	want := "github.com/Netcracker/*,*netcracker*/**"
	if strings.Join(got, ",") != want {
		t.Fatalf("repo allow = %v, want %q", got, want)
	}
	for _, remote := range []string{
		"https://github.com/Netcracker/project.git",
		"ssh://git@code.netcracker.example/group/project.git",
	} {
		if !repoAllowed(remote, got) {
			t.Errorf("default policy rejected %q", remote)
		}
	}
}

func TestFilterEventsNormalizesRepoRemoteForTelemetry(t *testing.T) {
	ev, err := newSkillEvent(
		"codex", "s1", "git@github.com:Netcracker/Project.git", "", "skill", time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := filterEventsByPolicy([]TelemetryEvent{ev}, telemetryPolicy{}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "github.com/netcracker/project" {
		t.Fatalf("repo remote = %q", got[0].RepoRemote)
	}
}

func TestSoleStarPolicyAllowsUnattributedEvents(t *testing.T) {
	ev := testSkillEvent(t, "cursor", "s1", "", "", "skill", time.Now())
	got := filterEventsByPolicy([]TelemetryEvent{ev}, telemetryPolicy{RepoAllowList: []string{"*"}}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "" {
		t.Fatalf("repo remote = %q, want empty", got[0].RepoRemote)
	}
}

func TestSoleStarPolicyNormalizesAndClearsRemotes(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "normalizes valid remote", raw: "git@github.com:Netcracker/Project.git", want: "github.com/netcracker/project"},
		{name: "clears unsafe remote", raw: "/home/private/project.git", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := newSkillEvent("cursor", "s1", tt.raw, "", "skill", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			got := filterEventsByPolicy([]TelemetryEvent{ev}, telemetryPolicy{RepoAllowList: []string{"*"}}, nil)
			if len(got) != 1 || got[0].RepoRemote != tt.want {
				t.Fatalf("events = %#v, want remote %q", got, tt.want)
			}
		})
	}
}

func TestMixedStarPolicyDoesNotAllowUnattributedEvents(t *testing.T) {
	ev := testSkillEvent(t, "cursor", "s1", "", "", "skill", time.Now())
	got := filterEventsByPolicy([]TelemetryEvent{ev}, telemetryPolicy{
		RepoAllowList: []string{"*", "github.com/Netcracker/*"},
	}, nil)
	if len(got) != 0 {
		t.Fatalf("got %#v, want no events", got)
	}
}

func TestSoleStarPolicyReportsAllScope(t *testing.T) {
	if got := (telemetryPolicy{RepoAllowList: []string{"*"}}).repoScope(); got != "all" {
		t.Fatalf("repo scope = %q, want all", got)
	}
}
