package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
