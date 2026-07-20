package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

const (
	hookReceiptName     = "hooks-uninstalled"
	hookReceiptContents = "version=1\nstate=uninstalled\n"
)

func stateBaseFrom(xdg, home string) string {
	if xdg != "" {
		return xdg
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state")
}

func hookReceiptPathFrom(xdg, home string) string {
	base := stateBaseFrom(xdg, home)
	if base == "" {
		return ""
	}
	return filepath.Join(base, pkgName, hookReceiptName)
}

func hookReceiptPath(home string) string {
	return hookReceiptPathFrom(os.Getenv("XDG_STATE_HOME"), home)
}

func validHookReceipt(home string) bool {
	path := hookReceiptPath(home)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && bytes.Equal(data, []byte(hookReceiptContents))
}

func writeHookReceipt(home string) error {
	path := hookReceiptPath(home)
	if path == "" {
		return errUserHomeUnavailable
	}
	return writeFileAtomically(path, []byte(hookReceiptContents), 0o600)
}

func invalidateHookReceipt(home string) error {
	path := hookReceiptPath(home)
	if path == "" {
		return errUserHomeUnavailable
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
