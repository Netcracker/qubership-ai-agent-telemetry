package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateHookFileCreatesMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(dir, "settings.json")
	changed, err := updateHookFile(path, func(root map[string]any) (bool, error) {
		root["enabled"] = true
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := []byte("{\n  \"enabled\": true\n}\n")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		assertPerm(t, dir, 0o700)
		assertPerm(t, path, 0o600)
	}
}

func TestUpdateHookFilePreservesLargeJSONNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{\"large\":9007199254740993123456789}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := updateHookFile(path, func(root map[string]any) (bool, error) {
		root["updated"] = true
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("9007199254740993123456789")) {
		t.Fatalf("large number changed: %s", got)
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(got))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	if root["large"] != json.Number("9007199254740993123456789") {
		t.Fatalf("large = %v", root["large"])
	}
}

func TestUpdateHookFilePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not portable to Windows")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := updateHookFile(path, func(root map[string]any) (bool, error) {
		root["updated"] = true
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPerm(t, path, 0o640)
}

func TestUpdateHookFileLeavesMalformedJSONUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := []byte("{not json\n")
	if err := os.WriteFile(path, want, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := updateHookFile(path, mergeClaudeHook); err == nil {
		t.Fatal("want malformed JSON error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("malformed file changed: %q", got)
	}
}

func TestUpdateHookFileLeavesNonObjectRootUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := []byte("[]\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := updateHookFile(path, mergeClaudeHook); err == nil {
		t.Fatal("want non-object root error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("non-object file changed: %q", got)
	}
}

func TestUpdateHookFileRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := []byte("{} {}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := updateHookFile(path, mergeClaudeHook); err == nil {
		t.Fatal("want trailing JSON error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file changed after trailing JSON error: %q", got)
	}
}

func TestUpdateHookFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{\"old\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := updateHookFile(path, func(root map[string]any) (bool, error) {
		delete(root, "old")
		root["new"] = true
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := []byte("{\n  \"new\": true\n}\n")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestUpdateHookFileDoesNotWriteAfterMergeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := []byte("{\"keep\":true}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("merge failed")
	_, err := updateHookFile(path, func(root map[string]any) (bool, error) {
		root["mutated"] = true
		return false, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file changed after merge error: %q", got)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
