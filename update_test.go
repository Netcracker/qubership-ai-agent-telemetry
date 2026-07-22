package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.0", "0.7.0", -1},
		{"v0.7.0", "v0.6.0", 1},
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"0.6.0-dev", "0.6.0", 0}, // pre-release suffix ignored
		{"0.6.0-dev", "0.5.3", 1}, // dev build ahead of an older release
		{"0.10.0", "0.9.9", 1},    // numeric, not lexical
		{"1.0", "1.0.0", 0},       // missing patch counts as 0
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestDefaultUpdateReleaseClientUsesInstallerOverrides(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		_, _ = io.WriteString(w, "fixture")
	}))
	t.Cleanup(server.Close)
	t.Setenv("AI_AGENT_TELEMETRY_INSTALL_VERSION", "v0.2.0")
	t.Setenv("AI_AGENT_TELEMETRY_INSTALL_BASE_URL", server.URL)

	deps := normalizeUpdateHandoffDeps(updateHandoffDeps{})
	tag, err := deps.Release.Latest(context.Background())
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("Latest() = %q, %v; want v0.2.0", tag, err)
	}
	destination := filepath.Join(t.TempDir(), "asset")
	if err := deps.Release.Download(context.Background(), tag, "asset", destination); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Release.Checksums(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	want := []string{"/download/v0.2.0/asset", "/download/v0.2.0/SHA256SUMS"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("release requests = %q, want %q", requests, want)
	}
}

func TestUpdateHandoffVerifiedReleaseRunsChildAndSkipsOldLifecycle(t *testing.T) {
	temp := t.TempDir()
	assetData := []byte("verified release")
	digest := sha256.Sum256(assetData)
	sums := hex.EncodeToString(digest[:]) + "  ai-agent-telemetry-linux-amd64\n"
	var childPath string
	var childArgs []string
	var childIn io.Reader
	var childOut, childErr io.Writer
	stdin := bytes.NewBufferString("input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	handoff := newUpdateHandoff(updateHandoffDeps{
		Installed: "0.7.0", GOOS: "linux", GOARCH: "amd64", ManagedPath: "/home/test/.local/bin/ai-agent-telemetry",
		TempBase: temp, ParentPID: func() int { return 0 }, Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Release: releaseClient{
			Latest: func(context.Context) (string, error) { return "v0.8.0", nil },
			Download: func(_ context.Context, _, _, path string) error {
				return os.WriteFile(path, assetData, 0o755)
			},
			Checksums: func(context.Context, string) (string, error) { return sums, nil },
		},
		Run: func(_ context.Context, path string, args []string, in io.Reader, out, errOut io.Writer) (int, error) {
			childPath, childArgs = path, append([]string(nil), args...)
			childIn, childOut, childErr = in, out, errOut
			return 23, nil
		},
	})
	oldLifecycleCalled := false
	code, err := runUpdateWithHandoff(context.Background(), []string{"--components", "telemetry,apm", "--non-interactive"}, handoff,
		func(context.Context, []string) int {
			oldLifecycleCalled = true
			return 0
		})
	if err != nil || code != 23 {
		t.Fatalf("runUpdateWithHandoff() = %d, %v; want child exit 23", code, err)
	}
	if oldLifecycleCalled {
		t.Fatal("old lifecycle callback ran after newer release handoff")
	}
	wantArgs := []string{
		"__update-runner", "--managed-path", "/home/test/.local/bin/ai-agent-telemetry",
		"--parent-pid", "0", "--release", "v0.8.0", "--",
		"--components", "telemetry,apm", "--non-interactive",
	}
	if !reflect.DeepEqual(childArgs, wantArgs) {
		t.Fatalf("child args = %q, want %q", childArgs, wantArgs)
	}
	if childPath == "" || filepath.Dir(filepath.Dir(childPath)) != temp || childIn != stdin || childOut != stdout || childErr != stderr {
		t.Fatalf("child path/streams = %q, %p, %p, %p", childPath, childIn, childOut, childErr)
	}
}

