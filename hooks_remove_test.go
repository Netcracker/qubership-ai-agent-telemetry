package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemoveHooksKeepsOnlyUnownedHandlers(t *testing.T) {
	tests := []struct {
		name   string
		root   map[string]any
		remove hookMergeFunc
	}{
		{
			name: "Claude",
			root: map[string]any{
				"theme": "dark",
				"hooks": map[string]any{
					"PreToolUse": []any{map[string]any{
						"matcher": "Skill",
						"hooks": []any{
							newCanonicalClaudeHandler(),
							map[string]any{"type": "command", "command": "user-hook"},
							map[string]any{"type": "command", "command": "legacy-hook", "_apm_source": hookAPMSource},
						},
					}},
				},
			},
			remove: removeClaudeHook,
		},
		{
			name: "Codex",
			root: map[string]any{
				"theme": "dark",
				"hooks": map[string]any{
					"Stop": []any{map[string]any{
						"hooks": []any{
							newCanonicalCodexHandler(),
							map[string]any{"type": "command", "command": "user-hook"},
							map[string]any{"type": "command", "command": "sh ./scripts/bootstrap.sh ingest --agent=codex"},
							map[string]any{"type": "command", "command": "tagged-hook", "_apm_source": hookAPMSource},
						},
					}},
				},
			},
			remove: removeCodexHook,
		},
		{
			name: "Cursor",
			root: map[string]any{
				"theme":   "dark",
				"version": 1,
				"hooks": map[string]any{
					"afterAgentResponse": []any{
						newCanonicalCursorHook(),
						map[string]any{"command": "user-hook"},
						map[string]any{"command": "sh ./scripts/bootstrap.sh ingest --agent=cursor"},
						map[string]any{"command": "tagged-hook", "_apm_source": hookAPMSource},
					},
				},
			},
			remove: removeCursorHook,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if changed, err := tt.remove(tt.root); err != nil || !changed {
				t.Fatalf("changed = %v, error = %v", changed, err)
			}
			if got := findRemovalCommands(tt.root); !reflect.DeepEqual(got, []string{"user-hook"}) {
				t.Fatalf("commands = %v", got)
			}
			if tt.root["theme"] != "dark" {
				t.Fatal("unrelated root property was removed")
			}
		})
	}
}

func TestRemoveGroupedHooksRemovesEmptyOwnedGroupAndManagedEvent(t *testing.T) {
	unrelated := []any{map[string]any{"hooks": []any{map[string]any{"command": "user-hook"}}}}
	root := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"matcher":     "",
				"hooks":       []any{newCanonicalCodexHandler()},
				"_apm_source": hookAPMSource,
			}},
			"UnrelatedEvent": unrelated,
		},
	}

	changed, err := removeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	hooks := root["hooks"].(map[string]any)
	if _, exists := hooks["Stop"]; exists {
		t.Fatal("empty managed event remains")
	}
	if !reflect.DeepEqual(hooks["UnrelatedEvent"], unrelated) {
		t.Fatal("unrelated event changed")
	}
}

func TestRemoveGroupedHooksKeepsExtendedGroupWithEmptyHooks(t *testing.T) {
	root := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"hooks":          []any{newCanonicalCodexHandler()},
				"groupExtension": "keep",
				"_apm_source":    hookAPMSource,
			}},
		},
	}

	changed, err := removeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	groups := root["hooks"].(map[string]any)["Stop"].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %#v", groups)
	}
	group := groups[0].(map[string]any)
	if group["groupExtension"] != "keep" || len(group["hooks"].([]any)) != 0 {
		t.Fatalf("group = %#v", group)
	}
	if _, exists := group["_apm_source"]; exists {
		t.Fatal("ownership marker remains")
	}
}

func TestRemoveCursorHookKeepsVersionAndUnrelatedEvents(t *testing.T) {
	unrelated := []any{map[string]any{"command": "user-hook"}}
	root := map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"afterAgentResponse":   []any{newCanonicalCursorHook()},
			"beforeShellExecution": unrelated,
		},
	}

	changed, err := removeCursorHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	if root["version"] != 1 {
		t.Fatalf("version = %v", root["version"])
	}
	hooks := root["hooks"].(map[string]any)
	if _, exists := hooks["afterAgentResponse"]; exists {
		t.Fatal("empty managed event remains")
	}
	if !reflect.DeepEqual(hooks["beforeShellExecution"], unrelated) {
		t.Fatal("unrelated Cursor event changed")
	}
}

func TestRemoveHooksRejectsInvalidStructureWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		root   map[string]any
		remove hookMergeFunc
	}{
		{name: "Claude", root: map[string]any{"hooks": map[string]any{"PreToolUse": "invalid"}}, remove: removeClaudeHook},
		{name: "Codex", root: map[string]any{"hooks": map[string]any{"Stop": []any{"invalid"}}}, remove: removeCodexHook},
		{name: "Cursor", root: map[string]any{"hooks": map[string]any{"afterAgentResponse": []any{"invalid"}}}, remove: removeCursorHook},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneJSONObject(tt.root)
			changed, err := tt.remove(tt.root)
			if err == nil || changed {
				t.Fatalf("changed = %v, error = %v", changed, err)
			}
			if !reflect.DeepEqual(tt.root, before) {
				t.Fatalf("root changed: %#v", tt.root)
			}
		})
	}
}

