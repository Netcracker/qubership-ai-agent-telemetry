package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunLifecyclePreflightsAllSelectedComponentsBeforeMutation(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, map[componentName]error{componentTelemetry: errors.New("collector unavailable")}, nil)

	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "collector unavailable") {
		t.Fatalf("runLifecycle() error = %v, want telemetry preflight failure", summary.Err)
	}
	want := []string{"preflight:apm", "preflight:telemetry", "preflight:git-hooks"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want all preflights and no mutation %v", calls, want)
	}
	if len(summary.Results) != 0 {
		t.Fatalf("results = %v, want none after preflight failure", summary.Results)
	}
}

func TestRunLifecycleUsesFixedInstallAndUpdateOrder(t *testing.T) {
	for _, action := range []lifecycleAction{actionInstall, actionUpdate} {
		t.Run(string(action), func(t *testing.T) {
			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			summary := runLifecycle(context.Background(), lifecycleOptions{
				Action: action, Components: []componentName{componentGitHooks, componentTelemetry, componentAPM},
			}, deps)
			if summary.Err != nil {
				t.Fatal(summary.Err)
			}
			verb := string(action)
			wantCalls := []string{
				"preflight:apm", "preflight:telemetry", "preflight:git-hooks",
				"managed:cli", verb + ":apm", verb + ":telemetry", verb + ":git-hooks",
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, wantCalls)
			}
			wantNames := []string{"managed-cli", "apm", "telemetry", "git-hooks"}
			if got := resultNames(summary.Results); !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("result names = %v, want %v", got, wantNames)
			}
		})
	}
}

func TestRunLifecycleContinuesIndependentComponentsAfterFailures(t *testing.T) {
	var calls []string
	failures := map[string]error{
		"managed:cli": errors.New("CLI copy failed"),
	}
	deps := fakeLifecycleDeps(&calls, nil, failures)

	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall}, deps)
	if summary.Err == nil {
		t.Fatal("runLifecycle() error = nil, want joined operational errors")
	}
	if !strings.Contains(summary.Err.Error(), "CLI copy failed") {
		t.Fatalf("joined error %q does not contain managed CLI failure", summary.Err)
	}
	wantCalls := []string{
		"preflight:apm", "preflight:telemetry", "preflight:git-hooks",
		"managed:cli", "install:apm", "install:git-hooks",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want only independent components to continue %v", calls, wantCalls)
	}
	wantStates := []operationState{operationFailed, operationOK, operationSkipped, operationOK}
	if got := resultStates(summary.Results); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("states = %v, want %v", got, wantStates)
	}
	if detail := summary.Results[2].Detail; !strings.Contains(detail, "managed CLI") {
		t.Fatalf("telemetry skip detail = %q, want managed CLI prerequisite", detail)
	}
}

func TestRunLifecycleUninstallsComponentsBeforeRemovingCLI(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUninstall}, deps)
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	want := []string{
		"preflight:apm", "preflight:telemetry", "preflight:git-hooks", "preflight-remove:cli",
		"uninstall:apm", "uninstall:telemetry", "uninstall:git-hooks", "uninstall:cli",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunLifecyclePartialUninstallPreservesCLIUnlessRequested(t *testing.T) {
	for _, tt := range []struct {
		name      string
		removeCLI bool
		wantCLI   bool
	}{
		{name: "preserve", wantCLI: false},
		{name: "remove", removeCLI: true, wantCLI: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			summary := runLifecycle(context.Background(), lifecycleOptions{
				Action: actionUninstall, Components: []componentName{componentTelemetry}, RemoveCLI: tt.removeCLI,
			}, deps)
			if summary.Err != nil {
				t.Fatal(summary.Err)
			}
			gotCLI := containsString(calls, "uninstall:cli")
			if gotCLI != tt.wantCLI {
				t.Fatalf("calls = %v, CLI removal = %t, want %t", calls, gotCLI, tt.wantCLI)
			}
			if !tt.removeCLI {
				want := []operationResult{
					{Name: "telemetry", State: operationOK, Detail: "done"},
					{Name: "managed-cli", State: operationSkipped, Detail: "preserved for partial uninstall"},
				}
				if !reflect.DeepEqual(summary.Results, want) {
					t.Fatalf("results = %#v, want %#v", summary.Results, want)
				}
			}
		})
	}
}

