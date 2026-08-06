package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRemoveGroupedHooksPreservesUnrelatedHandlersAndExtensions(t *testing.T) {
	root := map[string]any{"theme": "dark", "hooks": map[string]any{
		"PreToolUse": []any{
			map[string]any{"matcher": "Skill", "hooks": []any{
				map[string]any{"type": "command", "command": claudeHookCommand},
				map[string]any{"type": "command", "command": "keep"},
			}},
		},
		"UserPromptExpansion": []any{map[string]any{
			"hooks": []any{map[string]any{"command": claudeHookCommand}}, "extension": true,
		}},
		"Unrelated": []any{map[string]any{"hooks": []any{map[string]any{"command": "keep"}}}},
	}}

	changed, err := removeClaudeHook(root)
	if err != nil || !changed {
		t.Fatalf("removeClaudeHook() = %t, %v", changed, err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks removed with preexisting empty event: %#v", root)
	}
	pre := hooks["PreToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)
	if len(pre) != 1 || pre[0].(map[string]any)["command"] != "keep" {
		t.Fatalf("unrelated handler changed: %#v", pre)
	}
	group := hooks["UserPromptExpansion"].([]any)[0].(map[string]any)
	if group["extension"] != true || len(group["hooks"].([]any)) != 0 {
		t.Fatalf("extension group = %#v, want preserved empty group", group)
	}
	if _, ok := hooks["Unrelated"]; !ok || root["theme"] != "dark" {
		t.Fatalf("unrelated content changed: %#v", root)
	}
}

func TestRemoveGroupedHooksPreservesUnrelatedSourceMarker(t *testing.T) {
	root := map[string]any{"hooks": map[string]any{
		"Stop": []any{map[string]any{
			"_apm_source": "another-package",
			"hooks":       []any{map[string]any{"command": codexHookCommand}},
		}},
	}}

	changed, err := removeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("removeCodexHook() = %t, %v", changed, err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks removed with unrelated source marker: %#v", root)
	}
	groups := hooks["Stop"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["_apm_source"] != "another-package" {
		t.Fatalf("groups = %#v, want unrelated source marker preserved", groups)
	}
}

func TestRemoveGroupedHooksRemovesOwnedEmptyGroupsAndEvents(t *testing.T) {
	root := map[string]any{"hooks": map[string]any{
		"Stop": []any{map[string]any{
			"_apm_source": hookAPMSource,
			"hooks":       []any{map[string]any{"command": codexHookCommand}},
		}},
	}}
	changed, err := removeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("removeCodexHook() = %t, %v", changed, err)
	}
	if len(root) != 0 {
		t.Fatalf("root = %#v, want empty object", root)
	}
	changed, err = removeCodexHook(root)
	if err != nil || changed {
		t.Fatalf("repeated removeCodexHook() = %t, %v, want unchanged", changed, err)
	}
}

func TestRemoveHooksPreservesPreexistingEmptyStructures(t *testing.T) {
	emptyGroup := map[string]any{"matcher": "Skill", "hooks": []any{}}
	grouped := map[string]any{"hooks": map[string]any{
		"PreToolUse": []any{
			emptyGroup,
			map[string]any{"matcher": "Skill", "hooks": []any{map[string]any{"command": claudeHookCommand}}},
		},
	}}
	if changed, err := removeClaudeHook(grouped); err != nil || !changed {
		t.Fatalf("removeClaudeHook() = %t, %v", changed, err)
	}
	hooksValue, ok := grouped["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks removed with preexisting empty group: %#v", grouped)
	}
	groups := hooksValue["PreToolUse"].([]any)
	if len(groups) != 1 || !reflect.DeepEqual(groups[0], emptyGroup) {
		t.Fatalf("groups = %#v, want preexisting empty group preserved", groups)
	}

	cursor := map[string]any{"hooks": map[string]any{
		"afterAgentResponse": []any{map[string]any{"command": cursorHookCommand}},
		"afterMCPExecution":  []any{},
	}}
	if changed, err := removeCursorHook(cursor); err != nil || !changed {
		t.Fatalf("removeCursorHook() = %t, %v", changed, err)
	}
	hooks := cursor["hooks"].(map[string]any)
	if _, ok := hooks["afterMCPExecution"]; !ok {
		t.Fatalf("preexisting empty event removed: %#v", hooks)
	}
}

func TestRemoveGroupedHooksPreservesPreexistingEmptyEvent(t *testing.T) {
	root := map[string]any{"hooks": map[string]any{
		"Stop": []any{map[string]any{
			"hooks": []any{map[string]any{"command": codexHookCommand}},
		}},
		"PostToolUse": []any{},
	}}

	changed, err := removeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("removeCodexHook() = %t, %v", changed, err)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks removed with preexisting empty event: %#v", root)
	}
	if _, ok := hooks["Stop"]; ok {
		t.Fatalf("owned empty event remains: %#v", hooks)
	}
	if event, ok := hooks["PostToolUse"]; !ok || !reflect.DeepEqual(event, []any{}) {
		t.Fatalf("preexisting empty event changed: %#v", hooks)
	}
}

func TestRemoveCursorHookRequiresCanonicalCommandOrOwnershipMarker(t *testing.T) {
	modified := map[string]any{"command": cursorHookCommand + " --extra"}
	root := map[string]any{"version": json.Number("1"), "hooks": map[string]any{
		"afterAgentResponse": []any{
			map[string]any{"command": cursorHookCommand}, modified,
			map[string]any{"command": "custom", "_apm_source": hookAPMSource},
		},
	}}
	changed, err := removeCursorHook(root)
	if err != nil || !changed {
		t.Fatalf("removeCursorHook() = %t, %v", changed, err)
	}
	entries := root["hooks"].(map[string]any)["afterAgentResponse"].([]any)
	if !reflect.DeepEqual(entries, []any{modified}) {
		t.Fatalf("entries = %#v, want modified command only", entries)
	}
}

