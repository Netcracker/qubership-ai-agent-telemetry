package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeGlobalAPMManifest(t *testing.T, contents string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".apm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apm.yml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCleanupLegacyTelemetryAPMWithUninstallsMatchingDependency(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"#sha\n")
	var gotName string
	var gotArgs []string
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(name string) (string, error) { return "/tools/apm", nil },
		func(name string, args ...string) (string, error) {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return "removed\n", nil
		},
	)
	if gotName != "/tools/apm" || !reflect.DeepEqual(gotArgs, []string{"uninstall", "-g", legacyTelemetryAPMPackage}) {
		t.Fatalf("command = %q %v", gotName, gotArgs)
	}
	if warnings.Len() != 0 {
		t.Fatalf("warnings = %q, want none", warnings.String())
	}
}

func TestCleanupLegacyTelemetryAPMWithMissingManifestDoesNothing(t *testing.T) {
	var lookedUp, ran bool
	cleanupLegacyTelemetryAPMWith(
		t.TempDir(),
		&strings.Builder{},
		func(string) (string, error) { lookedUp = true; return "apm", nil },
		func(string, ...string) (string, error) { ran = true; return "", nil },
	)
	if lookedUp || ran {
		t.Fatalf("lookedUp = %v, ran = %v, want neither", lookedUp, ran)
	}
}

func TestCleanupLegacyTelemetryAPMWithBlankHomeDoesNothing(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	relativeHomes := []string{""}
	if runtime.GOOS != "windows" {
		relativeHomes = append(relativeHomes, "   ")
	}
	for _, relativeHome := range relativeHomes {
		dir := filepath.Join(relativeHome, ".apm")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(dir, "apm.yml")
		contents := []byte("dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n")
		if err := os.WriteFile(manifest, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		home string
	}{
		{name: "empty", home: ""},
		{name: "whitespace", home: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lookedUp, ran bool
			var warnings strings.Builder
			cleanupLegacyTelemetryAPMWith(
				tt.home,
				&warnings,
				func(string) (string, error) { lookedUp = true; return "apm", nil },
				func(string, ...string) (string, error) { ran = true; return "", nil },
			)
			if lookedUp || ran {
				t.Fatalf("lookedUp = %v, ran = %v, want neither", lookedUp, ran)
			}
			if warnings.Len() != 0 {
				t.Fatalf("warnings = %q, want none", warnings.String())
			}
		})
	}
}

func TestCleanupLegacyTelemetryAPMWithAbsentDependencyDoesNothing(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - another/package\n")
	var lookedUp, ran bool
	cleanupLegacyTelemetryAPMWith(
		home,
		&strings.Builder{},
		func(string) (string, error) { lookedUp = true; return "apm", nil },
		func(string, ...string) (string, error) { ran = true; return "", nil },
	)
	if lookedUp || ran {
		t.Fatalf("lookedUp = %v, ran = %v, want neither", lookedUp, ran)
	}
}

func TestCleanupLegacyTelemetryAPMWithWarnsWhenManifestIsUnreadable(t *testing.T) {
	home := t.TempDir()
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(manifestPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(string) (string, error) { t.Fatal("lookPath called"); return "", nil },
		func(string, ...string) (string, error) { t.Fatal("runCommand called"); return "", nil },
	)
	if !strings.Contains(warnings.String(), "warning: legacy APM cleanup could not verify or remove the telemetry dependency:") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestCleanupLegacyTelemetryAPMWithWarnsWhenManifestIsMalformed(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies: [\n")
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(string) (string, error) { t.Fatal("lookPath called"); return "", nil },
		func(string, ...string) (string, error) { t.Fatal("runCommand called"); return "", nil },
	)
	if !strings.Contains(warnings.String(), "could not verify or remove the telemetry dependency: parse "+manifestPath) {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestCleanupLegacyTelemetryAPMWithWarnsWhenAPMIsMissing(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"\n")
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(name string) (string, error) {
			if name != "apm" {
				t.Fatalf("lookPath name = %q, want apm", name)
			}
			return "", errors.New("not found")
		},
		func(string, ...string) (string, error) { t.Fatal("runCommand called"); return "", nil },
	)
	want := "warning: legacy APM cleanup could not remove the telemetry dependency: apm was not found on PATH\n"
	if warnings.String() != want {
		t.Fatalf("warnings = %q, want %q", warnings.String(), want)
	}
}

func TestCleanupLegacyTelemetryAPMWithReportsFailedUninstall(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"\n")
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(string) (string, error) { return "/tools/apm", nil },
		func(string, ...string) (string, error) { return "stdout\nstderr\n", errors.New("exit status 1") },
	)
	want := "warning: legacy APM cleanup failed: /tools/apm uninstall -g " + legacyTelemetryAPMPackage +
		": exit status 1\napm output:\nstdout\nstderr\n"
	if warnings.String() != want {
		t.Fatalf("warnings = %q, want %q", warnings.String(), want)
	}
}

