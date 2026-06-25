# Generic Skill-Path Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote Codex's path-tail skill-name matcher to a shared, location/separator/case-agnostic core reused by the Cursor detector, fixing the missed `.agents/skills` Windows path.

**Architecture:** A new `skillpath.go` holds one regex (`skillPathRe`) and two helpers (`skillNameInPath`, `skillNamesInText`). Codex and Cursor keep their own per-harness transcript parsing but delegate the final "string → skill name" step to these helpers, deleting their local regexes.

**Tech Stack:** Go (flat `package main` at the repo root), standard `regexp` and `testing`.

## Global Constraints

- **English only** in every committed file — code, comments, identifiers, commit messages.
- **The shared regex is verbatim:** `(?i)(?:^|[\s"'=/\\])skills[\\/]+([^\\/\s"']+)[\\/]+SKILL\.md`. No location/folder anchor (global and plugin skills live under arbitrary parents). `[\\/]+` for `/`, single `\`, and doubled `\\`. `(?i)` on structural literals only; the capture preserves the skill name's case.
- **False-positive contract:** a spurious name is one extra event, never a hook failure. Do not add guards that could fail closed.
- **Codex behavior must not change** beyond the new case-insensitivity; existing Codex tests are the regression guard.
- Tests run with `go test .` from the repo root; build with `go build ./...`.
- Commit messages in Conventional Commits.

---

### Task 1: Shared skill-path matcher

**Files:**
- Create: `skillpath.go`
- Test: `skillpath_test.go`

**Interfaces:**
- Consumes: nothing (leaf module).
- Produces:
  - `skillPathRe *regexp.Regexp` — the single source of truth.
  - `skillNameInPath(s string) (string, bool)` — first match for one path string (Cursor `input.path`). Returns `("", false)` when no skill path is present.
  - `skillNamesInText(s string) []string` — every match in order, duplicates kept, for free text that may hold several paths (Codex shell commands). Returns `nil` when none.

- [ ] **Step 1: Write the failing test**

Create `skillpath_test.go`:

```go
package main

import "testing"

func TestSkillNameInPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means no match expected
	}{
		{"cursor apm windows", `C:\Users\u\repo\.agents\skills\provision-skills-telemetry\SKILL.md`, "provision-skills-telemetry"},
		{"cursor legacy unix", "/repo/.cursor/skills/foo/SKILL.md", "foo"},
		{"cursor legacy windows", `C:\repo\.cursor\skills\foo\SKILL.md`, "foo"},
		{"global plugin", `C:\Users\u\.claude\plugins\cache\p\6.0.3\skills\brainstorming\SKILL.md`, "brainstorming"},
		{"global user", "/home/u/.claude/skills/foo/SKILL.md", "foo"},
		{"case-insensitive fs keeps name case", "/x/skills/Foo/skill.md", "Foo"},
		{"my-skills boundary", "my-skills/foo/SKILL.md", ""},
		{"no skills segment", "/repo/src/main.go", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := skillNameInPath(c.in)
			if c.want == "" {
				if ok {
					t.Fatalf("got %q, want no match", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("got (%q, %v), want %q", got, ok, c.want)
			}
		})
	}
}

