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

func TestPathFiltersCoverWorkflowInputs(t *testing.T) {
	contracts := []struct {
		workflow string
		filter   string
		paths    []string
	}{
		{
			workflow: ".github/workflows/go-build.yaml",
			filter:   "go",
			paths: []string{
				"*.go",
				"go.mod",
				"go.sum",
				"testdata/**",
				".github/workflows/installer-tests.yaml",
				".github/workflows/super-linter.yaml",
			},
		},
		{
			workflow: ".github/workflows/bootstrap-tests.yaml",
			filter:   "bootstrap",
			paths: []string{
				"scripts/install.sh",
				"scripts/install.ps1",
				"scripts/install_test.sh",
				"scripts/install.Tests.ps1",
				"go.mod",
			},
		},
		{
			workflow: ".github/workflows/installer-tests.yaml",
			filter:   "lifecycle",
			paths: []string{
				"*.go",
				"go.mod",
				"go.sum",
				"scripts/install.sh",
				"scripts/install.ps1",
			},
		},
		{
			workflow: ".github/workflows/telemetry-backend-tests.yaml",
			filter:   "backend",
			paths: []string{
				"telemetry-backend/**",
				"scripts/package-backend-release.sh",
				"scripts/package_backend_release_test.sh",
			},
		},
	}

	for _, contract := range contracts {
		t.Run(contract.workflow, func(t *testing.T) {
			data, err := os.ReadFile(contract.workflow)
			if err != nil {
				t.Fatal(err)
			}
			filterPaths := workflowFilterPaths(string(data), contract.filter)
			for _, path := range contract.paths {
				if !filterPaths[path] {
					t.Errorf("%s filter does not cover workflow input %q", contract.filter, path)
				}
			}
		})
	}
}

func workflowFilterPaths(data, filterName string) map[string]bool {
	marker := "            " + filterName + ":\n"
	start := strings.Index(data, marker)
	if start == -1 {
		return nil
	}
	data = data[start+len(marker):]
	paths := make(map[string]bool)
	for _, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(line, "              - ") {
			break
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "              - "))
		paths[strings.Trim(path, "'\"")] = true
	}
	return paths
}
