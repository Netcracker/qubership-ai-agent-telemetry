package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestMigrateLegacyTelemetryAPMWith(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		unreadable  bool
		lookPathErr error
		runOutput   string
		runErr      error
		wantErr     bool
		wantRun     bool
	}{
		{name: "missing manifest"},
		{name: "absent exact dependency", manifest: "dependencies:\n  apm:\n    - another/package\n"},
		{name: "unrelated dependency", manifest: "dependencies:\n  mcp:\n    - " + legacyTelemetryAPMPackage + "\n"},
		{name: "revision", manifest: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "#v1.0.0\n", wantRun: true},
		{name: "case insensitive revision", manifest: "dependencies:\n  apm:\n    - netcracker/Qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry#sha\n", wantRun: true},
		{name: "unreadable manifest", unreadable: true, wantErr: true},
		{name: "malformed manifest", manifest: "dependencies: [\n", wantErr: true},
		{name: "missing apm", manifest: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n", lookPathErr: errors.New("not found"), wantErr: true},
		{name: "failed uninstall", manifest: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n", runOutput: strings.Repeat("x", (4<<10)+1), runErr: errors.New("exit status 1"), wantErr: true, wantRun: true},
		{name: "successful exact uninstall", manifest: "dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n", wantRun: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			manifestPath := filepath.Join(home, ".apm", "apm.yml")
			if tt.unreadable {
				if err := os.MkdirAll(manifestPath, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if tt.manifest != "" {
				if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifestPath, []byte(tt.manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var gotName string
			var gotArgs []string
			err := migrateLegacyTelemetryAPMWith(
				home,
				func(name string) (string, error) {
					if name != "apm" {
						t.Fatalf("lookPath name = %q, want apm", name)
					}
					return "/tools/apm", tt.lookPathErr
				},
				func(name string, args ...string) (string, error) {
					gotName = name
					gotArgs = append([]string(nil), args...)
					return tt.runOutput, tt.runErr
				},
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("migrateLegacyTelemetryAPMWith() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), "apm uninstall -g "+legacyTelemetryAPMPackage) || !strings.Contains(err.Error(), "ai-agent-telemetry update") {
					t.Fatalf("error = %q, want recovery commands", err)
				}
			}
			if tt.wantRun {
				if gotName != "/tools/apm" || !reflect.DeepEqual(gotArgs, []string{"uninstall", "-g", legacyTelemetryAPMPackage}) {
					t.Fatalf("command = %q %v", gotName, gotArgs)
				}
			} else if gotName != "" {
				t.Fatalf("unexpected command = %q %v", gotName, gotArgs)
			}
			if tt.name == "failed uninstall" {
				if !strings.Contains(err.Error(), strings.Repeat("x", 4<<10)) || !strings.Contains(err.Error(), "[apm output truncated]") {
					t.Fatalf("error = %q, want bounded diagnostic", err)
				}
				if strings.Contains(err.Error(), strings.Repeat("x", (4<<10)+1)) {
					t.Fatal("error contains unbounded subprocess output")
				}
			}
		})
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
	const packageName = "example/package"
	data := []byte("dependencies:\n  apm:\n    " + packageName + ": main\n")
	installed, err := hasGlobalAPMDependency(data, packageName)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("mapping dependency was not detected")
	}
}
