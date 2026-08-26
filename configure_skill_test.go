package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigureSkillSkipsWhenAPMIsUnavailable(t *testing.T) {
	service := newConfigureSkillService(configureSkillDeps{
		Home:     t.TempDir(),
		Version:  "v1.2.3",
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("Run called")
			return "", nil
		},
	})

	result := service.Install(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookClaude}})
	if result.State != operationSkipped || !strings.Contains(result.Detail, "apm") {
		t.Fatalf("Install() = %#v, want skipped unavailable APM", result)
	}
}

func TestConfigureSkillSkipsDevBuild(t *testing.T) {
	service := newConfigureSkillService(configureSkillDeps{
		Home:    t.TempDir(),
		Version: "dev",
		LookPath: func(string) (string, error) {
			t.Fatal("LookPath called")
			return "", nil
		},
	})

	result := service.Install(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookClaude}})
	if result.State != operationSkipped || !strings.Contains(result.Detail, "release tag") {
		t.Fatalf("Install() = %#v, want skipped dev build", result)
	}
}

func TestConfigureSkillInstallUsesPinnedReleaseSource(t *testing.T) {
	var commands []string
	service := newConfigureSkillService(configureSkillDeps{
		Home:     t.TempDir(),
		Version:  "v1.2.3",
		LookPath: func(string) (string, error) { return "/tools/apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return "", nil
		},
	})

	result := service.Install(context.Background(), lifecycleOptions{
		Harnesses: []hookTarget{hookClaude, hookCline, hookCursor},
	})
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	want := []string{
		"/tools/apm install " + configureSkillPackage + "#v1.2.3 -g --target claude,agent-skills,cursor",
		"/tools/apm compile -g",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
}

func TestConfigureSkillUpdateUsesNewPinnedReleaseSource(t *testing.T) {
	var commands []string
	service := newConfigureSkillService(configureSkillDeps{
		Home:     t.TempDir(),
		Version:  "v2.0.0",
		LookPath: func(string) (string, error) { return "apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return "", nil
		},
	})

	result := service.Update(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookCodex}})
	if result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	want := []string{
		"apm install " + configureSkillPackage + "#v2.0.0 -g --target codex",
		"apm compile -g",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
	for _, command := range commands {
		if strings.Contains(command, "--update") {
			t.Fatalf("update used APM graph-wide refresh: %q", command)
		}
	}
}

func TestJoinConfigureSkillTargetsMapsClineAndDeduplicatesStably(t *testing.T) {
	targets := []hookTarget{hookCline, hookCursor, hookCline, hookClaude}
	if got, want := joinConfigureSkillTargets(targets), "agent-skills,cursor,claude"; got != want {
		t.Fatalf("joinConfigureSkillTargets(%v) = %q, want %q", targets, got, want)
	}
}

func TestConfigureSkillReportsCommandFailureAsWarn(t *testing.T) {
	service := newConfigureSkillService(configureSkillDeps{
		Home:     t.TempDir(),
		Version:  "v1.2.3",
		LookPath: func(string) (string, error) { return "apm", nil },
		Run:      func(context.Context, string, ...string) (string, error) { return "broken", errors.New("exit status 1") },
	})

	result := service.Install(context.Background(), lifecycleOptions{Harnesses: []hookTarget{hookClaude}})
	if result.State != operationWarn || result.Err == nil || !strings.Contains(result.Err.Error(), "broken") {
		t.Fatalf("Install() = %#v, want visible WARN command failure", result)
	}
}

func TestConfigureSkillUninstallRemovesOnlyExactGlobalPackage(t *testing.T) {
	home := writeConfigureSkillManifest(t, "dependencies:\n  apm:\n    "+configureSkillPackage+"#v1.2.3: main\n")
	var commands []string
	service := newConfigureSkillService(configureSkillDeps{
		Home:     home,
		Version:  "v1.2.3",
		LookPath: func(string) (string, error) { return "/tools/apm", nil },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return "", nil
		},
	})

	result := service.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationOK {
		t.Fatalf("Uninstall() = %#v", result)
	}
	want := []string{"/tools/apm uninstall -g " + configureSkillPackage}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
}

func TestConfigureSkillUninstallSkipsAbsentDependency(t *testing.T) {
	service := newConfigureSkillService(configureSkillDeps{
		Home:    writeConfigureSkillManifest(t, "dependencies:\n  apm:\n    another/package: main\n"),
		Version: "v1.2.3",
		LookPath: func(string) (string, error) {
			t.Fatal("LookPath called")
			return "", nil
		},
		Run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("Run called")
			return "", nil
		},
	})

	result := service.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationSkipped || !strings.Contains(result.Detail, "absent") {
		t.Fatalf("Uninstall() = %#v, want skipped absent package", result)
	}
}

func writeConfigureSkillManifest(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}
