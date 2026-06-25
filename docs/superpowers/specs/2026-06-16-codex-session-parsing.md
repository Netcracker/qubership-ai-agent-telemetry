# Detecting skill use in Codex sessions — 2026-06-16

How the sender recognizes that a skill ran inside a Codex turn. Codex has no dedicated "skill"
tool: a skill is loaded by reading its `SKILL.md` file, and the rollout transcript records that
read as an ordinary shell command. The sender uses two complementary signals so that no single
point of failure drops an event.

Scope: the Codex harness only. Claude, Cursor, and OpenCode are out of scope and detected
differently.

## Two signals, kept together

| Signal | Source | Catches | Misses |
|---|---|---|---|
| Marker | `[skill-called] skill=<name> source=<source>` baked into `SKILL.md`, matched in `last_assistant_message` | Turns where the model echoes the marker into its reply | Turns where the model does not echo it; the marker also leaks into user-visible output |
| Rollout parse | The session transcript on disk, matched on the `SKILL.md` read | Every turn that reads a `SKILL.md`, regardless of what the model writes | Skills loaded without a file read (for example, inlined into the system prompt) |

The two cover different cases, so the sender runs both and deduplicates the result. A
`codex exec` run that read `adr-authoring/SKILL.md` but never echoed the marker confirmed the
gap: the marker adapter saw nothing, the rollout parse caught it.

## Where the rollout lives

Codex writes one JSONL transcript per session:

```
~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<session_id>.jsonl
```

The Stop hook payload carries `session_id`. Locate the file by matching the `-<session_id>.jsonl`
suffix under the sessions directory, rather than reconstructing the timestamp.

## The parse rule

Each line is a JSON object with a `type` and a `payload`. A skill read is a `function_call` whose
shell command opens a `SKILL.md` under a `skills/` directory:

- Select lines where `.type == "response_item"` and `.payload.type == "function_call"` and
  `.payload.name == "exec_command"`.
- Parse `.payload.arguments` (itself a JSON string) and read its `cmd` field.
- Match the path with `(?:^|/)skills/([^/]+)/SKILL\.md`. Capture group 1 is the skill name.

Match on the path substring, not the command name. The reading command varies — `sed`, `cat`,
`head`, `tail`, `rg` — and the path is absolute in the desktop app but relative in `codex exec`.
Both forms end in `skills/<name>/SKILL.md`, so the substring match handles both.

## Enrichment

The first line, `.type == "session_meta"`, describes the session and removes the need for the
hook payload or a live `git` call:

- `payload.id` — the session ID.
- `payload.cwd` — the working directory.
- `payload.git.repository_url`, `payload.git.branch`, `payload.git.commit_hash` — the repository
  the session ran in.

Read the repository remote from `session_meta.git.repository_url`. It is recorded at session
start and works offline and in backfill.

## When to parse

Parse on the Stop hook, the same lifecycle point the marker adapter already uses, so the install
of the package stays the single consent boundary. Two consequences:

- Stop fires once per turn, and the rollout grows across turns. Keep a per-session cursor (a byte
  or line offset in the spool) so each run ingests only new `function_call` lines.
- One skill can be read by more than one command in a turn. Deduplicate by
  `(session_id, skill)` before emitting.

## CLI versus desktop

Verified against `codex-cli 0.140.0-alpha.2` (`codex exec`) and the desktop app. The transcript
path, the `function_call` / `exec_command` shape, and the `session_meta.git` block are identical.
The differences do not affect parsing:

- `session_meta.originator` is `codex_exec` and `source` is `exec` for the CLI; the desktop app
  reports its own values.
- The command path is relative under `codex exec` and absolute in the desktop app.
- The Stop hook fires in both, so the same ingest path serves both.

## Limitations

- The rollout schema is Codex-internal and can change between versions. Pin the parser to the
  fields above and treat an unrecognized shape as zero events, never as a hook failure.
- The parse detects only skills loaded by reading a `SKILL.md`. A skill delivered another way
  produces no `function_call` and no event.
- The marker still leaks into user-visible output when the model echoes it. That is the cost of
  keeping it as a second signal; revisit once the rollout parse proves its coverage.
