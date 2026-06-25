# Generic skill-name extraction from a SKILL.md path — 2026-06-23

Both transcript-scraped harnesses (Codex, Cursor) reduce to the same sub-problem: given a string
that contains a filesystem path to a skill body, recover the skill name. They solved it twice, and
the two solutions drifted. This spec promotes the robust one to a shared core and retires the
fragile one.

Claude is out of scope: it gets a structured `tool_input.skill` from a native hook and never parses
a path.

## The bug that triggered this

On a live Cursor 3.8.11 + APM session the hook fired, ingest ran (exit 0, transcript read to EOF),
yet zero events reached the outbox. The skill body *was* read — the transcript holds a `Read`
`tool_use` with:

```
…\.agents\skills\provision-skills-telemetry\SKILL.md
```

The Cursor detector missed it for two independent reasons:

```
cursorSkillReadRe = (?:^|/)\.cursor/skills/([^/]+)/SKILL\.md
```

1. **Location drift.** APM now installs skills under `.agents/skills/`, not `.cursor/skills/`. The
   2026-06-16 spec was written against Cursor 3.6.31, where they lived in `.cursor/skills/`.
2. **Separator.** The regex only accepts `/`. On Windows the path uses `\`, so even a correct
   `.cursor\skills\…` would not match.

Codex never had either bug, because its regex matches the path *tail* and is separator-agnostic.

## Decision: a shared extractor, per-harness parsing kept

Per-harness transcript parsing stays per-harness — different harnesses expose the path in different
places (Codex: shell-command text and `custom_tool_call` input; Cursor: a `Read` tool's
`input.path` plus the `<manually_attached_skills>` text block). What becomes shared is only the last
step: **turn a candidate string into a skill name.** The skill name is the one thing always visible,
regardless of which tool loaded the body, so detection anchors on the name.

This is deliberately *not* a unified "normalize-then-detect" engine. Three harnesses with disjoint
transcript formats do not justify a common event model; that is YAGNI. We share the 30-character
regex, nothing more.

### The shared rule

The matcher is Codex's current regex, promoted verbatim except for a case-insensitivity flag:

```
(?i)(?:^|[\s"'=/\\])skills[\\/]+([^\\/\s"']+)[\\/]+SKILL\.md
```

- **No location anchor.** It matches the `skills/<name>/SKILL.md` tail under any parent. This is
  required, not lax: global and plugin skills live outside the project under arbitrary parents —
  e.g. `~/.claude/plugins/cache/<plugin>/<version>/skills/<name>/SKILL.md`, where the segment before
  `skills` is a version number, not a dot-config dir. Any folder-based anchor would miss them.
- **Separator-agnostic, doubling-tolerant.** `[\\/]+` accepts `/` (Unix), `\` (Windows, Cursor
  `input.path` after JSON decode → single backslash), and `\\` (Codex `custom_tool_call`, where a
  Windows path embedded in a JS string literal arrives doubled).
- **Boundary before `skills`.** `(?:^|[\s"'=/\\])` requires a separator, quote, `=`, whitespace, or
  start-of-string, so `my-skills/…` does not match while both a clean path and a path embedded in a
  shell command do.
- **Case-insensitive structural literals.** `(?i)` lets `skills` / `SKILL.md` match in any case, for
  the case-insensitive filesystems on Windows (NTFS) and macOS (APFS) where a tool may record a
  non-canonical case. The capture group still preserves the skill name's original case, since `(?i)`
  affects matching, not the captured substring. On Linux the canonical case matches as before.

### False positives: the same contract Codex already accepts

Dropping the location anchor means a path like `node_modules/x/skills/y/SKILL.md` would match if the
agent ever read it. No path-only anchor can separate that from a real plugin skill — structurally
they are identical (`<arbitrary-parent>/skills/<name>/SKILL.md`); the only true discriminator, "was
this read *because* a skill activated," is not in the transcript. So we accept the existing Codex
contract: **a spurious name is one extra event, never a hook failure**, and the agent almost never
opens an unrelated `SKILL.md`. Cursor already failed *closed* (missed events); this trades a
near-zero false-positive risk for catching every real activation.

## Components

New file `skillpath.go`:

- `skillPathRe` — the regex above, the single source of truth.
- `skillNameInPath(s string) (string, bool)` — one match; for a clean path (Cursor `input.path`).
- `skillNamesInText(s string) []string` — all matches, in order; for free text that may hold several
  paths (Codex shell commands).

Changes:

- **`transcript_codex.go`** — delete the local `codexSkillReadRe`; call `skillNamesInText` /
  `skillPathRe.MatchString`. Behavior is unchanged (same pattern, plus the new `(?i)`), so existing
  Codex tests are the regression guard.
- **`transcript_cursor.go`** — delete `cursorSkillReadRe`; the `Read` `tool_use` branch calls
  `skillNameInPath(c.Input.Path)`. The `<manually_attached_skills>` / `Skill Name:` branch is
  untouched — it is a direct name signal and harness-specific, exactly the kind of per-harness tuning
  this design keeps.

## Tests (TDD — write first)

New `skillpath_test.go` table covering the cross-platform axes:

| Input | Expect |
|---|---|
| `…\.agents\skills\provision-skills-telemetry\SKILL.md` (Cursor APM, Windows) | `provision-skills-telemetry` |
| `/repo/.cursor/skills/foo/SKILL.md` (Cursor legacy, Unix) | `foo` |
| `C:\repo\.cursor\skills\foo\SKILL.md` (Cursor legacy, Windows) | `foo` |
| `C:\Users\u\.claude\plugins\cache\p\6.0.3\skills\brainstorming\SKILL.md` (global plugin) | `brainstorming` |
| `~/.claude/skills/foo/SKILL.md` (global user) | `foo` |
| `…/skills/Foo/skill.md` (case-insensitive FS) | `Foo` (capture keeps case) |
| `cat skills\\bar\\SKILL.md` (Codex doubled backslash) | `bar` |
| `my-skills/foo/SKILL.md` | no match (boundary) |
| `/repo/src/main.go` | no match (no skills segment) |

Plus per-harness integration coverage: a `transcript_cursor_test.go` case feeding the real Windows
`.agents` line end-to-end through `scanCursorTranscript`, and the existing Codex transcript tests
unchanged.

## Out of scope

- **Name normalization across harnesses.** A path yields a bare name (`brainstorming`) while native
  Claude yields the namespace form (`superpowers:brainstorming`). That gap predates this change and
  is a separate, all-harness decision.
- **A native Cursor/Codex skill-activation event.** If one ever exists it would replace scraping with
  a structured signal (as Claude has). None is exposed today, so transcript scraping is the floor.

## Limitations

- The transcript schema is harness-internal. Pin to the fields above; an unknown shape yields zero
  events, never a hook failure.
- `<manually_attached_skills>` remains text-shaped and the more fragile of the Cursor signals; it is
  unchanged here.
