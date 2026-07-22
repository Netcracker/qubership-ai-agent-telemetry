//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type posixPathManager struct {
	profile        string
	managedDir     string
	pathValue      string
	legacyProfiles []string
}

func newPosixPathManager(profile, managedDir, pathValue string) *posixPathManager {
	return &posixPathManager{profile: profile, managedDir: managedDir, pathValue: pathValue, legacyProfiles: []string{profile}}
}

func (m *posixPathManager) Ensure() (pathReceipt, bool, error) {
	if pathListContains(m.pathValue, m.managedDir, ':', false) {
		return pathReceipt{}, false, nil
	}
	profile, err := resolvePOSIXProfile(m.profile)
	if err != nil {
		return pathReceipt{}, false, err
	}
	receipt := pathReceipt{Version: 1, Kind: "posix-profile", Profile: profile, Entry: posixPathBlock}
	data, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return pathReceipt{}, false, err
	}
	mode := os.FileMode(0o600)
	if err == nil {
		info, statErr := os.Stat(profile)
		if statErr != nil {
			return pathReceipt{}, false, statErr
		}
		mode = info.Mode().Perm()
	}
	if containsStandalonePosixPathBlock(string(data)) {
		return pathReceipt{}, false, nil
	}
	updated := append([]byte(nil), data...)
	if len(updated) > 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, '\n')
	}
	if len(updated) > 0 {
		updated = append(updated, '\n')
	}
	updated = append(updated, posixPathBlock...)
	updated = append(updated, '\n')
	if err := writeFileAtomically(profile, updated, mode); err != nil {
		return pathReceipt{}, false, err
	}
	return receipt, true, nil
}

func (m *posixPathManager) Remove(receipt pathReceipt, receiptPresent bool) error {
	profiles := append([]string(nil), m.legacyProfiles...)
	if receiptPresent {
		if receipt.Kind != "posix-profile" || receipt.Entry != posixPathBlock ||
			receipt.Profile == "" || !filepath.IsAbs(receipt.Profile) || filepath.Clean(receipt.Profile) != receipt.Profile {
			return fmt.Errorf("PATH ownership receipt does not match a managed POSIX profile; preserve all profiles and remove the receipt manually")
		}
		profiles = []string{receipt.Profile}
	}
	seen := make(map[string]bool)
	for _, profile := range profiles {
		if profile == "" || seen[profile] {
			continue
		}
		seen[profile] = true
		if err := removeExactPosixPathBlock(profile, receiptPresent); err != nil {
			return err
		}
	}
	return nil
}

func resolvePOSIXProfile(profile string) (string, error) {
	absolute, err := filepath.Abs(profile)
	if err != nil {
		return "", fmt.Errorf("cannot resolve shell profile %s: %w", profile, err)
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return filepath.Clean(absolute), nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot inspect shell profile %s: %w", profile, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return "", fmt.Errorf("shell profile symlink %s does not resolve to a safe file: %w", profile, err)
		}
		absolute = resolved
		info, err = os.Stat(absolute)
		if err != nil {
			return "", fmt.Errorf("cannot inspect shell profile target %s: %w", absolute, err)
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("shell profile %s does not resolve to a regular file", profile)
	}
	return filepath.Clean(absolute), nil
}

func removeExactPosixPathBlock(profile string, literal bool) error {
	resolved := profile
	var err error
	if literal {
		info, lstatErr := os.Lstat(resolved)
		if os.IsNotExist(lstatErr) {
			return nil
		}
		if lstatErr != nil {
			return fmt.Errorf("cannot inspect recorded shell profile target %s: %w", resolved, lstatErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("recorded shell profile target %s is no longer a regular file", resolved)
		}
	} else {
		resolved, err = resolvePOSIXProfile(profile)
		if err != nil {
			return err
		}
	}
	data, err := os.ReadFile(resolved)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	original := string(data)
	updated, changed := removeStandalonePosixPathBlocks(original)
	if !changed {
		return nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	return writeFileAtomically(resolved, []byte(updated), info.Mode().Perm())
}

func containsStandalonePosixPathBlock(content string) bool {
	_, changed := removeStandalonePosixPathBlocks(content)
	return changed
}

func removeStandalonePosixPathBlocks(content string) (string, bool) {
	var output strings.Builder
	cursor := 0
	searchFrom := 0
	changed := false
	for searchFrom <= len(content) {
		relative := strings.Index(content[searchFrom:], posixPathBlock)
		if relative == -1 {
			break
		}
		start := searchFrom + relative
		end := start + len(posixPathBlock)
		startsOnLine := start == 0 || content[start-1] == '\n'
		endsOnLine := end == len(content) || content[end] == '\n'
		if !startsOnLine || !endsOnLine {
			searchFrom = start + 1
			continue
		}
		output.WriteString(content[cursor:start])
		if end < len(content) {
			end++
		}
		cursor = end
		searchFrom = end
		changed = true
	}
	if !changed {
		return content, false
	}
	output.WriteString(content[cursor:])
	return output.String(), true
}

func platformManagedCLIConfig(home string) managedCLIConfig {
	profile := filepath.Join(home, ".profile")
	shell := os.Getenv("SHELL")
	if strings.HasSuffix(shell, "zsh") {
		profile = filepath.Join(home, ".zshrc")
	} else if strings.HasSuffix(shell, "bash") {
		profile = filepath.Join(home, ".bashrc")
	}
	manager := newPosixPathManager(profile, filepath.Join(home, ".local", "bin"), os.Getenv("PATH"))
	manager.legacyProfiles = []string{
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
	}
	return managedCLIConfig{Home: home, GOOS: "linux", Paths: manager}
}
