package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootDiscoveryAndCommandRouting(t *testing.T) {
	tests := []struct {
		args     []string
		wantCode int
		contains string
	}{
		{nil, 0, "Available Commands:"},
		{[]string{"hooks"}, 0, "install"},
		{[]string{"completion"}, 0, "powershell"},
		{[]string{"unknown"}, 2, "unknown command"},
		{[]string{"version"}, 0, version},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, "_"), func(t *testing.T) {
			var out, errOut bytes.Buffer
			deps := appDeps{In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir}
			root := newRootCommand(deps)
			root.SetArgs(tt.args)
			root.SetIn(deps.In)
			root.SetOut(deps.Out)
			root.SetErr(deps.ErrOut)

			code := executeCommand(root)
			combined := out.String() + errOut.String()
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; output = %q", code, tt.wantCode, combined)
			}
			if !strings.Contains(combined, tt.contains) {
				t.Fatalf("output = %q, want %q", combined, tt.contains)
			}
		})
	}
}

func TestLegacyUpdateCommandsAreNotPublic(t *testing.T) {
	root := newRootCommand(appDeps{})
	for _, name := range []string{"update-check", "self-update"} {
		command, _, err := root.Find([]string{name})
		if err == nil && command != root {
			t.Fatalf("legacy command %q remains public", name)
		}
	}
}

func TestUpdateHandoffHiddenModesRouteOutsideCobra(t *testing.T) {
	var runner updateRunnerOptions
	var cleanup cleanupImageOptions
	deps := appDeps{
		In: strings.NewReader("input"), Out: io.Discard, ErrOut: io.Discard, Home: t.TempDir,
		UpdateRunner: func(_ context.Context, options updateRunnerOptions) int {
			runner = options
			return 17
		},
		CleanupImage: func(_ context.Context, options cleanupImageOptions) int {
			cleanup = options
			return 19
		},
	}
	runnerArgs := []string{
		"__update-runner", "--managed-path", "/managed/ai-agent-telemetry", "--parent-pid", "42",
		"--release", "v0.8.0", "--", "--components", "telemetry,apm", "--non-interactive",
	}
	if code := execute(runnerArgs, deps); code != 17 {
		t.Fatalf("runner exit code = %d, want 17", code)
	}
	wantRunner := updateRunnerOptions{
		ManagedPath: "/managed/ai-agent-telemetry", ParentPID: 42, Release: "v0.8.0",
		LifecycleArgs: []string{"--components", "telemetry,apm", "--non-interactive"},
	}
	if !reflect.DeepEqual(runner, wantRunner) {
		t.Fatalf("runner options = %#v, want %#v", runner, wantRunner)
	}
	if code := execute([]string{"__cleanup-update-image", "--path", "/managed/stale.exe", "--wait-pid", "42"}, deps); code != 19 {
		t.Fatalf("cleanup exit code = %d, want 19", code)
	}
	if cleanup != (cleanupImageOptions{Path: "/managed/stale.exe", WaitPID: 42}) {
		t.Fatalf("cleanup options = %#v", cleanup)
	}

	var help bytes.Buffer
	root := newRootCommand(appDeps{Out: &help})
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(help.String(), "__update-runner") || strings.Contains(help.String(), "__cleanup-update-image") {
		t.Fatalf("hidden modes appear in public help: %q", help.String())
	}
}

func TestCobraConfigureFlags(t *testing.T) {
	command, _, err := newRootCommand(appDeps{}).Find([]string{"configure"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"endpoint", "ca", "repo-allow", "hooks", "buffer-cap", "flush-timeout"} {
		if command.Flags().Lookup(name) == nil {
			t.Errorf("configure flag --%s is not registered", name)
		}
	}
}

func TestHooksUninstallRoutesSelectedTargets(t *testing.T) {
	home := t.TempDir()
	if err := seedHookFile(hookPath(home, hookClaude), mergeClaudeHook); err != nil {
		t.Fatal(err)
	}
	if err := seedHookFile(hookPath(home, hookCursor), mergeCursorHook); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := execute([]string{"hooks", "uninstall", "--target=claude"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: func() string { return home },
	})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, errOut.String())
	}
	claudeRoot, err := readHookRoot(hookPath(home, hookClaude))
	if err != nil || inspectClaudeHook(claudeRoot) {
		t.Fatalf("Claude hook remains: %#v, %v", claudeRoot, err)
	}
	cursorRoot, err := readHookRoot(hookPath(home, hookCursor))
	if err != nil || !inspectCursorHook(cursorRoot) {
		t.Fatalf("Cursor hook changed: %#v, %v", cursorRoot, err)
	}
}

