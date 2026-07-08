package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.0", "0.7.0", -1},
		{"v0.7.0", "v0.6.0", 1},
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"0.6.0-dev", "0.6.0", 0}, // pre-release suffix ignored
		{"0.6.0-dev", "0.5.3", 1}, // dev build ahead of an older release
		{"0.10.0", "0.9.9", 1},    // numeric, not lexical
		{"1.0", "1.0.0", 0},       // missing patch counts as 0
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestGatherUpdateCheckAvailable(t *testing.T) {
	r := gatherUpdateCheck("0.6.0", func() (string, error) { return "v0.7.0", nil })
	if !r.Available || r.Latest != "v0.7.0" {
		t.Fatalf("got %+v, want available with latest v0.7.0", r)
	}
	out := formatUpdateCheck(r)
	if !strings.Contains(out, "update_available: yes") {
		t.Fatalf("output missing yes verdict:\n%s", out)
	}
}

func TestGatherUpdateCheckUpToDate(t *testing.T) {
	r := gatherUpdateCheck("0.7.0", func() (string, error) { return "v0.7.0", nil })
	if r.Available {
		t.Fatalf("got available, want up to date: %+v", r)
	}
	if !strings.Contains(formatUpdateCheck(r), "update_available: no") {
		t.Fatal("output missing no verdict")
	}
}

func TestGatherUpdateCheckFetchErrorIsUnknown(t *testing.T) {
	r := gatherUpdateCheck("0.6.0", func() (string, error) { return "", errors.New("offline") })
	if r.Available {
		t.Fatal("a failed check must not report an update available")
	}
	out := formatUpdateCheck(r)
	if !strings.Contains(out, "update_available: unknown") || !strings.Contains(out, "error: offline") {
		t.Fatalf("output should surface unknown + error:\n%s", out)
	}
}

func TestReleaseAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "ai-agent-telemetry-linux-amd64"},
		{"darwin", "arm64", "ai-agent-telemetry-darwin-arm64"},
		{"windows", "amd64", "ai-agent-telemetry-windows-amd64.exe"},
	}
	for _, c := range cases {
		got, err := releaseAssetName(c.goos, c.goarch)
		if err != nil {
			t.Fatalf("releaseAssetName(%q,%q): %v", c.goos, c.goarch, err)
		}
		if got != c.want {
			t.Fatalf("releaseAssetName(%q,%q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestReleaseAssetNameRejectsUnsupported(t *testing.T) {
	if _, err := releaseAssetName("plan9", "amd64"); err == nil {
		t.Fatal("want unsupported OS error")
	}
	if _, err := releaseAssetName("linux", "386"); err == nil {
		t.Fatal("want unsupported arch error")
	}
}

func TestChecksumForAsset(t *testing.T) {
	sums := "abcd  ai-agent-telemetry-linux-amd64\n1234  other\n"
	got, err := checksumForAsset(sums, "ai-agent-telemetry-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcd" {
		t.Fatalf("checksum = %q, want abcd", got)
	}
}

func TestChecksumForAssetMissing(t *testing.T) {
	if _, err := checksumForAsset("abcd  other\n", "missing"); err == nil {
		t.Fatal("want missing checksum error")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSHA256(path, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Fatalf("verifyFileSHA256: %v", err)
	}
	if err := verifyFileSHA256(path, "bad"); err == nil {
		t.Fatal("want checksum mismatch")
	}
}
