package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstallerDocumentationWorkflowContract(t *testing.T) {
	documents := []string{
		"README.md",
		"docs/lifecycle-installer.md",
		"docs/cli.md",
		"docs/agent-integration.md",
		"docs/release.md",
		"docs/manual-uninstall.md",
		"agent-packages/ai-agent-telemetry/README.md",
		"agent-packages/ai-agent-telemetry-configure/README.md",
		"telemetry-backend/native-otlp-onboarding.md",
	}
	obsoleteFlag := regexp.MustCompile(`--skip(?:[ =,\x60]|$)`)
	const allowedSkipConfigRejection = "`--skip-config`, `bootstrap.sh`, `bootstrap.ps1`, and PowerShell named parameters are not supported aliases."
	forbidden := []string{
		"--force-git-hooks",
		"--cli-only",
		"--remove-cli",
		"qubership-global-essentials",
		"CYBER_FERRET_PASSWORD",
		"CyberFerret",
		"developer baseline",
		"Git and Java prerequisites",
		"partial uninstall",
	}

	for _, path := range documents {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if obsoleteFlag.MatchString(text) {
			t.Errorf("%s documents the removed --skip flag", path)
		}
		for _, value := range forbidden {
			if strings.Contains(text, value) {
				t.Errorf("%s documents removed installer behavior %q", path, value)
			}
		}
		withoutAllowedSkipConfig := text
		if path == "docs/cli.md" {
			if strings.Count(text, allowedSkipConfigRejection) != 1 {
				t.Errorf("%s must document the exact --skip-config rejection once", path)
			}
			withoutAllowedSkipConfig = strings.Replace(text, allowedSkipConfigRejection, "", 1)
		}
		if strings.Contains(withoutAllowedSkipConfig, "--skip-config") {
			t.Errorf("%s documents --skip-config outside the exact rejection context", path)
		}
		for lineNumber, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, "--components") {
				continue
			}
			if !strings.Contains(line, "releases/download/v1.2.0/") ||
				!strings.Contains(line, "uninstall --components apm,git-hooks") {
				t.Errorf("%s:%d documents --components outside the pinned legacy cleanup command", path, lineNumber+1)
			}
		}
	}

	configureReadmePath := "agent-packages/ai-agent-telemetry-configure/README.md"
	configureReadme, err := os.ReadFile(configureReadmePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"Install the APM CLI first",
		"managed CLI, components",
		"installs every component",
	} {
		if strings.Contains(string(configureReadme), value) {
			t.Errorf("%s documents removed installer behavior %q", configureReadmePath, value)
		}
	}
	for _, value := range []string{
		"optional configure skill",
		"only when APM is already on `PATH`",
		"does not install APM",
	} {
		if !strings.Contains(string(configureReadme), value) {
			t.Errorf("%s does not document %q", configureReadmePath, value)
		}
	}

	adr, err := os.ReadFile("docs/adr/0010-telemetry-installer-scope-and-lifecycle.md")
	if err != nil {
		t.Fatal(err)
	}
	adrText := string(adr)
	metadataStart := strings.Index(adrText, "<!-- markdownlint-disable MD001 -->")
	dateHeading := strings.Index(adrText, "#### Date\n\n#### Owner")
	metadataEnd := strings.Index(adrText, "<!-- markdownlint-enable MD001 -->")
	contextHeading := strings.Index(adrText, "## Context")
	if !strings.Contains(adrText, "## Status\n\nProposed") || metadataStart == -1 || dateHeading < metadataStart ||
		metadataEnd < dateHeading || contextHeading < metadataEnd {
		t.Error("ADR 0010 must keep its Proposed, blank-date metadata inside the repository MD001 exception")
	}

	for _, path := range []string{"README.md", "docs/manual-uninstall.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{
			"https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.sh | sh -s -- uninstall --components apm,git-hooks",
			"https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.ps1",
			"uninstall --components apm,git-hooks",
		} {
			if !strings.Contains(string(data), command) {
				t.Errorf("%s does not document the pinned legacy cleanup command %q", path, command)
			}
		}
	}
}

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

func TestSuperLinterExcludesGeneratedArtifacts(t *testing.T) {
	envData, err := os.ReadFile(".github/super-linter.env")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"docs/superpowers/", ".agents/", ".claude/", ".codex/", ".cursor/"} {
		if !strings.Contains(string(envData), path) {
			t.Errorf("super-linter exclusion must cover %s", path)
		}
	}

	workflowData, err := os.ReadFile(".github/workflows/super-linter.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(workflowData), "FILTER_REGEX_EXCLUDE:") {
		t.Fatal("workflow-level FILTER_REGEX_EXCLUDE overrides .github/super-linter.env")
	}
}

