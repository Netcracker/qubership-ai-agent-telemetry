//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedCLIPosixAddsExactMarkedBlockAndPreservesProfile(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".profile")
	wantPrefix := "# user configuration\nexport EDITOR=vim\n"
	if err := os.WriteFile(profile, []byte(wantPrefix), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Join(home, ".local", "bin"), "/usr/bin")
	receipt, changed, err := manager.Ensure()
	if err != nil || !changed {
		t.Fatalf("Ensure() = %#v, %t, %v; want changed", receipt, changed, err)
	}
	if receipt != (pathReceipt{Version: 1, Kind: "posix-profile", Profile: profile, Entry: posixPathBlock}) {
		t.Fatalf("receipt = %#v", receipt)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), wantPrefix+"\n"+posixPathBlock+"\n"; got != want {
		t.Fatalf("profile = %q, want %q", got, want)
	}
	info, err := os.Stat(profile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("profile mode = %o, want existing mode 644 preserved", info.Mode().Perm())
	}
	if _, changed, err := manager.Ensure(); err != nil || changed {
		t.Fatalf("repeated Ensure() changed profile: changed=%t err=%v", changed, err)
	}
}

func TestManagedCLIPosixUpdatesAndRemovesSymlinkTarget(t *testing.T) {
	physicalRoot := t.TempDir()
	aliasParent := t.TempDir()
	root := filepath.Join(aliasParent, "root-alias")
	if err := os.Symlink(physicalRoot, root); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "dotfiles", "profile")
	profile := filepath.Join(root, ".profile")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("export EDITOR=vim\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, profile); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Join(root, ".local", "bin"), "/usr/bin")

	receipt, changed, err := manager.Ensure()
	if err != nil || !changed {
		t.Fatalf("Ensure() = %#v, %t, %v; want target update", receipt, changed, err)
	}
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Profile != canonicalTarget {
		t.Fatalf("receipt profile = %q, want canonical edited target %q", receipt.Profile, canonicalTarget)
	}
	if info, err := os.Lstat(profile); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("profile symlink was replaced: info=%v err=%v", info, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), posixPathBlock) {
		t.Fatalf("symlink target after install = %q, %v", data, err)
	}

	if err := manager.Remove(receipt, true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(profile); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("profile symlink changed during removal: info=%v err=%v", info, err)
	}
	data, err = os.ReadFile(target)
	if err != nil || strings.Contains(string(data), posixPathBlock) || !strings.Contains(string(data), "export EDITOR=vim") {
		t.Fatalf("symlink target after removal = %q, %v", data, err)
	}
}

func TestManagedCLIPosixRejectsUnsafeSymlinkTargets(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(string, string) error
	}{
		{name: "dangling", setup: func(profile, target string) error { return os.Symlink(target, profile) }},
		{name: "directory", setup: func(profile, target string) error {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			return os.Symlink(target, profile)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, ".profile")
			target := filepath.Join(root, "target")
			if err := tt.setup(profile, target); err != nil {
				t.Fatal(err)
			}
			manager := newPosixPathManager(profile, filepath.Join(root, ".local", "bin"), "/usr/bin")
			if _, _, err := manager.Ensure(); err == nil || !strings.Contains(err.Error(), "profile") {
				t.Fatalf("Ensure() error = %v, want actionable unsafe-profile rejection", err)
			}
			if info, err := os.Lstat(profile); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("unsafe symlink changed: info=%v err=%v", info, err)
			}
		})
	}
}

func TestManagedCLIPosixReceiptRemovalIgnoresRetargetedProfileSymlink(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, ".profile")
	original := filepath.Join(root, "original-profile")
	replacement := filepath.Join(root, "replacement-profile")
	if err := os.WriteFile(original, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("replacement\n"+posixPathBlock+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, profile); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Join(root, ".local", "bin"), "/usr/bin")
	receipt, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(profile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, profile); err != nil {
		t.Fatal(err)
	}

	if err := manager.Remove(receipt, true); err != nil {
		t.Fatalf("Remove() after link drift = %v", err)
	}
	originalData, _ := os.ReadFile(original)
	replacementData, _ := os.ReadFile(replacement)
	if strings.Contains(string(originalData), posixPathBlock) {
		t.Fatalf("owned block remains in recorded target: %q", originalData)
	}
	if !strings.Contains(string(replacementData), posixPathBlock) {
		t.Fatalf("replacement link target was modified: %q", replacementData)
	}
}

