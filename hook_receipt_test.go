package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookReceiptPathFrom(t *testing.T) {
	tests := []struct{ name, xdg, home, want string }{
		{"XDG wins", "/state", "/home/u", filepath.Join("/state", pkgName, hookReceiptName)},
		{"home fallback", "", "/home/u", filepath.Join("/home/u", ".local", "state", pkgName, hookReceiptName)},
		{"unavailable", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hookReceiptPathFrom(tt.xdg, tt.home); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHookReceiptLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	if validHookReceipt(home) {
		t.Fatal("missing receipt reported valid")
	}
	if err := writeHookReceipt(home); err != nil {
		t.Fatal(err)
	}
	path := hookReceiptPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != hookReceiptContents || !validHookReceipt(home) {
		t.Fatalf("receipt = %q, valid = %v", data, validHookReceipt(home))
	}
	if err := invalidateHookReceipt(home); err != nil {
		t.Fatal(err)
	}
	if err := invalidateHookReceipt(home); err != nil {
		t.Fatalf("missing receipt invalidation = %v", err)
	}
}

func TestValidHookReceiptRejectsInvalidContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	path := filepath.Join(home, ".local", "state", pkgName, hookReceiptName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version=1\nstate=installed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if validHookReceipt(home) {
		t.Fatal("invalid receipt content reported valid")
	}
}