func TestGoBuildRetainsCoverageArtifact(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/go-build.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run  string         `yaml:"run"`
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}

	coverageGenerated := false
	coverageRetained := false
	for _, step := range workflow.Jobs["build"].Steps {
		if strings.Contains(step.Run, "go test ./...") && strings.Contains(step.Run, "-coverprofile=coverage.out") {
			coverageGenerated = true
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") &&
			step.With["path"] == "coverage.out" && step.With["if-no-files-found"] == "error" {
			coverageRetained = true
		}
	}
	if !coverageGenerated {
		t.Error("Go build must generate coverage.out")
	}
	if !coverageRetained {
		t.Error("Go build must retain coverage.out as an artifact")
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
				"agent-packages/ai-agent-telemetry/.apm/hooks/**",
				"agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/references/codex-sandbox.md",
				".github/workflows/go-build.yaml",
				".github/workflows/bootstrap-tests.yaml",
				".github/workflows/installer-tests.yaml",
				".github/workflows/super-linter.yaml",
				".github/workflows/telemetry-backend-tests.yaml",
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
				".github/workflows/bootstrap-tests.yaml",
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
				".github/workflows/installer-tests.yaml",
			},
		},
		{
			workflow: ".github/workflows/telemetry-backend-tests.yaml",
			filter:   "backend",
			paths: []string{
				"telemetry-backend/**",
				"scripts/package-backend-release.sh",
				"scripts/package_backend_release_test.sh",
				".github/workflows/telemetry-backend-tests.yaml",
			},
		},
	}

	for _, contract := range contracts {
		t.Run(contract.workflow, func(t *testing.T) {
			data, err := os.ReadFile(contract.workflow)
			if err != nil {
				t.Fatal(err)
			}
			filterPaths, err := workflowFilterPaths(string(data), contract.filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range contract.paths {
				if !filterPaths[path] {
					t.Errorf("%s filter does not cover workflow input %q", contract.filter, path)
				}
			}
		})
	}
}

func workflowFilterPaths(data, filterName string) (map[string]bool, error) {
	marker := "            " + filterName + ":\n"
	start := strings.Index(data, marker)
	if start == -1 {
		return nil, fmt.Errorf("filter %q was not found", filterName)
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
	return paths, nil
}

func TestCIGates(t *testing.T) {
	workflows := []string{
		".github/workflows/go-build.yaml",
		".github/workflows/installer-tests.yaml",
		".github/workflows/bootstrap-tests.yaml",
		".github/workflows/telemetry-backend-tests.yaml",
	}
	cases := []struct {
		name          string
		changesResult string
		runTests      string
		jobResults    string
		wantSuccess   bool
	}{
		{name: "relevant jobs succeeded", changesResult: "success", runTests: "true", jobResults: "success success", wantSuccess: true},
		{name: "irrelevant jobs skipped", changesResult: "success", runTests: "false", jobResults: "skipped skipped", wantSuccess: true},
		{name: "change detection failed", changesResult: "failure", runTests: "true", jobResults: "success success"},
		{name: "relevant job skipped", changesResult: "success", runTests: "true", jobResults: "success skipped"},
		{name: "relevant job cancelled", changesResult: "success", runTests: "true", jobResults: "success cancelled"},
		{name: "irrelevant job ran", changesResult: "success", runTests: "false", jobResults: "skipped success"},
	}

	for _, workflow := range workflows {
		t.Run(workflow, func(t *testing.T) {
			script, singularResult, err := workflowGateScript(workflow)
			if err != nil {
				t.Fatal(err)
			}
			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					command := exec.Command("bash", "-c", script)
					command.Env = append(os.Environ(),
						"CHANGES_RESULT="+testCase.changesResult,
						"RUN_TESTS="+testCase.runTests,
					)
					if singularResult {
						results := strings.Fields(testCase.jobResults)
						command.Env = append(command.Env, "JOB_RESULT="+results[len(results)-1])
					} else {
						command.Env = append(command.Env, "JOB_RESULTS="+testCase.jobResults)
					}
					err := command.Run()
					if testCase.wantSuccess && err != nil {
						t.Fatalf("gate failed: %v", err)
					}
					if !testCase.wantSuccess && err == nil {
						t.Fatal("gate unexpectedly succeeded")
					}
				})
			}
		})
	}
}

func workflowGateScript(workflow string) (string, bool, error) {
	data, err := os.ReadFile(workflow)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(data), "\n")
	inGate := false
	inRun := false
	var script []string
	for _, line := range lines {
		if line == "  ci-gate:" {
			inGate = true
			continue
		}
		if inGate && line == "        run: |" {
			inRun = true
			continue
		}
		if !inRun {
			continue
		}
		if line == "" {
			script = append(script, "")
			continue
		}
		if strings.HasPrefix(line, "          ") {
			script = append(script, strings.TrimPrefix(line, "          "))
			continue
		}
		break
	}
	if len(script) == 0 {
		return "", false, fmt.Errorf("CI gate script was not found in %s", workflow)
	}
	scriptText := strings.Join(script, "\n")
	singularResult := strings.Contains(scriptText, "$JOB_RESULT") && !strings.Contains(scriptText, "$JOB_RESULTS")
	return scriptText, singularResult, nil
}
