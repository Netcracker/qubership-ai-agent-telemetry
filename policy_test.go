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
		if got := remoteIdentity(in); got != want {
			t.Fatalf("remoteIdentity(%q) = %q, want %q", in, got, want)
		}
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

func TestResolveRepoAllowListDefaultsToNetcracker(t *testing.T) {
	got, defaulted := resolveRepoAllowList("", nil)
	if !defaulted {
		t.Fatal("missing policy should be marked as default")
	}
	if strings.Join(got, ",") != defaultRepoAllow {
		t.Fatalf("repo allow = %v, want %q", got, defaultRepoAllow)
	}
}

func TestFilterEventsNormalizesRepoRemoteForTelemetry(t *testing.T) {
	events := []TelemetryEvent{
		testSkillEvent(t, "codex", "s1", "git@github.com:Netcracker/Project.git", "", "skill", time.Now()),
	}
	got := filterEventsByPolicy(events, telemetryPolicy{}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "github.com/netcracker/project" {
		t.Fatalf("repo remote = %q", got[0].RepoRemote)
	}
}
