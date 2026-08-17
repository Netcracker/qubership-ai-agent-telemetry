package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRepositoryScopeUpdateDoesNotClaimCustomizedLegacyLookingFile(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(configDir, repoAllowFileName)
	custom := []byte("# Keep collection on GitHub only.\n" + legacyDefaultRepoAllow + "\n")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	service := newRepoScopeUpdateService(
		func() string { return configDir },
		func() string { return "" },
		nil,
		io.Discard,
	)
	if err := service.Prepare(lifecycleOptions{Action: actionUpdate, RepoScopeChange: repoScopeChangeAccept}); err != nil {
		t.Fatal(err)
	}
	if result, ok := service.Apply(); ok {
		t.Fatalf("apply result = %#v, want customized file ignored", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, custom) {
		t.Fatalf("repo-allow bytes = %q, want customized bytes preserved", got)
	}
}

func TestRepositoryScopeUpdatePreservesFileChangedAfterPreflight(t *testing.T) {
	configDir := t.TempDir()
	if err := writeRepoAllowFile(configDir, legacyDefaultRepoAllow); err != nil {
		t.Fatal(err)
	}
	service := newRepoScopeUpdateService(
		func() string { return configDir },
		func() string { return "" },
		nil,
		io.Discard,
	)
	if err := service.Prepare(lifecycleOptions{Action: actionUpdate, RepoScopeChange: repoScopeChangeAccept}); err != nil {
		t.Fatal(err)
	}
	if err := writeRepoAllowFile(configDir, "github.com/Qubership/*"); err != nil {
		t.Fatal(err)
	}
	result, ok := service.Apply()
	if !ok || result.State != operationSkipped {
		t.Fatalf("apply result = %#v, %t, want skipped", result, ok)
	}
	got := loadRepoAllowFile(filepath.Join(configDir, repoAllowFileName))
	if !reflect.DeepEqual(got, []string{"github.com/Qubership/*"}) {
		t.Fatalf("repo allow = %v, want concurrent custom scope preserved", got)
	}
}

func TestRepositoryScopeUpdatePreservesSameSemanticByteChangeAfterPreflight(t *testing.T) {
	configDir := t.TempDir()
	if err := writeRepoAllowFile(configDir, legacyDefaultRepoAllow); err != nil {
		t.Fatal(err)
	}
	service := newRepoScopeUpdateService(
		func() string { return configDir },
		func() string { return "" },
		nil,
		io.Discard,
	)
	if err := service.Prepare(lifecycleOptions{Action: actionUpdate, RepoScopeChange: repoScopeChangeAccept}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, repoAllowFileName)
	changed := []byte(legacyDefaultRepoAllow + "\n\n")
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	result, ok := service.Apply()
	if !ok || result.State != operationSkipped {
		t.Fatalf("apply result = %#v, %t, want skipped", result, ok)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, changed) {
		t.Fatalf("repo-allow bytes = %q, want same-semantic edit preserved", got)
	}
}
