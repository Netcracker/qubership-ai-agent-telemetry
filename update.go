package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const (
	installVersionEnv = "AI_AGENT_TELEMETRY_INSTALL_VERSION"
	installBaseURLEnv = "AI_AGENT_TELEMETRY_INSTALL_BASE_URL"
)

const updateCheckTimeout = 5 * time.Second
const updateDownloadTimeout = 30 * time.Second

type releaseClient struct {
	Latest    func(context.Context) (string, error)
	Download  func(context.Context, string, string, string) error
	Checksums func(context.Context, string) (string, error)
}

type handoffResult struct {
	HandedOff bool
	ExitCode  int
	Release   string
}

type updateHandoff struct {
	Prepare func(context.Context, []string) (handoffResult, error)
}

type updateHandoffDeps struct {
	Installed   string
	GOOS        string
	GOARCH      string
	ManagedPath string
	Bootstrap   bool
	TempBase    string
	ParentPID   func() int
	Release     releaseClient
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Run         func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error)
}

type updateRunnerOptions struct {
	ManagedPath   string
	ParentPID     int
	Release       string
	LifecycleArgs []string
}

type updateRunnerCallbacks struct {
	Executable func() (string, error)
	Preflight  func(context.Context, []string) error
	Lifecycle  func(context.Context, []string) int
	Stdout     io.Writer
	Stderr     io.Writer
}

type cleanupImageOptions struct {
	Path    string
	WaitPID int
}

type windowsUpdateSwapOps struct {
	Exists  func(string) (bool, error)
	Stage   func(string, string) error
	Move    func(string, string) error
	Install func(string, string) error
	Remove  func(string) error
}

type cleanupImageDeps struct {
	Wait     func(context.Context, int) error
	Remove   func(string) error
	Retry    func(context.Context) error
	Attempts int
}

func routeInternalUpdateMode(ctx context.Context, args []string, deps appDeps) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "__update-runner":
		options, err := parseUpdateRunnerOptions(args[1:])
		if err != nil {
			_, _ = fmt.Fprintln(deps.ErrOut, "internal update runner:", err)
			return true, 2
		}
		return true, deps.UpdateRunner(ctx, options)
	case "__cleanup-update-image":
		options, err := parseCleanupImageOptions(args[1:])
		if err != nil {
			_, _ = fmt.Fprintln(deps.ErrOut, "update image cleanup:", err)
			return true, 2
		}
		return true, deps.CleanupImage(ctx, options)
	default:
		return false, 0
	}
}

func parseUpdateRunnerOptions(args []string) (updateRunnerOptions, error) {
	separator := -1
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return updateRunnerOptions{}, errors.New("missing lifecycle argument separator")
	}
	internal := args[:separator]
	if len(internal) != 6 {
		return updateRunnerOptions{}, errors.New("expected --managed-path, --parent-pid, and --release")
	}
	values := make(map[string]string, 3)
	for index := 0; index < len(internal); index += 2 {
		flag := internal[index]
		if flag != "--managed-path" && flag != "--parent-pid" && flag != "--release" {
			return updateRunnerOptions{}, fmt.Errorf("unknown internal option %q", flag)
		}
		if values[flag] != "" || internal[index+1] == "" {
			return updateRunnerOptions{}, fmt.Errorf("invalid internal option %q", flag)
		}
		values[flag] = internal[index+1]
	}
	parentPID, err := strconv.Atoi(values["--parent-pid"])
	if err != nil || parentPID < 0 {
		return updateRunnerOptions{}, fmt.Errorf("invalid parent process ID %q", values["--parent-pid"])
	}
	if values["--managed-path"] == "" || values["--release"] == "" {
		return updateRunnerOptions{}, errors.New("managed path and release are required")
	}
	return updateRunnerOptions{
		ManagedPath: values["--managed-path"], ParentPID: parentPID, Release: values["--release"],
		LifecycleArgs: append([]string(nil), args[separator+1:]...),
	}, nil
}

func parseCleanupImageOptions(args []string) (cleanupImageOptions, error) {
	if len(args) != 4 || args[0] != "--path" || args[2] != "--wait-pid" || args[1] == "" {
		return cleanupImageOptions{}, errors.New("expected --path <exact-path> --wait-pid <pid>")
	}
	pid, err := strconv.Atoi(args[3])
	if err != nil || pid < 0 {
		return cleanupImageOptions{}, fmt.Errorf("invalid wait process ID %q", args[3])
	}
	return cleanupImageOptions{Path: args[1], WaitPID: pid}, nil
}

