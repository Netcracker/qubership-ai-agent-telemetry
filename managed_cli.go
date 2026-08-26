package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	installReceiptName          = ".ai-agent-telemetry-install.json"
	windowsBootstrapURL         = "https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1"
	posixPathBlock              = "# Added by ai-agent-telemetry installer\nexport PATH=\"$HOME/.local/bin:$PATH\""
	registryString       uint32 = 1
	registryExpandString uint32 = 2
)

type pathReceipt struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Profile string `json:"profile,omitempty"`
	Entry   string `json:"entry"`
}

type managedPathManager interface {
	Ensure() (pathReceipt, bool, error)
	Remove(pathReceipt, bool) error
}

type userPATHRegistry interface {
	Read() (string, uint32, error)
	Write(string, uint32) error
}

type windowsPathManager struct {
	entry    string
	registry userPATHRegistry
	notify   func() error
}

func newWindowsPathManager(entry string, registry userPATHRegistry, notify func() error) *windowsPathManager {
	return &windowsPathManager{entry: entry, registry: registry, notify: notify}
}

func (m *windowsPathManager) Ensure() (pathReceipt, bool, error) {
	value, valueType, err := m.registry.Read()
	if err != nil {
		return pathReceipt{}, false, err
	}
	if pathListContains(value, m.entry, ';', true) {
		return pathReceipt{}, false, nil
	}
	updated := m.entry
	if value != "" {
		updated = value + ";" + m.entry
	}
	if err := m.registry.Write(updated, valueType); err != nil {
		return pathReceipt{}, false, err
	}
	if err := m.notifyChange(value, valueType); err != nil {
		return pathReceipt{}, false, err
	}
	return pathReceipt{Version: 1, Kind: "windows-user-path", Entry: m.entry}, true, nil
}

func (m *windowsPathManager) Remove(receipt pathReceipt, receiptPresent bool) error {
	if !receiptPresent || receipt.Kind != "windows-user-path" || !strings.EqualFold(receipt.Entry, m.entry) {
		return nil
	}
	value, valueType, err := m.registry.Read()
	if err != nil {
		return err
	}
	entries := strings.Split(value, ";")
	owned := -1
	for index := len(entries) - 1; index >= 0; index-- {
		if strings.EqualFold(entries[index], receipt.Entry) {
			owned = index
			break
		}
	}
	if owned == -1 {
		return nil
	}
	entries = append(entries[:owned], entries[owned+1:]...)
	updated := strings.Join(entries, ";")
	if err := m.registry.Write(updated, valueType); err != nil {
		return err
	}
	return m.notifyChange(value, valueType)
}

func (m *windowsPathManager) notifyChange(previous string, previousType uint32) error {
	if m.notify == nil {
		return nil
	}
	if err := m.notify(); err != nil {
		rollbackErr := m.registry.Write(previous, previousType)
		notificationErr := fmt.Errorf("notify Windows applications about the PATH change: %w", err)
		if rollbackErr != nil {
			return errors.Join(notificationErr, fmt.Errorf("restore the previous user PATH after notification failure: %w", rollbackErr))
		}
		if rollbackNotifyErr := m.notify(); rollbackNotifyErr != nil {
			return errors.Join(notificationErr, fmt.Errorf("notify Windows applications about the restored PATH: %w", rollbackNotifyErr))
		}
		return notificationErr
	}
	return nil
}

type managedCLIConfig struct {
	Home         string
	GOOS         string
	Paths        managedPathManager
	Executable   func() (string, error)
	WriteReceipt func(string, pathReceipt) error
}

