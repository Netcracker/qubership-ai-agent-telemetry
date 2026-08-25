//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePathAllowFileSupportsLongConfigPath(t *testing.T) {
	configDir := t.TempDir()
	for len(filepath.Join(configDir, pathAllowFileName)) <= 300 {
		configDir = filepath.Join(configDir, strings.Repeat("nested", 8))
	}

	patterns := []string{`C:\Users\Alice\work\**`}
	if err := writePathAllowFile(configDir, patterns); err != nil {
		t.Fatal(err)
	}
	got, err := loadPathAllowFile(filepath.Join(configDir, pathAllowFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != patterns[0] {
		t.Fatalf("path allow = %#v, want %#v", got, patterns)
	}
}