func runPreparedUpdateRunner(ctx context.Context, options updateRunnerOptions, callbacks updateRunnerCallbacks) int {
	if callbacks.Stderr == nil {
		callbacks.Stderr = io.Discard
	}
	if callbacks.Stdout == nil {
		callbacks.Stdout = io.Discard
	}
	if callbacks.Executable == nil {
		callbacks.Executable = os.Executable
	}
	if callbacks.Preflight == nil || callbacks.Lifecycle == nil {
		_, _ = fmt.Fprintln(callbacks.Stderr, "internal update runner callbacks are not wired")
		return 1
	}
	if err := callbacks.Preflight(ctx, options.LifecycleArgs); err != nil {
		_, _ = fmt.Fprintln(callbacks.Stderr, "update preflight:", err)
		return 1
	}
	source, err := callbacks.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(callbacks.Stderr, "resolve verified update runner:", err)
		return 1
	}
	code, err := runPlatformUpdateRunner(source, options, func() int {
		return callbacks.Lifecycle(ctx, options.LifecycleArgs)
	}, callbacks.Stdout, callbacks.Stderr)
	if err != nil {
		_, _ = fmt.Fprintln(callbacks.Stderr, "install verified update runner:", err)
		return 1
	}
	return code
}

func runWindowsUpdateSwap(
	source, managedPath string,
	parentPID int,
	nonce string,
	startCleanup func(string, []string) error,
	lifecycle func() int,
	stderr io.Writer,
) (int, error) {
	ops := windowsUpdateSwapOps{
		Exists: func(path string) (bool, error) {
			_, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil, err
		},
		Stage:   copyExecutableAtomically,
		Move:    os.Rename,
		Install: os.Rename,
		Remove:  removeIfExists,
	}
	return runWindowsUpdateSwapWith(source, managedPath, parentPID, nonce, startCleanup, lifecycle, stderr, ops)
}

func runWindowsUpdateSwapWith(
	source, managedPath string,
	parentPID int,
	nonce string,
	startCleanup func(string, []string) error,
	lifecycle func() int,
	stderr io.Writer,
	ops windowsUpdateSwapOps,
) (int, error) {
	dir := filepath.Dir(managedPath)
	base := filepath.Base(managedPath)
	suffix := strconv.Itoa(parentPID) + "-" + nonce
	stagedPath := filepath.Join(dir, "."+base+".update-new-"+suffix)
	stalePath := filepath.Join(dir, "."+base+".update-old-"+suffix)
	staleExists, err := ops.Exists(stalePath)
	if err != nil {
		return 1, fmt.Errorf("inspect stale update path %s: %w", stalePath, err)
	}
	if staleExists {
		return 1, fmt.Errorf("refusing to overwrite stale update path %s: already exists", stalePath)
	}
	if err := ops.Stage(source, stagedPath); err != nil {
		return 1, fmt.Errorf("stage verified release on managed volume: %w", err)
	}
	stagedPresent := true
	defer func() {
		if stagedPresent {
			_ = ops.Remove(stagedPath)
		}
	}()

	managedExists, err := ops.Exists(managedPath)
	if err != nil {
		return 1, fmt.Errorf("inspect managed executable %s: %w", managedPath, err)
	}
	if !managedExists {
		if err := ops.Install(stagedPath, managedPath); err != nil {
			return 1, fmt.Errorf("install verified release at %s: %w", managedPath, err)
		}
		stagedPresent = false
		return lifecycle(), nil
	}

	if err := ops.Move(managedPath, stalePath); err != nil {
		return 1, fmt.Errorf("rename running managed executable to %s: %w", stalePath, err)
	}
	if err := ops.Install(stagedPath, managedPath); err != nil {
		rollbackErr := ops.Move(stalePath, managedPath)
		if rollbackErr != nil {
			return 1, errors.Join(
				fmt.Errorf("install verified release at %s: %w", managedPath, err),
				fmt.Errorf("restore original managed executable: %w", rollbackErr),
			)
		}
		return 1, fmt.Errorf("install verified release at %s: %w", managedPath, err)
	}
	stagedPresent = false
	cleanupArgs := []string{
		"__cleanup-update-image", "--path", stalePath, "--wait-pid", strconv.Itoa(parentPID),
	}
	if startCleanup != nil {
		if err := startCleanup(managedPath, cleanupArgs); err != nil && stderr != nil {
			_, _ = fmt.Fprintf(stderr, "warning: cannot start cleanup helper; remove leftover update image %s manually: %v\n", stalePath, err)
		}
	}
	return lifecycle(), nil
}

func cleanupUpdateImage(ctx context.Context, options cleanupImageOptions, stderr io.Writer) int {
	return cleanupUpdateImageWith(ctx, options, stderr, cleanupImageDeps{
		Wait:     waitForUpdateParent,
		Remove:   removeIfExists,
		Attempts: 20,
		Retry: func(ctx context.Context) error {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	})
}

func cleanupUpdateImageWith(
	ctx context.Context,
	options cleanupImageOptions,
	stderr io.Writer,
	deps cleanupImageDeps,
) int {
	if err := deps.Wait(ctx, options.WaitPID); err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot wait for update parent %d before removing %s: %v\n", options.WaitPID, options.Path, err)
		return 1
	}
	var lastErr error
	for attempt := 0; attempt < deps.Attempts; attempt++ {
		if err := deps.Remove(options.Path); err == nil || errors.Is(err, os.ErrNotExist) {
			return 0
		} else {
			lastErr = err
		}
		if attempt+1 < deps.Attempts {
			if err := deps.Retry(ctx); err != nil {
				lastErr = err
				break
			}
		}
	}
	_, _ = fmt.Fprintf(stderr, "cannot remove stale update image %s after %d attempts: %v\n", options.Path, deps.Attempts, lastErr)
	return 1
}

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
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(installBaseURLEnv)), "/")
	if baseURL == "" {
		baseURL = "https://github.com/" + repoSlug + "/releases"
	}
	return baseURL + "/download/" + tag + "/" + asset
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

