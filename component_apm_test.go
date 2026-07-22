package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const testAPMPackage = "qubership-global-essentials@qubership-ai-packages"

func TestAPMComponentInstallBootstrapsMissingCLIAndDiscoversItAgain(t *testing.T) {
	var calls []string
	lookups := 0
	component := newAPMComponent(apmDeps{
		Home: t.TempDir(),
		LookPath: func(name string) (string, error) {
			lookups++
			calls = append(calls, "look:"+name)
			if lookups == 1 {
				return "", errors.New("not found")
			}
			return "/tools/apm", nil
		},
		Download: func(_ context.Context, url, destination string) error {
			calls = append(calls, "download:"+url)
			if filepath.Dir(destination) == os.TempDir() || filepath.Base(destination) == "." {
				t.Fatalf("installer destination is not inside a private temporary directory: %q", destination)
			}
			return os.WriteFile(destination, []byte("installer"), 0o600)
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			if name == "sh" || name == "powershell.exe" {
				if runtime.GOOS != "windows" {
					installer := args[len(args)-1]
					dirInfo, err := os.Stat(filepath.Dir(installer))
					if err != nil {
						t.Fatal(err)
					}
					if dirInfo.Mode().Perm()&0o077 != 0 {
						t.Fatalf("installer directory mode = %o, want no group/world access", dirInfo.Mode().Perm())
					}
					fileInfo, err := os.Stat(installer)
					if err != nil {
						t.Fatal(err)
					}
					if fileInfo.Mode().Perm() != 0o600 {
						t.Fatalf("installer mode = %o, want 600", fileInfo.Mode().Perm())
					}
				}
				calls = append(calls, "bootstrap:"+name)
				return "", nil
			}
			calls = append(calls, strings.Join(append([]string{name}, args...), " "))
			if reflect.DeepEqual(args, []string{"marketplace", "list"}) {
				return "microsoft\n", nil
			}
			return "", nil
		},
	})

	result := component.Install(context.Background(), lifecycleOptions{
		Action: actionInstall, Harnesses: []hookTarget{hookClaude, hookCodex},
	})
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	wantURL := "https://aka.ms/apm-unix"
	wantShell := "sh"
	if runtime.GOOS == "windows" {
		wantURL = "https://aka.ms/apm-windows"
		wantShell = "powershell.exe"
	}
	want := []string{
		"look:apm", "download:" + wantURL, "bootstrap:" + wantShell, "look:apm",
		"/tools/apm marketplace list",
		"/tools/apm marketplace add Netcracker/qubership-ai-packages",
		"/tools/apm install " + testAPMPackage + " -g --target claude,codex",
		"/tools/apm compile -g", "/tools/apm deps list -g",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls =\n%q\nwant\n%q", calls, want)
	}
}

func TestAPMComponentUpdateBootstrapsMissingCLIThroughFreshInstall(t *testing.T) {
	var calls []string
	lookups := 0
	component := newAPMComponent(apmDeps{
		Home: t.TempDir(),
		LookPath: func(name string) (string, error) {
			lookups++
			calls = append(calls, "look:"+name)
			if lookups == 1 {
				return "", errors.New("not found")
			}
			return "apm", nil
		},
		Download: func(_ context.Context, _, destination string) error {
			calls = append(calls, "download")
			return os.WriteFile(destination, []byte("installer"), 0o600)
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			if name == "sh" || name == "powershell.exe" {
				calls = append(calls, "bootstrap")
				return "", nil
			}
			command := strings.Join(append([]string{name}, args...), " ")
			calls = append(calls, command)
			if reflect.DeepEqual(args, []string{"marketplace", "list"}) {
				return "microsoft", nil
			}
			return "", nil
		},
	})

	result := component.Update(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookCursor}})
	if result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	want := []string{
		"look:apm", "download", "bootstrap", "look:apm",
		"apm marketplace list",
		"apm marketplace add Netcracker/qubership-ai-packages",
		"apm install " + testAPMPackage + " -g --target cursor",
		"apm compile -g", "apm deps list -g",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %q, want fresh-install sequence %q", calls, want)
	}
	for _, call := range calls {
		if strings.Contains(call, "self-update") || strings.Contains(call, "marketplace update") ||
			strings.Contains(call, "install --update") {
			t.Fatalf("old update logic ran during missing-CLI bootstrap: %q", call)
		}
	}
}

func TestAPMComponentInstallPreservesPresentMarketplace(t *testing.T) {
	var commands []string
	component := newAPMComponent(apmDeps{
		Home:     t.TempDir(),
		LookPath: func(string) (string, error) { return "apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			command := strings.Join(append([]string{name}, args...), " ")
			commands = append(commands, command)
			if reflect.DeepEqual(args, []string{"marketplace", "list"}) {
				return "NAME URL\nqubership-ai-packages github.com/Netcracker/qubership-ai-packages\n", nil
			}
			return "", nil
		},
	})
	result := component.Install(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookCursor}})
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	want := []string{
		"apm marketplace list",
		"apm install " + testAPMPackage + " -g --target cursor",
		"apm compile -g",
		"apm deps list -g",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
}

