# Agent integration

The CLI collects skill runs, command invocations, and MCP tool executions from documented harness hooks. The shared
delivery path is the same everywhere; each harness exposes a different event set.

## The shared path

The CLI registers one global harness-specific hook in each native user configuration:

| Harness | Global file |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Cline on macOS and Linux | `~/Documents/Cline/Hooks/PostToolUse` |
| Cline on Windows | `~/Documents/Cline/Hooks/PostToolUse.ps1` |
| Codex | `~/.codex/hooks.json`, `~/.codex/rules/ai-agent-telemetry.rules` |
| Cursor | `~/.cursor/hooks.json` |

The default lifecycle install puts the binary on `PATH` and registers all four harnesses, even when a harness is not
installed yet. Use `--harnesses` to select a subset. Each hook calls the CLI by its bare name,
`ai-agent-telemetry ingest --agent=<name>`, which works across Git Bash, PowerShell, `cmd.exe`,
and POSIX `sh`. The Codex policy file allows only the hook and two diagnostic commands to access
the machine configuration and collector outside its sandbox.

The CLI reads the agent's payload on stdin and routes it by the harness and its documented event discriminator. It
queues valid events to an on-disk outbox and flushes opportunistically over OTLP/HTTPS. It always exits 0, so it never
fails an agent turn. For its internals, see [the ai-agent-telemetry CLI](cli.md).

After installation, follow the README's [verification, restart, and trust steps](../README.md#installation).

For unattended installation, provide `AI_AGENT_TELEMETRY_ENDPOINT` and the optional
`AI_AGENT_TELEMETRY_TOKEN`, then pass `--non-interactive`. The lifecycle resolves required telemetry input before any
hook, managed CLI, or component change.

## Capability matrix

| Harness | Hook and matcher | Event | Captured event fields |
| --- | --- | --- | --- |
| Claude Code | `PreToolUse`, matcher `Skill` | `skill_executed` | `skill.name` |
| Claude Code | `UserPromptExpansion`, no matcher | `command_invoked` | `command.name`, `command.source`, `command.expansion_type` |
| Claude Code | `PostToolUse`, matcher `mcp__.*` | `mcp_tool_executed` | Server and tool names, `succeeded`, optional duration |
| Claude Code | `PostToolUseFailure`, matcher `mcp__.*` | `mcp_tool_executed` | Server and tool names, `failed`, optional duration |
| Codex | `Stop`, no matcher | `skill_executed` | Zero or more transcript-derived skill names |
| Codex | `PostToolUse`, matcher `mcp__.*` | `mcp_tool_executed` | Server and tool names, `unknown` outcome |
| Cline | `PostToolUse` or `tool_result`, tool `use_skill` or `skills` | `skill_executed` | `skill.name` |
| Cline | `PostToolUse` or `tool_result`, MCP tool | `mcp_tool_executed` | Server/tool, exact outcome, optional duration |
| Cursor | `afterAgentResponse` | `skill_executed` | Zero or more transcript-derived skill names |
| Cursor | `afterMCPExecution` | `mcp_tool_executed` | Tool name, `unknown` outcome, optional duration; no server name |

Only Claude Code exposes the metadata required for `command_invoked`. Ordinary built-in tools are not collected.

Skill detection uses one of two signals, depending on what the harness exposes:

- **Native event** — the agent names the skill in the hook payload. Exact.
- **Session transcript** — where there is no native event, the CLI reads the session
  transcript for the `SKILL.md` files the agent loaded.

Both transcripts are JSONL — one JSON object per line — and the CLI streams them line by
line, so a large transcript never loads into memory at once. The parse is fail-safe: a
missing file, an unreadable line, or an unexpected shape yields zero events, never an
error that could fail the turn. Because the hook fires every turn while the transcript
only grows, the CLI keeps a byte offset per Codex session (`codex:<session>`) and per
Cursor transcript file (`cursor:<session>:<path-hash>`), then parses only the bytes written
since the last run; an offset past the end of the file means it rotated, so the CLI resets
to zero. Within one parse, skill
names are deduplicated, so a skill read by several commands counts once. The exact match
rule differs per agent — see each section below.