func TestRunLifecycleRejectsInvalidOperationStates(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*lifecycleDeps)
		result    int
	}{
		{
			name: "managed CLI empty state",
			configure: func(deps *lifecycleDeps) {
				deps.ManagedCLI.Install = func(string) operationResult {
					return operationResult{Name: "managed-cli", Detail: "copied"}
				}
			},
			result: 0,
		},
		{
			name: "component unknown state",
			configure: func(deps *lifecycleDeps) {
				operations := deps.Components[componentAPM]
				operations.Install = func(context.Context, lifecycleOptions) operationResult {
					return operationResult{Name: "apm", State: "PENDING", Detail: "waiting"}
				}
				deps.Components[componentAPM] = operations
			},
			result: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			tt.configure(&deps)
			summary := runLifecycle(context.Background(), lifecycleOptions{
				Action: actionInstall, Components: []componentName{componentAPM},
			}, deps)
			if summary.Err == nil || !strings.Contains(summary.Err.Error(), "invalid operation state") {
				t.Fatalf("runLifecycle() error = %v, want invalid-state failure", summary.Err)
			}
			result := summary.Results[tt.result]
			if result.State != operationFailed {
				t.Fatalf("result = %#v, want FAILED", result)
			}
			if !strings.Contains(result.Detail, "report OK, SKIPPED, or FAILED") {
				t.Fatalf("detail = %q, want actionable valid-state guidance", result.Detail)
			}
		})
	}
}

func TestRunLifecycleTelemetryFailurePreventsCLIRemoval(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, map[string]error{"uninstall:telemetry": errors.New("hook cleanup failed")})
	summary := runLifecycle(context.Background(), lifecycleOptions{
		Action: actionUninstall, Components: []componentName{componentTelemetry}, RemoveCLI: true,
	}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "hook cleanup failed") {
		t.Fatalf("runLifecycle() error = %v, want cleanup failure", summary.Err)
	}
	if containsString(calls, "uninstall:cli") {
		t.Fatalf("calls = %v, CLI removal must not run after telemetry cleanup failure", calls)
	}
	last := summary.Results[len(summary.Results)-1]
	if last.Name != "managed-cli" || last.State != operationSkipped {
		t.Fatalf("last result = %#v, want skipped managed CLI", last)
	}
}

func TestRunLifecyclePreservesCLIAndTelemetryDataForModifiedClineHook(t *testing.T) {
	tests := []struct {
		name string
		opts lifecycleOptions
	}{
		{
			name: "full uninstall",
			opts: lifecycleOptions{Action: actionUninstall, Purge: true},
		},
		{
			name: "telemetry uninstall with remove CLI",
			opts: lifecycleOptions{
				Action: actionUninstall, Components: []componentName{componentTelemetry}, Purge: true, RemoveCLI: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := hookInstallError(installHooks(home, []hookTarget{hookClaude, hookCline, hookCursor})); err != nil {
				t.Fatal(err)
			}
			clinePath := hookPath(home, hookCline)
			modified := append(clineHookContent(runtime.GOOS), []byte("# local change\n")...)
			if err := os.WriteFile(clinePath, modified, clineHookMode(runtime.GOOS)); err != nil {
				t.Fatal(err)
			}

			configDir := filepath.Join(home, ".config", pkgName)
			cacheDir := filepath.Join(home, ".cache", pkgName)
			for _, path := range []string{configDir, cacheDir} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			deps.Components[componentTelemetry] = newTelemetryComponent(telemetryDeps{
				Home: func() string { return home }, ConfigDir: func() string { return configDir },
				CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
			})

			summary := runLifecycle(context.Background(), tt.opts, deps)
			if summary.Err == nil || !strings.Contains(summary.Err.Error(), "cline") {
				t.Fatalf("runLifecycle() error = %v, want incomplete Cline cleanup", summary.Err)
			}
			if containsString(calls, "uninstall:cli") {
				t.Fatalf("calls = %v, CLI removal must not run while the Cline hook remains", calls)
			}
			if got, err := os.ReadFile(clinePath); err != nil || !bytes.Equal(got, modified) {
				t.Fatalf("modified Cline hook = %q, %v; want byte-for-byte preservation", got, err)
			}
			statuses := gatherHookStatus(home)
			states := make(map[hookTarget]hookState, len(statuses))
			for _, status := range statuses {
				states[status.Target] = status.State
			}
			for _, target := range []hookTarget{hookClaude, hookCursor} {
				if state := states[target]; state != hookMissing {
					t.Fatalf("%s hook status = %s, want missing after independent cleanup", target, state)
				}
			}
			for _, path := range []string{configDir, cacheDir} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("telemetry data removed after incomplete hook cleanup: %s: %v", path, err)
				}
			}
		})
	}
}

