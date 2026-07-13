package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type hookMergeFunc func(map[string]any) (bool, error)

func updateHookFile(path string, merge hookMergeFunc) (bool, error) {
	writePath, err := resolveHookWritePath(path)
	if err != nil {
		return false, err
	}
	root := map[string]any{}
	mode := os.FileMode(0o600)
	if data, err := os.ReadFile(writePath); err == nil {
		info, statErr := os.Stat(writePath)
		if statErr != nil {
			return false, statErr
		}
		mode = info.Mode().Perm()

		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
		var ok bool
		root, ok = decoded.(map[string]any)
		if !ok {
			return false, fmt.Errorf("parse %s: root must be an object", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	changed, err := merge(root)
	if err != nil || !changed {
		return changed, err
	}
	return true, writeJSONAtomically(writePath, root, mode)
}

func resolveHookWritePath(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve symlink %s: %w", path, err)
	}
	return resolved, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSONAtomically(path string, root map[string]any, mode os.FileMode) error {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomically(path, data, mode)
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ai-agent-telemetry-hooks-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
