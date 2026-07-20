package main

import (
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