func TestRemoveHooksRejectsInvalidJSONWithoutChangingBytes(t *testing.T) {
	tests := []struct {
		name    string
		target  hookTarget
		remove  hookMergeFunc
		content []byte
	}{
		{name: "malformed Claude", target: hookClaude, remove: removeClaudeHook, content: []byte("{not json\n")},
		{name: "malformed Codex", target: hookCodex, remove: removeCodexHook, content: []byte("{not json\n")},
		{name: "malformed Cursor", target: hookCursor, remove: removeCursorHook, content: []byte("{not json\n")},
		{name: "invalid Claude structure", target: hookClaude, remove: removeClaudeHook, content: []byte(`{"hooks":{"PreToolUse":[42]}}`)},
		{name: "invalid Codex structure", target: hookCodex, remove: removeCodexHook, content: []byte(`{"hooks":{"Stop":[42]}}`)},
		{name: "invalid Cursor structure", target: hookCursor, remove: removeCursorHook, content: []byte(`{"hooks":{"afterAgentResponse":[42]}}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := hookPath(home, tt.target)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			before := tt.content
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}

			changed, err := updateHookFile(path, tt.remove)
			if err == nil || changed {
				t.Fatalf("changed = %v, error = %v", changed, err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("bytes changed from %q to %q", before, after)
			}
		})
	}
}

func TestUninstallHooksSecondRemovalReportsUnchanged(t *testing.T) {
	home := t.TempDir()
	if err := hookInstallError(installHooks(home, []hookTarget{hookClaude})); err != nil {
		t.Fatal(err)
	}
	if err := hookInstallError(uninstallHooks(home, []hookTarget{hookClaude}, io.Discard)); err != nil {
		t.Fatal(err)
	}
	results := uninstallHooks(home, []hookTarget{hookClaude}, io.Discard)
	if len(results) != 1 || results[0].Changed || results[0].Err != nil {
		t.Fatalf("second removal results = %#v", results)
	}
}

func TestUninstallHooksContinuesAfterMalformedTarget(t *testing.T) {
	home := t.TempDir()
	if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
		t.Fatal(err)
	}
	claudePath := hookPath(home, hookClaude)
	malformed := []byte("{not json\n")
	if err := os.WriteFile(claudePath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	results := uninstallHooks(home, []hookTarget{hookCursor, hookClaude, hookCodex}, io.Discard)
	if len(results) != 3 || results[0].Target != hookClaude || results[1].Target != hookCodex || results[2].Target != hookCursor {
		t.Fatalf("results = %#v, want canonical order", results)
	}
	if results[0].Err == nil || results[1].Err != nil || results[2].Err != nil {
		t.Fatalf("results = %#v, want only Claude error", results)
	}
	if !results[1].Changed || !results[2].Changed {
		t.Fatalf("successful results = %#v, want changed", results)
	}
	after, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, malformed) {
		t.Fatalf("malformed target changed from %q to %q", malformed, after)
	}
	assertNoRemovalCommands(t, hookPath(home, hookCodex))
	assertNoRemovalCommands(t, hookPath(home, hookCursor))
}

func TestRemoveCodexRuleMissingReportsUnchanged(t *testing.T) {
	var warnings strings.Builder
	changed, err := removeCodexRule(filepath.Join(t.TempDir(), "missing.rules"), &warnings)
	if err != nil || changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	if warnings.Len() != 0 {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestRemoveCodexRuleRemovesCanonicalPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai-agent-telemetry.rules")
	if err := os.WriteFile(path, []byte(codexExecutionPolicy), 0o600); err != nil {
		t.Fatal(err)
	}

	var warnings strings.Builder
	changed, err := removeCodexRule(path, &warnings)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rule still exists: %v", err)
	}
	if warnings.Len() != 0 {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestRemoveCodexRulePreservesModifiedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai-agent-telemetry.rules")
	before := []byte(codexExecutionPolicy + "# user modification\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	var warnings strings.Builder
	changed, err := removeCodexRule(path, &warnings)
	if err != nil || changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("modified rule changed from %q to %q", before, after)
	}
	if !strings.Contains(warnings.String(), "preserved modified Codex execution policy") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func findRemovalCommands(value any) []string {
	var commands []string
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if command, ok := typed["command"].(string); ok {
				commands = append(commands, command)
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return commands
}

func assertNoRemovalCommands(t *testing.T, path string) {
	t.Helper()
	root, err := readHookRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	if commands := findRemovalCommands(root); len(commands) != 0 {
		t.Fatalf("commands = %v, want none", commands)
	}
}