func newManagedCLIService(config managedCLIConfig) managedCLIService {
	if config.Executable == nil {
		config.Executable = os.Executable
	}
	if config.WriteReceipt == nil {
		config.WriteReceipt = writePathReceipt
	}
	target := managedCLIPath(config.Home, config.GOOS)
	receiptFile := filepath.Join(filepath.Dir(target), installReceiptName)

	return managedCLIService{
		Install: func(source string) operationResult {
			if err := validateManagedCLIHome(config.Home); err != nil {
				return managedCLIFailure("cannot install managed executable", err)
			}
			if !sameExecutablePath(source, target, config.GOOS) {
				if err := copyExecutableAtomically(source, target); err != nil {
					return managedCLIFailure("cannot install managed executable", err)
				}
			}
			receipt, changed, err := config.Paths.Ensure()
			if err != nil {
				return managedCLIFailure("cannot add managed directory to PATH", err)
			}
			if changed {
				if err := config.WriteReceipt(receiptFile, receipt); err != nil {
					rollbackErr := config.Paths.Remove(receipt, true)
					return managedCLIFailure("cannot persist PATH ownership receipt", errors.Join(err, rollbackErr))
				}
			}
			detail := "installed"
			if sameExecutablePath(source, target, config.GOOS) {
				detail = "unchanged"
			}
			return operationResult{Name: "managed-cli", State: operationOK, Detail: detail}
		},
		Remove: func() operationResult {
			if err := validateManagedCLIHome(config.Home); err != nil {
				return managedCLIFailure("cannot remove managed executable", err)
			}
			receipt, found, err := readPathReceipt(receiptFile)
			if err != nil {
				return managedCLIFailure("cannot read PATH ownership receipt", err)
			}
			if err := config.Paths.Remove(receipt, found); err != nil {
				return managedCLIFailure("cannot remove owned PATH entry", err)
			}
			if err := removeIfExists(target); err != nil {
				return managedCLIFailure("cannot remove managed executable", err)
			}
			if found {
				if err := removeIfExists(receiptFile); err != nil {
					return managedCLIFailure("cannot remove PATH ownership receipt", err)
				}
			}
			return operationResult{Name: "managed-cli", State: operationOK, Detail: "removed"}
		},
		Preflight: func(lifecycleOptions) error {
			return validateManagedCLIHome(config.Home)
		},
		PreflightRemove: func(opts lifecycleOptions) error {
			if config.GOOS != "windows" || opts.Action != actionUninstall {
				return nil
			}
			current, err := config.Executable()
			if err != nil {
				return fmt.Errorf("cannot resolve the running executable: %w", err)
			}
			if sameExecutablePath(current, target, config.GOOS) {
				return fmt.Errorf("the installed Windows executable cannot remove itself; run %s", windowsBootstrapUninstallCommand(opts))
			}
			return nil
		},
	}
}

func validateManagedCLIHome(home string) error {
	if strings.TrimSpace(home) == "" || !filepath.IsAbs(home) {
		return fmt.Errorf("managed CLI home must be a non-empty absolute path")
	}
	return nil
}

func windowsBootstrapUninstallCommand(opts lifecycleOptions) string {
	arguments := []string{"uninstall"}
	if opts.Purge {
		arguments = append(arguments, "--purge")
	}
	bootstrap := "& ([scriptblock]::Create((Invoke-RestMethod '" + windowsBootstrapURL + "'))) " +
		strings.Join(arguments, " ")
	return "powershell.exe -NoProfile -Command \"" + bootstrap + "\""
}

func defaultManagedCLIService(home string) managedCLIService {
	return newManagedCLIService(platformManagedCLIConfig(home))
}

func managedCLIPath(home, goos string) string {
	name := "ai-agent-telemetry"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".local", "bin", name)
}

func copyExecutableAtomically(source, target string) (returnErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".ai-agent-telemetry.tmp-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		_ = os.Remove(temporaryName)
	}()
	if _, err := io.Copy(temporary, input); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	return os.Rename(temporaryName, target)
}

func writePathReceipt(path string, receipt pathReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomically(path, data, 0o600)
}

func readPathReceipt(path string) (pathReceipt, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return pathReceipt{}, false, nil
	}
	if err != nil {
		return pathReceipt{}, false, err
	}
	var receipt pathReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return pathReceipt{}, false, err
	}
	if receipt.Version != 1 || receipt.Kind == "" || receipt.Entry == "" {
		return pathReceipt{}, false, fmt.Errorf("unsupported or incomplete PATH ownership receipt")
	}
	return receipt, true, nil
}

func sameExecutablePath(left, right, goos string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if goos == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func managedCLIFailure(detail string, err error) operationResult {
	return operationResult{Name: "managed-cli", State: operationFailed, Detail: detail + ": " + err.Error(), Err: err}
}

func pathListContains(value, entry string, separator rune, foldCase bool) bool {
	for _, candidate := range strings.Split(value, string(separator)) {
		if candidate == entry || (foldCase && strings.EqualFold(candidate, entry)) {
			return true
		}
	}
	return false
}
