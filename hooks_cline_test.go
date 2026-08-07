package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
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

func encodeUTF16Test(text string, order binary.ByteOrder, bom []byte) []byte {
	data := append([]byte(nil), bom...)
	for _, word := range utf16.Encode([]rune(text)) {
		encoded := make([]byte, 2)
		order.PutUint16(encoded, word)
		data = append(data, encoded...)
	}
	return data
}

func TestInstallClineHookByPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		mode     os.FileMode
		contains []string
	}{
		{
			goos: "darwin",
			mode: 0o755,
			contains: []string{
				"#!/bin/sh",
				"Do not add commands to this file",
				"remove the telemetry command and ownership comment",
				"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true",
				"exit 0",
			},
		},
		{
			goos: "windows",
			mode: 0o600,
			contains: []string{
				"Do not add commands to this file",
				"remove the telemetry command and ownership comment",
				"& ai-agent-telemetry ingest --agent=cline *> $null",
				"exit 0",
			},
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
			if runtime.GOOS != "windows" && info.Mode().Perm() != tt.mode {
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hooks cannot execute on Windows")
	}
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

func TestInstallClineHookPreservesPreviousManagedContent(t *testing.T) {
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
			if err == nil || changed || !strings.Contains(err.Error(), "preserved") {
				t.Fatalf("install changed = %v, err = %v; want legacy hook preserved as a conflict", changed, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tt.content)) {
				t.Fatalf("legacy hook changed: got %q, want %q", got, tt.content)
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
	if err == nil || changed {
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

	t.Run("previous silent managed version", func(t *testing.T) {
		home := t.TempDir()
		path := clineHookPath(home, "darwin")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		previous := []byte("#!/bin/sh\n# Managed by ai-agent-telemetry. Do not edit.\n" +
			"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\nexit 0\n")
		if err := os.WriteFile(path, previous, 0o755); err != nil {
			t.Fatal(err)
		}
		changed, err := removeClineHook(path, "darwin", &bytes.Buffer{})
		if err != nil || !changed {
			t.Fatalf("changed = %v, err = %v", changed, err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("previous silent managed hook remains: %v", err)
		}
	})

	t.Run("ownership comment remains after telemetry command was removed", func(t *testing.T) {
		for _, goos := range []string{"darwin", "windows"} {
			t.Run(goos, func(t *testing.T) {
				home := t.TempDir()
				path := clineHookPath(home, goos)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				modified := []byte("# " + clineHookOwner + "\ncustom-hook-command\n")
				if err := os.WriteFile(path, modified, clineHookMode(goos)); err != nil {
					t.Fatal(err)
				}
				var warnings bytes.Buffer
				changed, err := removeClineHook(path, goos, &warnings)
				if err == nil || changed || !strings.Contains(err.Error(), "cleanup is incomplete") ||
					!strings.Contains(warnings.String(), "preserved Cline hook ownership conflict") {
					t.Fatalf("changed = %v, err = %v, warnings = %q", changed, err, warnings.String())
				}
				got, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(got, modified) {
					t.Fatalf("modified hook changed: %q, %v", got, readErr)
				}
			})
		}
	})

	t.Run("telemetry command without ownership comment is user-owned", func(t *testing.T) {
		home := t.TempDir()
		path := clineHookPath(home, "darwin")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		original := []byte("#!/bin/sh\n# ai-agent-telemetry ingest --agent=cline\n" +
			"echo ai-agent-telemetry ingest --agent=cline\n" +
			"echo \"text && ai-agent-telemetry ingest --agent=cline --not-run\"\n" +
			"echo unrelated # && ai-agent-telemetry ingest --agent=cline --not-run\n")
		if err := os.WriteFile(path, original, 0o755); err != nil {
			t.Fatal(err)
		}
		var warnings bytes.Buffer
		changed, err := removeClineHook(path, "darwin", &warnings)
		if err != nil || changed || !strings.Contains(warnings.String(), "preserved user-owned Cline hook") {
			t.Fatalf("changed = %v, err = %v, warnings = %q", changed, err, warnings.String())
		}
		if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, original) {
			t.Fatalf("unrelated hook changed: %q, %v", got, readErr)
		}
	})

	t.Run("encoded ownership comments remain managed conflicts", func(t *testing.T) {
		text := "# " + clineHookOwner + "\r\ncustom-hook-command\r\n"
		utf16LE := encodeUTF16Test(text, binary.LittleEndian, []byte{0xff, 0xfe})
		utf16BE := encodeUTF16Test(text, binary.BigEndian, []byte{0xfe, 0xff})
		tests := []struct {
			name string
			data []byte
		}{
			{name: "UTF-8 BOM", data: append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)},
			{name: "UTF-16LE BOM", data: utf16LE},
			{name: "UTF-16BE BOM", data: utf16BE},
			{name: "UTF-16LE incomplete tail", data: append(append([]byte(nil), utf16LE...), 0x23)},
			{name: "UTF-16BE incomplete tail", data: append(append([]byte(nil), utf16BE...), 0x23)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				home := t.TempDir()
				path := clineHookPath(home, "windows")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, tt.data, 0o600); err != nil {
					t.Fatal(err)
				}
				changed, err := removeClineHook(path, "windows", &bytes.Buffer{})
				if err == nil || changed || !strings.Contains(err.Error(), "cleanup is incomplete") {
					t.Fatalf("changed = %v, err = %v; want encoded ownership conflict", changed, err)
				}
				if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, tt.data) {
					t.Fatalf("encoded hook changed: %q, %v", got, readErr)
				}
			})
		}
	})

	t.Run("unrelated symlink", func(t *testing.T) {
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
		var warnings bytes.Buffer
		changed, err := removeClineHook(path, "darwin", &warnings)
		if err != nil || changed || !strings.Contains(warnings.String(), "preserved user-owned Cline hook") {
			t.Fatalf("changed = %v, err = %v, warnings = %q", changed, err, warnings.String())
		}
		if info, statErr := os.Lstat(path); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link changed: info=%v err=%v", info, statErr)
		}
	})
}

