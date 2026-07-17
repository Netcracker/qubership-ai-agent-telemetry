package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMergeCodexHookAddsCanonicalGroupAndPreservesStopHandlers(t *testing.T) {
	unrelated := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "user-hook", "extension": true}}, "groupExtension": "keep"}
	root := map[string]any{"theme": "dark", "hooks": map[string]any{"Stop": []any{unrelated}, "SessionStart": []any{map[string]any{"keep": true}}}}
	changed, err := mergeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	stop := root["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || !reflect.DeepEqual(stop[0], unrelated) {
		t.Fatalf("Stop = %#v", stop)
	}
	if !reflect.DeepEqual(stop[1], canonicalCodexGroup()) {
		t.Fatalf("canonical group = %#v, want %#v", stop[1], canonicalCodexGroup())
	}
	if !inspectCodexHook(root) {
		t.Fatal("inspectCodexHook = false, want true")
	}
}

func TestMergeCodexHookAddsCompleteManagedEventSet(t *testing.T) {
	root := map[string]any{}
	changed, err := mergeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	hooks := root["hooks"].(map[string]any)
	for _, spec := range codexHookSpecs {
		groups, ok := hooks[spec.event].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("hooks[%q] = %#v, want one group", spec.event, hooks[spec.event])
		}
		group := groups[0].(map[string]any)
		if spec.matcher == "" {
			if _, exists := group["matcher"]; exists {
				t.Fatalf("hooks[%q] matcher = %#v, want absent", spec.event, group["matcher"])
			}
		} else if group["matcher"] != spec.matcher {
			t.Fatalf("hooks[%q] matcher = %#v, want %q", spec.event, group["matcher"], spec.matcher)
		}
		if got := group["hooks"].([]any); !reflect.DeepEqual(got, []any{canonicalCodexHandler()}) {
			t.Fatalf("hooks[%q] handlers = %#v", spec.event, got)
		}
	}
}

func TestInspectCodexHookRequiresCompleteManagedEventSet(t *testing.T) {
	root := map[string]any{"hooks": map[string]any{"Stop": []any{canonicalCodexGroup()}}}
	if inspectCodexHook(root) {
		t.Fatal("inspectCodexHook = true for partial managed event set")
	}
	if _, err := mergeCodexHook(root); err != nil {
		t.Fatal(err)
	}
	if !inspectCodexHook(root) {
		t.Fatal("inspectCodexHook = false for complete managed event set")
	}
}

func TestMergeCodexHookCanonicalizesEveryManagedEvent(t *testing.T) {
	userHandler := map[string]any{"type": "command", "command": "user-hook", "extension": true}
	hooks := map[string]any{"SessionStart": []any{map[string]any{"keep": true}}}
	for _, spec := range codexHookSpecs {
		group := map[string]any{
			"hooks":          []any{canonicalCodexHandler(), userHandler, canonicalCodexHandler()},
			"groupExtension": "keep",
			"_apm_source":    hookAPMSource,
		}
		if spec.matcher != "" {
			group["matcher"] = spec.matcher
		}
		hooks[spec.event] = []any{group}
	}
	root := map[string]any{"hooks": hooks}
	if changed, err := mergeCodexHook(root); err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	for _, spec := range codexHookSpecs {
		group := hooks[spec.event].([]any)[0].(map[string]any)
		if _, exists := group["_apm_source"]; exists {
			t.Fatalf("hooks[%q] retains APM marker", spec.event)
		}
		if group["groupExtension"] != "keep" {
			t.Fatalf("hooks[%q] lost group extension", spec.event)
		}
		want := []any{userHandler, canonicalCodexHandler()}
		if got := group["hooks"].([]any); !reflect.DeepEqual(got, want) {
			t.Fatalf("hooks[%q] handlers = %#v, want %#v", spec.event, got, want)
		}
	}
	if !reflect.DeepEqual(hooks["SessionStart"], []any{map[string]any{"keep": true}}) {
		t.Fatalf("unrelated event changed: %#v", hooks["SessionStart"])
	}
}

