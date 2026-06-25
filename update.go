package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// repoSlug is the GitHub owner/repo the releases come from; it mirrors the
// BASE_URL the installer scripts download from.
const repoSlug = "Netcracker/qubership-ai-agent-telemetry"

const updateCheckTimeout = 5 * time.Second

// latestReleaseTag returns the tag_name of the latest GitHub release. The GitHub
// API requires a User-Agent header, so one is always set. A short timeout keeps
// the check from hanging a caller.
func latestReleaseTag(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	url := "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ai-agent-telemetry/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api status %d", resp.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("no tag_name in release response")
	}
	return r.TagName, nil
}

// normalizeVersion strips a leading "v" and any pre-release/build suffix, leaving
// the MAJOR.MINOR.PATCH core. So "v0.6.0", "0.6.0-dev", "0.6.0+meta" all reduce
// to "0.6.0".
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

// compareSemver returns -1 if a<b, 0 if equal, 1 if a>b, comparing the numeric
// MAJOR.MINOR.PATCH cores. Non-numeric or missing parts count as 0, so a dev or
// malformed version never spuriously reports an update.
func compareSemver(a, b string) int {
	pa := strings.Split(normalizeVersion(a), ".")
	pb := strings.Split(normalizeVersion(b), ".")
	for i := 0; i < 3; i++ {
		na, nb := 0, 0
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

// updateCheckResult is the verdict a caller (skill or, later, a hook) consumes.
type updateCheckResult struct {
	Installed string
	Latest    string
	Available bool
	Err       error
}

// gatherUpdateCheck compares the installed version against the latest one the
// fetch func reports. A fetch error yields an "unknown" verdict, never a crash:
// an update check must never become a reason telemetry stops working.
func gatherUpdateCheck(installed string, fetch func() (string, error)) updateCheckResult {
	latest, err := fetch()
	if err != nil {
		return updateCheckResult{Installed: installed, Err: err}
	}
	return updateCheckResult{
		Installed: installed,
		Latest:    latest,
		Available: compareSemver(installed, latest) < 0,
	}
}

// formatUpdateCheck renders the verdict as stable key: value lines, so a skill
// or hook can grep `update_available:` without parsing prose.
func formatUpdateCheck(r updateCheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "installed: %s\n", r.Installed)
	if r.Err != nil {
		fmt.Fprint(&b, "latest: unknown\n")
		fmt.Fprint(&b, "update_available: unknown\n")
		fmt.Fprintf(&b, "error: %s\n", r.Err.Error())
		return b.String()
	}
	fmt.Fprintf(&b, "latest: %s\n", r.Latest)
	if r.Available {
		fmt.Fprint(&b, "update_available: yes\n")
	} else {
		fmt.Fprint(&b, "update_available: no\n")
	}
	return b.String()
}