func TestUpdateHandoffCurrentVersionRunsLifecycleDirectly(t *testing.T) {
	latestCalls := 0
	handoff := newUpdateHandoff(updateHandoffDeps{
		Installed: "0.8.0", GOOS: "linux", GOARCH: "amd64",
		Release: releaseClient{Latest: func(context.Context) (string, error) {
			latestCalls++
			return "v0.8.0", nil
		}},
		Run: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			t.Fatal("child started for current version")
			return 0, nil
		},
	})
	wantArgs := []string{"--components", "telemetry"}
	code, err := runUpdateWithHandoff(context.Background(), wantArgs, handoff, func(_ context.Context, args []string) int {
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("direct args = %q, want %q", args, wantArgs)
		}
		return 7
	})
	if err != nil || code != 7 || latestCalls != 1 {
		t.Fatalf("runUpdateWithHandoff() = %d, %v; latest calls = %d", code, err, latestCalls)
	}
}

func TestUpdateHandoffBootstrapRunnerSkipsReleaseLookup(t *testing.T) {
	handoff := newUpdateHandoff(updateHandoffDeps{
		Bootstrap: true,
		Release: releaseClient{Latest: func(context.Context) (string, error) {
			t.Fatal("bootstrap runner looked up latest release")
			return "", nil
		}},
	})
	called := false
	code, err := runUpdateWithHandoff(context.Background(), []string{"--cli-only"}, handoff, func(context.Context, []string) int {
		called = true
		return 0
	})
	if err != nil || code != 0 || !called {
		t.Fatalf("runUpdateWithHandoff() = %d, %v; direct called = %t", code, err, called)
	}
}