func TestAPMComponentUpdateRunsExactRefreshSequence(t *testing.T) {
	var commands []string
	component := newAPMComponent(apmDeps{
		Home:     t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return "", nil
		},
	})
	result := component.Update(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookClaude}})
	if result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	want := []string{
		"/bin/apm self-update",
		"/bin/apm marketplace update qubership-ai-packages",
		"/bin/apm install --update " + testAPMPackage + " -g --target claude",
		"/bin/apm compile -g",
		"/bin/apm deps list -g",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
}

func TestAPMComponentUninstallUsesMappingManifestAndPreservesSharedResources(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    "+testAPMPackage+": main\n")
	var commands []string
	component := newAPMComponent(apmDeps{
		Home:     home,
		LookPath: func(string) (string, error) { return "/tools/apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return "removed", nil
		},
	})
	result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationOK {
		t.Fatalf("Uninstall() = %#v", result)
	}
	want := []string{"/tools/apm uninstall -g " + testAPMPackage}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q; CLI and marketplace must be preserved", commands, want)
	}
}

func TestAPMComponentUninstallSkipsAbsentManifestOrPackage(t *testing.T) {
	tests := []struct {
		name string
		home func(*testing.T) string
	}{
		{name: "manifest", home: func(t *testing.T) string { return t.TempDir() }},
		{name: "package", home: func(t *testing.T) string {
			return writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    another/package: main\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			component := newAPMComponent(apmDeps{
				Home:     tt.home(t),
				LookPath: func(string) (string, error) { t.Fatal("LookPath called"); return "", nil },
				Run: func(context.Context, string, ...string) (string, error) {
					t.Fatal("Run called")
					return "", nil
				},
			})
			for attempt := 0; attempt < 2; attempt++ {
				result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
				if result.State != operationSkipped {
					t.Fatalf("attempt %d: Uninstall() = %#v", attempt, result)
				}
			}
		})
	}
}

func TestAPMComponentUninstallFailsForMalformedManifest(t *testing.T) {
	component := newAPMComponent(apmDeps{Home: writeGlobalAPMManifest(t, "dependencies: [\n")})
	result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Err.Error(), "parse") {
		t.Fatalf("Uninstall() = %#v, want parse failure", result)
	}
}

func TestAPMComponentUninstallFailsWhenHomeIsUnavailable(t *testing.T) {
	component := newAPMComponent(apmDeps{Home: "   "})
	result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Err.Error(), "home") {
		t.Fatalf("Uninstall() = %#v, want unavailable-home failure", result)
	}
}

func TestAPMComponentBoundsFailedCommandOutput(t *testing.T) {
	component := newAPMComponent(apmDeps{
		Home:     t.TempDir(),
		LookPath: func(string) (string, error) { return "apm", nil },
		Run: func(context.Context, string, ...string) (string, error) {
			return strings.Repeat("x", (4<<10)+100), errors.New("exit status 1")
		},
	})
	result := component.Install(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookClaude}})
	if result.State != operationFailed || result.Err == nil {
		t.Fatalf("Install() = %#v", result)
	}
	message := result.Err.Error()
	if !strings.Contains(message, "[apm output truncated]") || strings.Contains(message, strings.Repeat("x", (4<<10)+1)) {
		t.Fatalf("unbounded or unmarked error: %q", message)
	}
}

func TestAPMComponentFailureDoesNotHideIndependentLifecycleResults(t *testing.T) {
	apm := newAPMComponent(apmDeps{
		Home:     t.TempDir(),
		LookPath: func(string) (string, error) { return "apm", nil },
		Run:      func(context.Context, string, ...string) (string, error) { return "bad", errors.New("failed") },
	})
	telemetryRan := false
	summary := runLifecycle(context.Background(), lifecycleOptions{
		Action: actionInstall, Components: []componentName{componentAPM, componentTelemetry},
	}, lifecycleDeps{
		ManagedCLI: managedCLIService{Install: func(string) operationResult {
			return operationResult{Name: "managed-cli", State: operationOK}
		}},
		Components: map[componentName]componentOps{
			componentAPM: apm,
			componentTelemetry: {Install: func(context.Context, lifecycleOptions) operationResult {
				telemetryRan = true
				return operationResult{Name: "telemetry", State: operationOK}
			}},
		},
	})
	if !telemetryRan || len(summary.Results) != 3 || summary.Results[1].State != operationFailed || summary.Results[2].State != operationOK {
		t.Fatalf("summary = %#v, telemetryRan = %v", summary, telemetryRan)
	}
}
