package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeManagedPluginSurvivesRootChanges(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	t.Setenv("OPENCODE_CONFIG_DIR", a)
	first := installHooks(home, []hookTarget{"opencode"})
	if err := hookInstallError(first); err != nil || len(first) != 1 {
		t.Fatalf("install A = %#v, %v", first, err)
	}
	second := installHooks(home, []hookTarget{"opencode"})
	if len(second) != 1 || second[0].Changed {
		t.Fatalf("repeat = %#v", second)
	}
	t.Setenv("OPENCODE_CONFIG_DIR", b)
	if err := hookInstallError(installHooks(home, []hookTarget{"opencode"})); err != nil {
		t.Fatal(err)
	}
	if err := hookInstallError(uninstallHooks(home, []hookTarget{"opencode"}, nil)); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{a, b} {
		path := filepath.Join(root, "plugins", "ai-agent-telemetry", "index.mjs")
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("plugin %s remains: %v", path, err)
		}
	}
}

func TestOpenCodeCleanupPreservesModifiedPluginAndBlocksRemoval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	results := installHooks(home, []hookTarget{"opencode"})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("install = %#v", results)
	}
	path := results[0].Path
	modified := []byte("// custom plugin\n")
	if err := os.WriteFile(path, modified, 0o600); err != nil {
		t.Fatal(err)
	}
	results = uninstallHooks(home, []hookTarget{"opencode"}, nil)
	if err := hookInstallError(results); err == nil {
		t.Fatal("uninstall succeeded with modified owned plugin")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(modified) {
		t.Fatalf("preserved = %q, %v", got, err)
	}
}

func TestOpenCodeInstallPreservesForeignAndSymbolicEntries(t *testing.T) {
	for _, kind := range []string{"foreign", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			t.Setenv("OPENCODE_CONFIG_DIR", root)
			path := filepath.Join(root, "plugins", "ai-agent-telemetry", "index.mjs")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if kind == "foreign" {
				if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("foreign"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err := hookInstallError(installHooks(home, []hookTarget{"opencode"})); err == nil {
				t.Fatal("foreign entry replaced")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "foreign" {
				t.Fatalf("foreign = %q, %v", got, err)
			}
		})
	}
}

func TestOpenCodeStatusReportsRecordedRootConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	installed := installHooks(home, []hookTarget{"opencode"})
	if err := hookInstallError(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed[0].Path, []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	found := false
	for _, status := range gatherHookStatus(home) {
		if status.Target == "opencode" {
			found = true
			if status.State != hookInvalid || !strings.Contains(status.Detail, installed[0].Path) {
				t.Errorf("status = %#v", status)
			}
		}
	}
	if !found {
		t.Fatal("OpenCode status missing")
	}
}

func TestOpenCodeCorruptReceiptBlocksCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	results := installHooks(home, []hookTarget{hookOpenCode})
	if err := hookInstallError(results); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opencodeReceiptPath(home), []byte(`{"version":1,"paths":["/unrelated/file"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hookInstallError(uninstallHooks(home, []hookTarget{hookOpenCode}, nil)); err == nil {
		t.Fatal("corrupt receipt allowed cleanup")
	}
	if _, err := os.Stat(results[0].Path); err != nil {
		t.Fatalf("plugin not preserved: %v", err)
	}
}

func TestOpenCodeUninstallIgnoresUnownedForeignPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	path := opencodePluginPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hookInstallError(uninstallHooks(home, []hookTarget{hookOpenCode}, nil)); err != nil {
		t.Fatalf("unowned foreign plugin blocks unrelated CLI cleanup: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("foreign plugin = %q, %v", got, err)
	}
}

func TestOpenCodeUpgradeAndRemovalRecognizePreviousManagedBytes(t *testing.T) {
	for _, action := range []string{"upgrade", "remove"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
			current := opencodePluginContent
			t.Cleanup(func() { opencodePluginContent = current })
			opencodePluginContent = []byte("// Managed by ai-agent-telemetry. Do not edit.\nexport default {};\n")
			if _, err := installOpenCodePlugin(home); err != nil {
				t.Fatal(err)
			}
			opencodePluginContent = current
			path := opencodePluginPath(home)
			if got := openCodeStatus(home); got.State != hookOutdated || !strings.Contains(got.Detail, "update") {
				t.Errorf("status = %#v; want outdated", got)
			}
			if action == "upgrade" {
				if changed, err := installOpenCodePlugin(home); err != nil || !changed {
					t.Fatalf("upgrade changed=%v, err=%v", changed, err)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(current) {
					t.Fatalf("updated plugin = %q, %v", got, err)
				}
			}
			if changed, err := removeOpenCodePlugins(home); err != nil || !changed {
				t.Fatalf("remove changed=%v, err=%v", changed, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Errorf("plugin remains: %v", err)
			}
		})
	}
}

func TestOpenCodeReceiptHashesPreserveEditedPreviousPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
	current := opencodePluginContent
	t.Cleanup(func() { opencodePluginContent = current })
	opencodePluginContent = []byte("// Managed by ai-agent-telemetry.\nexport default {};\n")
	if _, err := installOpenCodePlugin(home); err != nil {
		t.Fatal(err)
	}
	path := opencodePluginPath(home)
	modified := append(append([]byte{}, opencodePluginContent...), []byte("// user customization\n")...)
	if err := os.WriteFile(path, modified, 0o600); err != nil {
		t.Fatal(err)
	}
	opencodePluginContent = current
	if _, err := installOpenCodePlugin(home); err == nil {
		t.Error("upgrade accepted edited previous plugin")
	}
	if _, err := removeOpenCodePlugins(home); err == nil {
		t.Error("removal accepted edited previous plugin")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(modified) {
		t.Errorf("preserved = %q, %v", got, err)
	}
}

func TestOpenCodeReceiptRejectsInvalidHashesWithRepairDetails(t *testing.T) {
	for _, digest := range []string{"", "not-hex", strings.Repeat("a", 62), strings.Repeat("A", 64)} {
		t.Run(digest, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
			if _, err := installOpenCodePlugin(home); err != nil {
				t.Fatal(err)
			}
			path := opencodePluginPath(home)
			receipt := opencodeReceipt{Version: 1, Paths: []string{path}, SHA256: map[string]string{path: digest}}
			data, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(opencodeReceiptPath(home), data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = removeOpenCodePlugins(home)
			if err == nil || !strings.Contains(err.Error(), opencodeReceiptPath(home)) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "#opencode-plugin-ownership-conflicts") {
				t.Fatalf("cleanup error = %v; want both paths and repair link", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("plugin not preserved: %v", err)
			}
		})
	}
}

func TestOpenCodeIsolatedReplacementIsRestoredWhenOwnershipChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.mjs")
	foreign := []byte("export default { user: true };\n")
	if err := os.WriteFile(path, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := isolateOpenCodePlugin(path, strings.Repeat("a", 64)); err == nil {
		t.Error("foreign replacement accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(foreign) {
		t.Errorf("restored = %q, %v", got, err)
	}
}

func TestOpenCodeRelativeRootErrorNamesItsEnvironmentVariable(t *testing.T) {
	for _, name := range []string{"OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			t.Setenv(name, "relative-root")
			_, err := installOpenCodePlugin(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), name+`="relative-root"`) {
				t.Errorf("error = %v; want variable and value", err)
			}
		})
	}
}
