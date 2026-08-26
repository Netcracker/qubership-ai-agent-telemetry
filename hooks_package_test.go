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

func TestCodexPolicyUsesLifecycleUpdateCommand(t *testing.T) {
	if count := strings.Count(codexExecutionPolicy, `"ai-agent-telemetry update"`); count != 3 {
		t.Fatalf("Codex execution policy update negative matches = %d, want 3", count)
	}
	if strings.Contains(codexExecutionPolicy, "update-check") {
		t.Fatal("Codex execution policy still references removed update-check command")
	}
}

func TestHooksPackageConfigureSkillUpdateMigrationContract(t *testing.T) {
	packageDir := filepath.Join("agent-packages", "ai-agent-telemetry-configure")
	skillDir := filepath.Join(packageDir, ".apm", "skills", "ai-agent-telemetry-configure")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	skill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"legacy telemetry APM migration failed",
		"apm was not found on PATH",
		"Do not run `ai-agent-telemetry hooks install` manually.",
		"Do not remove unrelated global packages",
		"edit a project-local manifest.",
	} {
		if !strings.Contains(string(skill), want) {
			t.Fatalf("%s does not document %q", skillPath, want)
		}
	}
	_, recovery, found := strings.Cut(string(skill), "### Legacy APM migration during an explicit update")
	if !found {
		t.Fatalf("%s does not contain the legacy migration recovery section", skillPath)
	}
	recovery, _, _ = strings.Cut(recovery, "\n## Failure")
	offset := 0
	for _, want := range []string{
		"command -v apm",
		"Get-Command apm",
		"apm uninstall -g " + legacyTelemetryAPMPackage,
		"\nai-agent-telemetry update\n",
		"ai-agent-telemetry status --verbose",
	} {
		index := strings.Index(recovery[offset:], want)
		if index < 0 {
			t.Fatalf("%s recovery section does not document %q in order", skillPath, want)
		}
		offset += index + len(want)
	}
	if strings.Contains(string(skill), "--cli-only") {
		t.Fatalf("%s still documents --cli-only", skillPath)
	}
	if strings.Contains(string(skill), "every selected component") {
		t.Fatalf("%s still describes the removed component lifecycle", skillPath)
	}

	readme, err := os.ReadFile(filepath.Join(packageDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "--cli-only") {
		t.Fatalf("%s still documents --cli-only", filepath.Join(packageDir, "README.md"))
	}

	deploymentPath := filepath.Join(skillDir, "references", "deployment.md")
	deployment, err := os.ReadFile(deploymentPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{
		"all components", "installs APM", "qubership-global-essentials", "Git hooks", "prerequisite fails",
	} {
		if strings.Contains(string(deployment), removed) {
			t.Errorf("%s still documents removed lifecycle behavior %q", deploymentPath, removed)
		}
	}

	manifest, err := os.ReadFile(filepath.Join(packageDir, "apm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "version: 3.4.0") {
		t.Fatalf("%s does not declare version 3.4.0", filepath.Join(packageDir, "apm.yml"))
	}
}
