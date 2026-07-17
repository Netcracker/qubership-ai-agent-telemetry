package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHookPackageParity(t *testing.T) {
	tests := []struct {
		target hookTarget
		file   string
		merge  func(map[string]any) (bool, error)
	}{
		{
			target: hookClaude,
			file:   "skill-call-claude-hooks.json",
			merge:  mergeClaudeHook,
		},
		{
			target: hookCodex,
			file:   "skill-call-codex-hooks.json",
			merge:  mergeCodexHook,
		},
		{
			target: hookCursor,
			file:   "skill-call-cursor-hooks.json",
			merge:  mergeCursorHook,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.target), func(t *testing.T) {
			path := filepath.Join("agent-packages", "ai-agent-telemetry", ".apm", "hooks", tt.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s hook package %s: %v", tt.target, path, err)
			}
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			decoder.UseNumber()
			var packaged map[string]any
			if err := decoder.Decode(&packaged); err != nil {
				t.Fatalf("%s hook package %s: decode JSON: %v", tt.target, path, err)
			}
			managed := map[string]any{}
			changed, err := tt.merge(managed)
			if err != nil || !changed {
				t.Fatalf("%s managed hooks: changed = %v, error = %v", tt.target, changed, err)
			}
			if !reflect.DeepEqual(packaged, managed) {
				t.Fatalf("%s hook package %s = %#v, want %#v", tt.target, path, packaged, managed)
			}
		})
	}
}

func TestCodexPolicyReferenceParity(t *testing.T) {
	path := filepath.Join(
		"agent-packages", "ai-agent-telemetry-configure", ".apm", "skills",
		"ai-agent-telemetry-configure", "references", "codex-sandbox.md",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const start = "```python\n"
	_, remaining, found := strings.Cut(string(data), start)
	if !found {
		t.Fatalf("%s does not contain a Python rule block", path)
	}
	rule, _, found := strings.Cut(remaining, "```\n")
	if !found {
		t.Fatalf("%s has an unterminated Python rule block", path)
	}
	if rule != codexExecutionPolicy {
		t.Fatalf("%s rule differs from the CLI-managed policy", path)
	}
}
