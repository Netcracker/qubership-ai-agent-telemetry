package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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

func TestLifecycleCommandsArePublicWithDocumentedFlags(t *testing.T) {
	root := newRootCommand(appDeps{})
	want := map[string][]string{
		"install":   {"components", "skip", "harnesses", "force-git-hooks", "non-interactive"},
		"update":    {"components", "skip", "harnesses", "force-git-hooks", "non-interactive", "cli-only"},
		"uninstall": {"components", "skip", "purge", "remove-cli"},
	}
	for name, flags := range want {
		command, _, err := root.Find([]string{name})
		if err != nil || command == root {
			t.Fatalf("command %q was not registered: %v", name, err)
		}
		for _, flag := range flags {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%s --%s was not registered", name, flag)
			}
		}
	}
}

func TestLifecycleEnumFlagsDescribeAcceptedValues(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"install", "--help"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	for _, want := range []string{
		"all, apm, telemetry, git-hooks",
		"all, claude, cline, codex, cursor",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help = %q, want %q", out.String(), want)
		}
	}
}

func TestUnknownLifecycleEnumListsAcceptedValues(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "--components", "bogus"},
			"valid components: all, apm, telemetry, git-hooks"},
		{[]string{"install", "--harnesses", "bogus"},
			"valid harnesses: all, claude, cline, codex, cursor"},
	} {
		var errOut bytes.Buffer
		code := execute(tt.args, appDeps{ErrOut: &errOut, Home: t.TempDir})
		if code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Fatalf("args = %v, code = %d, stderr = %q", tt.args, code, errOut.String())
		}
	}
}

func TestConfigureHooksCompletionUsesDocumentedValues(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"__complete", "configure", "--hooks", ""}, appDeps{
		Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	for _, want := range []string{"all", "none", "claude", "codex", "cursor", ":6"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("completion = %q, want %q", out.String(), want)
		}
	}
}

func TestCompletionLifecycleCSVFlags(t *testing.T) {
	command, _, err := newRootCommand(appDeps{}).Find([]string{"install"})
	if err != nil {
		t.Fatal(err)
	}
	completion, ok := command.GetFlagCompletionFunc("components")
	if !ok {
		t.Fatal("install --components completion was not registered")
	}
	got, directive := completion(command, nil, "apm,t")
	if want := []string{"apm,telemetry"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completion = %v, want %v", got, want)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Fatalf("directive = %v, want NoFileComp", directive)
	}
	for commandName, flags := range map[string][]string{
		"install":   {"components", "skip", "harnesses"},
		"update":    {"components", "skip", "harnesses"},
		"uninstall": {"components", "skip"},
	} {
		command, _, err := newRootCommand(appDeps{}).Find([]string{commandName})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range flags {
			if _, ok := command.GetFlagCompletionFunc(flag); !ok {
				t.Errorf("%s --%s completion was not registered", commandName, flag)
			}
		}
	}
}

func TestLifecycleCommandRoutesNormalizedOptionsAndSummary(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want lifecycleOptions
	}{
		{name: "separate values", args: []string{"install", "--components", "telemetry,apm", "--skip", "apm", "--harnesses", "codex,claude", "--force-git-hooks", "--non-interactive"},
			want: lifecycleOptions{Action: actionInstall, Components: []componentName{componentTelemetry}, Harnesses: []hookTarget{hookClaude, hookCodex}, ForceGitHooks: true, NonInteractive: true}},
		{name: "equals values", args: []string{"uninstall", "--components=telemetry", "--purge", "--remove-cli"},
			want: lifecycleOptions{Action: actionUninstall, Components: []componentName{componentTelemetry}, Purge: true, RemoveCLI: true}},
		{name: "defaults", args: []string{"install"}, want: lifecycleOptions{Action: actionInstall, Components: allComponents(), Harnesses: append([]hookTarget(nil), allHookTargets...)}},
		{name: "CLI only", args: []string{"update", "--cli-only"}, want: lifecycleOptions{Action: actionUpdate, CLIOnly: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got lifecycleOptions
			deps := lifecycleCaptureDeps(&got, nil)
			var out, errOut bytes.Buffer
			code := execute(tt.args, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir, Lifecycle: deps,
				Update: updateHandoff{Prepare: func(context.Context, []string) (handoffResult, error) { return handoffResult{}, nil }}},
			)
			if code != 0 {
				t.Fatalf("code = %d; stderr = %q", code, errOut.String())
			}
			if !tt.want.CLIOnly && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("options = %#v, want %#v", got, tt.want)
			}
			if !strings.Contains(out.String(), "managed-cli  OK") {
				t.Fatalf("summary = %q", out.String())
			}
			if tt.want.CLIOnly && (got.Action != "" || strings.Contains(out.String(), "apm") || strings.Contains(out.String(), "telemetry") || strings.Contains(out.String(), "git-hooks")) {
				t.Fatalf("CLI-only update ran a component: options = %#v, summary = %q", got, out.String())
			}
		})
	}
}

func TestLifecycleCommandUsageAndOperationalExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		failure  error
		wantCode int
		want     string
	}{
		{name: "invalid combination", args: []string{"update", "--cli-only", "--components", "apm"}, wantCode: 2, want: "--cli-only cannot be combined"},
		{name: "CLI only with skip", args: []string{"update", "--cli-only", "--skip", "apm"}, wantCode: 2, want: "--cli-only cannot be combined"},
		{name: "CLI only with harnesses", args: []string{"update", "--cli-only", "--harnesses", "codex"}, wantCode: 2, want: "--cli-only cannot be combined"},
		{name: "CLI only with force Git hooks", args: []string{"update", "--cli-only", "--force-git-hooks"}, wantCode: 2, want: "--cli-only cannot be combined"},
		{name: "CLI only noninteractive", args: []string{"update", "--cli-only", "--non-interactive"}, wantCode: 2, want: "--cli-only cannot be combined"},
		{name: "skip all", args: []string{"install", "--skip", "all"}, wantCode: 2, want: "component selection must not be empty"},
		{name: "purge without telemetry", args: []string{"uninstall", "--components", "apm", "--purge"}, wantCode: 2, want: "--purge requires telemetry"},
		{name: "remove CLI without telemetry", args: []string{"uninstall", "--components", "git-hooks", "--remove-cli"}, wantCode: 2, want: "--remove-cli requires telemetry"},
		{name: "uninstall harnesses", args: []string{"uninstall", "--harnesses", "codex"}, wantCode: 2, want: "unknown flag: --harnesses"},
		{name: "unknown option", args: []string{"install", "--wat"}, wantCode: 2, want: "unknown flag: --wat"},
		{name: "operational failure", args: []string{"install"}, failure: errors.New("disk full"), wantCode: 1, want: "disk full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got lifecycleOptions
			var out, errOut bytes.Buffer
			code := execute(tt.args, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir, Lifecycle: lifecycleCaptureDeps(&got, tt.failure)})
			if code != tt.wantCode || !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("code = %d, stderr = %q; want %d and %q", code, errOut.String(), tt.wantCode, tt.want)
			}
			if tt.wantCode == 2 && out.Len() != 0 {
				t.Fatalf("usage-invalid command produced lifecycle output %q", out.String())
			}
		})
	}
}

func TestLegacyOptionErrorsAreActionable(t *testing.T) {
	for _, tt := range []struct{ option, hint string }{
		{"--force-update", "use update"}, {"--force", "update --components telemetry"}, {"--skip-config", "--skip telemetry"},
		{"--force=false", "update --components telemetry"}, {"--force-update=false", "use update"}, {"--skip-config=false", "--skip telemetry"},
		{"-ForceUpdate", "use update"}, {"-Force", "update --components telemetry"}, {"-SkipConfig", "--skip telemetry"},
	} {
		var errOut bytes.Buffer
		code := execute([]string{"install", tt.option}, appDeps{ErrOut: &errOut, Home: t.TempDir})
		if code != 2 || !strings.Contains(errOut.String(), tt.hint) {
			t.Errorf("%s: code = %d, stderr = %q", tt.option, code, errOut.String())
		}
	}
	var errOut bytes.Buffer
	if code := execute([]string{"--force-update"}, appDeps{ErrOut: &errOut, Home: t.TempDir}); code != 2 || !strings.Contains(errOut.String(), "use update") {
		t.Fatalf("root legacy option: code = %d, stderr = %q", code, errOut.String())
	}
	errOut.Reset()
	if code := execute([]string{"install", "-Forceful"}, appDeps{ErrOut: &errOut, Home: t.TempDir}); code != 2 || !strings.Contains(errOut.String(), "unknown shorthand flag") {
		t.Fatalf("near-match unknown option: code = %d, stderr = %q", code, errOut.String())
	}
}

