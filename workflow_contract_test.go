package main

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsLifecycleBootstrapCallsUseCheckedChildProcesses(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/installer-tests.yaml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	invocations := 0
	inRunBlock := false
	runIndent := 0
	for index, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.TrimSpace(line) == "run: |" {
			inRunBlock = true
			runIndent = indent
			continue
		}
		if inRunBlock && strings.TrimSpace(line) != "" && indent <= runIndent {
			inRunBlock = false
		}
		if !inRunBlock {
			continue
		}
		if !strings.Contains(line, "scripts/install.ps1") {
			continue
		}
		invocations++
		if !strings.Contains(line, "powershell.exe -NoProfile -File ./scripts/install.ps1") {
			t.Errorf("line %d invokes install.ps1 without a child PowerShell process: %s", index+1, strings.TrimSpace(line))
			continue
		}
		windowEnd := min(index+4, len(lines))
		window := strings.Join(lines[index+1:windowEnd], "\n")
		if !strings.Contains(window, "$LASTEXITCODE") || !strings.Contains(window, "-ne 0") {
			t.Errorf("line %d does not capture and check the child exit code", index+1)
		}
	}
	if invocations != 4 {
		t.Fatalf("install.ps1 invocation count = %d, want 4", invocations)
	}
}

func TestSuperLinterExcludesSuperpowersArtifacts(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/super-linter.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "FILTER_REGEX_EXCLUDE: '(^|/)docs/superpowers/'") {
		t.Fatal("super-linter must exclude generated Superpowers artifacts")
	}
}
