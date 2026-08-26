package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTelemetryComponentInteractivePreflightRetainsConfigurationUntilInstall(t *testing.T) {
	var calls []string
	var configuredEndpoint, configuredToken string
	component := newTelemetryComponent(telemetryDeps{
		Home:      func() string { return "/home/test" },
		ConfigDir: func() string { return "/config/pkg" },
		Endpoint:  func() string { return "" },
		Token:     func() string { return "" },
		PromptEndpoint: func(context.Context) (string, error) {
			calls = append(calls, "prompt:endpoint")
			return "https://collector.example/v1/logs", nil
		},
		PromptToken: func(context.Context) (string, error) {
			calls = append(calls, "prompt:token")
			return "secret", nil
		},
		Configure: func(_ string, endpoint, _, token, _ string, _ deliverySettingOverrides) error {
			calls = append(calls, "configure")
			configuredEndpoint, configuredToken = endpoint, token
			return nil
		},
		InstallHooks: func(_ string, targets []hookTarget, _ io.Writer) ([]hookInstallResult, error) {
			calls = append(calls, "hooks:"+joinHookTargets(targets))
			return nil, nil
		},
	})
	opts := lifecycleOptions{Action: actionInstall, Harnesses: []hookTarget{hookClaude}}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"prompt:endpoint", "prompt:token"}) {
		t.Fatalf("preflight calls = %v", calls)
	}
	result := component.Install(context.Background(), opts)
	if result.State != operationOK || configuredEndpoint != "https://collector.example/v1/logs" || configuredToken != "secret" {
		t.Fatalf("result = %#v, endpoint = %q, token = %q", result, configuredEndpoint, configuredToken)
	}
	if !reflect.DeepEqual(calls, []string{"prompt:endpoint", "prompt:token", "configure", "hooks:claude"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestTelemetryComponentNonInteractiveMissingEndpointFailsBeforeMutation(t *testing.T) {
	mutated := false
	component := newTelemetryComponent(telemetryDeps{
		Endpoint: func() string { return "" },
		PromptEndpoint: func(context.Context) (string, error) {
			t.Fatal("prompted in noninteractive mode")
			return "", nil
		},
		Configure: func(string, string, string, string, string, deliverySettingOverrides) error {
			mutated = true
			return nil
		},
	})
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("Preflight() error = %v, want missing endpoint", err)
	}
	if mutated {
		t.Fatal("preflight mutated telemetry state")
	}
}

func TestTelemetryComponentInstallUsesFinalHarnessSelection(t *testing.T) {
	var installed []hookTarget
	component := newTelemetryComponent(telemetryDeps{
		Endpoint:  func() string { return "https://collector.example/v1/logs" },
		Configure: func(string, string, string, string, string, deliverySettingOverrides) error { return nil },
		InstallHooks: func(_ string, targets []hookTarget, _ io.Writer) ([]hookInstallResult, error) {
			installed = append([]hookTarget(nil), targets...)
			return nil, nil
		},
	})
	opts := lifecycleOptions{Action: actionUpdate, Harnesses: []hookTarget{hookCursor, hookClaude}}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if result := component.Update(context.Background(), opts); result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	if !reflect.DeepEqual(installed, opts.Harnesses) {
		t.Fatalf("installed = %v, want %v", installed, opts.Harnesses)
	}
}

func TestTelemetryComponentInstallReturnsLegacyMigrationFailure(t *testing.T) {
	component := newTelemetryComponent(telemetryDeps{
		Endpoint:  func() string { return "https://collector.example/v1/logs" },
		Configure: func(string, string, string, string, string, deliverySettingOverrides) error { return nil },
		InstallHooks: func(string, []hookTarget, io.Writer) ([]hookInstallResult, error) {
			return nil, errors.New("legacy telemetry migration failed")
		},
	})
	opts := lifecycleOptions{Action: actionInstall, Harnesses: []hookTarget{hookClaude}}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Install(context.Background(), opts)
	if result.State != operationFailed || !strings.Contains(result.Err.Error(), "legacy telemetry migration failed") {
		t.Fatalf("Install() = %#v", result)
	}
}

func TestTelemetryComponentUninstallAlwaysTargetsAllHarnessesAndPurgeWaitsForCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			base := t.TempDir()
			configDir := filepath.Join(base, "config", pkgName)
			cacheDir := filepath.Join(base, "cache", pkgName)
			for _, path := range []string{configDir, cacheDir} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var targets []hookTarget
			component := newTelemetryComponent(telemetryDeps{
				ConfigDir: func() string { return configDir }, CacheDir: func() string { return cacheDir },
				UninstallHooks: func(_ string, got []hookTarget, _ io.Writer) []hookInstallResult {
					targets = append([]hookTarget(nil), got...)
					if fail {
						return []hookInstallResult{{Target: hookClaude, Err: errors.New("malformed hook")}}
					}
					return nil
				},
			})
			result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall, Purge: true})
			if !reflect.DeepEqual(targets, allHookTargets) {
				t.Fatalf("targets = %v, want all", targets)
			}
			for _, path := range []string{configDir, cacheDir} {
				_, err := os.Stat(path)
				if fail && err != nil {
					t.Fatalf("%s removed after cleanup failure: %v", path, err)
				}
				if !fail && !os.IsNotExist(err) {
					t.Fatalf("%s remains after purge: %v", path, err)
				}
				if _, err := os.Stat(filepath.Dir(path)); err != nil {
					t.Fatalf("shared parent removed: %v", err)
				}
			}
			if fail && result.State != operationFailed || !fail && result.State != operationOK {
				t.Fatalf("Uninstall() = %#v", result)
			}
		})
	}
}

func joinHookTargets(targets []hookTarget) string {
	values := make([]string, len(targets))
	for i, target := range targets {
		values[i] = string(target)
	}
	return strings.Join(values, ",")
}
