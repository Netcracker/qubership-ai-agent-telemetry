package main

import "testing"

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
	ev := SkillEvent{RepoRemote: "git@github.com:some-user/project.git", RepoDir: "/repo"}
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	allowed := eventAllowed(ev, policy, nil, func(cwd string) []string {
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
	ev := SkillEvent{RepoRemote: "git@github.com:some-user/project.git", RepoDir: "/repo"}
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	got := filterEventsByPolicy([]SkillEvent{ev}, policy, nil, func(string) []string {
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

func TestPolicyGitConfigOverrideWinsOverAllowlist(t *testing.T) {
	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	ev := SkillEvent{RepoRemote: "git@github.com:Netcracker/project.git", RepoDir: "/repo"}
	no := false
	if eventAllowed(ev, policy, func(string) *bool { return &no }, nil) {
		t.Fatal("git config false should deny an otherwise allowed repo")
	}
	yes := true
	ev.RepoRemote = "git@github.com:some-user/project.git"
	if !eventAllowed(ev, policy, func(string) *bool { return &yes }, nil) {
		t.Fatal("git config true should allow an otherwise denied repo")
	}
}

func TestPolicyGitConfigOverrideUsesGitRemoteWhenEventRemoteMissing(t *testing.T) {
	yes := true
	ev := SkillEvent{RepoDir: "/repo"}
	got := filterEventsByPolicy([]SkillEvent{ev}, telemetryPolicy{}, func(string) *bool {
		return &yes
	}, func(cwd string) []string {
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
	if eventAllowed(SkillEvent{}, policy, nil, nil) {
		t.Fatal("empty remote should be denied when an allowlist is configured")
	}
}

func TestFilterEventsNormalizesRepoRemoteForTelemetry(t *testing.T) {
	events := []SkillEvent{{RepoRemote: "git@github.com:Netcracker/Project.git"}}
	got := filterEventsByPolicy(events, telemetryPolicy{}, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].RepoRemote != "github.com/netcracker/project" {
		t.Fatalf("repo remote = %q", got[0].RepoRemote)
	}
}
