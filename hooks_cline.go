package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

const (
	clineHookOwner       = "Managed by ai-agent-telemetry. Do not edit."
	clineManualUninstall = "https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/docs/manual-uninstall.md"
)

var errClineHookOwnershipConflict = errors.New("cline hook cleanup is incomplete")

type clineHookEntryKind uint8

const (
	clineEntryMissing clineHookEntryKind = iota
	clineEntryRegular
	clineEntrySymlink
	clineEntryOther
)

type clineHookEntry struct {
	kind clineHookEntryKind
	file *os.File
	info os.FileInfo
	data []byte
}

func (entry *clineHookEntry) close() error {
	if entry.file == nil {
		return nil
	}
	err := entry.file.Close()
	entry.file = nil
	return err
}

func readClineHookEntry(path string) (clineHookEntry, error) {
	file, err := openClineHookNoFollow(path)
	if err != nil {
		info, lstatErr := os.Lstat(path)
		if errors.Is(lstatErr, os.ErrNotExist) {
			return clineHookEntry{kind: clineEntryMissing}, nil
		}
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return clineHookEntry{kind: clineEntrySymlink, info: info}, nil
		}
		if lstatErr == nil && !info.Mode().IsRegular() {
			return clineHookEntry{kind: clineEntryOther, info: info}, nil
		}
		return clineHookEntry{}, err
	}
	entry := clineHookEntry{file: file}
	info, err := file.Stat()
	if err != nil {
		_ = entry.close()
		return clineHookEntry{}, err
	}
	entry.info = info
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		entry.kind = clineEntrySymlink
		return entry, nil
	case !info.Mode().IsRegular():
		entry.kind = clineEntryOther
		return entry, nil
	default:
		entry.kind = clineEntryRegular
	}
	entry.data, err = io.ReadAll(file)
	if err != nil {
		_ = entry.close()
		return clineHookEntry{}, err
	}
	return entry, nil
}

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
			"# Do not add commands to this file; use a Cline workspace hook instead.\n" +
			"# To keep custom commands during uninstall, remove the telemetry command and ownership comment, then rerun uninstall.\n" +
			"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
			"exit 0\n")
	}
	return []byte("#!/bin/sh\n# " + clineHookOwner + "\n" +
		"# Do not add commands to this file; use a Cline workspace hook instead.\n" +
		"# To keep custom commands during uninstall, remove the telemetry command and ownership comment, then rerun uninstall.\n" +
		"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
		"exit 0\n")
}

