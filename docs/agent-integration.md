# Agent integration

How a skill run is caught in each supported agent. The shared path is the same
everywhere; what differs is the hook the agent offers and how much it tells us.

## The shared path

The CLI registers one global harness-specific hook in each native user configuration:

| Harness | Global file |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/hooks.json`, `~/.codex/rules/ai-agent-telemetry.rules` |
| Cursor | `~/.cursor/hooks.json` |

One platform-installer run puts the binary on `PATH` and registers all three hooks, even when a
harness is not installed yet. Each hook calls the CLI by its bare name,
`ai-agent-telemetry ingest --agent=<name>`, which works across Git Bash, PowerShell, `cmd.exe`,
and POSIX `sh`. The Codex policy file allows only the hook and two diagnostic commands to access
the machine configuration and collector outside its sandbox.

The CLI reads the agent's payload on stdin, detects any skill that ran, queues the event to an
on-disk outbox, and flushes opportunistically over OTLP/HTTPS. It always exits 0, so it never
fails an agent turn. For its internals, see [the ai-agent-telemetry CLI](cli.md).

After installation, follow the README's [verification, restart, and trust steps](../README.md#installation).

Detection uses one of two signals, depending on what the agent exposes:

- **Native event** — the agent names the skill in the hook payload. Exact.
- **Session transcript** — where there is no native event, the CLI reads the session
  transcript for the `SKILL.md` files the agent loaded.

Both transcripts are JSONL — one JSON object per line — and the CLI streams them line by
line, so a large transcript never loads into memory at once. The parse is fail-safe: a
missing file, an unreadable line, or an unexpected shape yields zero events, never an
error that could fail the turn. Because the hook fires every turn while the transcript
only grows, the CLI keeps a per-session byte offset (keyed `codex:<session>` or
`cursor:<session>`) and parses only the bytes written since the last run; an offset past
the end of the file means it rotated, so the CLI resets to zero. Within one parse, skill
names are deduplicated, so a skill read by several commands counts once. The exact match
rule differs per agent — see each section below.

The CLI does not rely on a marker printed into the model's response — see
[ADR 0001](adr/0001-skill-detection-via-hooks-and-transcripts.md).

## Claude Code

**Hook:** `PreToolUse`, matched on the `Skill` tool.

Claude Code runs a skill as a tool call, so the hook fires before the tool runs and the
payload names the skill. This is the native-event path: the CLI reads the skill name
straight from the tool input and needs no transcript fallback.

```json
"PreToolUse": [
  { "matcher": "Skill",
    "hooks": [ { "type": "command",
      "command": "ai-agent-telemetry ingest --agent=claude" } ] }
]
```

## Codex

**Hook:** `Stop`, fired at the end of a turn.

A skill in Codex is not a tool and emits no activation event, so there is nothing to
intercept mid-turn. The `Stop` hook runs after the turn, and the CLI detects the skill
from the `SKILL.md` reads recorded in the rollout transcript named by `transcript_path`.

Each rollout line has a `type` and a `payload`. The CLI treats a skill read as a shell
command that opens a `SKILL.md`:

1. keep `response_item` lines for `exec_command` and `shell_command` function calls, or for a
   `custom_tool_call` that invokes either command tool;
2. ignore custom tool calls such as `apply_patch`, because their payload can contain skill paths
   as source-code fixtures without reading those skills;
3. match the command text against the skill path — capture group 1 is the skill name.

```text
(?i)(?:^|[\s"'=/\\])skills[\\/]+(?:[^\\/\s"']+[\\/]+)*([a-z0-9][a-z0-9-]{0,63})[\\/]+SKILL\.md
```

The match is on the path, not the reading command: Codex opens the file with `sed`,
`cat`, `head`, or `rg`, and the path is absolute in the desktop app but relative under
`codex exec`. The leading separator stops a directory such as `my-skills/` from
matching. Intermediate directories support nested skill layouts, including Codex's bundled
`skills/.system/<name>/SKILL.md` path. The final directory before `SKILL.md` must follow the
Agent Skills name shape (matched case-insensitively for compatibility), which prevents regex
and glob fragments in that final segment from becoming skill names. After matching, the CLI
rejects non-ASCII names and names ending in or containing consecutive hyphens. The repository
remote comes from the first line, `session_meta`, field `git.repository_url`, which is read
regardless of the offset. See the [Codex spec] for the full record.

## Cursor

**Hook:** `afterAgentResponse`, fired after each response.

Like Codex, Cursor has no skill-activation event. The `afterAgentResponse` hook names
the transcript in `transcript_path`. Each line is a message with a `message.content`
array, and two content shapes count as a skill load:

- a `tool_use` entry named `Read` whose `input.path` matches a `skills/(<group>/)*<name>/SKILL.md`
  path — an automatic skill load;
- a `text` entry that contains a `<manually_attached_skills>` block, where each
  `Skill Name: <name>` line is a manually attached skill.

```text
(?i)(?:^|[\s"'=/\\])skills[\\/]+(?:[^\\/\s"']+[\\/]+)*([a-z0-9][a-z0-9-]{0,63})[\\/]+SKILL\.md
^Skill Name:\s*(\S+)
```

Unlike Codex, the transcript carries no git data, so the repository remote is resolved
from the hook's `workspace_roots`. The manual-block scan is gated on the block being
present, not bounded to it, so a stray `Skill Name:` line elsewhere in the same message
would also match — the cost is a spurious name, never a missed turn.

Cursor requires a numeric top-level `version` in `.cursor/hooks.json`. The CLI preserves an
existing numeric value and adds `version: 1` when it is absent, so no manual step is needed. The
historical APM issue is recorded in the [Cursor workaround].

```json
{
  "version": 1,
  "hooks": {
    "afterAgentResponse": [
      { "command": "ai-agent-telemetry ingest --agent=cursor" }
    ]
  }
}
```

## OpenCode (planned)

OpenCode is not shipped yet. It emits a native event: when skills are managed through
the `.claude/skills/` compatibility extension, activation is a `use_skill` tool call
caught by the pre-tool-call hook, with the skill name in its arguments — the same
native-event path as Claude Code.

## Legacy APM compatibility

The retained `ai-agent-telemetry` APM hook package remains compatible with repositories that
already consume it. New setups use the machine-wide files created by the platform installer. A
parity test keeps the package's three command strings aligned with the CLI while existing
consumers migrate.

[Codex spec]: superpowers/specs/2026-06-16-codex-session-parsing.md
[Cursor workaround]: superpowers/decisions/2026-06-17-cursor-hooks-version-workaround.md
