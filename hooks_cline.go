package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if runtime.GOOS == "windows" || info.Mode().Perm() == wantMode {
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
	if runtime.GOOS != "windows" && info.Mode().Perm() != clineHookMode(goos) {
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
	if !info.Mode().IsRegular() {
		warnPreservedClineHook(warnings, path)
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(data, clineHookContent(goos)) && !bytes.Equal(data, clinePreviousHookContent(goos)) {
		warnPreservedClineHook(warnings, path)
		if clineHookInvokesManagedCLI(data) {
			return false, errors.New("hook cleanup is incomplete: modified hook retains the managed CLI invocation")
		}
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func clineHookInvokesManagedCLI(data []byte) bool {
	tokens := clineHookTokens(data)
	for i := range tokens {
		if i > 0 && !clineCommandBoundary(tokens[i-1]) {
			continue
		}
		if clineInvocationFields(tokens[i:]) {
			return true
		}
	}
	return false
}

func clineHookTokens(data []byte) []string {
	input := strings.NewReplacer("\\\r\n", " ", "\\\n", " ", "`\r\n", " ", "`\n", " ").Replace(string(data))
	tokens := make([]string, 0, 16)
	var word strings.Builder
	var quote byte
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, word.String())
			word.Reset()
		}
	}
	boundary := func(token string) {
		flush()
		if len(tokens) == 0 || tokens[len(tokens)-1] != token {
			tokens = append(tokens, token)
		}
	}

	for i := 0; i < len(input); i++ {
		current := input[i]
		if quote != 0 {
			if current == quote {
				quote = 0
			} else if quote == '"' && (current == '\\' || current == '`') && i+1 < len(input) {
				i++
				word.WriteByte(input[i])
			} else {
				word.WriteByte(current)
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '#':
			if word.Len() > 0 {
				word.WriteByte(current)
				continue
			}
			for i < len(input) && input[i] != '\n' {
				i++
			}
			boundary(";")
		case '\r', '\n':
			boundary(";")
		case ' ', '\t':
			flush()
		case ';', '{', '}', '(', ')':
			boundary(string(current))
		case '&', '|':
			operator := string(current)
			if i+1 < len(input) && input[i+1] == current {
				operator += string(current)
				i++
			}
			boundary(operator)
		case '\\', '`':
			if i+1 < len(input) {
				i++
				word.WriteByte(input[i])
			} else {
				word.WriteByte(current)
			}
		default:
			word.WriteByte(current)
		}
	}
	flush()
	return tokens
}

func clineCommandBoundary(token string) bool {
	switch token {
	case ";", "&&", "||", "|", "&", "{", "}", "(", ")":
		return true
	default:
		return false
	}
}

func clineInvocationFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for len(fields) > 0 && (fields[0] == "if" || fields[0] == "then" || fields[0] == "do" || fields[0] == "!") {
		fields = fields[1:]
	}
	for len(fields) > 0 && clineShellAssignment(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "&" {
		fields = fields[1:]
	} else if strings.HasPrefix(fields[0], "&") {
		fields[0] = strings.TrimPrefix(fields[0], "&")
	}
	for len(fields) > 0 {
		token := strings.Trim(fields[0], "'\"")
		if token == "command" || token == "exec" || token == "nohup" {
			fields = fields[1:]
			continue
		}
		if token == "env" {
			fields = fields[1:]
			for len(fields) > 0 && (strings.HasPrefix(fields[0], "-") || strings.Contains(fields[0], "=")) {
				fields = fields[1:]
			}
			continue
		}
		break
	}

	if len(fields) < 3 {
		return false
	}
	binary := strings.Trim(fields[0], "'\"")
	if binary != "ai-agent-telemetry" && binary != "ai-agent-telemetry.exe" || fields[1] != "ingest" {
		return false
	}
	for i := 2; i < len(fields) && !clineCommandBoundary(fields[i]); i++ {
		if fields[i] == "--agent=cline" {
			return true
		}
		if fields[i] == "--agent" && i+1 < len(fields) && strings.Trim(fields[i+1], "'\"") == "cline" {
			return true
		}
	}
	return false
}

func clineShellAssignment(token string) bool {
	equals := strings.IndexByte(token, '=')
	if equals < 1 {
		return false
	}
	for i, current := range token[:equals] {
		if (current < 'A' || current > 'Z') && (current < 'a' || current > 'z') && current != '_' &&
			(i == 0 || current < '0' || current > '9') {
			return false
		}
	}
	return true
}

func warnPreservedClineHook(warnings io.Writer, path string) {
	if warnings != nil {
		_, _ = fmt.Fprintf(warnings, "warning: preserved modified Cline hook: %s\n", path)
	}
}