func TestHooksUninstallTargetCompletionUsesCSVValues(t *testing.T) {
	command, _, err := newRootCommand(appDeps{}).Find([]string{"hooks", "uninstall"})
	if err != nil {
		t.Fatal(err)
	}
	flag := command.Flag("target")
	if flag == nil {
		t.Fatal("hooks uninstall --target is not registered")
	}
	completion, ok := command.GetFlagCompletionFunc("target")
	if !ok {
		t.Fatal("hooks uninstall --target completion is not registered")
	}
	completions, directive := completion(command, nil, "claude,co")
	if !reflect.DeepEqual(completions, []string{"claude,codex"}) || directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Fatalf("completion = %v, %v", completions, directive)
	}
}

func TestCobraStatusVerbose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code := execute([]string{"status", "--verbose"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: func() string { return home },
	})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "config_dir:") || !strings.Contains(out.String(), "buffer_cap:") {
		t.Fatalf("output = %q, want verbose status fields", out.String())
	}
}

func TestCobraSelftest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	var out, errOut bytes.Buffer
	code := execute([]string{"selftest"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q; stderr = %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "machine is not configured") {
		t.Fatalf("stderr = %q, want self-test configuration error", errOut.String())
	}
}

func TestCobraIngestUsesRawArgumentsAndRemainsFailOpen(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	var out, errOut bytes.Buffer
	code := execute([]string{"ingest", "--agent=codex", "--endpoint=https://example.invalid"}, appDeps{
		In: strings.NewReader("{}"), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want hook-safe 0", code)
	}
	if !strings.Contains(errOut.String(), "does not accept additional arguments") {
		t.Fatalf("stderr = %q, want raw-argument validation error", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(cacheHome, pkgName)); !os.IsNotExist(err) {
		t.Fatalf("outbox was opened for rejected arguments: %v", err)
	}
}

func TestFlushCommandPrintsNothingForEmptyOutbox(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	var out, errOut bytes.Buffer
	code := execute([]string{"flush"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut.String())
	}
	if out.String() != "nothing to flush\n" {
		t.Fatalf("stdout = %q, want empty-outbox message", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestFlushCommandEmptyOutboxSkipsDeliverySettings(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envFlushTimeout, "invalid")
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = writeEnd
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = readEnd.Close()
		_ = writeEnd.Close()
	})

	var out, errOut bytes.Buffer
	code := execute([]string{"flush"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	os.Stderr = originalStderr
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	processStderr, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut.String())
	}
	if len(processStderr) != 0 {
		t.Fatalf("process stderr = %q, want no delivery-setting warnings", processStderr)
	}
}

func TestFlushCommandReturnsFailureAndRetainsQueuedEvent(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	outbox, err := DefaultOutbox()
	if err != nil {
		t.Fatal(err)
	}
	seed(t, outbox, 1)
	var out, errOut bytes.Buffer
	code := execute([]string{"flush"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q; stderr = %q", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "endpoint") {
		t.Fatalf("stderr = %q, want endpoint error", errOut.String())
	}
	assertOutboxCount(t, outbox, 1)
}

func TestCobraExplicitHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"help", "configure"}, appDeps{
		In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, errOut.String())
	}
	for _, want := range []string{"Usage:", "--repo-allow", "--flush-timeout"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want %q", out.String(), want)
		}
	}
}

func TestCobraCompletionWritesScriptsOnlyToStdout(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute([]string{"completion", shell}, appDeps{
				In: strings.NewReader(""), Out: &out, ErrOut: &errOut, Home: t.TempDir,
			})
			if code != 0 {
				t.Fatalf("exit code = %d; stderr = %q", code, errOut.String())
			}
			if out.Len() == 0 {
				t.Fatal("completion script is empty")
			}
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", errOut.String())
			}
		})
	}
}

func TestCobraUnknownCommandReturnsUsageError(t *testing.T) {
	root := newRootCommand(appDeps{})
	root.SetArgs([]string{"unknown"})
	err := root.Execute()
	var target usageError
	if !errors.As(err, &target) {
		t.Fatalf("error = %T %v, want usageError", err, err)
	}
}

func TestCobraFreshTreeIsolation(t *testing.T) {
	first := newRootCommand(appDeps{})
	configure, _, err := first.Find([]string{"configure"})
	if err != nil {
		t.Fatal(err)
	}
	if err := configure.Flags().Set("endpoint", "https://first.invalid"); err != nil {
		t.Fatal(err)
	}

	second := newRootCommand(appDeps{})
	configure, _, err = second.Find([]string{"configure"})
	if err != nil {
		t.Fatal(err)
	}
	if got := configure.Flags().Lookup("endpoint").Value.String(); got != "" {
		t.Fatalf("second tree endpoint = %q, want empty", got)
	}
}

func executeCommand(root interface {
	Execute() error
	ErrOrStderr() io.Writer
}) int {
	err := root.Execute()
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
	if isUsageError(err) {
		return 2
	}
	return 1
}
