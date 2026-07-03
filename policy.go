package main

import (
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

const (
	envTelemetryDisabled = "AI_AGENT_TELEMETRY_DISABLED"
	envRepoAllow         = "AI_AGENT_TELEMETRY_REPO_ALLOW"
	defaultRepoAllow     = "github.com/Netcracker/*"
	gitEnabledKey        = "ai-agent-telemetry.enabled"
)

type telemetryPolicy struct {
	Disabled      bool
	RepoAllowList []string
}

func resolveTelemetryPolicy() telemetryPolicy {
	env := pkgEnv()
	return telemetryPolicy{
		Disabled:      truthy(firstNonEmpty(os.Getenv(envTelemetryDisabled), env[envTelemetryDisabled])),
		RepoAllowList: splitList(firstNonEmpty(os.Getenv(envRepoAllow), env[envRepoAllow])),
	}
}

func (p telemetryPolicy) repoScope() string {
	if p.Disabled {
		return "disabled"
	}
	if len(p.RepoAllowList) == 0 {
		return "all"
	}
	return strings.Join(p.RepoAllowList, ",")
}

func filterEventsByPolicy(events []SkillEvent, policy telemetryPolicy, override func(string) *bool, remotes func(string) []string) []SkillEvent {
	if len(events) == 0 {
		return events
	}
	out := events[:0]
	for _, ev := range events {
		if allowedRemote, ok := eventAllowedRemote(ev, policy, override, remotes); ok {
			ev.RepoRemote = allowedRemote
			out = append(out, ev)
		}
	}
	return out
}

func eventAllowed(ev SkillEvent, policy telemetryPolicy, override func(string) *bool, remotes func(string) []string) bool {
	_, ok := eventAllowedRemote(ev, policy, override, remotes)
	return ok
}

func eventAllowedRemote(ev SkillEvent, policy telemetryPolicy, override func(string) *bool, remotes func(string) []string) (string, bool) {
	if policy.Disabled {
		return "", false
	}
	if override != nil {
		if v := override(ev.RepoDir); v != nil {
			if !*v {
				return "", false
			}
			if origin := remoteIdentity(ev.RepoRemote); origin != "" {
				return origin, true
			}
			if remotes != nil && ev.RepoDir != "" {
				if remote := firstRemoteIdentity(remotes(ev.RepoDir)); remote != "" {
					return remote, true
				}
			}
			return "", true
		}
	}
	origin := remoteIdentity(ev.RepoRemote)
	if len(policy.RepoAllowList) == 0 {
		return origin, true
	}
	candidates := []string{ev.RepoRemote}
	if remotes != nil && ev.RepoDir != "" {
		candidates = append(candidates, remotes(ev.RepoDir)...)
	}
	if allowed := firstAllowedRemote(candidates, policy.RepoAllowList); allowed != "" {
		return allowed, true
	}
	return "", false
}

func firstAllowedRemote(remotes, allow []string) string {
	for _, remote := range remotes {
		id := remoteIdentity(remote)
		if id == "" {
			continue
		}
		for _, pat := range allow {
			if repoPatternMatch(pat, id) {
				return id
			}
		}
	}
	return ""
}

func firstRemoteIdentity(remotes []string) string {
	for _, remote := range remotes {
		if id := remoteIdentity(remote); id != "" {
			return id
		}
	}
	return ""
}

func repoAllowed(remote string, allow []string) bool {
	return firstAllowedRemote([]string{remote}, allow) != ""
}

func remoteIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := scpRemoteRe.FindStringSubmatch(raw); m != nil {
		return cleanRemoteIdentity(m[1] + "/" + m[2])
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		return cleanRemoteIdentity(u.Host + "/" + strings.TrimPrefix(u.Path, "/"))
	}
	return cleanRemoteIdentity(raw)
}

var scpRemoteRe = regexp.MustCompile(`^[^@\s]+@([^:\s]+):(.+)$`)

func cleanRemoteIdentity(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	for len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return ""
	}
	for i := range parts {
		parts[i] = strings.ToLower(parts[i])
	}
	return strings.Join(parts, "/")
}

func repoPatternMatch(pattern, id string) bool {
	pattern = remoteIdentity(pattern)
	if pattern == "" || id == "" {
		return false
	}
	return matchPathSegments(strings.Split(pattern, "/"), strings.Split(id, "/"))
}

func matchPathSegments(pattern, id []string) bool {
	if len(pattern) == 0 {
		return len(id) == 0
	}
	if pattern[0] == "**" {
		return matchPathSegments(pattern[1:], id) || (len(id) > 0 && matchPathSegments(pattern, id[1:]))
	}
	if len(id) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], id[0])
	if err != nil || !ok {
		return false
	}
	return matchPathSegments(pattern[1:], id[1:])
}

func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\t' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func gitTelemetryOverride(cwd string) *bool {
	if cwd == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", cwd, "config", "--bool", "--get", gitEnabledKey)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	switch strings.TrimSpace(strings.ToLower(string(out))) {
	case "true":
		v := true
		return &v
	case "false":
		v := false
		return &v
	default:
		return nil
	}
}

func gitRemotes(cwd string) []string {
	if cwd == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", cwd, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var remotes []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		remotes = append(remotes, sanitizeRemote(fields[1]))
	}
	return remotes
}
