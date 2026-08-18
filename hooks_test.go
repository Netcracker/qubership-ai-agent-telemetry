package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestInstallManagedHooksWithCleansBeforeInstalling(t *testing.T) {
	var calls []string
	var warnings strings.Builder
	results := installManagedHooksWith(
		"/home/test",
		[]hookTarget{hookClaude},
		&warnings,
		func(_ string, warnings io.Writer) {
			calls = append(calls, "cleanup")
			_, _ = fmt.Fprintln(warnings, "cleanup warning")
		},
		func(_ string, _ []hookTarget) []hookInstallResult {
			calls = append(calls, "install")
			return []hookInstallResult{{Target: hookClaude, Path: "/hook"}}
		},
	)
	if !reflect.DeepEqual(calls, []string{"cleanup", "install"}) {
		t.Fatalf("calls = %v", calls)
	}
	if err := hookInstallError(results); err != nil {
		t.Fatalf("hook result changed by cleanup warning: %v", err)
	}
}

func TestInstallManagedHooksWithSkipsCleanupForNoTargets(t *testing.T) {
	cleanupCalled := false
	installCalled := false
	results := installManagedHooksWith(
		"/home/test",
		nil,
		io.Discard,
		func(string, io.Writer) { cleanupCalled = true },
		func(_ string, targets []hookTarget) []hookInstallResult {
			installCalled = true
			if len(targets) != 0 {
				t.Fatalf("targets = %v, want empty", targets)
			}
			return nil
		},
	)
	if cleanupCalled {
		t.Fatal("cleanup called with no targets")
	}
	if !installCalled {
		t.Fatal("install not called with empty targets")
	}
	if results != nil {
		t.Fatalf("results = %#v, want nil", results)
	}
}

func TestInstallHooksContinuesAfterFailure(t *testing.T) {
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := installHooks(home, []hookTarget{hookCursor, hookClaude, hookCodex})
	if len(results) != 3 || results[0].Target != hookClaude || results[1].Target != hookCodex || results[2].Target != hookCursor {
		t.Fatalf("results = %#v, want canonical order", results)
	}
	if results[0].Err == nil || results[1].Err != nil || results[2].Err != nil {
		t.Fatalf("results = %#v, want only Claude error", results)
	}
	if !results[1].Changed || !results[2].Changed {
		t.Fatalf("successful results = %#v, want changed", results)
	}

	err := hookInstallError(results)
	if err == nil || !strings.Contains(err.Error(), "claude") || strings.Contains(err.Error(), "codex") || strings.Contains(err.Error(), "cursor") {
		t.Fatalf("error = %v, want only Claude", err)
	}
	assertInstalledHook(t, filepath.Join(home, ".codex", "hooks.json"), inspectCodexHook)
	assertInstalledHook(t, filepath.Join(home, ".cursor", "hooks.json"), inspectCursorHook)
}

func TestInstallHooksWritesCodexExecutionPolicy(t *testing.T) {
	home := t.TempDir()
	results := installHooks(home, []hookTarget{hookCodex})
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	path := codexRulePath(home)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != codexExecutionPolicy {
		t.Fatalf("rule = %q, want canonical policy", got)
	}
	if !strings.Contains(string(got), `"ai-agent-telemetry.exe"`) {
		t.Fatalf("rule does not allow the Windows executable: %s", got)
	}
	if !strings.Contains(string(got), `"ingest", "--agent=codex"`) {
		t.Fatalf("rule does not allow Codex ingest: %s", got)
	}
	if runtime.GOOS != "windows" {
		assertPerm(t, filepath.Dir(path), 0o700)
		assertPerm(t, path, 0o600)
	}

	results = installHooks(home, []hookTarget{hookCodex})
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Changed {
		t.Fatalf("second install results = %#v, want unchanged", results)
	}
}

func TestGatherHookStatusReportsInstalledMissingAndInvalid(t *testing.T) {
	home := t.TempDir()
	results := installHooks(home, allHookTargets)
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hookPath(home, hookCodex)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath(home, hookCursor), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := gatherHookStatus(home)
	if len(got) != 4 {
		t.Fatalf("statuses = %#v, want four", got)
	}
	wantStates := []hookState{hookInstalled, hookInstalled, hookMissing, hookInvalid}
	for i, target := range allHookTargets {
		if got[i].Target != target || got[i].Path != hookPath(home, target) || got[i].State != wantStates[i] {
			t.Fatalf("status[%d] = %#v", i, got[i])
		}
	}
	if got[0].Detail != "" || got[1].Detail != "" || got[2].Detail != "" {
		t.Fatalf("non-invalid details = %#v", got)
	}
	if !strings.Contains(got[3].Detail, "parse") {
		t.Fatalf("Cursor detail = %q, want parse error", got[3].Detail)
	}
}

func TestGatherHookStatusDoesNotCreateFiles(t *testing.T) {
	home := t.TempDir()
	got := gatherHookStatus(home)
	for i, status := range got {
		if status.State != hookMissing {
			t.Fatalf("status[%d] = %#v, want missing", i, status)
		}
		if _, err := os.Stat(filepath.Dir(status.Path)); !os.IsNotExist(err) {
			t.Fatalf("status created %s: %v", filepath.Dir(status.Path), err)
		}
	}
}

