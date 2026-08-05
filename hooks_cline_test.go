package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClineHookPathByPlatform(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "test")
	if got, want := clineHookPath(home, "darwin"), filepath.Join(home, "Documents", "Cline", "Hooks", "PostToolUse"); got != want {
		t.Fatalf("POSIX path = %q, want %q", got, want)
	}
	if got, want := clineHookPath(home, "windows"), filepath.Join(home, "Documents", "Cline", "Hooks", "PostToolUse.ps1"); got != want {
		t.Fatalf("Windows path = %q, want %q", got, want)
	}
}

func TestInstallClineHookByPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		mode     os.FileMode
		contains []string
	}{
		{
			goos:     "darwin",
			mode:     0o755,
			contains: []string{"#!/bin/sh", "ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true", "exit 0"},
		},
		{
			goos:     "windows",
			mode:     0o600,
			contains: []string{"& ai-agent-telemetry ingest --agent=cline *> $null", "exit 0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			home := t.TempDir()
			path, changed, err := installClineHook(home, tt.goos)
			if err != nil {
				t.Fatal(err)
			}
			if !changed || path != clineHookPath(home, tt.goos) {
				t.Fatalf("path = %q, changed = %v", path, changed)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range tt.contains {
				if !strings.Contains(string(data), fragment) {
					t.Fatalf("hook does not contain %q:\n%s", fragment, data)
				}
			}
			if strings.Contains(string(data), "cancel") {
				t.Fatalf("hook writes a Cline response to stdout:\n%s", data)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != tt.mode {
				t.Fatalf("mode = %o, want %o", info.Mode().Perm(), tt.mode)
			}

			_, changed, err = installClineHook(home, tt.goos)
			if err != nil || changed {
				t.Fatalf("second install changed = %v, err = %v", changed, err)
			}
			state, detail := inspectClineHook(path, tt.goos)
			if state != hookInstalled || detail != "" {
				t.Fatalf("status = %s, detail = %q", state, detail)
			}
		})
	}
}

func TestInstallClineHookRepairsOwnedMode(t *testing.T) {
	home := t.TempDir()
	path, _, err := installClineHook(home, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := installClineHook(home, "darwin")
	if err != nil || !changed {
		t.Fatalf("repair changed = %v, err = %v", changed, err)
	}
	if state, _ := inspectClineHook(path, "darwin"); state != hookInstalled {
		t.Fatalf("status = %s, want installed", state)
	}
}

func TestClinePOSIXHookIsSilentAndFailsOpenWhenTelemetryCLIIsMissing(t *testing.T) {
	home := t.TempDir()
	path, _, err := installClineHook(home, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path)
	cmd.Env = []string{"PATH=" + t.TempDir()}
	cmd.Stdin = strings.NewReader(`{"hookName":"PostToolUse"}`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v: %s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("output = %q, want empty", output)
	}
}

func TestInstallClineHookMigratesPreviousManagedContent(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		content string
	}{
		{
			name: "POSIX",
			goos: "darwin",
			content: "#!/bin/sh\n# Managed by ai-agent-telemetry. Do not edit.\n" +
				"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
				"printf '%s\\n' '{\"cancel\":false}'\nexit 0\n",
		},
		{
			name: "Windows",
			goos: "windows",
			content: "# Managed by ai-agent-telemetry. Do not edit.\n" +
				"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
				"[Console]::Out.WriteLine('{\"cancel\":false}')\nexit 0\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := clineHookPath(home, tt.goos)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.content), clineHookMode(tt.goos)); err != nil {
				t.Fatal(err)
			}

			_, changed, err := installClineHook(home, tt.goos)
			if err != nil || !changed {
				t.Fatalf("migration changed = %v, err = %v", changed, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), "cancel") {
				t.Fatalf("migrated hook still writes stdout:\n%s", got)
			}
			if state, detail := inspectClineHook(path, tt.goos); state != hookInstalled || detail != "" {
				t.Fatalf("status = %s, detail = %q", state, detail)
			}
		})
	}
}

func TestInstallClineHookPreservesConflict(t *testing.T) {
	home := t.TempDir()
	path := clineHookPath(home, "darwin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("#!/bin/sh\necho unrelated\n")
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatal(err)
	}
	_, changed, err := installClineHook(home, "darwin")
	if err == nil || changed || !strings.Contains(err.Error(), "existing Cline hook is not managed") {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("conflicting hook changed: %q, %v", got, readErr)
	}
	if state, _ := inspectClineHook(path, "darwin"); state != hookInvalid {
		t.Fatalf("status = %s, want invalid", state)
	}
}

func TestInstallClineHookPreservesSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "unrelated-hook")
	original := []byte("#!/bin/sh\necho unrelated\n")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}
	path := clineHookPath(home, "darwin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, changed, err := installClineHook(home, "darwin")
	if err == nil || changed || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	if info, statErr := os.Lstat(path); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link changed: info=%v err=%v", info, statErr)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("target changed: %q, %v", got, readErr)
	}
}

func TestRemoveClineHookOwnership(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		home := t.TempDir()
		path, _, err := installClineHook(home, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		changed, err := removeClineHook(path, "darwin", &bytes.Buffer{})
		if err != nil || !changed {
			t.Fatalf("changed = %v, err = %v", changed, err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned hook remains: %v", err)
		}
	})

	t.Run("previous managed version", func(t *testing.T) {
		home := t.TempDir()
		path := clineHookPath(home, "darwin")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		previous := []byte("#!/bin/sh\n# Managed by ai-agent-telemetry. Do not edit.\n" +
			"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
			"printf '%s\\n' '{\"cancel\":false}'\nexit 0\n")
		if err := os.WriteFile(path, previous, 0o755); err != nil {
			t.Fatal(err)
		}
		changed, err := removeClineHook(path, "darwin", &bytes.Buffer{})
		if err != nil || !changed {
			t.Fatalf("changed = %v, err = %v", changed, err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("previous managed hook remains: %v", err)
		}
	})

	t.Run("modified", func(t *testing.T) {
		home := t.TempDir()
		path, _, err := installClineHook(home, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		modified := append(clineHookContent("darwin"), []byte("# local change\n")...)
		if err := os.WriteFile(path, modified, 0o755); err != nil {
			t.Fatal(err)
		}
		var warnings bytes.Buffer
		changed, err := removeClineHook(path, "darwin", &warnings)
		if err != nil || changed || !strings.Contains(warnings.String(), "preserved modified Cline hook") {
			t.Fatalf("changed = %v, err = %v, warnings = %q", changed, err, warnings.String())
		}
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, modified) {
			t.Fatalf("modified hook changed: %q, %v", got, readErr)
		}
	})
}
