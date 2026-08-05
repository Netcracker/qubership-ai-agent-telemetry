package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const clineHookOwner = "Managed by ai-agent-telemetry. Do not edit."

func clineHookPath(home, goos string) string {
	if home == "" {
		return ""
	}
	name := "PostToolUse"
	if goos == "windows" {
		name += ".ps1"
	}
	return filepath.Join(home, "Documents", "Cline", "Hooks", name)
}

func clineHookContent(goos string) []byte {
	if goos == "windows" {
		return []byte("# " + clineHookOwner + "\n" +
			"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
			"exit 0\n")
	}
	return []byte("#!/bin/sh\n# " + clineHookOwner + "\n" +
		"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
		"exit 0\n")
}

func clinePreviousHookContent(goos string) []byte {
	if goos == "windows" {
		return []byte("# " + clineHookOwner + "\n" +
			"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
			"[Console]::Out.WriteLine('{\"cancel\":false}')\n" +
			"exit 0\n")
	}
	return []byte("#!/bin/sh\n# " + clineHookOwner + "\n" +
		"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
		"printf '%s\\n' '{\"cancel\":false}'\n" +
		"exit 0\n")
}

func clineHookMode(goos string) os.FileMode {
	if goos == "windows" {
		return 0o600
	}
	return 0o755
}

func installClineHook(home, goos string) (string, bool, error) {
	path := clineHookPath(home, goos)
	if path == "" {
		return "", false, errUserHomeUnavailable
	}
	want := clineHookContent(goos)
	wantMode := clineHookMode(goos)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeClineHookExclusively(path, want, wantMode); err != nil {
			return path, false, err
		}
		return path, true, nil
	}
	if err != nil {
		return path, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return path, false, fmt.Errorf("existing Cline hook is a symbolic link and was preserved: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return path, false, err
	}
	if bytes.Equal(data, clinePreviousHookContent(goos)) {
		if err := writeFileAtomically(path, want, wantMode); err != nil {
			return path, false, err
		}
		return path, true, nil
	}
	if !bytes.Equal(data, want) {
		return path, false, fmt.Errorf("existing Cline hook is not managed by ai-agent-telemetry and was preserved: %s", path)
	}
	if info.Mode().Perm() == wantMode {
		return path, false, nil
	}
	if err := os.Chmod(path, wantMode); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func writeClineHookExclusively(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("Cline hook appeared during installation and was preserved: %s", path)
		}
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func inspectClineHook(path, goos string) (hookState, string) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return hookMissing, ""
	}
	if err != nil {
		return hookInvalid, err.Error()
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return hookInvalid, fmt.Sprintf("Cline hook is a symbolic link: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return hookInvalid, err.Error()
	}
	if !bytes.Equal(data, clineHookContent(goos)) && !bytes.Equal(data, clinePreviousHookContent(goos)) {
		return hookInvalid, "hook is not managed by ai-agent-telemetry"
	}
	if info.Mode().Perm() != clineHookMode(goos) {
		return hookInvalid, fmt.Sprintf("mode is %04o, want %04o", info.Mode().Perm(), clineHookMode(goos))
	}
	return hookInstalled, ""
}

func removeClineHook(path, goos string, warnings io.Writer) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		warnPreservedClineHook(warnings, path)
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(data, clineHookContent(goos)) && !bytes.Equal(data, clinePreviousHookContent(goos)) {
		warnPreservedClineHook(warnings, path)
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func warnPreservedClineHook(warnings io.Writer, path string) {
	if warnings != nil {
		_, _ = fmt.Fprintf(warnings, "warning: preserved modified Cline hook: %s\n", path)
	}
}