func TestCleanupLegacyTelemetryAPMWithMarksTruncatedOutput(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"\n")
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(string) (string, error) { return "apm", nil },
		func(string, ...string) (string, error) { return strings.Repeat("x", (4<<10)+1), errors.New("failed") },
	)
	if !strings.Contains(warnings.String(), strings.Repeat("x", 4<<10)+"\n[apm output truncated]\n") {
		t.Fatalf("warnings do not contain bounded output and truncation marker: %q", warnings.String())
	}
	if strings.Contains(warnings.String(), strings.Repeat("x", (4<<10)+1)) {
		t.Fatal("warnings contain unbounded subprocess output")
	}
}

func TestCleanupLegacyTelemetryAPMWithSuppressesSuccessfulOutput(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"\n")
	var warnings strings.Builder
	cleanupLegacyTelemetryAPMWith(
		home,
		&warnings,
		func(string) (string, error) { return "apm", nil },
		func(string, ...string) (string, error) { return "successful internal output\n", nil },
	)
	if warnings.Len() != 0 {
		t.Fatalf("warnings = %q, want successful output suppressed", warnings.String())
	}
}

func TestLimitAPMDiagnostic(t *testing.T) {
	diagnostic, truncated := limitAPMDiagnostic(" \n" + strings.Repeat("x", (4<<10)+1) + "\n ")
	if diagnostic != strings.Repeat("x", 4<<10) {
		t.Fatalf("diagnostic length = %d, want %d", len(diagnostic), 4<<10)
	}
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
}

func TestHasLegacyTelemetryAPMDependency(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{name: "plain", yaml: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n", want: true},
		{name: "revision", yaml: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "#v1.0.0\n", want: true},
		{name: "single quoted", yaml: "dependencies:\n  apm:\n    - '" + legacyTelemetryAPMPackage + "#sha'\n", want: true},
		{name: "double quoted", yaml: "dependencies:\n  apm:\n    - \"" + legacyTelemetryAPMPackage + "#sha\"\n", want: true},
		{name: "comment", yaml: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "#sha # old hook\n", want: true},
		{name: "case insensitive", yaml: "dependencies:\n  apm:\n    - netcracker/Qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry\n", want: true},
		{name: "near match", yaml: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "-extra\n"},
		{
			name: "other dependency categories",
			yaml: "dependencies:\n  mcp:\n    - " + legacyTelemetryAPMPackage +
				"\n  lsp:\n    - " + legacyTelemetryAPMPackage + "\n",
		},
		{
			name: "object form neighbor",
			yaml: "dependencies:\n  apm:\n    - git: example.com/team/package\n    - " +
				legacyTelemetryAPMPackage + "\n",
			want: true,
		},
		{name: "object form only", yaml: "dependencies:\n  apm:\n    - git: example.com/team/package\n"},
		{name: "unrelated list", yaml: "examples:\n  - " + legacyTelemetryAPMPackage + "\n"},
		{name: "absent", yaml: "dependencies:\n  apm:\n    - another/package\n"},
		{
			name: "mapping form",
			yaml: "dependencies:\n  apm:\n    " + legacyTelemetryAPMPackage + ": sha\n",
			want: true,
		},
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

func TestHasLegacyTelemetryAPMDependencyRejectsInvalidAPMEntryType(t *testing.T) {
	if _, err := hasLegacyTelemetryAPMDependency([]byte("dependencies:\n  apm:\n    - 42\n")); err == nil {
		t.Fatal("expected invalid dependencies.apm entry error")
	}
}

func TestGlobalAPMManifestMatchesMappingDependency(t *testing.T) {
	data := []byte("dependencies:\n  apm:\n    " + testAPMPackage + ": main\n")
	installed, err := hasGlobalAPMDependency(data, testAPMPackage)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("mapping dependency was not detected")
	}
}