func TestSkillNamesInText(t *testing.T) {
	// Two real reads plus noise, including a Codex doubled-backslash path.
	text := `cat /Users/me/repo/.agents/skills/alpha/SKILL.md && rg foo && ls skills\\beta\\SKILL.md`
	got := skillNamesInText(text)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got %v, want [alpha beta]", got)
	}
	if n := skillNamesInText("cat README.md"); n != nil {
		t.Fatalf("got %v, want nil", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'TestSkillNameInPath|TestSkillNamesInText' -v`
Expected: FAIL — compile error `undefined: skillNameInPath` / `undefined: skillNamesInText`.

- [ ] **Step 3: Write minimal implementation**

Create `skillpath.go`:

```go
package main

import "regexp"

// skillPathRe matches the tail of a path to a skill body: skills/<name>/SKILL.md.
// It is the single source of truth shared by every transcript-scraped harness
// (Codex, Cursor); Claude gets a structured skill name and never parses a path.
//
// No location anchor. The tail is matched under any parent, because global and
// plugin skills live outside the project under arbitrary parents — e.g.
// ~/.claude/plugins/cache/<plugin>/<version>/skills/<name>/SKILL.md, where the
// segment before `skills` is a version number, not a dot-config dir. Any
// folder-based anchor would miss them.
//
// Separators repeat ([\\/]+) so the same pattern matches `/` (Unix), a single
// `\` (a Windows path in Cursor's input.path after JSON decode), and a doubled
// `\\` (a Windows path embedded in a JS string literal inside Codex's
// custom_tool_call input, where each backslash arrives doubled).
//
// The boundary before `skills` ((?:^|[\s"'=/\\])) requires a separator, quote,
// `=`, whitespace, or start-of-string, so `my-skills/...` does not match while a
// clean path and a path embedded in a shell command both do.
//
// (?i) lets the structural literals `skills` and `SKILL.md` match in any case,
// for the case-insensitive filesystems on Windows (NTFS) and macOS (APFS). The
// capture group still preserves the skill name's original case, since (?i)
// affects matching, not the captured substring.
var skillPathRe = regexp.MustCompile(`(?i)(?:^|[\s"'=/\\])skills[\\/]+([^\\/\s"']+)[\\/]+SKILL\.md`)

// skillNameInPath returns the skill name carried by a single filesystem path, or
// ("", false) when the path is not a skill body. Use it for a clean path such as
// a Cursor Read tool's input.path.
func skillNameInPath(s string) (string, bool) {
	if m := skillPathRe.FindStringSubmatch(s); m != nil {
		return m[1], true
	}
	return "", false
}

// skillNamesInText returns every skill name matched in a free-text string, in
// order, with duplicates kept. Use it for text that may embed several paths,
// such as a Codex shell command. It returns nil when there are no matches.
func skillNamesInText(s string) []string {
	matches := skillPathRe.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run 'TestSkillNameInPath|TestSkillNamesInText' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add skillpath.go skillpath_test.go
git commit -m "feat(detect): shared location/separator/case-agnostic skill-path matcher"
```

---

### Task 2: Route Codex through the shared matcher

**Files:**
- Modify: `transcript_codex.go` (delete `codexSkillReadRe`, drop the `regexp` import, call the shared helpers)
- Test: `transcript_codex_test.go` (unchanged — existing tests are the regression guard)

**Interfaces:**
- Consumes: `skillPathRe`, `skillNamesInText` from Task 1.
- Produces: no new exported names; `scanCodexRollout` / `codexTranscriptEvents` behavior is unchanged.

- [ ] **Step 1: Run the existing Codex tests to confirm the baseline is green**

Run: `go test . -run TestScanCodexRollout -v`
Expected: PASS (these become the regression guard for the refactor).

- [ ] **Step 2: Delete the local regex and its `regexp` import**

In `transcript_codex.go`, remove the `codexSkillReadRe` block (the doc comment plus the `var` on lines ~13-22):

```go
// codexSkillReadRe matches a shell command that opens a skill's SKILL.md. ...
var codexSkillReadRe = regexp.MustCompile(`(?:^|[\s"'=/\\])skills[\\/]+([^\\/\s"']+)[\\/]+SKILL\.md`)
```

Then remove the now-unused import. The import block becomes:

```go
import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)
```

- [ ] **Step 3: Call the shared helpers**

In `processCodexLine`, replace the regex loop:

```go
		for _, text := range texts {
			for _, m := range codexSkillReadRe.FindAllStringSubmatch(text, -1) {
				name := m[1]
				if !seen[name] {
					seen[name] = true
					out.skills = append(out.skills, name)
				}
			}
		}
```

with:

```go
		for _, text := range texts {
			for _, name := range skillNamesInText(text) {
				if !seen[name] {
					seen[name] = true
					out.skills = append(out.skills, name)
				}
			}
		}
```

In `codexFunctionCallTexts`, replace the fallback match:

```go
		if s, ok := v.(string); ok && s != "" && codexSkillReadRe.MatchString(s) {
```

with:

```go
		if s, ok := v.(string); ok && s != "" && skillPathRe.MatchString(s) {
```

- [ ] **Step 4: Run the full suite to verify no regression**

Run: `go test .`
Expected: PASS (Codex tests still green; build clean with `regexp` removed from this file).

- [ ] **Step 5: Commit**

```bash
git add transcript_codex.go
git commit -m "refactor(codex): use shared skill-path matcher"
```

---

### Task 3: Route Cursor through the shared matcher and cover the failing path

**Files:**
- Modify: `transcript_cursor.go` (delete `cursorSkillReadRe`, call `skillNameInPath`)
- Test: `transcript_cursor_test.go` (add the Windows `.agents` end-to-end case)

**Interfaces:**
- Consumes: `skillNameInPath` from Task 1.
- Produces: no new exported names; `scanCursorTranscript` now matches `.agents/skills`, backslash separators, and any-case literals. `cursorManualSkillRe` and the `<manually_attached_skills>` branch are unchanged.

- [ ] **Step 1: Write the failing test**

Add to `transcript_cursor_test.go`. It marshals the path through `encoding/json` so the backslashes are escaped on the way in and decoded to single backslashes by the scanner — the real Cursor-on-Windows pipeline. Add `"encoding/json"` to the file's import block.

```go
func TestScanCursorTranscriptWindowsAgentsPath(t *testing.T) {
	path := `C:\Users\denif\repo\.agents\skills\provision-skills-telemetry\SKILL.md`
	line, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "Read", "input": map[string]any{"path": path}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	skills, _ := scanCursorTranscript(strings.NewReader(string(line)+"\n"), 0)
	if len(skills) != 1 || skills[0] != "provision-skills-telemetry" {
		t.Fatalf("skills = %v, want [provision-skills-telemetry]", skills)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestScanCursorTranscriptWindowsAgentsPath -v`
Expected: FAIL — `skills = []` (current `cursorSkillReadRe` matches neither `.agents` nor backslashes).

- [ ] **Step 3: Replace the Cursor regex with the shared helper**

In `transcript_cursor.go`, delete the `cursorSkillReadRe` block (the doc comment plus the `var` on lines ~14-18):

```go
// cursorSkillReadRe matches the Read tool input.path of a skill body. ...
var cursorSkillReadRe = regexp.MustCompile(`(?:^|/)\.cursor/skills/([^/]+)/SKILL\.md`)
```

In `processCursorLine`, replace the `tool_use` match:

```go
			if m := cursorSkillReadRe.FindStringSubmatch(c.Input.Path); m != nil {
				add(m[1])
			}
```

with:

```go
			if name, ok := skillNameInPath(c.Input.Path); ok {
				add(name)
			}
```

Leave `cursorManualSkillRe` and the `"text"` branch untouched, so the `regexp` import stays.

- [ ] **Step 4: Run the full suite to verify it passes**

Run: `go test .`
Expected: PASS — the new Windows `.agents` test passes, and the existing Cursor tests (legacy `.cursor/` unix paths, manual-attach, offset, dedup) stay green.

- [ ] **Step 5: Commit**

```bash
git add transcript_cursor.go transcript_cursor_test.go
git commit -m "fix(cursor): detect .agents/skills and Windows paths via shared matcher"
```

---

### Task 4: Full build and verification

**Files:** none (verification only).

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 2: Run the full test suite and vet**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet warnings.

- [ ] **Step 3: Confirm no dangling references to the deleted regexes**

Run: `grep -rn 'cursorSkillReadRe\|codexSkillReadRe' .`
Expected: no matches.