func TestRemoveClineHookDoesNotParseUnownedCommands(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		content string
	}{
		{
			name:    "POSIX whitespace",
			goos:    "darwin",
			content: "#!/bin/sh\nai-agent-telemetry\t ingest   --agent=cline >/dev/null 2>&1 || true\n",
		},
		{
			name:    "POSIX continuation and wrapper",
			goos:    "darwin",
			content: "#!/bin/sh\ncommand ai-agent-telemetry \\\n  ingest --agent=cline >/dev/null 2>&1 || true\n",
		},
		{
			name:    "POSIX assignment prefix",
			goos:    "darwin",
			content: "VAR=value ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n",
		},
		{
			name:    "POSIX pipeline",
			goos:    "darwin",
			content: "printf payload | ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n",
		},
		{
			name:    "PowerShell whitespace",
			goos:    "windows",
			content: "&  ai-agent-telemetry\t ingest --agent cline *> $null\n",
		},
		{
			name:    "PowerShell quoted command",
			goos:    "windows",
			content: "& \"ai-agent-telemetry\" ingest --agent=cline *> $null\n",
		},
		{
			name:    "PowerShell pipeline",
			goos:    "windows",
			content: "Get-Content payload | & ai-agent-telemetry ingest --agent=cline *> $null\n",
		},
		{
			name:    "flag order",
			goos:    "darwin",
			content: "ai-agent-telemetry ingest --endpoint=https://collector.example/v1/logs --agent=cline\n",
		},
		{
			name:    "Windows executable suffix",
			goos:    "windows",
			content: "& ai-agent-telemetry.exe ingest --agent=cline *> $null\n",
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
			var warnings bytes.Buffer
			changed, err := removeClineHook(path, tt.goos, &warnings)
			if err != nil || changed || !strings.Contains(warnings.String(), "preserved user-owned Cline hook") {
				t.Fatalf("changed = %v, err = %v, warnings = %q; want user-owned preservation",
					changed, err, warnings.String())
			}
		})
	}
}

func TestRemoveClineHookPreservesSymlinkWithoutReadingTarget(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "directory target",
			setup: func(t *testing.T, target string) {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized target",
			setup: func(t *testing.T, target string) {
				if err := os.WriteFile(target, bytes.Repeat([]byte("unrelated\n"), 1<<17), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(home, "unmanaged-target")
			tt.setup(t, target)
			path := clineHookPath(home, "darwin")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			changed, err := removeClineHook(path, "darwin", &bytes.Buffer{})
			if err != nil || changed {
				t.Fatalf("changed = %v, err = %v, want unmanaged symlink preserved", changed, err)
			}
			if info, statErr := os.Lstat(path); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("link changed: info=%v err=%v", info, statErr)
			}
		})
	}
}

func TestRemoveClineHookRechecksQuarantinedEntryBeforeDelete(t *testing.T) {
	home := t.TempDir()
	path := clineHookPath(home, "darwin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, clineHookContent("darwin"), 0o755); err != nil {
		t.Fatal(err)
	}
	matched, err := os.ReadFile(path)
	if err != nil || !isKnownClineHookContent(matched, "darwin") {
		t.Fatalf("initial hook did not match: %q, %v", matched, err)
	}

	replacement := []byte("#!/bin/sh\necho user-owned\n")
	if err := os.WriteFile(path, replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	changed, err := removeClineHookAfterMatch(path, "darwin")
	if err == nil || changed || !strings.Contains(err.Error(), "changed during removal") {
		t.Fatalf("changed = %v, err = %v; want replacement preserved", changed, err)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, replacement) {
		t.Fatalf("replacement hook = %q, %v; want byte-for-byte restoration", got, readErr)
	}
	leftovers, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".PostToolUse.ai-agent-telemetry-remove-*"))
	if globErr != nil || len(leftovers) != 0 {
		t.Fatalf("temporary preservation paths = %v, %v; want none after restoration", leftovers, globErr)
	}
}
