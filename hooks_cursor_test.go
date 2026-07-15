package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMergeCursorHookAddsVersionAndPreservesUnrelatedEvents(t *testing.T) {
	unrelated := map[string]any{"command": "user-hook", "extension": true}
	root := map[string]any{"theme": "dark", "hooks": map[string]any{"afterAgentResponse": []any{unrelated}, "beforeSubmitPrompt": []any{map[string]any{"command": "keep"}}}}
	changed, err := mergeCursorHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	if root["version"] != json.Number("1") {
		t.Fatalf("version = %#v, want 1", root["version"])
	}
	event := root["hooks"].(map[string]any)["afterAgentResponse"].([]any)
	want := []any{unrelated, canonicalCursorHook()}
	if !reflect.DeepEqual(event, want) {
		t.Fatalf("afterAgentResponse = %#v, want %#v", event, want)
	}
	if !inspectCursorHook(root) {
		t.Fatal("inspectCursorHook = false, want true")
	}
}

func TestMergeCursorHookPreservesNumericVersion(t *testing.T) {
	root := map[string]any{"version": json.Number("7")}
	if _, err := mergeCursorHook(root); err != nil {
		t.Fatal(err)
	}
	if root["version"] != json.Number("7") {
		t.Fatalf("version = %#v, want 7", root["version"])
	}
}

func TestMergeCursorHookRemovesDuplicates(t *testing.T) {
	root := map[string]any{"version": json.Number("1"), "hooks": map[string]any{"afterAgentResponse": []any{
		canonicalCursorHook(),
		map[string]any{"command": "user-hook"},
		map[string]any{"command": cursorHookCommand, "_apm_source": hookAPMSource},
	}}}
	if changed, err := mergeCursorHook(root); err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	event := root["hooks"].(map[string]any)["afterAgentResponse"].([]any)
	want := []any{canonicalCursorHook(), map[string]any{"command": "user-hook"}}
	if !reflect.DeepEqual(event, want) {
		t.Fatalf("afterAgentResponse = %#v, want %#v", event, want)
	}
}

func TestMergeCursorHookRejectsIncompatibleStructure(t *testing.T) {
	tests := []struct {
		name string
		root map[string]any
		want string
	}{
		{name: "version", root: map[string]any{"version": "1"}, want: "version must be a number"},
		{name: "invalid number", root: map[string]any{"version": json.Number("not-a-number")}, want: "version must be a number"},
		{name: "hooks", root: map[string]any{"hooks": []any{}}, want: "hooks must be an object"},
		{name: "event", root: map[string]any{"hooks": map[string]any{"afterAgentResponse": map[string]any{}}}, want: "hooks.afterAgentResponse must be an array"},
		{name: "entry", root: map[string]any{"hooks": map[string]any{"afterAgentResponse": []any{"bad"}}}, want: "hooks.afterAgentResponse[0] must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneHookMap(t, tt.root)
			changed, err := mergeCursorHook(tt.root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("changed = %v, error = %v, want %q", changed, err, tt.want)
			}
			if !reflect.DeepEqual(tt.root, before) {
				t.Fatalf("root changed on error: %#v", tt.root)
			}
		})
	}
}

func TestMergeCursorHookIsIdempotent(t *testing.T) {
	root := map[string]any{}
	if changed, err := mergeCursorHook(root); err != nil || !changed {
		t.Fatalf("first merge: changed = %v, error = %v", changed, err)
	}
	before := cloneHookMap(t, root)
	if changed, err := mergeCursorHook(root); err != nil || changed {
		t.Fatalf("second merge: changed = %v, error = %v", changed, err)
	}
	if !reflect.DeepEqual(root, before) {
		t.Fatalf("root changed: %#v", root)
	}
}

func canonicalCursorHook() map[string]any {
	return map[string]any{"command": "ai-agent-telemetry ingest --agent=cursor"}
}