The CLI does not rely on a marker printed into the model's response — see
[ADR 0001](adr/0001-skill-detection-via-hooks-and-transcripts.md).

## Claude Code

**Hooks:** `PreToolUse`, `UserPromptExpansion`, `PostToolUse`, and `PostToolUseFailure`.

Claude Code runs a skill as a tool call, so the hook fires before the tool runs and the
payload names the skill. This is the native-event path: the CLI reads the skill name
straight from the tool input and needs no transcript fallback.

`UserPromptExpansion` provides the command name, source, and expansion type. The CLI accepts only `slash_command` and
`mcp_prompt`; it does not decode the prompt or command arguments. `PostToolUse` and `PostToolUseFailure` accept only
MCP names shaped as `mcp__<server>__<tool>`. They provide exact `succeeded` and `failed` outcomes, respectively.

An older `PreToolUse` registration without `hook_event_name` remains readable only for the `Skill` tool. The adapter
does not infer any other event when the discriminator is absent.

```json
"PreToolUse": [
  { "matcher": "Skill",
    "hooks": [ { "type": "command",
      "command": "ai-agent-telemetry ingest --agent=claude" } ] }
]
```

## Codex

**Hooks:** `Stop` and `PostToolUse`.

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

`PostToolUse` reports MCP names as `mcp__<server>__<tool>`. The CLI records the server and tool names with outcome
`unknown`; Codex does not expose a separate documented failure event or duration for this signal. It never inspects
the tool response.

## Cline

**Hook:** the global `PostToolUse` file.

The same global file covers Cline's VS Code Extension, JetBrains plugin, Cline extensions running in compatible VS
Code hosts such as Cursor, and Cline CLI. Native Cursor sessions continue to use
`~/.cursor/hooks.json` and the Cursor adapter.

Cline exposes successful skill execution directly in the hook payload. The VS Code and JetBrains extensions use
`hookName=PostToolUse`, tool `use_skill`, and parameter `skill_name`. Cline CLI uses the compatibility envelope
`hookName=tool_result`, tool `skills`, and parameter `skill`. The adapter also accepts Cline's compatibility parameter
`skillName`. It requires `success=true` and emits exactly one `skill_executed` event.

Cline also exposes completed MCP tools through the same hook. The classic `use_mcp_tool` wrapper provides exact
`server_name` and `tool_name` parameters. Newer SDK clients expose a direct `<server>__<tool>` name. The adapter accepts
a direct name only when it can exclude Cline's sanitization, hash suffix, and truncation beyond Cline's 64-character
limit. Ambiguous names produce no event. This favors missing telemetry over assigning a call to the wrong MCP identity.

For MCP tools, `success=true` maps to `succeeded`, and `success=false` maps to `failed`. The adapter includes a
non-negative integer duration from `executionTimeMs` or the compatibility alias `durationMs`. Missing or invalid
durations do not drop an otherwise valid event.

The adapter decodes only `taskId`, `workspaceRoots`, the tool name, success, duration, the three supported skill
parameter names, and the two classic MCP identity fields. It does not decode or send the user ID, model, tool result,
arguments, errors, workspace metadata, branch, commit, or Cline's associated remote URLs. A single workspace root can
resolve the Git remote. With multiple roots, all non-empty roots must resolve to one normalized repository for remote
attribution. Detection retains an event with empty repository attribution when resolution is ambiguous or fails, and
passes every root to the collection policy. A repository or path rule must then authorize the event. Local paths are
not serialized. Selecting an operation-specific root remains [issue 66].

The hook discards stdout and stderr and exits with code `0`. Cline treats a successful empty response as
`cancel=false`, so telemetry errors cannot corrupt the response or block a turn. Empty output removes the telemetry
response text that Cline previously showed after every tool call. It does not remove Cline's own
`Hook: PostToolUse` status card: Cline creates the `hook_status` message before starting the hook process. Cline 4.1.4
has no separate setting to hide that card while keeping hooks enabled.