func TestUpdateHandoffRunnerPreflightsBeforeSwapAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-runner")
	managed := filepath.Join(dir, "bin", "ai-agent-telemetry")
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls []string
	code := runPreparedUpdateRunner(context.Background(), updateRunnerOptions{
		ManagedPath: managed, ParentPID: 42, Release: "v0.8.0", LifecycleArgs: []string{"--cli-only"},
	}, updateRunnerCallbacks{
		Executable: func() (string, error) { return source, nil },
		Preflight: func(_ context.Context, args []string) error {
			calls = append(calls, "preflight:"+strings.Join(args, " "))
			data, _ := os.ReadFile(managed)
			if string(data) != "old" {
				t.Fatalf("managed executable changed before preflight: %q", data)
			}
			return nil
		},
		Lifecycle: func(_ context.Context, args []string) int {
			calls = append(calls, "lifecycle:"+strings.Join(args, " "))
			data, _ := os.ReadFile(managed)
			if string(data) != "new" {
				t.Fatalf("managed executable before lifecycle: %q", data)
			}
			return 41
		},
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 41 || !reflect.DeepEqual(calls, []string{"preflight:--cli-only", "lifecycle:--cli-only"}) {
		t.Fatalf("runner code = %d, calls = %v", code, calls)
	}
}

func TestUpdateHandoffRunnerPreflightFailureLeavesManagedExecutableUntouched(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-runner")
	managed := filepath.Join(dir, "ai-agent-telemetry")
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runPreparedUpdateRunner(context.Background(), updateRunnerOptions{ManagedPath: managed}, updateRunnerCallbacks{
		Executable: func() (string, error) { return source, nil },
		Preflight:  func(context.Context, []string) error { return errors.New("configuration missing") },
		Lifecycle: func(context.Context, []string) int {
			t.Fatal("lifecycle ran after failed preflight")
			return 0
		},
		Stderr: &stderr,
	})
	data, err := os.ReadFile(managed)
	if code != 1 || err != nil || string(data) != "old" || !strings.Contains(stderr.String(), "configuration missing") {
		t.Fatalf("runner code = %d, managed = %q, read err = %v, stderr = %q", code, data, err, stderr.String())
	}
}

func TestWindowsUpdateSwapStagesOnManagedVolumeAndPreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "verified.exe")
	managed := filepath.Join(dir, "ai-agent-telemetry.exe")
	staleSibling := filepath.Join(dir, ".ai-agent-telemetry.exe.update-old-7-stale")
	unrelated := filepath.Join(dir, "keep.txt")
	for path, data := range map[string]string{source: "new", managed: "old", staleSibling: "stale", unrelated: "keep"} {
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var helperPath string
	var helperArgs []string
	var lifecycleSaw string
	var warnings bytes.Buffer
	code, err := runWindowsUpdateSwap(source, managed, 42, "nonce", func(path string, args []string) error {
		helperPath, helperArgs = path, append([]string(nil), args...)
		return nil
	}, func() int {
		data, readErr := os.ReadFile(managed)
		if readErr != nil {
			t.Fatal(readErr)
		}
		lifecycleSaw = string(data)
		return 31
	}, &warnings)
	if err != nil || code != 31 || lifecycleSaw != "new" {
		t.Fatalf("runWindowsUpdateSwap() = %d, %v; lifecycle saw %q", code, err, lifecycleSaw)
	}
	wantStale := filepath.Join(dir, ".ai-agent-telemetry.exe.update-old-42-nonce")
	wantHelperArgs := []string{"__cleanup-update-image", "--path", wantStale, "--wait-pid", "42"}
	if helperPath != managed || !reflect.DeepEqual(helperArgs, wantHelperArgs) {
		t.Fatalf("helper = %q %q, want %q %q", helperPath, helperArgs, managed, wantHelperArgs)
	}
	for path, want := range map[string]string{staleSibling: "stale", unrelated: "keep"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != want {
			t.Fatalf("sibling %s = %q, %v; want %q", path, data, readErr, want)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.update-state*")); len(matches) != 0 {
		t.Fatalf("durable update state created: %v", matches)
	}
}

func TestWindowsUpdateSwapRefusesStalePathCollision(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified.exe")
	managed := filepath.Join(dir, "ai-agent-telemetry.exe")
	stale := filepath.Join(dir, ".ai-agent-telemetry.exe.update-old-42-collision")
	for path, data := range map[string]string{source: "new", managed: "old", stale: "unrelated"} {
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	_, err := runWindowsUpdateSwap(source, managed, 42, "collision", nil, func() int {
		called = true
		return 0
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") || called {
		t.Fatalf("runWindowsUpdateSwap() error = %v; lifecycle called = %t", err, called)
	}
	for path, want := range map[string]string{managed: "old", stale: "unrelated"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != want {
			t.Fatalf("collision path %s = %q, %v; want %q", path, data, readErr, want)
		}
	}
}

func TestWindowsUpdateSwapRollsBackCanonicalInstallFailure(t *testing.T) {
	var moves [][2]string
	installFailure := errors.New("canonical copy failed")
	ops := windowsUpdateSwapOps{
		Exists: func(path string) (bool, error) {
			return strings.HasSuffix(path, "ai-agent-telemetry.exe") && !strings.Contains(path, "update-old"), nil
		},
		Stage: func(string, string) error { return nil },
		Move: func(from, to string) error {
			moves = append(moves, [2]string{from, to})
			return nil
		},
		Install: func(string, string) error { return installFailure },
		Remove:  func(string) error { return nil },
	}
	called := false
	_, err := runWindowsUpdateSwapWith("C:/temp/new.exe", "C:/bin/ai-agent-telemetry.exe", 42, "nonce", nil, func() int {
		called = true
		return 0
	}, io.Discard, ops)
	if !errors.Is(err, installFailure) || called {
		t.Fatalf("runWindowsUpdateSwapWith() error = %v; lifecycle called = %t", err, called)
	}
	wantMoves := [][2]string{
		{"C:/bin/ai-agent-telemetry.exe", "C:/bin/.ai-agent-telemetry.exe.update-old-42-nonce"},
		{"C:/bin/.ai-agent-telemetry.exe.update-old-42-nonce", "C:/bin/ai-agent-telemetry.exe"},
	}
	if !reflect.DeepEqual(moves, wantMoves) {
		t.Fatalf("moves = %#v, want rollback %#v", moves, wantMoves)
	}
}

func TestWindowsUpdateSwapHelperFailureWarnsExactLeftoverPath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified.exe")
	managed := filepath.Join(dir, "ai-agent-telemetry.exe")
	for path, data := range map[string]string{source: "new", managed: "old"} {
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var warnings bytes.Buffer
	code, err := runWindowsUpdateSwap(source, managed, 42, "leftover", func(string, []string) error {
		return errors.New("cannot start helper")
	}, func() int { return 37 }, &warnings)
	wantPath := filepath.Join(dir, ".ai-agent-telemetry.exe.update-old-42-leftover")
	if err != nil || code != 37 || !strings.Contains(warnings.String(), wantPath) {
		t.Fatalf("swap = %d, %v; warnings = %q, want exact path %q", code, err, warnings.String(), wantPath)
	}
}

func TestWindowsUpdateSwapCleanupRetriesExactPathOnly(t *testing.T) {
	var removed []string
	var waits []int
	var stderr bytes.Buffer
	options := cleanupImageOptions{Path: "C:/bin/exact-old.exe", WaitPID: 42}
	code := cleanupUpdateImageWith(context.Background(), options, &stderr, cleanupImageDeps{
		Wait: func(_ context.Context, pid int) error {
			waits = append(waits, pid)
			return nil
		},
		Remove: func(path string) error {
			removed = append(removed, path)
			return errors.New("sharing violation")
		},
		Retry:    func(context.Context) error { return nil },
		Attempts: 4,
	})
	if code != 1 || !reflect.DeepEqual(waits, []int{42}) || !reflect.DeepEqual(removed, []string{
		options.Path, options.Path, options.Path, options.Path,
	}) {
		t.Fatalf("cleanup code = %d, waits = %v, removed = %v", code, waits, removed)
	}
	if !strings.Contains(stderr.String(), options.Path) {
		t.Fatalf("cleanup diagnostic = %q, want exact path", stderr.String())
	}
}

func TestReleaseAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "ai-agent-telemetry-linux-amd64"},
		{"darwin", "arm64", "ai-agent-telemetry-darwin-arm64"},
		{"windows", "amd64", "ai-agent-telemetry-windows-amd64.exe"},
	}
	for _, c := range cases {
		got, err := releaseAssetName(c.goos, c.goarch)
		if err != nil {
			t.Fatalf("releaseAssetName(%q,%q): %v", c.goos, c.goarch, err)
		}
		if got != c.want {
			t.Fatalf("releaseAssetName(%q,%q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestReleaseAssetNameRejectsUnsupported(t *testing.T) {
	if _, err := releaseAssetName("plan9", "amd64"); err == nil {
		t.Fatal("want unsupported OS error")
	}
	if _, err := releaseAssetName("linux", "386"); err == nil {
		t.Fatal("want unsupported arch error")
	}
}

func TestChecksumForAsset(t *testing.T) {
	sums := "abcd  ai-agent-telemetry-linux-amd64\n1234  other\n"
	got, err := checksumForAsset(sums, "ai-agent-telemetry-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcd" {
		t.Fatalf("checksum = %q, want abcd", got)
	}
}

func TestChecksumForAssetMissing(t *testing.T) {
	if _, err := checksumForAsset("abcd  other\n", "missing"); err == nil {
		t.Fatal("want missing checksum error")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSHA256(path, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Fatalf("verifyFileSHA256: %v", err)
	}
	if err := verifyFileSHA256(path, "bad"); err == nil {
		t.Fatal("want checksum mismatch")
	}
}
