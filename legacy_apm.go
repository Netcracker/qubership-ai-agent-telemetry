package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const legacyTelemetryAPMPackage = "Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry"

type globalAPMManifest struct {
	Dependencies struct {
		APM yaml.Node `yaml:"apm"`
	} `yaml:"dependencies"`
}

func normalizeAPMDependency(value string) string {
	value = strings.TrimSpace(value)
	if revision := strings.IndexByte(value, '#'); revision >= 0 {
		value = value[:revision]
	}
	return strings.TrimSpace(value)
}

func hasLegacyTelemetryAPMDependency(data []byte) (bool, error) {
	return hasGlobalAPMDependency(data, legacyTelemetryAPMPackage)
}

func hasGlobalAPMDependency(data []byte, packageName string) (bool, error) {
	var manifest globalAPMManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return false, err
	}
	apm := manifest.Dependencies.APM
	if apm.Kind == 0 {
		return false, nil
	}
	if apm.Kind == yaml.MappingNode {
		for index := 0; index < len(apm.Content); index += 2 {
			dependency := apm.Content[index]
			if dependency.Kind != yaml.ScalarNode || dependency.Tag != "!!str" {
				return false, fmt.Errorf("dependencies.apm key %d must be a string", index/2)
			}
			if strings.EqualFold(normalizeAPMDependency(dependency.Value), packageName) {
				return true, nil
			}
		}
		return false, nil
	}
	if apm.Kind != yaml.SequenceNode {
		return false, errors.New("dependencies.apm must be a sequence or mapping")
	}
	for index, dependency := range apm.Content {
		if dependency.Kind == yaml.MappingNode {
			continue
		}
		if dependency.Kind != yaml.ScalarNode || dependency.Tag != "!!str" {
			return false, fmt.Errorf("dependencies.apm entry %d must be a string or mapping", index)
		}
		if strings.EqualFold(normalizeAPMDependency(dependency.Value), packageName) {
			return true, nil
		}
	}
	return false, nil
}

func migrateLegacyTelemetryAPM(home string) error {
	return migrateLegacyTelemetryAPMWith(home, exec.LookPath, func(name string, args ...string) (string, error) {
		output, err := exec.Command(name, args...).CombinedOutput()
		return string(output), err
	})
}

func migrateLegacyTelemetryAPMWith(
	home string,
	lookPath func(string) (string, error),
	runCommand func(string, ...string) (string, error),
) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return legacyTelemetryMigrationError(fmt.Errorf("read %s: %w", manifestPath, err))
	}
	installed, err := hasLegacyTelemetryAPMDependency(data)
	if err != nil {
		return legacyTelemetryMigrationError(fmt.Errorf("parse %s: %w", manifestPath, err))
	}
	if !installed {
		return nil
	}
	apm, err := lookPath("apm")
	if err != nil {
		return legacyTelemetryMigrationError(errors.New("apm was not found on PATH"))
	}
	output, err := runCommand(apm, "uninstall", "-g", legacyTelemetryAPMPackage)
	if err == nil {
		return nil
	}
	diagnostic, truncated := limitAPMDiagnostic(output)
	failure := fmt.Errorf("%s uninstall -g %s: %w", apm, legacyTelemetryAPMPackage, err)
	if diagnostic != "" {
		failure = fmt.Errorf("%w\napm output:\n%s", failure, diagnostic)
	}
	if truncated {
		failure = fmt.Errorf("%w\n[apm output truncated]", failure)
	}
	return legacyTelemetryMigrationError(failure)
}

func legacyTelemetryMigrationError(err error) error {
	return fmt.Errorf("legacy telemetry APM migration failed: %w\nRun:\n  apm uninstall -g %s\n  ai-agent-telemetry update",
		err, legacyTelemetryAPMPackage)
}

func limitAPMDiagnostic(output string) (string, bool) {
	const limit = 4 << 10
	output = strings.TrimSpace(output)
	if len(output) <= limit {
		return output, false
	}
	return output[:limit], true
}
