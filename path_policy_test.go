package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidatePathAllowGrammar(t *testing.T) {
	valid := []string{
		"*",
		"/work/repo",
		"/work/*/src",
		"/work/**",
		`C:\Users\Alice\**`,
		"c:/Users/*/repo",
		`\\server\share\projects\**`,
	}
	for _, pattern := range valid {
		t.Run("valid "+pattern, func(t *testing.T) {
			if err := validatePathAllow([]string{pattern}); err != nil {
				t.Fatalf("validatePathAllow(%q): %v", pattern, err)
			}
		})
	}

	invalid := []string{
		"",
		"relative/path",
		"!/work/**",
		"/work/[ab]",
		"/work/file?",
		"/work/**suffix",
		"/work/prefix**",
		"/work/./repo",
		"/work/../repo",
		`C:relative\path`,
		`\\server`,
		`\\server\[share]\repo`,
		`\\server\..\repo`,
		"/work/repo\n/other/**",
	}
	for _, pattern := range invalid {
		t.Run("invalid "+pattern, func(t *testing.T) {
			if err := validatePathAllow([]string{pattern}); err == nil {
				t.Fatalf("validatePathAllow(%q) succeeded", pattern)
			}
		})
	}
}

func TestPathPatternMatch(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		candidate string
		want      bool
	}{
		{name: "recursive root", pattern: "/work/**", candidate: "/work", want: true},
		{name: "recursive descendant", pattern: "/work/**", candidate: "/work/a/repo", want: true},
		{name: "segment boundary", pattern: "/work/**", candidate: "/workspace/repo", want: false},
		{name: "single segment", pattern: "/work/*/src", candidate: "/work/repo/src", want: true},
		{name: "single segment depth", pattern: "/work/*/src", candidate: "/work/team/repo/src", want: false},
		{name: "in-segment wildcard", pattern: "/work/repo-*", candidate: "/work/repo-api", want: true},
		{name: "double star zero segments", pattern: "/work/**/src", candidate: "/work/src", want: true},
		{name: "double star several segments", pattern: "/work/**/src", candidate: "/work/team/repo/src", want: true},
		{name: "POSIX case sensitive", pattern: "/Work/**", candidate: "/work/repo", want: false},
		{name: "drive case insensitive", pattern: `C:\Users\Alice\**`, candidate: `c:/users/ALICE/repo`, want: true},
		{name: "UNC case insensitive", pattern: `\\Server\Share\Projects\**`, candidate: `//server/share/projects/repo`, want: true},
		{name: "normalized separators", pattern: `C:\\Users\\Alice\\**\\`, candidate: `c:\users\alice\repo`, want: true},
		{name: "all paths", pattern: "*", candidate: "/work/repo", want: true},
		{name: "all paths needs a path", pattern: "*", candidate: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pathPatternMatch(tt.pattern, tt.candidate)
			if err != nil {
				t.Fatalf("pathPatternMatch(%q, %q): %v", tt.pattern, tt.candidate, err)
			}
			if got != tt.want {
				t.Fatalf("pathPatternMatch(%q, %q) = %v, want %v", tt.pattern, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestPathPatternMatchCompletesForAdversarialPatterns(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		candidate string
	}{
		{
			name:      "segment wildcards",
			pattern:   "/" + strings.Repeat("*a", 12) + "b",
			candidate: "/" + strings.Repeat("a", 32),
		},
		{
			name:      "recursive segment wildcards",
			pattern:   "/" + strings.Repeat("**/", 12) + "missing",
			candidate: "/" + strings.TrimSuffix(strings.Repeat("a/", 32), "/"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{})
			var got bool
			var err error
			go func() {
				got, err = pathPatternMatch(tt.pattern, tt.candidate)
				close(done)
			}()

			select {
			case <-done:
				if err != nil {
					t.Fatal(err)
				}
				if got {
					t.Fatal("non-matching path matched")
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("path match exceeded 500 ms")
			}
		})
	}
}

func TestPathAllowedCanonicalizesCandidatesAndRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	inside := filepath.Join(allowed, "team", "repo")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	pattern := filepath.ToSlash(allowed) + "/**"
	if !pathAllowed([]string{inside}, []string{pattern}) {
		t.Fatalf("existing descendant %q did not match %q", inside, pattern)
	}
	if pathAllowed([]string{filepath.Join(allowed, "missing")}, []string{"*"}) {
		t.Fatal("standalone * matched a path that cannot be canonicalized")
	}
	spaced := filepath.Join(root, "workspace ")
	if err := os.MkdirAll(spaced, 0o700); err != nil {
		t.Fatal(err)
	}
	if !pathAllowed([]string{spaced}, []string{filepath.ToSlash(spaced)}) {
		t.Fatal("policy matching changed a structured path with trailing whitespace")
	}

	link := filepath.Join(allowed, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if pathAllowed([]string{link}, []string{pattern}) {
		t.Fatal("symlink authorized a physical directory outside the allowed tree")
	}
}

func TestLoadPathAllowFile(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		got, err := loadPathAllowFile(filepath.Join(t.TempDir(), "missing"))
		if err != nil || got != nil {
			t.Fatalf("loadPathAllowFile(missing) = %#v, %v", got, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "path-allow")
		if err := os.WriteFile(path, []byte("# workspaces\n/work/**\n/work/trailing \n\nC:\\Users\\Alice\\**\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := loadPathAllowFile(path)
		want := []string{"/work/**", "/work/trailing ", `C:\Users\Alice\**`}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("loadPathAllowFile() = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "path-allow")
		if err := os.WriteFile(path, []byte("/work/[ab]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadPathAllowFile(path); err == nil {
			t.Fatal("loadPathAllowFile accepted an invalid pattern")
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		if _, err := loadPathAllowFile(t.TempDir()); err == nil {
			t.Fatal("loadPathAllowFile accepted a directory as a policy file")
		}
	})
}
