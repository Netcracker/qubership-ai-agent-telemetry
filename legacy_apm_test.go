package main

import "testing"

func TestHasLegacyTelemetryAPMDependency(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "plain", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "\n", want: true},
		{name: "revision", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "#v1.0.0\n", want: true},
		{name: "single quoted", yaml: "dependencies:\n  - '" + legacyTelemetryAPMPackage + "#sha'\n", want: true},
		{name: "double quoted", yaml: "dependencies:\n  - \"" + legacyTelemetryAPMPackage + "#sha\"\n", want: true},
		{name: "comment", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "#sha # old hook\n", want: true},
		{name: "case insensitive", yaml: "dependencies:\n  - netcracker/Qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry\n", want: true},
		{name: "near match", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "-extra\n"},
		{name: "unrelated list", yaml: "examples:\n  - " + legacyTelemetryAPMPackage + "\n"},
		{name: "absent", yaml: "dependencies:\n  - another/package\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hasLegacyTelemetryAPMDependency([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("match = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasLegacyTelemetryAPMDependencyRejectsMalformedYAML(t *testing.T) {
	if _, err := hasLegacyTelemetryAPMDependency([]byte("dependencies: [\n")); err == nil {
		t.Fatal("expected malformed YAML error")
	}
}