Installation creates a missing hook, treats the exact current content as idempotent, and preserves every other entry
as a conflict. It does not rewrite a supported legacy template. Uninstall removes only exact current or supported
legacy content. A mismatched regular file with the telemetry ownership comment blocks the remaining telemetry cleanup;
the [manual conflict-resolution guide](manual-uninstall.md) explains how to keep user commands and complete uninstall.
Cline supports one file per hook type, so automatic composition with an unrelated `PostToolUse` hook is outside the
current implementation.

On macOS and Linux the file is executable with mode `0755`. On Windows it is a PowerShell file. When APM is already on
`PATH`, selecting the `cline` lifecycle harness installs the optional configure skill through APM's `agent-skills`
target because APM has no native Cline target and Cline discovers `.agents/skills`.

[ADR 0007](adr/0007-cline-harness-support.md) records why Cline was added and why the integration uses this hook.
[ADR 0008](adr/0008-cline-hook-installation-and-removal.md) defines its ownership and lifecycle rules.
[ADR 0009](adr/0009-cline-mcp-tool-telemetry.md) records the MCP identity and privacy boundaries.

## Cursor

**Hooks:** `afterAgentResponse`, `subagentStop`, and `afterMCPExecution`.

Like Codex, Cursor has no skill-activation event. The `afterAgentResponse` hook names
the parent transcript in `transcript_path`; the CLI also scans direct
`subagents/*.jsonl` children as a fallback for Cursor versions that omit a subagent
transcript path. `subagentStop.agent_transcript_path`, when present, is scanned directly.
Each file has its own byte offset, so repeated hooks do not replay a skill and the same
skill used by separate parent or subagent transcripts remains separate telemetry.

Each line is a message with a `message.content` array, and two content shapes count as a
skill load:

- a `tool_use` entry named `Read` or `ReadFile` whose `input.path` matches a
  `skills/(<group>/)*<name>/SKILL.md` path — an automatic skill load;
- a `text` entry that contains a `<manually_attached_skills>` block, where each
  `Skill Name: <name>` line is a manually attached skill.

```text
(?i)(?:^|[\s"'=/\\])skills[\\/]+(?:[^\\/\s"']+[\\/]+)*([a-z0-9][a-z0-9-]{0,63})[\\/]+SKILL\.md
^Skill Name:\s*(\S+)
```

Unlike Codex, the transcript carries no git data. The CLI uses operation paths in each transcript to resolve one
unambiguous Git root within `workspace_roots`; otherwise it leaves repository identity empty. Repository and path
rules independently determine whether an unattributed event may be delivered. Local operation and workspace paths
remain policy inputs and are not serialized. Selecting an operation-specific root remains [issue 66]. The manual-block
scan is gated on the block being present, not bounded to it, so a stray `Skill Name:` line elsewhere in the same
message would also match. The cost is a spurious name, never a missed turn.

Cursor requires a numeric top-level `version` in `.cursor/hooks.json`. The CLI preserves an
existing numeric value and adds `version: 1` when it is absent, so no manual step is needed. The
historical APM issue is recorded in the [Cursor workaround].

These user-level hooks cover local Cursor sessions. Cursor cloud agents do not load
`~/.cursor/hooks.json`, so the machine-wide installation does not cover cloud-agent runs.

`afterMCPExecution` provides a tool name and optional duration. Cursor does not provide a stable MCP server name or
outcome, so the CLI omits the server and records `unknown`. It never inspects `result_json`.

```json
{
  "version": 1,
  "hooks": {
    "afterAgentResponse": [
      { "command": "ai-agent-telemetry ingest --agent=cursor" }
    ],
    "afterMCPExecution": [
      { "command": "ai-agent-telemetry ingest --agent=cursor" }
    ],
    "subagentStop": [
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

The retained `ai-agent-telemetry` APM hook package remains compatible with repositories that already consume it. New
setups use the machine-wide files created by the platform installer. A parity test keeps every event registration and
command aligned with the CLI-managed hooks while existing consumers migrate.

[Codex spec]: superpowers/specs/2026-06-16-codex-session-parsing.md
[Cursor workaround]: superpowers/decisions/2026-06-17-cursor-hooks-version-workaround.md
[issue 66]: https://github.com/Netcracker/qubership-ai-agent-telemetry/issues/66