func TestGatherHookStatusRequiresCodexExecutionPolicy(t *testing.T) {
	home := t.TempDir()
	if _, err := updateHookFile(hookPath(home, hookCodex), mergeCodexHook); err != nil {
		t.Fatal(err)
	}

	statuses := gatherHookStatus(home)
	status := statuses[2]
	if status.Target != hookCodex || status.State != hookInvalid {
		t.Fatalf("Codex status = %#v, want invalid", status)
	}
	if !strings.Contains(status.Detail, codexRulePath(home)) {
		t.Fatalf("Codex detail = %q, want missing rule path", status.Detail)
	}
}

func TestInstallHooksContinuesAfterClineConflict(t *testing.T) {
	home := t.TempDir()
	path := hookPath(home, hookCline)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho unrelated\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	results := installHooks(home, []hookTarget{hookCursor, hookCline, hookClaude})
	if len(results) != 3 || results[0].Target != hookClaude || results[1].Target != hookCline || results[2].Target != hookCursor {
		t.Fatalf("results = %#v, want canonical order", results)
	}
	if results[0].Err != nil || results[1].Err == nil || results[2].Err != nil {
		t.Fatalf("results = %#v, want only Cline error", results)
	}
	assertInstalledHook(t, hookPath(home, hookClaude), inspectClaudeHook)
	assertInstalledHook(t, hookPath(home, hookCursor), inspectCursorHook)
}

func TestGatherHookStatusUnavailableHomeIgnoresRelativeHooks(t *testing.T) {
	workingDir := t.TempDir()
	results := installHooks(workingDir, allHookTargets)
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workingDir)

	got := gatherHookStatus("")
	if len(got) != len(allHookTargets) {
		t.Fatalf("statuses = %#v, want %d", got, len(allHookTargets))
	}
	for i, status := range got {
		if status.Target != allHookTargets[i] || status.Path != "" || status.State != hookInvalid {
			t.Fatalf("status[%d] = %#v, want unavailable-home invalid status", i, status)
		}
		if !strings.Contains(status.Detail, "user home directory") {
			t.Fatalf("status[%d] detail = %q, want actionable unavailable-home error", i, status.Detail)
		}
	}
	if err := hookInstallError(installHooks("", allHookTargets)); err == nil || !strings.Contains(err.Error(), "user home directory") {
		t.Fatalf("install error = %v, want actionable unavailable-home error", err)
	}
}

func assertInstalledHook(t *testing.T, path string, inspect func(map[string]any) bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	if !inspect(root) {
		t.Fatalf("hook is not installed: %s", data)
	}
}

func TestParseHookTargets(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []hookTarget
		wantErr bool
	}{
		{name: "default", want: allHookTargets},
		{name: "all", raw: "all", want: allHookTargets},
		{name: "none", raw: "none", want: []hookTarget{}},
		{name: "subset", raw: "codex,claude", want: []hookTarget{hookClaude, hookCodex}},
		{name: "Cline subset", raw: "cursor,cline", want: []hookTarget{hookCline, hookCursor}},
		{name: "deduplicate", raw: "cursor,cursor", want: []hookTarget{hookCursor}},
		{name: "whitespace", raw: " cursor, claude ", want: []hookTarget{hookClaude, hookCursor}},
		{name: "unknown", raw: "windsurf", wantErr: true},
		{name: "empty member", raw: "claude,,codex", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHookTargets(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("targets = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseHookTargetsNamesInvalidValue(t *testing.T) {
	_, err := parseHookTargets("claude, windsurf ")
	if err == nil || !strings.Contains(err.Error(), " windsurf ") {
		t.Fatalf("error = %v, want invalid value", err)
	}
}

func TestParseHooksCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []hookTarget
		wantErr bool
	}{
		{name: "install all", args: []string{"install"}, want: allHookTargets},
		{name: "install subset", args: []string{"install", "--target=cursor,claude"}, want: []hookTarget{hookClaude, hookCursor}},
		{name: "missing action", wantErr: true},
		{name: "unknown action", args: []string{"remove"}, wantErr: true},
		{name: "unknown flag", args: []string{"install", "--bogus"}, wantErr: true},
		{name: "unknown target", args: []string{"install", "--target=windsurf"}, wantErr: true},
		{name: "explicit all target", args: []string{"install", "--target=all"}, wantErr: true},
		{name: "explicit no targets", args: []string{"install", "--target=none"}, wantErr: true},
		{name: "empty explicit target", args: []string{"install", "--target="}, wantErr: true},
		{name: "empty target before valid target", args: []string{"install", "--target=", "--target=codex"}, wantErr: true},
		{name: "duplicate target flag", args: []string{"install", "--target=codex", "--target=claude"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHooksCommand(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("targets = %v, want %v", got, tt.want)
			}
		})
	}
}
