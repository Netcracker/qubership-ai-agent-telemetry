package main

import (
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"unicode"
)

const (
	envTelemetryDisabled = "AI_AGENT_TELEMETRY_DISABLED"
	envRepoAllow         = "AI_AGENT_TELEMETRY_REPO_ALLOW"
	repoAllowFileName    = "repo-allow"
	defaultRepoAllow     = "github.com/Netcracker/*,*netcracker*/**"
)

type telemetryPolicy struct {
	Disabled         bool
	RepoAllowList    []string
	RepoAllowDefault bool
	PathAllowList    []string
	PathAllowError   error
}

func resolveTelemetryPolicy() telemetryPolicy {
	env := pkgEnv()
	repoAllow, defaulted := resolveRepoAllowList(
		os.Getenv(envRepoAllow),
		loadRepoAllowFile(pkgConfigPath(repoAllowFileName)),
	)
	pathAllow, pathErr := loadPathAllowFile(pkgConfigPath(pathAllowFileName))
	return telemetryPolicy{
		Disabled:         truthy(firstNonEmpty(os.Getenv(envTelemetryDisabled), env[envTelemetryDisabled])),
		RepoAllowList:    repoAllow,
		RepoAllowDefault: defaulted,
		PathAllowList:    pathAllow,
		PathAllowError:   pathErr,
	}
}

func (p telemetryPolicy) repoScope() string {
	if p.Disabled {
		return "disabled"
	}
	if len(p.RepoAllowList) == 0 || policyUnrestricted(p.RepoAllowList) {
		return "all"
	}
	if p.RepoAllowDefault {
		return strings.Join(p.RepoAllowList, ",") + " (default)"
	}
	return strings.Join(p.RepoAllowList, ",")
}

func (p telemetryPolicy) pathScope() string {
	if p.PathAllowError != nil {
		return "invalid"
	}
	if len(p.PathAllowList) == 0 {
		return "not configured"
	}
	return "configured"
}

func policyUnrestricted(allow []string) bool {
	return len(allow) == 1 && strings.TrimSpace(allow[0]) == "*"
}

func resolveRepoAllowList(envValue string, fileValues []string) ([]string, bool) {
	if allow := splitList(envValue); len(allow) > 0 {
		return allow, false
	}
	if len(fileValues) > 0 {
		return fileValues, false
	}
	return splitList(defaultRepoAllow), true
}

func filterEventsByPolicy(events []TelemetryEvent, policy telemetryPolicy, remotes func(string) []string) []TelemetryEvent {
	if len(events) == 0 {
		return events
	}
	out := events[:0]
	for _, ev := range events {
		if allowedRemote, ok := eventAllowedRemote(ev, policy, remotes); ok {
			ev.RepoRemote = allowedRemote
			ev.RepoDir = ""
			ev.PolicyPaths = nil
			out = append(out, ev)
		}
	}
	return out
}

func eventAllowed(ev TelemetryEvent, policy telemetryPolicy, remotes func(string) []string) bool {
	_, ok := eventAllowedRemote(ev, policy, remotes)
	return ok
}

func eventAllowedRemote(ev TelemetryEvent, policy telemetryPolicy, remotes func(string) []string) (string, bool) {
	if policy.Disabled {
		return "", false
	}
	origin := normalizeRawRemote(ev.RepoRemote)
	if len(policy.RepoAllowList) == 0 || policyUnrestricted(policy.RepoAllowList) {
		return origin, true
	}
	candidates := []string{ev.RepoRemote}
	if remotes != nil && ev.RepoDir != "" {
		candidates = append(candidates, remotes(ev.RepoDir)...)
	}
	if allowed := firstAllowedRemote(candidates, policy.RepoAllowList); allowed != "" {
		return allowed, true
	}
	if policy.PathAllowError == nil && pathAllowed(ev.PolicyPaths, policy.PathAllowList) {
		return "", true
	}
	return "", false
}

func firstAllowedRemote(remotes, allow []string) string {
	for _, remote := range remotes {
		id := normalizeRawRemote(remote)
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

func repoAllowed(remote string, allow []string) bool {
	return firstAllowedRemote([]string{remote}, allow) != ""
}

func normalizeRawRemote(raw string) string {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || hasUnsafeRemoteCharacters(raw) || strings.ContainsAny(raw, "*?[") {
		return ""
	}
	if m := scpRemoteRe.FindStringSubmatch(raw); m != nil {
		return normalizeCanonicalIdentity(m[1] + "/" + m[2])
	}
	if u, err := url.Parse(raw); err == nil && supportedRemoteScheme(u.Scheme) && u.Host != "" &&
		u.RawQuery == "" && u.Fragment == "" {
		return normalizeCanonicalIdentity(u.Host + "/" + strings.TrimPrefix(u.Path, "/"))
	}
	return ""
}

var scpRemoteRe = regexp.MustCompile(`^[^@\s]+@([^:\s]+):(.+)$`)

func normalizeCanonicalIdentity(s string) string {
	s = strings.TrimSpace(s)
	if hasUnsafeRemoteCharacters(s) || strings.Contains(s, "\\") || strings.HasPrefix(s, "/") {
		return ""
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	for i := range parts {
		if parts[i] == "" || parts[i] == "." || parts[i] == ".." {
			return ""
		}
		parts[i] = strings.ToLower(parts[i])
	}
	return strings.Join(parts, "/")
}

func supportedRemoteScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "git", "http", "https", "ssh":
		return true
	default:
		return false
	}
}

func hasUnsafeRemoteCharacters(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) >= 0
}

func repoPatternMatch(pattern, id string) bool {
	pattern = normalizeRepoPattern(pattern)
	if pattern == "" || id == "" || normalizeCanonicalIdentity(id) != id {
		return false
	}
	return matchPathSegments(strings.Split(pattern, "/"), strings.Split(id, "/"))
}

func normalizeRepoPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if m := scpRemoteRe.FindStringSubmatch(pattern); m != nil {
		return normalizeCanonicalIdentity(m[1] + "/" + m[2])
	}
	if u, err := url.Parse(pattern); err == nil && supportedRemoteScheme(u.Scheme) && u.Host != "" &&
		u.RawQuery == "" && u.Fragment == "" {
		return normalizeCanonicalIdentity(u.Host + "/" + strings.TrimPrefix(u.Path, "/"))
	}
	return normalizeCanonicalIdentity(pattern)
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