func TestRemovedCommandsReturnActionableMigrationErrors(t *testing.T) {
	for _, tt := range []struct{ command, hint string }{
		{"update-check", "use update"},
		{"self-update", "use update --cli-only"},
	} {
		t.Run(tt.command, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute([]string{tt.command}, appDeps{
				Out: &out, ErrOut: &errOut, Home: t.TempDir, Lifecycle: failingLifecycleDeps(t),
			})
			if code != 2 || !strings.Contains(errOut.String(), tt.hint) || out.Len() != 0 {
				t.Fatalf("%s: code = %d, stdout = %q, stderr = %q", tt.command, code, out.String(), errOut.String())
			}
		})
	}

	var help bytes.Buffer
	root := newRootCommand(appDeps{Out: &help})
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(help.String(), "update-check") || strings.Contains(help.String(), "self-update") {
		t.Fatalf("root help exposes removed commands: %q", help.String())
	}
}

func TestUnknownCommandIncludesSuggestionWithoutUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"instll"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 2 || !strings.Contains(errOut.String(), "Did you mean this?") ||
		!strings.Contains(errOut.String(), "install") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
	}
	if strings.Contains(errOut.String(), "Usage:") || out.Len() != 0 {
		t.Fatalf("unknown command rendered usage: stdout = %q, stderr = %q", out.String(), errOut.String())
	}
}

func failingLifecycleDeps(t *testing.T) lifecycleDeps {
	t.Helper()
	fail := func() {
		t.Fatal("removed command must not invoke lifecycle dependencies")
	}
	deps := lifecycleDeps{
		ManagedCLI: managedCLIService{
			Install: func(string) operationResult { fail(); return operationResult{} },
			Remove:  func() operationResult { fail(); return operationResult{} },
		},
		Components: map[componentName]componentOps{},
	}
	for _, name := range allComponents() {
		deps.Components[name] = componentOps{
			Install: func(context.Context, lifecycleOptions) operationResult { fail(); return operationResult{} },
			Update:  func(context.Context, lifecycleOptions) operationResult { fail(); return operationResult{} },
			Uninstall: func(context.Context, lifecycleOptions) operationResult {
				fail()
				return operationResult{}
			},
		}
	}
	return deps
}

func TestCompletionCommandReturnsConcreteLifecycleCandidates(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"__complete", "install", "--components", "apm,t"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "apm,telemetry") || !strings.Contains(out.String(), ":6") {
		t.Fatalf("completion output = %q, want retained-prefix candidate and NoFileComp|NoSpace directive", out.String())
	}
}

func TestCompletionSkipDoesNotSuggestInvalidAllSelection(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"__complete", "install", "--skip", "a"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "all") || !strings.Contains(out.String(), "apm") {
		t.Fatalf("skip completion = %q, want apm without invalid all", out.String())
	}
}

func TestLifecycleCommandPreservesHandoffExitStatus(t *testing.T) {
	for _, childCode := range []int{2, 37} {
		t.Run(fmt.Sprintf("code_%d", childCode), func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute([]string{"update", "--cli-only"}, appDeps{
				Out: &out, ErrOut: &errOut, Home: t.TempDir,
				Update: updateHandoff{Prepare: func(context.Context, []string) (handoffResult, error) {
					return handoffResult{HandedOff: true, ExitCode: childCode}, nil
				}},
			})
			if code != childCode {
				t.Fatalf("execute() = %d, want child code %d", code, childCode)
			}
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q, want child diagnostics only", errOut.String())
			}
		})
	}
}

