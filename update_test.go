package main

import (
	"errors"
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