func TestManagedCLIPosixReceiptRemovalAllowsDeletedRecordedTarget(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, ".profile")
	target := filepath.Join(root, "profile-target")
	if err := os.WriteFile(target, []byte("user content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, profile); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Join(root, ".local", "bin"), "/usr/bin")
	receipt, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatalf("Remove() for deleted recorded target = %v, want no-op", err)
	}
}

func TestManagedCLIPosixDoesNotClaimExistingPATH(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".profile")
	managedDir := filepath.Join(home, ".local", "bin")
	manager := newPosixPathManager(profile, managedDir, "/usr/bin:"+managedDir)
	_, changed, err := manager.Ensure()
	if err != nil || changed {
		t.Fatalf("Ensure() = changed %t, err %v; want unchanged", changed, err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("profile was created despite existing PATH: %v", err)
	}
}

func TestManagedCLIPosixRemovesOnlyExactOwnedOrLegacyBlock(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".profile")
	unrelated := "# user configuration\nexport PATH=\"$HOME/tools:$PATH\"\n"
	content := unrelated + "\n" + posixPathBlock + "\n# keep this\n"
	if err := os.WriteFile(profile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Join(home, ".local", "bin"), "/usr/bin")
	// An empty receipt models the previous POSIX installer, which did not write one.
	if err := manager.Remove(pathReceipt{}, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), posixPathBlock) {
		t.Fatalf("legacy block remains: %q", data)
	}
	for _, want := range []string{"# user configuration", "$HOME/tools", "# keep this"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("unrelated profile content %q was removed: %q", want, data)
		}
	}
	if err := manager.Remove(pathReceipt{}, false); err != nil {
		t.Fatalf("repeated Remove() = %v", err)
	}
}

func TestManagedCLIPosixDoesNotRemoveSimilarUnownedText(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ".profile")
	content := "# Added by another installer\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	if err := os.WriteFile(profile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Dir(profile), "/usr/bin")
	if err := manager.Remove(pathReceipt{}, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	if string(data) != content {
		t.Fatalf("similar unowned text changed: got %q, want %q", data, content)
	}
}

func TestManagedCLIPosixReceiptRollbackRemovesOnlyRecordedProfile(t *testing.T) {
	home := t.TempDir()
	recorded := filepath.Join(home, ".profile")
	legacy := filepath.Join(home, ".bashrc")
	content := posixPathBlock + "\n"
	for _, path := range []string{recorded, legacy} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager := newPosixPathManager(recorded, filepath.Join(home, ".local", "bin"), "/usr/bin")
	manager.legacyProfiles = []string{recorded, legacy}
	receipt := pathReceipt{Version: 1, Kind: "posix-profile", Profile: recorded, Entry: posixPathBlock}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatal(err)
	}
	recordedData, _ := os.ReadFile(recorded)
	legacyData, _ := os.ReadFile(legacy)
	if strings.Contains(string(recordedData), posixPathBlock) {
		t.Fatalf("recorded block remains: %q", recordedData)
	}
	if string(legacyData) != content {
		t.Fatalf("unrecorded profile changed: got %q, want %q", legacyData, content)
	}
}