func TestMergeCodexHookCanonicalizesOwnedEntries(t *testing.T) {
	legacyCommand := `sh "$(git rev-parse --show-toplevel)/apm_modules/_local/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh" ingest --agent=codex --endpoint=https://REPLACE_ME/v1/logs`
	tests := []struct {
		name string
		stop []any
	}{
		{name: "APM source", stop: []any{map[string]any{"_apm_source": hookAPMSource, "hooks": []any{canonicalCodexHandler()}}}},
		{name: "duplicates", stop: []any{canonicalCodexGroup(), canonicalCodexGroup()}},
		{name: "legacy", stop: []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": legacyCommand, "timeout": json.Number("30")}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := map[string]any{"hooks": map[string]any{"Stop": tt.stop}}
			changed, err := mergeCodexHook(root)
			if err != nil || !changed {
				t.Fatalf("changed = %v, error = %v", changed, err)
			}
			stop := root["hooks"].(map[string]any)["Stop"].([]any)
			var canonical int
			for _, value := range stop {
				group := value.(map[string]any)
				if _, exists := group["_apm_source"]; exists {
					t.Fatalf("owned APM marker remains: %#v", group)
				}
				for _, handler := range group["hooks"].([]any) {
					if reflect.DeepEqual(handler, canonicalCodexHandler()) {
						canonical++
					}
				}
			}
			if canonical != 1 {
				t.Fatalf("canonical handlers = %d, want 1; Stop = %#v", canonical, stop)
			}
		})
	}
}

func TestMergeCodexHookPreservesUnrelatedHandlerInAPMGroup(t *testing.T) {
	userHandler := map[string]any{
		"type":      "command",
		"command":   "user-hook",
		"extension": "keep",
	}
	root := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"_apm_source": hookAPMSource,
					"hooks":       []any{canonicalCodexHandler(), userHandler},
				},
			},
		},
	}

	changed, err := mergeCodexHook(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, error = %v", changed, err)
	}
	group := root["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)
	if _, exists := group["_apm_source"]; exists {
		t.Fatalf("APM marker remains: %#v", group)
	}
	want := []any{userHandler, canonicalCodexHandler()}
	if handlers := group["hooks"].([]any); !reflect.DeepEqual(handlers, want) {
		t.Fatalf("handlers = %#v, want %#v", handlers, want)
	}
}

func TestMergeCodexHookRejectsIncompatibleStructure(t *testing.T) {
	tests := []struct {
		name string
		root map[string]any
		want string
	}{
		{name: "hooks", root: map[string]any{"hooks": []any{}}, want: "hooks must be an object"},
		{name: "Stop", root: map[string]any{"hooks": map[string]any{"Stop": map[string]any{}}}, want: "hooks.Stop must be an array"},
		{name: "group", root: map[string]any{"hooks": map[string]any{"Stop": []any{"bad"}}}, want: "hooks.Stop[0] must be an object"},
		{
			name: "handlers",
			root: map[string]any{
				"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": true}}},
			},
			want: "hooks.Stop[0].hooks must be an array",
		},
		{
			name: "handler",
			root: map[string]any{
				"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{"bad"}}}},
			},
			want: "hooks.Stop[0].hooks[0] must be an object",
		},
		{
			name: "managed MCP event",
			root: map[string]any{"hooks": map[string]any{"PostToolUse": map[string]any{}}},
			want: "hooks.PostToolUse must be an array",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneHookMap(t, tt.root)
			changed, err := mergeCodexHook(tt.root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("changed = %v, error = %v, want %q", changed, err, tt.want)
			}
			if !reflect.DeepEqual(tt.root, before) {
				t.Fatalf("root changed on error: %#v", tt.root)
			}
		})
	}
}

func TestMergeCodexHookIsIdempotent(t *testing.T) {
	root := map[string]any{}
	if changed, err := mergeCodexHook(root); err != nil || !changed {
		t.Fatalf("first merge: changed = %v, error = %v", changed, err)
	}
	before := cloneHookMap(t, root)
	if changed, err := mergeCodexHook(root); err != nil || changed {
		t.Fatalf("second merge: changed = %v, error = %v", changed, err)
	}
	if !reflect.DeepEqual(root, before) {
		t.Fatalf("root changed: %#v", root)
	}
}

func canonicalCodexGroup() map[string]any {
	return map[string]any{"hooks": []any{canonicalCodexHandler()}}
}

func canonicalCodexHandler() map[string]any {
	return map[string]any{"type": "command", "command": "ai-agent-telemetry ingest --agent=codex", "timeout": json.Number("30"), "statusMessage": "Recording agent telemetry"}
}