func TestUninstallHooksPreservesMalformedFileAndContinues(t *testing.T) {
	home := t.TempDir()
	malformed := []byte("{not json\n")
	claudePath := hookPath(home, hookClaude)
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedHookFile(hookPath(home, hookCursor), mergeCursorHook); err != nil {
		t.Fatal(err)
	}

	results := uninstallHooks(home, []hookTarget{hookCursor, hookClaude}, &bytes.Buffer{})
	if len(results) != 2 || results[0].Target != hookClaude || results[0].Err == nil || results[1].Target != hookCursor || results[1].Err != nil || !results[1].Changed {
		t.Fatalf("results = %#v", results)
	}
	got, err := os.ReadFile(claudePath)
	if err != nil || !bytes.Equal(got, malformed) {
		t.Fatalf("malformed file = %q, %v; want byte-for-byte preservation", got, err)
	}
}

func TestUninstallHooksRemovesCanonicalCodexRuleAndPreservesModifiedRule(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "modified"}[modified], func(t *testing.T) {
			home := t.TempDir()
			if err := seedHookFile(hookPath(home, hookCodex), mergeCodexHook); err != nil {
				t.Fatal(err)
			}
			rule := codexExecutionPolicy
			if modified {
				rule += "# local change\n"
			}
			if err := os.MkdirAll(filepath.Dir(codexRulePath(home)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(codexRulePath(home), []byte(rule), 0o600); err != nil {
				t.Fatal(err)
			}
			var warnings bytes.Buffer
			results := uninstallHooks(home, []hookTarget{hookCodex}, &warnings)
			if err := hookInstallError(results); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(codexRulePath(home))
			if modified {
				if err != nil || !strings.Contains(warnings.String(), "modified") {
					t.Fatalf("modified rule stat = %v, warnings = %q", err, warnings.String())
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("canonical rule still exists: %v", err)
			}
		})
	}
}

func TestUninstallHooksPreservesCodexRuleWhenHookCleanupFails(t *testing.T) {
	home := t.TempDir()
	hookFile := hookPath(home, hookCodex)
	if err := os.MkdirAll(filepath.Dir(hookFile), 0o700); err != nil {
		t.Fatal(err)
	}
	incompatible := []byte("{\"hooks\":{\"Stop\":\"invalid\"}}\n")
	if err := os.WriteFile(hookFile, incompatible, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexRulePath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexRulePath(home), []byte(codexExecutionPolicy), 0o600); err != nil {
		t.Fatal(err)
	}

	results := uninstallHooks(home, []hookTarget{hookCodex}, &bytes.Buffer{})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %#v, want one failed Codex cleanup", results)
	}
	gotHook, err := os.ReadFile(hookFile)
	if err != nil || !bytes.Equal(gotHook, incompatible) {
		t.Fatalf("hook file = %q, %v; want byte-for-byte preservation", gotHook, err)
	}
	gotRule, err := os.ReadFile(codexRulePath(home))
	if err != nil || string(gotRule) != codexExecutionPolicy {
		t.Fatalf("Codex rule = %q, %v; want canonical rule preserved", gotRule, err)
	}
}

func TestUninstallHooksContinuesAfterModifiedClineHook(t *testing.T) {
	home := t.TempDir()
	if err := hookInstallError(installHooks(home, []hookTarget{hookClaude, hookCline, hookCursor})); err != nil {
		t.Fatal(err)
	}
	clinePath := hookPath(home, hookCline)
	modified := append(clineHookContent(runtime.GOOS), []byte("# local change\n")...)
	if err := os.WriteFile(clinePath, modified, clineHookMode(runtime.GOOS)); err != nil {
		t.Fatal(err)
	}

	var warnings bytes.Buffer
	results := uninstallHooks(home, []hookTarget{hookCursor, hookCline, hookClaude}, &warnings)
	if err := hookInstallError(results); err == nil || !strings.Contains(err.Error(), "cline") {
		t.Fatalf("hookInstallError() = %v, want incomplete Cline cleanup", err)
	}
	if len(results) != 3 || !results[0].Changed || results[1].Changed || !results[2].Changed {
		t.Fatalf("results = %#v", results)
	}
	if !strings.Contains(warnings.String(), "preserved modified Cline hook") {
		t.Fatalf("warnings = %q", warnings.String())
	}
	if got, err := os.ReadFile(clinePath); err != nil || !bytes.Equal(got, modified) {
		t.Fatalf("modified Cline hook = %q, %v", got, err)
	}
}

func TestUninstallHooksUpdatesSymlinkTargetWithoutReplacingLink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "shared", "claude.json")
	if err := seedHookFile(target, mergeClaudeHook); err != nil {
		t.Fatal(err)
	}
	path := hookPath(home, hookClaude)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	results := uninstallHooks(home, []hookTarget{hookClaude}, &bytes.Buffer{})
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("hook link replaced: %v, %v", info, err)
	}
	root, err := readHookRoot(target)
	if err != nil || inspectClaudeHook(root) {
		t.Fatalf("target still has managed hook: %#v, %v", root, err)
	}
}

func seedHookFile(path string, merge hookMergeFunc) error {
	_, err := updateHookFile(path, merge)
	return err
}