func TestManagedCLIPosixExistingMismatchedReceiptPreservesEveryProfile(t *testing.T) {
	home := t.TempDir()
	profiles := []string{
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}
	content := "user content\n" + posixPathBlock + "\n"
	for _, profile := range profiles {
		if err := os.WriteFile(profile, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	manager := newPosixPathManager(profiles[0], filepath.Join(home, ".local", "bin"), "/usr/bin")
	manager.legacyProfiles = profiles

	for _, receipt := range []pathReceipt{
		{Version: 1, Kind: "windows-user-path", Entry: filepath.Join(home, ".local", "bin")},
		{Version: 1, Kind: "posix-profile", Profile: profiles[0], Entry: "different block"},
		{Version: 1, Kind: "posix-profile", Profile: "", Entry: posixPathBlock},
		{Version: 1, Kind: "posix-profile", Profile: "relative-profile", Entry: posixPathBlock},
	} {
		if err := manager.Remove(receipt, true); err == nil {
			t.Fatalf("Remove(%#v, true) error = nil, want ownership mismatch", receipt)
		}
		for _, profile := range profiles {
			data, err := os.ReadFile(profile)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != content {
				t.Fatalf("profile %q changed for receipt %#v: got %q, want %q", profile, receipt, data, content)
			}
		}
	}
}

func TestManagedCLIPosixServicePreservesFilesForExistingPlatformMismatchedReceipt(t *testing.T) {
	home := t.TempDir()
	managed := managedCLIPath(home, "linux")
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	profiles := []string{filepath.Join(home, ".profile"), filepath.Join(home, ".bashrc"), filepath.Join(home, ".zshrc")}
	content := posixPathBlock + "\n"
	for _, profile := range profiles {
		if err := os.WriteFile(profile, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	receiptFile := filepath.Join(filepath.Dir(managed), installReceiptName)
	receipt := pathReceipt{Version: 1, Kind: "windows-user-path", Entry: filepath.Join(home, ".local", "bin")}
	if err := writePathReceipt(receiptFile, receipt); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profiles[0], filepath.Dir(managed), "/usr/bin")
	manager.legacyProfiles = profiles
	result := newManagedCLIService(managedCLIConfig{Home: home, GOOS: "linux", Paths: manager}).Remove()
	if result.State != operationFailed || !strings.Contains(result.Detail, "does not match a managed POSIX profile") {
		t.Fatalf("Remove() = %#v, want safe receipt mismatch failure", result)
	}
	for _, path := range append(profiles, managed, receiptFile) {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("owned file %q was not preserved: %v", path, err)
		}
	}
}

func TestManagedCLIPosixRemovesExactLegacyBlockAtEOF(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "only block", content: posixPathBlock, want: ""},
		{name: "after unrelated content", content: "export EDITOR=vim\n" + posixPathBlock, want: "export EDITOR=vim\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), ".profile")
			if err := os.WriteFile(profile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			manager := newPosixPathManager(profile, filepath.Dir(profile), "/usr/bin")
			if err := manager.Remove(pathReceipt{}, false); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(profile)
			if string(data) != tt.want {
				t.Fatalf("profile = %q, want %q", data, tt.want)
			}
		})
	}
}

func TestManagedCLIPosixEnsureIgnoresBlockLookalikes(t *testing.T) {
	for _, lookalike := range []string{
		"prefix " + posixPathBlock + "\n",
		posixPathBlock + " suffix\n",
	} {
		profile := filepath.Join(t.TempDir(), ".profile")
		if err := os.WriteFile(profile, []byte(lookalike), 0o600); err != nil {
			t.Fatal(err)
		}
		manager := newPosixPathManager(profile, filepath.Dir(profile), "/usr/bin")
		_, changed, err := manager.Ensure()
		if err != nil || !changed {
			t.Fatalf("Ensure() = changed %t, err %v; want exact owned block appended after lookalike", changed, err)
		}
		data, _ := os.ReadFile(profile)
		if !strings.HasSuffix(string(data), "\n"+posixPathBlock+"\n") {
			t.Fatalf("exact standalone block not appended: %q", data)
		}
	}
}

func TestManagedCLIPosixRemovePreservesBlockLookalikes(t *testing.T) {
	content := "prefix " + posixPathBlock + "\n" + posixPathBlock + " suffix\n"
	profile := filepath.Join(t.TempDir(), ".profile")
	if err := os.WriteFile(profile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := newPosixPathManager(profile, filepath.Dir(profile), "/usr/bin")
	if err := manager.Remove(pathReceipt{}, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	if string(data) != content {
		t.Fatalf("lookalikes changed: got %q, want %q", data, content)
	}
}

func TestUpdateHandoffPOSIXRunnerReplacesManagedPathBeforeLifecycle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "verified-runner")
	target := filepath.Join(dir, "bin", "ai-agent-telemetry")
	if err := os.WriteFile(source, []byte("new release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old release"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	code, err := runPOSIXUpdateRunner(source, target, func() int {
		called = true
		data, readErr := os.ReadFile(target)
		if readErr != nil || string(data) != "new release" {
			t.Fatalf("managed executable before lifecycle = %q, %v", data, readErr)
		}
		return 29
	})
	if err != nil || code != 29 || !called {
		t.Fatalf("runPOSIXUpdateRunner() = %d, %v; called = %t", code, err, called)
	}
}
