package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/gofrs/flock"
)

//go:embed opencode_plugin.mjs
var opencodePluginContent []byte

type opencodeReceipt struct {
	Version int      `json:"version"`
	Paths   []string `json:"paths"`
}

func opencodePluginPath(home string) string {
	root := os.Getenv("OPENCODE_CONFIG_DIR")
	if root == "" {
		root = os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		root = filepath.Join(root, "opencode")
	}
	return filepath.Join(root, "plugins", "ai-agent-telemetry", "index.mjs")
}

func opencodeReceiptPath(home string) string {
	return filepath.Join(filepath.Dir(managedCLIPath(home, runtime.GOOS)), ".ai-agent-telemetry-opencode.json")
}

func readOpenCodeReceipt(home string) (opencodeReceipt, error) {
	entry, err := readClineHookEntry(opencodeReceiptPath(home))
	if err != nil {
		return opencodeReceipt{}, err
	}
	defer func() { _ = entry.close() }()
	if entry.kind == clineEntryMissing {
		return opencodeReceipt{Version: 1}, nil
	}
	var receipt opencodeReceipt
	if entry.kind != clineEntryRegular || json.Unmarshal(entry.data, &receipt) != nil || receipt.Version != 1 {
		return receipt, errors.New("invalid OpenCode managed-location receipt; preserve CLI and repair the receipt")
	}
	for _, path := range receipt.Paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "index.mjs" || filepath.Base(filepath.Dir(path)) != "ai-agent-telemetry" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "plugins" {
			return receipt, errors.New("invalid OpenCode managed plugin path in receipt")
		}
	}
	return receipt, nil
}

func validateOpenCodeParents(path string) error {
	for parent, depth := filepath.Dir(path), 0; depth < 2; parent, depth = filepath.Dir(parent), depth+1 {
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("OpenCode plugin directory is not a regular directory and was preserved: %s", parent)
		}
	}
	return nil
}

func inspectOpenCodePlugin(path string) (hookState, string) {
	if err := validateOpenCodeParents(path); err != nil {
		return hookInvalid, err.Error()
	}
	entry, err := readClineHookEntry(path)
	if err != nil {
		return hookInvalid, err.Error()
	}
	defer func() { _ = entry.close() }()
	if entry.kind == clineEntryMissing {
		return hookMissing, ""
	}
	if entry.kind != clineEntryRegular || !bytes.Equal(entry.data, opencodePluginContent) {
		return hookInvalid, "modified, foreign, or symbolic OpenCode plugin was preserved: " + path
	}
	return hookInstalled, ""
}

func installOpenCodePlugin(home string) (bool, error) {
	path := opencodePluginPath(home)
	if !filepath.IsAbs(path) {
		return false, errors.New("OpenCode configuration directory must be absolute")
	}
	lock := flock.New(opencodeReceiptPath(home) + ".lock")
	if err := os.MkdirAll(filepath.Dir(opencodeReceiptPath(home)), 0o700); err != nil {
		return false, err
	}
	if err := lock.Lock(); err != nil {
		return false, err
	}
	defer func() { _ = lock.Unlock() }()
	receipt, err := readOpenCodeReceipt(home)
	if err != nil {
		return false, err
	}
	state, detail := inspectOpenCodePlugin(path)
	if state == hookInvalid {
		return false, errors.New(detail)
	}
	if !slices.Contains(receipt.Paths, path) {
		receipt.Paths = append(receipt.Paths, path)
		data, err := json.Marshal(receipt)
		if err != nil {
			return false, err
		}
		if err := writeFileAtomically(opencodeReceiptPath(home), data, 0o600); err != nil {
			return false, err
		}
	}
	if state == hookInstalled {
		return false, nil
	}
	if err := writeClineHookExclusively(path, opencodePluginContent, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func openCodeStatus(home string) hookStatus {
	selected := opencodePluginPath(home)
	status := hookStatus{Target: hookOpenCode, Path: selected, State: hookMissing}
	receipt, err := readOpenCodeReceipt(home)
	if err != nil {
		status.State = hookInvalid
		status.Detail = err.Error()
		return status
	}
	paths := append([]string{selected}, receipt.Paths...)
	for _, path := range paths {
		state, detail := inspectOpenCodePlugin(path)
		if state == hookInvalid {
			status.State = state
			status.Detail = detail
			return status
		}
		if path == selected {
			status.State = state
		}
	}
	if len(receipt.Paths) > 1 || (len(receipt.Paths) == 1 && receipt.Paths[0] != selected) {
		status.Detail = "recorded managed locations: " + strings.Join(receipt.Paths, ", ")
	}
	return status
}

func removeOpenCodePlugins(home string) (bool, error) {
	if _, err := os.Lstat(opencodeReceiptPath(home)); errors.Is(err, os.ErrNotExist) {
		state, detail := inspectOpenCodePlugin(opencodePluginPath(home))
		if state == hookMissing {
			return false, nil
		}
		if state == hookInvalid {
			entry, err := readClineHookEntry(opencodePluginPath(home))
			if err != nil {
				return false, err
			}
			owned := bytes.HasPrefix(entry.data, []byte("// Managed by ai-agent-telemetry."))
			_ = entry.close()
			if !owned {
				return false, nil
			}
			return false, errors.New(detail)
		}
	}
	if err := os.MkdirAll(filepath.Dir(opencodeReceiptPath(home)), 0o700); err != nil {
		return false, err
	}
	lock := flock.New(opencodeReceiptPath(home) + ".lock")
	if err := lock.Lock(); err != nil {
		return false, err
	}
	defer func() { _ = lock.Unlock() }()
	receipt, err := readOpenCodeReceipt(home)
	if err != nil {
		return false, err
	}
	paths := append(slices.Clone(receipt.Paths), opencodePluginPath(home))
	changed := false
	var errs []error
	for _, path := range slices.Compact(paths) {
		state, detail := inspectOpenCodePlugin(path)
		if state == hookMissing {
			continue
		}
		if state == hookInvalid {
			if !slices.Contains(receipt.Paths, path) {
				continue
			}
			errs = append(errs, errors.New(detail))
			continue
		}
		quarantine, err := quarantineClineHook(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		entry, err := readClineHookEntry(quarantine)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		matches := entry.kind == clineEntryRegular && bytes.Equal(entry.data, opencodePluginContent)
		closeErr := entry.close()
		if closeErr != nil {
			errs = append(errs, closeErr)
			continue
		}
		if !matches {
			errs = append(errs, fmt.Errorf("OpenCode plugin changed during removal: %s", path), restoreQuarantinedClineHook(path, quarantine))
			continue
		}
		if err := os.Remove(quarantine); err != nil {
			errs = append(errs, err)
			continue
		}
		changed = true
	}
	if err := errors.Join(errs...); err != nil {
		return changed, fmt.Errorf("OpenCode cleanup incomplete; managed CLI and telemetry data must be preserved: %w", err)
	}
	return changed, removeIfExists(opencodeReceiptPath(home))
}
