package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const legacyTelemetryAPMPackage = "Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry"

type globalAPMManifest struct {
	Dependencies []string `yaml:"dependencies"`
}

func normalizeAPMDependency(value string) string {
	value = strings.TrimSpace(value)
	if revision := strings.IndexByte(value, '#'); revision >= 0 {
		value = value[:revision]
	}
	return strings.TrimSpace(value)
}

func hasLegacyTelemetryAPMDependency(data []byte) (bool, error) {
	var manifest globalAPMManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return false, err
	}
	for _, dependency := range manifest.Dependencies {
		if strings.EqualFold(normalizeAPMDependency(dependency), legacyTelemetryAPMPackage) {
			return true, nil
		}
	}
	return false, nil
}

func cleanupLegacyTelemetryAPM(home string, warnings io.Writer) {
	cleanupLegacyTelemetryAPMWith(home, warnings, exec.LookPath, func(name string, args ...string) (string, error) {
		output, err := exec.Command(name, args...).CombinedOutput()
		return string(output), err
	})
}

func cleanupLegacyTelemetryAPMWith(
	home string,
	warnings io.Writer,
	lookPath func(string) (string, error),
	runCommand func(string, ...string) (string, error),
) {
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		warnLegacyAPMVerification(warnings, err)
		return
	}
	installed, err := hasLegacyTelemetryAPMDependency(data)
	if err != nil {
		warnLegacyAPMVerification(warnings, fmt.Errorf("parse %s: %w", manifestPath, err))
		return
	}
	if !installed {
		return
	}
	apm, err := lookPath("apm")
	if err != nil {
		fmt.Fprintln(warnings,
			"warning: legacy APM cleanup could not remove the telemetry dependency: apm was not found on PATH")
		return
	}
	output, err := runCommand(apm, "uninstall", "-g", legacyTelemetryAPMPackage)
	if err == nil {
		return
	}
	diagnostic, truncated := limitAPMDiagnostic(output)
	fmt.Fprintf(warnings, "warning: legacy APM cleanup failed: %s uninstall -g %s: %v\n",
		apm, legacyTelemetryAPMPackage, err)
	if diagnostic != "" {
		fmt.Fprintf(warnings, "apm output:\n%s\n", diagnostic)
	}
	if truncated {
		fmt.Fprintln(warnings, "[apm output truncated]")
	}
}

func warnLegacyAPMVerification(warnings io.Writer, err error) {
	fmt.Fprintf(warnings,
		"warning: legacy APM cleanup could not verify or remove the telemetry dependency: %v\n", err)
}

func limitAPMDiagnostic(output string) (string, bool) {
	const limit = 4 << 10
	output = strings.TrimSpace(output)
	if len(output) <= limit {
		return output, false
	}
	return output[:limit], true
}