func clineLegacyHookContents(goos string) [][]byte {
	if goos == "windows" {
		return [][]byte{
			[]byte("# " + clineHookOwner + "\n" +
				"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
				"exit 0\n"),
			[]byte("# " + clineHookOwner + "\n" +
				"& ai-agent-telemetry ingest --agent=cline *> $null\n" +
				"[Console]::Out.WriteLine('{\"cancel\":false}')\n" +
				"exit 0\n"),
		}
	}
	return [][]byte{
		[]byte("#!/bin/sh\n# " + clineHookOwner + "\n" +
			"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
			"exit 0\n"),
		[]byte("#!/bin/sh\n# " + clineHookOwner + "\n" +
			"ai-agent-telemetry ingest --agent=cline >/dev/null 2>&1 || true\n" +
			"printf '%s\\n' '{\"cancel\":false}'\n" +
			"exit 0\n"),
	}
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
	entry, err := readClineHookEntry(path)
	if err != nil {
		return path, false, err
	}
	defer func() { _ = entry.close() }()
	switch entry.kind {
	case clineEntryMissing:
		if err := writeClineHookExclusively(path, want, wantMode); err != nil {
			return path, false, err
		}
		return path, true, nil
	case clineEntrySymlink:
		return path, false, fmt.Errorf("existing Cline hook is a symbolic link and was preserved: %s", path)
	case clineEntryOther:
		return path, false, fmt.Errorf("existing Cline hook is not a regular file and was preserved: %s", path)
	}
	if !bytes.Equal(entry.data, want) {
		return path, false, fmt.Errorf(
			"existing Cline hook does not exactly match the current managed version and was preserved: %s; "+
				"resolve the conflict before installing; see %s", path, clineManualUninstall)
	}
	if runtime.GOOS == "windows" || entry.info.Mode().Perm() == wantMode {
		return path, false, nil
	}
	if err := entry.file.Chmod(wantMode); err != nil {
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
			return fmt.Errorf("cline hook appeared during installation and was preserved: %s", path)
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
	entry, err := readClineHookEntry(path)
	if err != nil {
		return hookInvalid, err.Error()
	}
	defer func() { _ = entry.close() }()
	switch entry.kind {
	case clineEntryMissing:
		return hookMissing, ""
	case clineEntrySymlink:
		return hookInvalid, fmt.Sprintf("Cline hook is a symbolic link: %s", path)
	case clineEntryOther:
		return hookInvalid, fmt.Sprintf("Cline hook is not a regular file: %s", path)
	}
	if !isKnownClineHookContent(entry.data, goos) {
		return hookInvalid, "hook is not managed by ai-agent-telemetry"
	}
	if runtime.GOOS != "windows" && entry.info.Mode().Perm() != clineHookMode(goos) {
		return hookInvalid, fmt.Sprintf("mode is %04o, want %04o", entry.info.Mode().Perm(), clineHookMode(goos))
	}
	return hookInstalled, ""
}

func removeClineHook(path, goos string, warnings io.Writer) (bool, error) {
	entry, err := readClineHookEntry(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = entry.close() }()
	switch entry.kind {
	case clineEntryMissing:
		return false, nil
	case clineEntrySymlink:
		warnPreservedUserOwnedClineHook(warnings, path)
		return false, nil
	case clineEntryOther:
		warnPreservedUserOwnedClineHook(warnings, path)
		return false, nil
	}
	if !isKnownClineHookContent(entry.data, goos) {
		if clineHookHasOwnerComment(entry.data) {
			warnClineHookOwnershipConflict(warnings, path)
			return false, fmt.Errorf("%w: preserved hook does not match a generated version: %s",
				errClineHookOwnershipConflict, path)
		}
		warnPreservedUserOwnedClineHook(warnings, path)
		return false, nil
	}
	if err := entry.close(); err != nil {
		return false, err
	}
	return removeClineHookAfterMatch(path, goos)
}

func removeClineHookAfterMatch(path, goos string) (bool, error) {
	quarantine, err := quarantineClineHook(path)
	if err != nil {
		return false, fmt.Errorf("cannot isolate the matched Cline hook before removal: %w", err)
	}
	entry, err := readClineHookEntry(quarantine)
	if err != nil {
		return false, fmt.Errorf("cannot recheck isolated Cline hook %s: %w", quarantine, err)
	}
	defer func() { _ = entry.close() }()
	if entry.kind != clineEntryRegular {
		return false, fmt.Errorf(
			"cline hook changed during removal; the replacement was preserved at %s and was not deleted", quarantine)
	}
	if !isKnownClineHookContent(entry.data, goos) {
		if err := entry.close(); err != nil {
			return false, fmt.Errorf("cannot close changed Cline hook preserved at %s: %w", quarantine, err)
		}
		if err := restoreQuarantinedClineHook(path, quarantine); err != nil {
			return false, err
		}
		return false, fmt.Errorf("cline hook changed during removal and was restored without deletion: %s", path)
	}
	if err := entry.close(); err != nil {
		return false, fmt.Errorf("cannot close matched Cline hook preserved at %s: %w", quarantine, err)
	}
	if err := os.Remove(quarantine); err != nil {
		return false, fmt.Errorf("cannot remove matched Cline hook preserved at %s: %w", quarantine, err)
	}
	return true, nil
}

func quarantineClineHook(path string) (string, error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".ai-agent-telemetry-remove-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	temporaryInfo, statErr := temporary.Stat()
	if statErr != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return "", statErr
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", err
	}
	if err := os.Rename(path, temporaryPath); err != nil {
		if current, statErr := os.Lstat(temporaryPath); statErr == nil && os.SameFile(temporaryInfo, current) {
			_ = os.Remove(temporaryPath)
		}
		return "", err
	}
	return temporaryPath, nil
}

func restoreQuarantinedClineHook(path, quarantine string) error {
	if err := os.Link(quarantine, path); err != nil {
		return fmt.Errorf(
			"cline hook changed during removal; preserved the replacement at %s because it could not be restored to %s: %w",
			quarantine, path, err)
	}
	if err := os.Remove(quarantine); err != nil {
		return fmt.Errorf("restored changed Cline hook to %s but could not remove its temporary link %s: %w",
			path, quarantine, err)
	}
	return nil
}

func isKnownClineHookContent(data []byte, goos string) bool {
	if bytes.Equal(data, clineHookContent(goos)) {
		return true
	}
	for _, legacy := range clineLegacyHookContents(goos) {
		if bytes.Equal(data, legacy) {
			return true
		}
	}
	return false
}

func clineHookHasOwnerComment(data []byte) bool {
	want := "# " + clineHookOwner
	for _, line := range strings.Split(decodeClineHookText(data), "\n") {
		if strings.TrimSuffix(line, "\r") == want {
			return true
		}
	}
	return false
}

func decodeClineHookText(data []byte) string {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return string(data[3:])
	}
	littleEndian := bytes.HasPrefix(data, []byte{0xff, 0xfe})
	bigEndian := bytes.HasPrefix(data, []byte{0xfe, 0xff})
	if !littleEndian && !bigEndian || len(data) < 2 {
		return string(data)
	}
	encoded := data[2:]
	words := make([]uint16, len(encoded)/2)
	for i := range words {
		if littleEndian {
			words[i] = binary.LittleEndian.Uint16(encoded[i*2:])
		} else {
			words[i] = binary.BigEndian.Uint16(encoded[i*2:])
		}
	}
	return string(utf16.Decode(words))
}

func warnClineHookOwnershipConflict(warnings io.Writer, path string) {
	if warnings != nil {
		_, _ = fmt.Fprintf(warnings, "warning: preserved Cline hook ownership conflict: %s\n", path)
	}
}

func warnPreservedUserOwnedClineHook(warnings io.Writer, path string) {
	if warnings != nil {
		_, _ = fmt.Fprintf(warnings, "warning: preserved user-owned Cline hook entry: %s\n", path)
	}
}
