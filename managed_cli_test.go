package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagedPathUsesFixedPlatformLocation(t *testing.T) {
	home := filepath.Join("home", "developer")
	if got, want := managedCLIPath(home, "linux"), filepath.Join(home, ".local", "bin", "ai-agent-telemetry"); got != want {
		t.Fatalf("managedCLIPath(linux) = %q, want %q", got, want)
	}
	if got, want := managedCLIPath(home, "windows"), filepath.Join(home, ".local", "bin", "ai-agent-telemetry.exe"); got != want {
		t.Fatalf("managedCLIPath(windows) = %q, want %q", got, want)
	}
}

func TestPathReceiptRoundTripIsAtomicAndContainsOnlyOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, installReceiptName)
	want := pathReceipt{Version: 1, Kind: "posix-profile", Profile: "/tmp/profile", Entry: posixPathBlock}
	if err := writePathReceipt(path, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := readPathReceipt(path)
	if err != nil || !found || got != want {
		t.Fatalf("readPathReceipt() = %#v, %t, %v; want %#v, true, nil", got, found, err, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != installReceiptName {
		t.Fatalf("receipt directory entries = %v, want only %q", entries, installReceiptName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "endpoint", "managed_directory"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("receipt contains non-ownership field %q: %s", forbidden, data)
		}
	}
}

func TestManagedCLIInstallCopiesAtomicallyWithExecutableMode(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(source, []byte("new executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := &fakeManagedPathManager{}
	service := newManagedCLIService(managedCLIConfig{Home: home, GOOS: "linux", Paths: paths})
	result := service.Install(source)
	if result.State != operationOK {
		t.Fatalf("Install() = %#v, want OK", result)
	}
	target := managedCLIPath(home, "linux")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new executable" {
		t.Fatalf("managed executable = %q", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("managed executable mode = %o, want 755", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("atomic copy left temporary file %q", entry.Name())
		}
	}
}

func TestManagedCLIInstallRollsBackPATHWhenReceiptWriteFails(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(source, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := pathReceipt{Version: 1, Kind: "windows-user-path", Entry: filepath.Join(home, ".local", "bin")}
	paths := &fakeManagedPathManager{receipt: receipt, changed: true}
	service := newManagedCLIService(managedCLIConfig{
		Home: home, GOOS: "windows", Paths: paths,
		WriteReceipt: func(string, pathReceipt) error { return errors.New("disk full") },
	})
	result := service.Install(source)
	if result.State != operationFailed || !strings.Contains(result.Detail, "disk full") {
		t.Fatalf("Install() = %#v, want receipt failure", result)
	}
	if paths.removed != receipt {
		t.Fatalf("rolled-back receipt = %#v, want %#v", paths.removed, receipt)
	}
}

func TestManagedCLIRemoveDeletesOwnedFilesButPreservesManagedDirectory(t *testing.T) {
	home := t.TempDir()
	target := managedCLIPath(home, "linux")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := pathReceipt{Version: 1, Kind: "posix-profile", Profile: filepath.Join(home, ".profile"), Entry: posixPathBlock}
	if err := writePathReceipt(filepath.Join(filepath.Dir(target), installReceiptName), receipt); err != nil {
		t.Fatal(err)
	}
	paths := &fakeManagedPathManager{}
	service := newManagedCLIService(managedCLIConfig{Home: home, GOOS: "linux", Paths: paths})
	for attempt := 0; attempt < 2; attempt++ {
		if result := service.Remove(); result.State != operationOK {
			t.Fatalf("Remove() attempt %d = %#v, want OK", attempt+1, result)
		}
	}
	if paths.removed != receipt {
		t.Fatalf("removed PATH receipt = %#v, want %#v", paths.removed, receipt)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("managed executable still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), installReceiptName)); !os.IsNotExist(err) {
		t.Fatalf("receipt still exists or stat failed: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(target)); err != nil || !info.IsDir() {
		t.Fatalf("managed directory was removed: info=%v err=%v", info, err)
	}
}

func TestManagedCLIPreflightRejectsInstalledWindowsRemovalBeforeMutation(t *testing.T) {
	home := t.TempDir()
	target := managedCLIPath(home, "windows")
	paths := &fakeManagedPathManager{}
	service := newManagedCLIService(managedCLIConfig{
		Home: home, GOOS: "windows", Paths: paths,
		Executable: func() (string, error) { return target, nil },
	})
	opts := lifecycleOptions{Action: actionUninstall, Purge: true}
	command := "powershell.exe -NoProfile -Command \"& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'))) uninstall --purge\""
	err := service.PreflightRemove(opts)
	if err == nil || !strings.Contains(err.Error(), command) {
		t.Fatalf("PreflightRemove(%#v) = %v, want command %q", opts, err, command)
	}
	if paths.ensureCalls != 0 || paths.removeCalls != 0 {
		t.Fatalf("preflight mutated PATH: ensure=%d remove=%d", paths.ensureCalls, paths.removeCalls)
	}
}

func TestManagedCLIPreflightAllowsPOSIXAndTemporaryWindowsRunner(t *testing.T) {
	home := t.TempDir()
	for _, tt := range []struct {
		goos string
		exe  string
	}{
		{goos: "linux", exe: managedCLIPath(home, "linux")},
		{goos: "windows", exe: filepath.Join(t.TempDir(), "runner.exe")},
	} {
		service := newManagedCLIService(managedCLIConfig{
			Home: home, GOOS: tt.goos, Paths: &fakeManagedPathManager{},
			Executable: func() (string, error) { return tt.exe, nil },
		})
		if err := service.PreflightRemove(lifecycleOptions{Action: actionUninstall}); err != nil {
			t.Fatalf("PreflightRemove(%s, %s) = %v", tt.goos, tt.exe, err)
		}
	}
}

func TestManagedCLIRejectsInvalidHomeBeforeManagedMutation(t *testing.T) {
	for _, home := range []string{"", "relative/home"} {
		for _, tt := range []struct {
			name string
			opts lifecycleOptions
		}{
			{name: "install", opts: lifecycleOptions{Action: actionInstall}},
			{name: "update", opts: lifecycleOptions{Action: actionUpdate}},
			{name: "uninstall", opts: lifecycleOptions{Action: actionUninstall}},
		} {
			t.Run(home+"/"+tt.name, func(t *testing.T) {
				paths := &fakeManagedPathManager{}
				deps := lifecycleDeps{
					ManagedCLI: newManagedCLIService(managedCLIConfig{Home: home, GOOS: "linux", Paths: paths}),
					Telemetry:  componentOps{},
				}
				summary := runLifecycle(context.Background(), tt.opts, deps)
				if summary.Err == nil || !strings.Contains(summary.Err.Error(), "absolute") {
					t.Fatalf("runLifecycle() error = %v, want invalid managed home", summary.Err)
				}
				if paths.ensureCalls != 0 || paths.removeCalls != 0 {
					t.Fatalf("invalid home mutated PATH: ensure=%d remove=%d", paths.ensureCalls, paths.removeCalls)
				}
			})
		}
	}
}

type fakeManagedPathManager struct {
	receipt     pathReceipt
	changed     bool
	ensureErr   error
	removeErr   error
	removed     pathReceipt
	ensureCalls int
	removeCalls int
}

func (f *fakeManagedPathManager) Ensure() (pathReceipt, bool, error) {
	f.ensureCalls++
	return f.receipt, f.changed, f.ensureErr
}

func (f *fakeManagedPathManager) Remove(receipt pathReceipt, _ bool) error {
	f.removeCalls++
	if receipt != (pathReceipt{}) {
		f.removed = receipt
	}
	return f.removeErr
}