func TestHiddenUpdateRunnerUsesCanonicalManagedSource(t *testing.T) {
	home := t.TempDir()
	managed := managedCLIPath(home, runtime.GOOS)
	var source string
	deps := lifecycleDeps{
		ManagedCLI: managedCLIService{Install: func(value string) operationResult {
			source = value
			return operationResult{Name: "managed-cli", State: operationOK, Detail: "unchanged"}
		}},
		Components: map[componentName]componentOps{},
	}
	var out, errOut bytes.Buffer
	code := execute([]string{
		"__update-runner", "--managed-path", managed, "--parent-pid", "42", "--release", "v1.0.0", "--", "--cli-only",
	}, appDeps{Out: &out, ErrOut: &errOut, Home: func() string { return home }, Lifecycle: deps})
	if code != 0 || source != managed {
		t.Fatalf("code = %d, managed install source = %q, want canonical %q; stderr = %q", code, source, managed, errOut.String())
	}
}

func lifecycleCaptureDeps(captured *lifecycleOptions, failure error) lifecycleDeps {
	result := func(name string) operationResult {
		if failure != nil && name == string(componentTelemetry) {
			return operationResult{Name: name, State: operationFailed, Detail: failure.Error(), Err: failure}
		}
		return operationResult{Name: name, State: operationOK, Detail: "done"}
	}
	deps := lifecycleDeps{ManagedCLI: managedCLIService{Install: func(string) operationResult { return result("managed-cli") }, Remove: func() operationResult { return result("managed-cli") }}, Components: map[componentName]componentOps{}}
	for _, name := range allComponents() {
		component := name
		capture := func(_ context.Context, opts lifecycleOptions) operationResult {
			*captured = opts
			return result(string(component))
		}
		deps.Components[component] = componentOps{Install: capture, Update: capture, Uninstall: capture}
	}
	return deps
}

