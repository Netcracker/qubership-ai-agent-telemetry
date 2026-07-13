package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyHookPackageParity(t *testing.T) {
	tests := []struct {
		target hookTarget
		file   string
		path   []any
	}{
		{
			target: hookClaude,
			file:   "skill-call-claude-hooks.json",
			path:   []any{"hooks", "PreToolUse", 0, "hooks", 0, "command"},
		},
		{
			target: hookCodex,
			file:   "skill-call-codex-hooks.json",
			path:   []any{"hooks", "Stop", 0, "hooks", 0, "command"},
		},
		{
			target: hookCursor,
			file:   "skill-call-cursor-hooks.json",
			path:   []any{"hooks", "afterAgentResponse", 0, "command"},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.target), func(t *testing.T) {
			path := filepath.Join("agent-packages", "ai-agent-telemetry", ".apm", "hooks", tt.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s hook package %s: %v", tt.target, path, err)
			}
			var root any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("%s hook package %s: decode JSON: %v", tt.target, path, err)
			}
			command, ok := jsonPathString(root, tt.path...)
			if !ok {
				t.Fatalf("%s hook package %s: command is missing or not a string", tt.target, path)
			}
			if want := canonicalHookCommand(tt.target); command != want {
				t.Fatalf("%s hook package %s: command = %q, want %q", tt.target, path, command, want)
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

func jsonPathString(root any, path ...any) (string, bool) {
	value := root
	for _, part := range path {
		switch part := part.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				return "", false
			}
			value, ok = object[part]
			if !ok {
				return "", false
			}
		case int:
			array, ok := value.([]any)
			if !ok || part < 0 || part >= len(array) {
				return "", false
			}
			value = array[part]
		default:
			return "", false
		}
	}
	result, ok := value.(string)
	return result, ok
}
