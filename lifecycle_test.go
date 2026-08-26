package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunLifecyclePreflightsTelemetryBeforeMutation(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, errors.New("collector unavailable"), nil)

	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "collector unavailable") {
		t.Fatalf("runLifecycle() error = %v, want telemetry preflight failure", summary.Err)
	}
	want := []string{"preflight:telemetry"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want telemetry preflight and no mutation %v", calls, want)
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
			summary := runLifecycle(context.Background(), lifecycleOptions{Action: action}, deps)
			if summary.Err != nil {
				t.Fatal(summary.Err)
			}
			verb := string(action)
			wantCalls := []string{
				"preflight:telemetry", "managed:cli", verb + ":telemetry",
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, wantCalls)
			}
			wantNames := []string{"managed-cli", "telemetry"}
			if got := resultNames(summary.Results); !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("result names = %v, want %v", got, wantNames)
			}
		})
	}
}

func TestRunLifecycleSkipsTelemetryAfterManagedCLIFailure(t *testing.T) {
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
		"preflight:telemetry", "managed:cli",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want only independent components to continue %v", calls, wantCalls)
	}
	wantStates := []operationState{operationFailed, operationSkipped}
	if got := resultStates(summary.Results); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("states = %v, want %v", got, wantStates)
	}
	if detail := summary.Results[1].Detail; !strings.Contains(detail, "managed CLI") {
		t.Fatalf("telemetry skip detail = %q, want managed CLI prerequisite", detail)
	}
}

func TestRunLifecycleContinuesAfterOptionalConfigureSkillWarning(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	deps.ConfigureSkill = configureSkillService{
		Install: func(context.Context, lifecycleOptions) operationResult {
			calls = append(calls, "install:configure-skill")
			return operationResult{Name: "configure-skill", State: operationWarn, Detail: "APM command failed", Err: errors.New("failed")}
		},
	}

	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall}, deps)
	if summary.Err != nil {
		t.Fatalf("runLifecycle() error = %v, want optional warning to stay non-fatal", summary.Err)
	}
	wantCalls := []string{
		"preflight:telemetry", "managed:cli", "install:telemetry", "install:configure-skill",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want lifecycle continuation %v", calls, wantCalls)
	}
	last := summary.Results[len(summary.Results)-1]
	if last.Name != "configure-skill" || last.State != operationWarn {
		t.Fatalf("last result = %#v, want visible configure-skill WARN", last)
	}
}

func TestRunLifecycleUninstallsTelemetryAndConfigureSkillBeforeRemovingCLI(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	deps.ConfigureSkill = configureSkillService{Uninstall: func(context.Context, lifecycleOptions) operationResult {
		calls = append(calls, "uninstall:configure-skill")
		return operationResult{Name: "configure-skill", State: operationWarn, Detail: "APM unavailable"}
	}}
	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUninstall}, deps)
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	want := []string{
		"preflight:telemetry", "preflight-remove:cli", "uninstall:telemetry",
		"uninstall:configure-skill", "uninstall:cli",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
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
			name: "telemetry unknown state",
			configure: func(deps *lifecycleDeps) {
				deps.Telemetry.Install = func(context.Context, lifecycleOptions) operationResult {
					return operationResult{Name: "telemetry", State: "PENDING", Detail: "waiting"}
				}
			},
			result: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			deps := fakeLifecycleDeps(&calls, nil, nil)
			tt.configure(&deps)
			summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall}, deps)
			if summary.Err == nil || !strings.Contains(summary.Err.Error(), "invalid operation state") {
				t.Fatalf("runLifecycle() error = %v, want invalid-state failure", summary.Err)
			}
			result := summary.Results[tt.result]
			if result.State != operationFailed {
				t.Fatalf("result = %#v, want FAILED", result)
			}
			if !strings.Contains(result.Detail, "report OK, SKIPPED, WARN, or FAILED") {
				t.Fatalf("detail = %q, want actionable valid-state guidance", result.Detail)
			}
		})
	}
}

func TestRunLifecycleTelemetryFailurePreventsCLIRemoval(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, map[string]error{"uninstall:telemetry": errors.New("hook cleanup failed")})
	deps.ConfigureSkill = configureSkillService{Uninstall: func(context.Context, lifecycleOptions) operationResult {
		calls = append(calls, "uninstall:configure-skill")
		return operationResult{Name: "configure-skill", State: operationWarn, Detail: "APM unavailable"}
	}}
	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUninstall}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "hook cleanup failed") {
		t.Fatalf("runLifecycle() error = %v, want cleanup failure", summary.Err)
	}
	if containsString(calls, "uninstall:cli") {
		t.Fatalf("calls = %v, CLI removal must not run after telemetry cleanup failure", calls)
	}
	if !containsString(calls, "uninstall:configure-skill") {
		t.Fatalf("calls = %v, configure-skill cleanup must remain best effort", calls)
	}
	last := summary.Results[len(summary.Results)-1]
	if last.Name != "managed-cli" || last.State != operationSkipped {
		t.Fatalf("last result = %#v, want skipped managed CLI", last)
	}
}

