# CLI refactor: drop the marker, consolidate files, rename off "sender"

Date: 2026-06-17
Status: approved (brainstorming)

## Summary

Three coupled changes to the Go CLI in `sender/`, done as one effort in a fixed order:

1. **Drop the `[skill-called]` response marker.** It is retired terminology in the docs, but the
   code still implements it. Removing it makes the session transcript the sole skill-detection
   signal for Codex and Cursor, which collapses the marker adapters, the merge step, and the
   per-agent branching in `ingest`.
2. **Consolidate the 17 source files into 8.** Many files are 24–75 lines; the split is too
   atomic. Group by domain, staying in a flat `package main`.
3. **Rename off "sender".** Move `sender/` to `cli/`, rename the `service.name` resource
   attribute, rename the `Spool` type to `Outbox`, and drop "sender" from Go comments.

The order matters: dropping the marker is what unlocks the file consolidation, and renaming last
avoids churning files that the first two steps already rewrite.

## Decisions

Settled during brainstorming:

| Decision | Choice |
| --- | --- |
| Directory rename | `sender/` → `cli/`; the Go module stays rooted in the directory |
| `service.name` | `qubership-skills-telemetry-sender` → `qubership-skills-telemetry` (backend-visible) |
| `Source` field | Remove the field and the `skill.source` log attribute (dead after the marker) |
| `Spool` type | Rename to `Outbox`; file `outbox.go`; on-disk dir `spool` → `outbox` |
| Scope | One spec and plan; implement in three sequential steps |
| File layout | 8 source files; test files mirror the source names |

The on-disk config dir name `qubership-skills-telemetry` (the `pkgName` constant) does **not**
change — it is the install path, and renaming it would orphan every existing install's config,
machine id, token, and CA. Only the `-sender` suffix on `service.name` and the `spool`
sub-directory move.

## Step 1 — Drop the `[skill-called]` marker

### What the marker is today

Codex and Cursor each have two detection signals. The marker is a line the skill emits into the
agent's response text (`[skill-called] skill=<name> source=<source>`), matched by `markerRe`. The
transcript parse is the second signal: a `SKILL.md` read in the session rollout (Codex) or a
`Read` of `.cursor/skills/.../SKILL.md` (Cursor). Claude Code has no marker — it uses a native
`PreToolUse` hook.

`ingest` runs the marker adapter first, then merges the transcript events into it with
`mergeBySkill`, which keeps the marker's richer `source` and lets the transcript fill any gaps.

### After removal

The transcript becomes the only signal for Codex and Cursor. The cascade:

- Delete `markerRe`, the marker branches of `codexAdapter` and `cursorAdapter`.
- Codex and Cursor now route straight to their transcript parsers
  (`codexTranscriptEventsAuto`, `cursorTranscriptEventsAuto`). The named "adapter" for each
  becomes the transcript parser.
- Delete `mergeBySkill` — one signal each, nothing to merge.
- Delete the `if agent == "codex"` / `if agent == "cursor"` branches in `ingest`.
- Remove the `Source` field from `SkillEvent` and the `skill.source` attribute from `flush.go`.
- Stop emitting the marker from the `agent-packages/adr-authoring` fixture (its `SKILL.md` and
  `README.md`).
- Update the affected tests: `adapter_test.go`, `ingest_test.go`, `event_test.go`.

Do **not** touch `docs/superpowers/` — those are dated archive snapshots and stay as written.

### The other "marker" — keep it, rename it

`main.go` has a second, unrelated use of the word: `markerName` / `touchMarker` is the
flush-throttle timestamp file, not the response marker. It stays. Rename it to remove the
collision — `flushStampName` / `touchFlushStamp` (or similar) — so the two concepts never read as
one.

### Verification

`go test ./...` in the CLI directory passes. A Codex and a Cursor run still produce skill events
end to end (transcript path), confirmed against the existing transcript test fixtures.

## Step 2 — Consolidate to 8 files

Flat `package main`. No sub-packages — that would deepen the fragmentation the refactor is meant
to fix.

| File | Absorbs | ~lines |
| --- | --- | --- |
| `main.go` | entry, `run`, `ingest`, flush throttle | ~250 |
| `detect.go` | `claudeAdapter` + the dispatch table (one detector per harness) | ~80 |
| `transcript_codex.go` | Codex rollout parser (unchanged) | ~150 |
| `transcript_cursor.go` | Cursor transcript parser (unchanged) | ~150 |
| `config.go` | `endpoint` + `envfile` + `token` + `ca` + `paths` + `machine` | ~230 |
| `outbox.go` | `event` + `outbox` (was `spool`) + `offset` | ~200 |
| `flush.go` | OTLP export (unchanged) | ~140 |
| `commands.go` | `provision` + `selftest` + `status` | ~200 |

Grouping rationale:

- `config.go` — everything that resolves runtime configuration: endpoint, env file, token, CA
  cert, paths, machine id. These are the smallest files and the most tightly related.
- `outbox.go` — all local on-disk state: the event type, the disk-backed queue, and the
  transcript read-offset store.
- `commands.go` — the CLI subcommand handlers.
- `detect.go` plus the two `transcript_*.go` files — detection. The transcript parsers are already
  ~150 lines each; merging them would produce a ~450-line file, so they stay separate.

Test files mirror the new source names: `config_test.go`, `outbox_test.go`, `commands_test.go`,
`detect_test.go`, etc. Tests stay in `package main`; behavior is unchanged, so this is a move, not
a rewrite.

### Verification

`go test ./...` passes with the same set of test cases as before the move. `go vet ./...` is clean.

## Step 3 — Rename off "sender"

- **Directory.** `git mv sender cli`. Update `.github/workflows/release.yml`: the four
  `working-directory: sender`, `go-version-file: sender/go.mod`, and
  `cache-dependency-path: sender/go.sum` references, plus the header comment. Update `apm.yml` and
  any build path that points at `sender/`.
- **`service.name`.** `qubership-skills-telemetry-sender` → `qubership-skills-telemetry` in
  `flush.go` (two occurrences). This is backend-visible: Grafana dashboards key on it and need the
  new value. Call this out in the PR.
- **`Spool` → `Outbox`.** Rename the type, `DefaultSpool`, `s.Dir`, and the on-disk `spool`
  sub-directory to `outbox` (in `outbox.go` and `paths.go`/`config.go`). Existing unsent events in
  the old `spool` dir are orphaned — acceptable, since they are transient best-effort telemetry.
- **Comments.** Drop "sender" from Go comments where it names the component; use "the CLI" or
  "skills-telemetry CLI".

The Go module is already named `skills-telemetry`, so no `go.mod` module rename is needed.

### Verification

`go build ./...` and `go test ./...` pass from the new `cli/` directory. The release workflow's
build step is re-pointed and its YAML still parses. A local `apm install` / `apm compile` still
finds and builds the binary.

## Out of scope

- The OpenCode adapter, token auth, and offset-file garbage collection — separate open work.
- Any change to the OTLP wire format beyond dropping the `skill.source` attribute.
- Renaming `pkgName` / the config directory.

## Risks

- **`service.name` change breaks dashboards.** Mitigation: flagged in the PR; the backend owner
  updates the Grafana key. Low blast radius — solo project, one backend.
- **Orphaned `spool` files on the `Outbox` rename.** Telemetry is best-effort; a one-time loss of
  unsent events on upgrade is acceptable.
- **Losing the marker's `source` data.** The docs already retired the marker, and the transcript
  signal carries no source, so this is an accepted, already-decided trade-off.
