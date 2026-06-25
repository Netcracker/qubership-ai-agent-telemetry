package main

import (
	"path/filepath"
	"testing"
)

// isolateConfigCache points the config and cache resolvers at throwaway temp
// dirs so a test that exercises Flush/ingest (which mints a machine-id via
// resolveMachineID) never writes into the developer's real ~/.config or
// ~/.cache. $XDG_CONFIG_HOME/$XDG_CACHE_HOME win in configBase/cacheBase on
// every OS, so this isolates uniformly — unlike setting only HOME, which
// os.UserHomeDir() ignores on Windows (it reads %USERPROFILE%).
func isolateConfigCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// The config and cache directories must resolve to one uniform, XDG-style
// location on every OS — the same philosophy the binary uses for ~/.local/bin —
// so a packaged harness (Claude Desktop on Windows, whose %AppData% is
// virtualized by MSIX) and a plain shell resolve the SAME path.

func TestConfigBaseFromXDGWins(t *testing.T) {
	if got := configBaseFrom("/x/cfg", "/home/u"); got != "/x/cfg" {
		t.Fatalf("got %q, want the XDG_CONFIG_HOME value", got)
	}
}

func TestConfigBaseFromHomeFallback(t *testing.T) {
	want := filepath.Join("/home/u", ".config")
	if got := configBaseFrom("", "/home/u"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestConfigBaseFromNoHomeIsEmpty(t *testing.T) {
	if got := configBaseFrom("", ""); got != "" {
		t.Fatalf("got %q, want empty when no XDG dir and no home", got)
	}
}

func TestCacheBaseFromXDGWins(t *testing.T) {
	if got := cacheBaseFrom("/x/cache", "/home/u"); got != "/x/cache" {
		t.Fatalf("got %q, want the XDG_CACHE_HOME value", got)
	}
}

func TestCacheBaseFromHomeFallback(t *testing.T) {
	want := filepath.Join("/home/u", ".cache")
	if got := cacheBaseFrom("", "/home/u"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCacheBaseFromNoHomeIsEmpty(t *testing.T) {
	if got := cacheBaseFrom("", ""); got != "" {
		t.Fatalf("got %q, want empty when no XDG dir and no home", got)
	}
}

// pkgConfigDir must honour XDG_CONFIG_HOME on every OS, proving it no longer
// routes through os.UserConfigDir() (which is %AppData% on Windows, the path
// MSIX virtualizes).
func TestPkgConfigDirHonorsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	want := filepath.Join(dir, pkgName)
	if got := pkgConfigDir(); got != want {
		t.Fatalf("got %q, want %q (must follow XDG_CONFIG_HOME uniformly)", got, want)
	}
}
