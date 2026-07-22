package main

import (
	"path/filepath"
	"testing"
)

func TestManagedCLIWindowsAddsEntryAndPreservesExistingPATHAndType(t *testing.T) {
	home := t.TempDir()
	entry := filepath.Join(home, ".local", "bin")
	registry := &fakeUserPATHRegistry{value: `C:\Tools;C:\Windows`, valueType: registryExpandString}
	manager := newWindowsPathManager(entry, registry, nil)
	receipt, changed, err := manager.Ensure()
	if err != nil || !changed {
		t.Fatalf("Ensure() = %#v, %t, %v; want changed", receipt, changed, err)
	}
	if registry.value != `C:\Tools;C:\Windows;`+entry {
		t.Fatalf("user PATH = %q", registry.value)
	}
	if registry.writtenType != registryExpandString {
		t.Fatalf("written registry type = %d, want EXPAND_SZ", registry.writtenType)
	}
	if receipt != (pathReceipt{Version: 1, Kind: "windows-user-path", Entry: entry}) {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestManagedCLIWindowsPreservesExistingEntryWithoutReceipt(t *testing.T) {
	entry := `C:\Users\dev\.local\bin`
	registry := &fakeUserPATHRegistry{value: `C:\Tools;` + entry, valueType: registryString}
	manager := newWindowsPathManager(entry, registry, nil)
	_, changed, err := manager.Ensure()
	if err != nil || changed || registry.writes != 0 {
		t.Fatalf("Ensure() changed existing PATH: changed=%t writes=%d err=%v", changed, registry.writes, err)
	}
	if err := manager.Remove(pathReceipt{}, false); err != nil {
		t.Fatal(err)
	}
	if registry.value != `C:\Tools;`+entry || registry.writes != 0 {
		t.Fatalf("unowned PATH entry changed: value=%q writes=%d", registry.value, registry.writes)
	}
}

func TestManagedCLIWindowsRemovesOnlyReceiptedEntry(t *testing.T) {
	entry := `C:\Users\dev\.local\bin`
	registry := &fakeUserPATHRegistry{value: `C:\Tools;` + entry + `;C:\Keep`, valueType: registryString}
	manager := newWindowsPathManager(entry, registry, nil)
	receipt := pathReceipt{Version: 1, Kind: "windows-user-path", Entry: entry}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatal(err)
	}
	if registry.value != `C:\Tools;C:\Keep` {
		t.Fatalf("user PATH = %q, want unrelated entries preserved", registry.value)
	}
	if registry.writtenType != registryString {
		t.Fatalf("written registry type = %d, want REG_SZ", registry.writtenType)
	}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatalf("repeated Remove() = %v", err)
	}
	if registry.value != `C:\Tools;C:\Keep` {
		t.Fatalf("repeated Remove() changed PATH to %q", registry.value)
	}
}

func TestManagedCLIWindowsRemovalRestoresExistingPATHFormatting(t *testing.T) {
	entry := `C:\Users\dev\.local\bin`
	original := `C:\Tools;C:\Keep;`
	registry := &fakeUserPATHRegistry{value: original, valueType: registryExpandString}
	manager := newWindowsPathManager(entry, registry, nil)
	receipt, changed, err := manager.Ensure()
	if err != nil || !changed {
		t.Fatalf("Ensure() = %#v, %t, %v; want changed", receipt, changed, err)
	}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatal(err)
	}
	if registry.value != original {
		t.Fatalf("PATH after install/remove = %q, want original %q", registry.value, original)
	}
}

type fakeUserPATHRegistry struct {
	value       string
	valueType   uint32
	writtenType uint32
	writes      int
}

func (f *fakeUserPATHRegistry) Read() (string, uint32, error) {
	return f.value, f.valueType, nil
}

func (f *fakeUserPATHRegistry) Write(value string, valueType uint32) error {
	f.value = value
	f.writtenType = valueType
	f.writes++
	return nil
}