func TestRunLifecycleRemovesCLIAndTelemetryDataForUnrelatedClineHook(t *testing.T) {
	tests := []struct {
		name    string
		symlink bool
	}{
		{name: "file"},
		{name: "symlink", symlink: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			clinePath := hookPath(home, hookCline)
			if err := os.MkdirAll(filepath.Dir(clinePath), 0o700); err != nil {
				t.Fatal(err)
			}
			original := []byte("#!/bin/sh\necho unrelated\n")
			writePath := clinePath
			if tt.symlink {
				writePath = filepath.Join(home, "unrelated-hook")
			}
			if err := os.WriteFile(writePath, original, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.symlink {
				if err := os.Symlink(writePath, clinePath); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}

			configDir := filepath.Join(home, ".config", pkgName)
			cacheDir := filepath.Join(home, ".cache", pkgName)
			for _, path := range []string{configDir, cacheDir} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			deps.Components[componentTelemetry] = newTelemetryComponent(telemetryDeps{
				Home: func() string { return home }, ConfigDir: func() string { return configDir },
				CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
			})
			summary := runLifecycle(context.Background(), lifecycleOptions{
				Action: actionUninstall, Components: []componentName{componentTelemetry}, Purge: true, RemoveCLI: true,
			}, deps)
			if summary.Err != nil {
				t.Fatalf("runLifecycle() error = %v, want unrelated hook preserved without blocking cleanup", summary.Err)
			}
			if !containsString(calls, "uninstall:cli") {
				t.Fatalf("calls = %v, want CLI removal for unrelated Cline hook", calls)
			}
			if got, err := os.ReadFile(clinePath); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("unrelated Cline hook = %q, %v; want byte-for-byte preservation", got, err)
			}
			for _, path := range []string{configDir, cacheDir} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("telemetry data remains after successful purge: %s: %v", path, err)
				}
			}
		})
	}
}

func TestRunLifecycleCLIOnlySkipsComponentPreflightAndExecution(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, map[componentName]error{componentAPM: errors.New("must not run")}, nil)
	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUpdate, CLIOnly: true}, deps)
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	if want := []string{"managed:cli"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunLifecycleNormalizesBeforeSideEffects(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	summary := runLifecycle(context.Background(), lifecycleOptions{
		Action: actionUninstall, Components: []componentName{componentAPM}, Purge: true,
	}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "--purge requires telemetry") {
		t.Fatalf("runLifecycle() error = %v, want invalid purge selection", summary.Err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no side effects for invalid options", calls)
	}
}

func TestRunLifecycleFormatsDeterministicFixedWidthSummary(t *testing.T) {
	summary := lifecycleSummary{Results: []operationResult{
		{Name: "managed-cli", State: operationOK, Detail: "installed"},
		{Name: "apm", State: operationSkipped, Detail: "already configured"},
		{Name: "telemetry", State: operationFailed, Detail: "collector unavailable"},
	}}
	want := "managed-cli  OK       installed\n" +
		"apm          SKIPPED  already configured\n" +
		"telemetry    FAILED   collector unavailable\n"
	if got := formatLifecycleSummary(summary); got != want {
		t.Fatalf("formatLifecycleSummary() = %q, want %q", got, want)
	}
}

func TestPreparedLifecycleDoesNotRepeatPreflightAfterUpdateSwap(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	opts, err := normalizeLifecycleOptions(lifecycleOptions{Action: actionUpdate})
	if err != nil {
		t.Fatal(err)
	}
	if err := preflightLifecycle(context.Background(), opts, deps); err != nil {
		t.Fatal(err)
	}
	summary := executePreparedLifecycle(context.Background(), opts, deps)
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	want := []string{
		"preflight:apm", "preflight:telemetry", "preflight:git-hooks",
		"managed:cli", "update:apm", "update:telemetry", "update:git-hooks",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want exactly-once preflight and execution %v", calls, want)
	}
}

func fakeLifecycleDeps(calls *[]string, preflightFailures map[componentName]error, operationFailures map[string]error) lifecycleDeps {
	result := func(call, name string) operationResult {
		*calls = append(*calls, call)
		if err := operationFailures[call]; err != nil {
			return operationResult{Name: name, State: operationFailed, Detail: err.Error(), Err: err}
		}
		return operationResult{Name: name, State: operationOK, Detail: "done"}
	}
	deps := lifecycleDeps{
		ManagedCLI: managedCLIService{
			Install: func(string) operationResult {
				return result("managed:cli", "managed-cli")
			},
			Remove: func() operationResult { return result("uninstall:cli", "managed-cli") },
			PreflightRemove: func(lifecycleOptions) error {
				*calls = append(*calls, "preflight-remove:cli")
				return nil
			},
		},
		Components: make(map[componentName]componentOps),
	}
	for _, component := range allComponents() {
		component := component
		deps.Components[component] = componentOps{
			Preflight: func(context.Context, lifecycleOptions) error {
				*calls = append(*calls, "preflight:"+string(component))
				return preflightFailures[component]
			},
			Install: func(context.Context, lifecycleOptions) operationResult {
				return result("install:"+string(component), string(component))
			},
			Update: func(context.Context, lifecycleOptions) operationResult {
				return result("update:"+string(component), string(component))
			},
			Uninstall: func(context.Context, lifecycleOptions) operationResult {
				return result("uninstall:"+string(component), string(component))
			},
		}
	}
	return deps
}

func resultNames(results []operationResult) []string {
	names := make([]string, len(results))
	for i, result := range results {
		names[i] = result.Name
	}
	return names
}

func resultStates(results []operationResult) []operationState {
	states := make([]operationState, len(results))
	for i, result := range results {
		states[i] = result.State
	}
	return states
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
