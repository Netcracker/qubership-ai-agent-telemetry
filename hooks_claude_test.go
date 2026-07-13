package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMergeClaudeHookAddsCanonicalHandler(t *testing.T) {
	root := map[string]any{"theme": "dark"}
	changed, err := mergeClaudeHook(root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Skill",
					"hooks":   []any{canonicalClaudeHandler()},
				},
			},
		},
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("root = %#v, want %#v", root, want)
	}
	if !inspectClaudeHook(root) {
		t.Fatal("inspectClaudeHook = false, want true")
	}
}

func TestMergeClaudeHookPreservesUnrelatedMatcher(t *testing.T) {
	bashGroup := map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": "audit-shell", "custom": true},
		},
		"groupExtension": "keep",
	}
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{bashGroup},
			"Stop":       []any{map[string]any{"keep": true}},
		},
	}
	if _, err := mergeClaudeHook(root); err != nil {
		t.Fatal(err)
	}
	preToolUse := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if !reflect.DeepEqual(preToolUse[0], bashGroup) {
		t.Fatalf("unrelated matcher changed: %#v", preToolUse[0])
	}
	if len(preToolUse) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2", len(preToolUse))
	}
}

func TestMergeClaudeHookReusesSkillMatcherAndPreservesUnknownFields(t *testing.T) {
	unrelatedHandler := map[string]any{"type": "command", "command": "user-hook", "extension": 42}
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher":        "Skill",
					"hooks":          []any{unrelatedHandler},
					"groupExtension": "keep",
				},
			},
		},
	}
	if _, err := mergeClaudeHook(root); err != nil {
		t.Fatal(err)
	}
	group := root["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	if group["groupExtension"] != "keep" {
		t.Fatalf("group extension = %v", group["groupExtension"])
	}
	handlers := group["hooks"].([]any)
	if len(handlers) != 2 || !reflect.DeepEqual(handlers[0], unrelatedHandler) {
		t.Fatalf("handlers = %#v", handlers)
	}
}

func TestMergeClaudeHookCanonicalizesOwnedHandlers(t *testing.T) {
	tests := []struct {
		name     string
		handlers []any
	}{
		{
			name: "duplicate canonical",
			handlers: []any{
				canonicalClaudeHandler(),
				canonicalClaudeHandler(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{"matcher": "Skill", "hooks": tt.handlers},
					},
				},
			}
			changed, err := mergeClaudeHook(root)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			handlers := root["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)
			want := []any{canonicalClaudeHandler()}
			if !reflect.DeepEqual(handlers, want) {
				t.Fatalf("handlers = %#v, want %#v", handlers, want)
			}
		})
	}
}

func TestMergeClaudeHookPreservesUnrelatedHandlerInAPMGroup(t *testing.T) {
	userHandler := map[string]any{
		"type":      "command",
		"command":   "user-hook",
		"extension": "keep",
	}
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher":     "Skill",
					"_apm_source": hookAPMSource,
					"hooks":       []any{canonicalClaudeHandler(), userHandler},
				},
			},
		},
	}

	changed, err := mergeClaudeHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	group := root["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	if _, exists := group["_apm_source"]; exists {
		t.Fatalf("APM marker remains: %#v", group)
	}
	want := []any{userHandler, canonicalClaudeHandler()}
	if handlers := group["hooks"].([]any); !reflect.DeepEqual(handlers, want) {
		t.Fatalf("handlers = %#v, want %#v", handlers, want)
	}
}

func TestMergeClaudeHookIsIdempotent(t *testing.T) {
	root := map[string]any{}
	if changed, err := mergeClaudeHook(root); err != nil || !changed {
		t.Fatalf("first merge: changed = %v, error = %v", changed, err)
	}
	want := cloneHookMap(t, root)
	changed, err := mergeClaudeHook(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second merge changed canonical structure")
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("second merge root = %#v, want %#v", root, want)
	}
}

func TestMergeClaudeHookRejectsIncompatibleStructure(t *testing.T) {
	tests := []struct {
		name string
		root map[string]any
		want string
	}{
		{name: "hooks", root: map[string]any{"hooks": []any{}}, want: "hooks must be an object"},
		{name: "PreToolUse", root: map[string]any{"hooks": map[string]any{"PreToolUse": map[string]any{}}}, want: "hooks.PreToolUse must be an array"},
		{name: "group", root: map[string]any{"hooks": map[string]any{"PreToolUse": []any{"bad"}}}, want: "hooks.PreToolUse[0] must be an object"},
		{name: "matcher", root: map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": true}}}}, want: "hooks.PreToolUse[0].matcher must be a string"},
		{name: "handlers", root: map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Skill", "hooks": map[string]any{}}}}}, want: "hooks.PreToolUse[0].hooks must be an array"},
		{name: "handler", root: map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Skill", "hooks": []any{"bad"}}}}}, want: "hooks.PreToolUse[0].hooks[0] must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneHookMap(t, tt.root)
			changed, err := mergeClaudeHook(tt.root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("changed = %v, error = %v, want %q", changed, err, tt.want)
			}
			if !reflect.DeepEqual(tt.root, before) {
				t.Fatalf("root changed on error: %#v", tt.root)
			}
		})
	}
}

func canonicalClaudeHandler() map[string]any {
	return map[string]any{
		"type":          "command",
		"command":       "ai-agent-telemetry ingest --agent=claude",
		"timeout":       json.Number("30"),
		"statusMessage": "Recording skill telemetry",
	}
}

func cloneHookMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	return cloneHookValue(value).(map[string]any)
}

func cloneHookValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneHookValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneHookValue(item)
		}
		return cloned
	default:
		return value
	}
}