func newUpdateHandoff(deps updateHandoffDeps) updateHandoff {
	deps = normalizeUpdateHandoffDeps(deps)
	return updateHandoff{Prepare: func(ctx context.Context, lifecycleArgs []string) (handoffResult, error) {
		if deps.Bootstrap {
			return handoffResult{}, nil
		}
		latest, err := deps.Release.Latest(ctx)
		if err != nil {
			return handoffResult{}, fmt.Errorf("resolve latest release: %w", err)
		}
		if compareSemver(deps.Installed, latest) >= 0 {
			return handoffResult{Release: latest}, nil
		}
		asset, err := releaseAssetName(deps.GOOS, deps.GOARCH)
		if err != nil {
			return handoffResult{}, err
		}
		temporaryDir, err := os.MkdirTemp(deps.TempBase, "ai-agent-telemetry-update-*")
		if err != nil {
			return handoffResult{}, fmt.Errorf("create private update directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(temporaryDir) }()
		if err := os.Chmod(temporaryDir, 0o700); err != nil {
			return handoffResult{}, fmt.Errorf("secure private update directory: %w", err)
		}
		candidate := filepath.Join(temporaryDir, asset)
		downloadCtx, cancel := context.WithTimeout(ctx, updateDownloadTimeout)
		defer cancel()
		if err := deps.Release.Download(downloadCtx, latest, asset, candidate); err != nil {
			return handoffResult{}, fmt.Errorf("download verified runner: %w", err)
		}
		sums, err := deps.Release.Checksums(downloadCtx, latest)
		if err != nil {
			return handoffResult{}, fmt.Errorf("download release checksums: %w", err)
		}
		want, err := checksumForAsset(sums, asset)
		if err != nil {
			return handoffResult{}, err
		}
		if err := verifyFileSHA256(candidate, want); err != nil {
			return handoffResult{}, fmt.Errorf("verify release runner: %w", err)
		}
		if err := os.Chmod(candidate, 0o700); err != nil {
			return handoffResult{}, fmt.Errorf("make release runner executable: %w", err)
		}
		args := []string{
			"__update-runner", "--managed-path", deps.ManagedPath,
			"--parent-pid", strconv.Itoa(deps.ParentPID()), "--release", latest, "--",
		}
		args = append(args, lifecycleArgs...)
		exitCode, err := deps.Run(ctx, candidate, args, deps.Stdin, deps.Stdout, deps.Stderr)
		if err != nil {
			return handoffResult{}, fmt.Errorf("start verified release runner: %w", err)
		}
		return handoffResult{HandedOff: true, ExitCode: exitCode, Release: latest}, nil
	}}
}

func normalizeUpdateHandoffDeps(deps updateHandoffDeps) updateHandoffDeps {
	if deps.Installed == "" {
		deps.Installed = version
	}
	if deps.GOOS == "" {
		deps.GOOS = runtime.GOOS
	}
	if deps.GOARCH == "" {
		deps.GOARCH = runtime.GOARCH
	}
	if deps.ParentPID == nil {
		deps.ParentPID = os.Getpid
	}
	if deps.Release.Latest == nil {
		deps.Release.Latest = func(ctx context.Context) (string, error) {
			if selected := strings.TrimSpace(os.Getenv(installVersionEnv)); selected != "" && selected != "latest" {
				return selected, nil
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				return latestReleaseTag(updateCheckTimeout)
			}
			return latestReleaseTag(time.Until(deadline))
		}
	}
	if deps.Release.Download == nil {
		deps.Release.Download = downloadReleaseAsset
	}
	if deps.Release.Checksums == nil {
		deps.Release.Checksums = fetchSHA256SUMS
	}
	if deps.Stdin == nil {
		deps.Stdin = os.Stdin
	}
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Run == nil {
		deps.Run = runVerifiedUpdateChild
	}
	return deps
}

func runVerifiedUpdateChild(ctx context.Context, path string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if err == nil {
		return 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode(), nil
	}
	return 0, err
}

func runUpdateWithHandoff(
	ctx context.Context,
	lifecycleArgs []string,
	handoff updateHandoff,
	runCurrent func(context.Context, []string) int,
) (int, error) {
	result, err := handoff.Prepare(ctx, lifecycleArgs)
	if err != nil {
		return 1, err
	}
	if result.HandedOff {
		return result.ExitCode, nil
	}
	return runCurrent(ctx, lifecycleArgs), nil
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