func TestUpdateHandoffHiddenModesRouteOutsideCobra(t *testing.T) {
	home := t.TempDir()
	managed := managedCLIPath(home, runtime.GOOS)
	nonce := randHex()
	if len(nonce) != 8 || nonce != strings.ToLower(nonce) {
		t.Fatalf("randHex() = %q, want eight lowercase hexadecimal characters", nonce)
	}
	stale := filepath.Join(filepath.Dir(managed), "."+filepath.Base(managed)+".update-old-42-"+nonce)
	var runner updateRunnerOptions
	var cleanup cleanupImageOptions
	deps := appDeps{
		In: strings.NewReader("input"), Out: io.Discard, ErrOut: io.Discard, Home: func() string { return home },
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
		"__update-runner", "--managed-path", managed, "--parent-pid", "42",
		"--release", "v0.8.0", "--", "--components", "telemetry,apm", "--non-interactive",
	}
	if code := execute(runnerArgs, deps); code != 17 {
		t.Fatalf("runner exit code = %d, want 17", code)
	}
	wantRunner := updateRunnerOptions{
		ManagedPath: managed, ParentPID: 42, Release: "v0.8.0",
		LifecycleArgs: []string{"--components", "telemetry,apm", "--non-interactive"},
	}
	if !reflect.DeepEqual(runner, wantRunner) {
		t.Fatalf("runner options = %#v, want %#v", runner, wantRunner)
	}
	if code := execute([]string{"__cleanup-update-image", "--path", stale, "--wait-pid", "42"}, deps); code != 19 {
		t.Fatalf("cleanup exit code = %d, want 19", code)
	}
	if cleanup != (cleanupImageOptions{Path: stale, WaitPID: 42}) {
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

func TestHiddenUpdateModesRejectNoncanonicalTargetsAndInvalidPIDs(t *testing.T) {
	home := t.TempDir()
	managed := managedCLIPath(home, runtime.GOOS)
	dir := filepath.Dir(managed)
	base := filepath.Base(managed)
	validStale := filepath.Join(dir, "."+base+".update-old-42-deadbeef")
	tests := []struct {
		name string
		args []string
	}{
		{name: "runner unrelated path", args: []string{
			"__update-runner", "--managed-path", filepath.Join(home, "other"), "--parent-pid", "42", "--release", "v1", "--",
		}},
		{name: "runner noncanonical path", args: []string{
			"__update-runner", "--managed-path", dir + string(filepath.Separator) + ".." + string(filepath.Separator) +
				filepath.Base(dir) + string(filepath.Separator) + base, "--parent-pid", "42", "--release", "v1", "--",
		}},
		{name: "runner zero PID", args: []string{
			"__update-runner", "--managed-path", managed, "--parent-pid", "0", "--release", "v1", "--",
		}},
		{name: "cleanup unrelated path", args: []string{
			"__cleanup-update-image", "--path", filepath.Join(dir, "unrelated"), "--wait-pid", "42",
		}},
		{name: "cleanup invalid nonce", args: []string{
			"__cleanup-update-image", "--path", filepath.Join(dir, "."+base+".update-old-42-not_hex"), "--wait-pid", "42",
		}},
		{name: "cleanup mismatched PID", args: []string{
			"__cleanup-update-image", "--path", validStale, "--wait-pid", "43",
		}},
		{name: "cleanup zero PID", args: []string{
			"__cleanup-update-image", "--path", filepath.Join(dir, "."+base+".update-old-0-deadbeef"), "--wait-pid", "0",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			deps := appDeps{
				Home: func() string { return home }, ErrOut: io.Discard,
				UpdateRunner: func(context.Context, updateRunnerOptions) int { called = true; return 0 },
				CleanupImage: func(context.Context, cleanupImageOptions) int { called = true; return 0 },
			}
			if code := execute(tt.args, deps); code != 2 || called {
				t.Fatalf("execute() = %d, callback called = %t; want rejected before mutation", code, called)
			}
		})
	}
}

func TestUpdateRejectsInvalidManagedHomeBeforeHandoff(t *testing.T) {
	handoffCalled := false
	deps := appDeps{
		Home: func() string { return "relative/home" },
		Lifecycle: lifecycleDeps{
			ManagedCLI: newManagedCLIService(managedCLIConfig{
				Home: "relative/home", GOOS: "linux", Paths: &fakeManagedPathManager{},
			}),
			Components: map[componentName]componentOps{},
		},
		Update: updateHandoff{Prepare: func(context.Context, []string) (handoffResult, error) {
			handoffCalled = true
			return handoffResult{}, nil
		}},
	}
	code := execute([]string{"update", "--cli-only"}, deps)
	if code != 1 || handoffCalled {
		t.Fatalf("execute() = %d, handoff called = %t; want pre-handoff rejection", code, handoffCalled)
	}
}

func TestCobraConfigureFlags(t *testing.T) {
	command, _, err := newRootCommand(appDeps{}).Find([]string{"configure"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"endpoint", "ca", "repo-allow", "path-allow", "clear-path-allow", "hooks", "buffer-cap", "flush-timeout"} {
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

func TestCobraIngestAcceptsClineHook(t *testing.T) {
	testCobraIngestAcceptsClineHook(t, "toolName")
}

func TestCobraIngestAcceptsClineToolField(t *testing.T) {
	testCobraIngestAcceptsClineHook(t, "tool")
}

func testCobraIngestAcceptsClineHook(t *testing.T, toolField string) {
	t.Helper()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv(envRepoAllow, "github.com/Netcracker/*")
	t.Setenv(envTelemetryDisabled, "")
	repoRoot := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", repoRoot},
		{"-C", repoRoot, "remote", "add", "origin", "git@github.com:Netcracker/cline-ingest-test.git"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if got := gitRemote(repoRoot); got != "git@github.com:Netcracker/cline-ingest-test.git" {
		t.Fatalf("gitRemote(%q) = %q", repoRoot, got)
	}
	payloadBytes, err := json.Marshal(map[string]any{
		"hookName":       "PostToolUse",
		"taskId":         "cline-session",
		"workspaceRoots": []string{repoRoot},
		"postToolUse": map[string]any{
			toolField: "use_skill", "parameters": map[string]any{"skill_name": "cline-hook-probe"}, "success": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := execute([]string{"ingest", "--agent=cline"}, appDeps{
		In: bytes.NewReader(payloadBytes), Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	outbox := &Outbox{Dir: filepath.Join(cacheHome, pkgName, "outbox")}
	files, err := outbox.List()
	if err != nil || len(files) != 1 {
		t.Fatalf("outbox files = %v, err = %v; want one", files, err)
	}
	event, err := outbox.Read(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if event.Agent != "cline" || skillName(t, event) != "cline-hook-probe" {
		t.Fatalf("event = %#v", event)
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
