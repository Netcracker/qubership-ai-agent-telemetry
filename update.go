package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// repoSlug is the GitHub owner/repo the releases come from; it mirrors the
// BASE_URL the installer scripts download from.
const repoSlug = "Netcracker/qubership-ai-agent-telemetry"

const updateCheckTimeout = 5 * time.Second
const selfUpdateTimeout = 30 * time.Second

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
	defer func() { _ = resp.Body.Close() }()
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

func releaseDownloadURL(tag, asset string) string {
	return "https://github.com/" + repoSlug + "/releases/download/" + tag + "/" + asset
}

func releaseAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return "", fmt.Errorf("unsupported OS %s", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported arch %s", goarch)
	}
	name := "ai-agent-telemetry-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

func downloadReleaseAsset(ctx context.Context, tag, asset, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseDownloadURL(tag, asset), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ai-agent-telemetry/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", asset, resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func fetchSHA256SUMS(ctx context.Context, tag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseDownloadURL(tag, "SHA256SUMS"), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ai-agent-telemetry/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download SHA256SUMS: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func checksumForAsset(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", asset)
}

func verifyFileSHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(want) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", want, got)
	}
	return nil
}

func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func replaceExecutable(target, tmp string) error {
	if runtime.GOOS == "windows" {
		return replaceExecutableWindows(target, tmp)
	}
	if fi, err := os.Stat(target); err == nil {
		_ = os.Chmod(tmp, fi.Mode().Perm())
	} else {
		_ = os.Chmod(tmp, 0o755)
	}
	return os.Rename(tmp, target)
}

func replaceExecutableWindows(target, tmp string) error {
	pid := os.Getpid()
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$target = %q
$tmp = %q
$pidToWait = %d
Wait-Process -Id $pidToWait -ErrorAction SilentlyContinue
Move-Item -Force $tmp $target
`, target, tmp, pid)
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("ai-agent-telemetry-self-update-%d.ps1", pid))
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	return cmd.Start()
}

func runSelfUpdate(installed string, stdout func(string)) error {
	latest, err := latestReleaseTag(updateCheckTimeout)
	if err != nil {
		return err
	}
	if compareSemver(installed, latest) >= 0 {
		stdout(fmt.Sprintf("already current: %s\n", installed))
		return nil
	}
	asset, err := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	target, err := currentExecutablePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".ai-agent-telemetry-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	if err := downloadReleaseAsset(ctx, latest, asset, tmpPath); err != nil {
		return err
	}
	sums, err := fetchSHA256SUMS(ctx, latest)
	if err != nil {
		return err
	}
	want, err := checksumForAsset(sums, asset)
	if err != nil {
		return err
	}
	if err := verifyFileSHA256(tmpPath, want); err != nil {
		return err
	}
	if err := replaceExecutable(target, tmpPath); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		keepTmp = true
	}
	stdout(fmt.Sprintf("updated: %s -> %s\n", installed, latest))
	if runtime.GOOS == "windows" {
		stdout("replacement will complete after this process exits\n")
	}
	return nil
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