func TestRunLifecyclePreservesCLIAndTelemetryDataForModifiedClineHook(t *testing.T) {
	tests := []struct {
		name            string
		modifiedContent []byte
	}{
		{name: "default"},
		{
			name: "UTF-8 BOM ownership comment",
			modifiedContent: append([]byte{0xef, 0xbb, 0xbf},
				[]byte("# "+clineHookOwner+"\ncustom-hook-command\n")...),
		},
		{
			name: "UTF-16LE ownership comment with incomplete tail",
			modifiedContent: append(encodeUTF16Test(
				"# "+clineHookOwner+"\r\ncustom-hook-command\r\n",
				binary.LittleEndian, []byte{0xff, 0xfe}), 0x23),
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
			if tt.modifiedContent != nil {
				modified = tt.modifiedContent
			}
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
			deps.Telemetry = newTelemetryComponent(telemetryDeps{
				Home: func() string { return home }, ConfigDir: func() string { return configDir },
				CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
			})

			summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUninstall, Purge: true}, deps)
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

func TestRunLifecycleResolvesModifiedClineHookAfterManualEdit(t *testing.T) {
	home := t.TempDir()
	clinePath := hookPath(home, hookCline)
	if err := os.MkdirAll(filepath.Dir(clinePath), 0o700); err != nil {
		t.Fatal(err)
	}
	conflict := []byte("# " + clineHookOwner + "\ncustom-hook-command\n")
	if err := os.WriteFile(clinePath, conflict, clineHookMode(runtime.GOOS)); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(home, ".config", pkgName)
	cacheDir := filepath.Join(home, ".cache", pkgName)
	for _, path := range []string{configDir, cacheDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	var firstCalls []string
	firstDeps := fakeLifecycleDeps(&firstCalls, nil, nil)
	firstDeps.Telemetry = newTelemetryComponent(telemetryDeps{
		Home: func() string { return home }, ConfigDir: func() string { return configDir },
		CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
	})
	opts := lifecycleOptions{Action: actionUninstall, Purge: true}
	first := runLifecycle(context.Background(), opts, firstDeps)
	if first.Err == nil || !strings.Contains(first.Err.Error(), "docs/manual-uninstall.md") {
		t.Fatalf("first uninstall error = %v, want manual conflict-resolution guidance", first.Err)
	}
	if containsString(firstCalls, "uninstall:cli") {
		t.Fatalf("first calls = %v, managed CLI must remain while the ownership comment remains", firstCalls)
	}
	for _, path := range []string{configDir, cacheDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("first uninstall removed telemetry data: %s: %v", path, err)
		}
	}

	userHook := []byte("custom-hook-command\n")
	if err := os.WriteFile(clinePath, userHook, clineHookMode(runtime.GOOS)); err != nil {
		t.Fatal(err)
	}
	var secondCalls []string
	secondDeps := fakeLifecycleDeps(&secondCalls, nil, nil)
	secondDeps.Telemetry = newTelemetryComponent(telemetryDeps{
		Home: func() string { return home }, ConfigDir: func() string { return configDir },
		CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
	})
	second := runLifecycle(context.Background(), opts, secondDeps)
	if second.Err != nil {
		t.Fatalf("second uninstall error = %v", second.Err)
	}
	if !containsString(secondCalls, "uninstall:cli") {
		t.Fatalf("second calls = %v, want managed CLI removal", secondCalls)
	}
	if got, err := os.ReadFile(clinePath); err != nil || !bytes.Equal(got, userHook) {
		t.Fatalf("user-owned hook = %q, %v; want byte-for-byte preservation", got, err)
	}
	for _, path := range []string{configDir, cacheDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("telemetry data remains after resolved uninstall: %s: %v", path, err)
		}
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
			deps.Telemetry = newTelemetryComponent(telemetryDeps{
				Home: func() string { return home }, ConfigDir: func() string { return configDir },
				CacheDir: func() string { return cacheDir }, Warnings: &bytes.Buffer{},
			})
			summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionUninstall, Purge: true}, deps)
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

func TestRunLifecycleNormalizesBeforeSideEffects(t *testing.T) {
	var calls []string
	deps := fakeLifecycleDeps(&calls, nil, nil)
	summary := runLifecycle(context.Background(), lifecycleOptions{Action: actionInstall, Purge: true}, deps)
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "--purge is valid only for uninstall") {
		t.Fatalf("runLifecycle() error = %v, want invalid purge action", summary.Err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no side effects for invalid options", calls)
	}
}

func TestRunLifecycleFormatsDeterministicFixedWidthSummary(t *testing.T) {
	summary := lifecycleSummary{Results: []operationResult{
		{Name: "managed-cli", State: operationOK, Detail: "installed"},
		{Name: "configure-skill", State: operationSkipped, Detail: "APM unavailable"},
		{Name: "telemetry", State: operationFailed, Detail: "collector unavailable"},
	}}
	want := "managed-cli  OK       installed\n" +
		"configure-skill SKIPPED  APM unavailable\n" +
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
		"preflight:telemetry", "managed:cli", "update:telemetry",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want exactly-once preflight and execution %v", calls, want)
	}
}

func fakeLifecycleDeps(calls *[]string, preflightFailure error, operationFailures map[string]error) lifecycleDeps {
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
		Telemetry: componentOps{
			Preflight: func(context.Context, lifecycleOptions) error {
				*calls = append(*calls, "preflight:telemetry")
				return preflightFailure
			},
			Install: func(context.Context, lifecycleOptions) operationResult {
				return result("install:telemetry", "telemetry")
			},
			Update: func(context.Context, lifecycleOptions) operationResult {
				return result("update:telemetry", "telemetry")
			},
			Uninstall: func(context.Context, lifecycleOptions) operationResult {
				return result("uninstall:telemetry", "telemetry")
			},
		},
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
