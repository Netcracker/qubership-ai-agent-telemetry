# Generic telemetry events design

**Date:** July 16, 2026

**Status:** Approved for implementation planning

## Summary

Extend `ai-agent-telemetry` beyond skill activations with command-invocation and
MCP-tool events. Keep one local event pipeline, preserve existing
`skill_executed` output, and accept older outbox files without migration.
Collect only names, outcomes, and durations that a harness exposes through a
documented hook. Never collect prompts, tool inputs, tool results, or error
text.

This change is the first part of the telemetry expansion. Token and usage
attribution remain a separate follow-up because they use different signals and
have different attribution accuracy across harnesses.

## Scope

The change adds:

- `command_invoked` for Claude Code prompt expansion;
- `mcp_tool_executed` for documented MCP completion hooks in Claude Code, Codex,
  and Cursor;
- a versioned, typed outbox event envelope;
- backward-compatible reading of legacy skill-event files;
- CLI-managed and APM-package hook configuration for the new signals;
- documentation and an ADR for the expanded privacy allowlist.

The change does not add:

- telemetry for ordinary built-in tools such as shell, read, edit, or web
  search;
- prompt, command-argument, tool-input, result, or error-content collection;
- token or cost telemetry;
- inferred success or server identity when a harness does not provide a reliable
  signal;
- new CLI flags or commands;
- a new CI workflow.

## Harness contracts

The design uses documented lifecycle hooks instead of transcript inference where
a native event exists.

### Claude Code

Claude Code's
[`UserPromptExpansion`](https://code.claude.com/docs/en/hooks#userpromptexpansion)
hook reports `command_name`, `command_source`, and `expansion_type`. It covers
direct slash-command invocation, which bypasses the `PreToolUse` hook for the
`Skill` tool. The adapter must not decode or retain the original `prompt` or
`command_args`.

MCP tools appear in tool hooks with names in the `mcp__<server>__<tool>` form.
`PostToolUse` and `PostToolUseFailure` distinguish successful and failed calls,
and both may include `duration_ms`. These hooks provide the only exact MCP
outcome classification in this change.

### Codex

Codex [`PostToolUse`](https://learn.chatgpt.com/docs/hooks#posttooluse) supports
MCP tools and reports a canonical `mcp__<server>__<tool>` name. It does not
provide a separate MCP failure hook or a documented outcome field. The adapter
therefore records the outcome as `unknown` and does not inspect `tool_response`.

The existing `Stop` hook remains responsible for transcript-based skill
detection. The same ingest command handles both events and routes by
`hook_event_name`. The Codex hook reference states that transcript format is not
a stable hook interface, so the new MCP path must use `PostToolUse` rather than
add transcript parsing.

### Cursor

Cursor's [`afterMCPExecution`](https://cursor.com/docs/hooks#aftermcpexecution)
hook reports `tool_name` and `duration`. It does not provide a stable MCP server
name. The adapter records the tool name, omits the server name, and records the
outcome as `unknown` without inspecting `result_json`.

The existing `afterAgentResponse` hook remains responsible for transcript-based
skill detection. The global user hook supports local Cursor sessions. Cursor
cloud agents do not load a user's `~/.cursor/hooks.json`, so cloud-agent
coverage is outside this machine-wide installer.

## Event model

Replace the single-purpose `SkillEvent` outbox type with a discriminated
`TelemetryEvent` envelope:

```text
TelemetryEvent
├── schema_version
├── event_name
├── agent
├── session_id
├── repo_remote
├── repo_dir        # local only; never serialized
├── ts
└── payload         # one shape selected by event_name
```

The envelope has these invariants:

- `schema_version` is `1` for the new format.
- `event_name` is `skill_executed`, `command_invoked`, or `mcp_tool_executed`.
- `agent` is `claude`, `codex`, or `cursor`.
- The typed payload is present and its shape matches `event_name`.
- `agent`, `ts`, and the payload's primary name are required.
- `repo_dir` is available to local repository-policy code but has `json:"-"` and
  cannot enter the outbox.
- Optional duration is a non-negative integer number of milliseconds.

The JSON payloads contain only fields that can leave the process. They use
separate structs rather than a generic attribute map so a newly decoded harness
field cannot enter the outbox accidentally.

### Version 1 JSON format

Version 1 uses one top-level envelope and a direct event-specific `payload`
object. It does not use `payload.skill`, `payload.command`, `payload.mcp`, or
nullable payload fields. Optional fields are omitted rather than serialized as
`null`.

Timestamps retain the existing `ts` JSON name. Writers serialize UTC values in
RFC 3339 with optional fractional seconds, equivalent to Go's
`time.RFC3339Nano`; readers accept the same format. The canonical skill event
is:

```json
{
  "schema_version": 1,
  "event_name": "skill_executed",
  "agent": "codex",
  "session_id": "session-123",
  "repo_remote": "github.com/netcracker/project",
  "ts": "2026-07-16T12:34:56.123456789Z",
  "payload": {
    "skill_name": "superpowers:brainstorming"
  }
}
```

The canonical command event is:

```json
{
  "schema_version": 1,
  "event_name": "command_invoked",
  "agent": "claude",
  "session_id": "session-123",
  "repo_remote": "github.com/netcracker/project",
  "ts": "2026-07-16T12:34:56.123456789Z",
  "payload": {
    "command_name": "review-pr",
    "command_source": "plugin",
    "expansion_type": "slash_command"
  }
}
```

The canonical MCP event is:

```json
{
  "schema_version": 1,
  "event_name": "mcp_tool_executed",
  "agent": "claude",
  "session_id": "session-123",
  "repo_remote": "github.com/netcracker/project",
  "ts": "2026-07-16T12:34:56.123456789Z",
  "payload": {
    "server_name": "github",
    "tool_name": "get_issue",
    "outcome": "succeeded",
    "duration_ms": 42
  }
}
```

`repo_remote`, `server_name`, and `duration_ms` are optional and are absent
when unavailable. All other fields shown for the corresponding event are
required. A present field cannot be `null`. The decoder rejects unknown
top-level and payload fields, unknown schema versions, payloads that do not
match `event_name`, and invalid timestamp values.

### Legacy outbox compatibility

`Outbox.Read` first identifies the stored format. A JSON object without
`schema_version` and `event_name`, but with the legacy `skill` field, decodes as
`skill_executed`. New events pass envelope validation before they are returned.

The legacy on-disk shape remains exactly:

```json
{
  "agent": "codex",
  "session_id": "session-123",
  "repo_remote": "github.com/netcracker/project",
  "skill": "superpowers:brainstorming",
  "ts": "2026-07-16T12:34:56.123456789Z"
}
```

Legacy `repo_remote` remains optional. The decoder maps this object to an
in-memory `skill_executed` event and applies the same identifier validation as
version 1; writers emit only the version 1 format.

No eager migration rewrites buffered files. A mixed batch of legacy and
versioned files flushes in filename order. Unreadable or invalid entries remain
buffered while readable entries continue through the batch, matching current
behavior.

## OTLP event schema

Every event retains these common log attributes:

- `agent`
- `session.id`
- `repo.remote`

Event-specific bodies and attributes are:

- `skill_executed` requires `skill.name`.
- `command_invoked` requires `command.name`, `command.source`, and
  `command.expansion_type`.
- `mcp_tool_executed` requires `mcp.tool.name` and `mcp.outcome`. It may also
  contain `mcp.server.name` and `mcp.duration_ms`.

`mcp.outcome` is one of:

- `succeeded` when the harness provides a documented success event;
- `failed` when the harness provides a documented failure event;
- `unknown` when the harness reports completion without a reliable outcome.

Existing `skill_executed` records keep the same body and attributes. Resource
attributes also remain unchanged: `service.name`, `service.version`, `os.type`,
and optional `machine.id`.

OpenTelemetry defines tool names but warns that tool arguments and results may
contain sensitive information. This project keeps its small event-oriented
schema and deliberately omits those content fields. See the
[OpenTelemetry GenAI attribute registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/).

## Detection and normalization

`detect` continues to route by agent, then the agent adapter routes by
`hook_event_name`:

- Claude Code `PreToolUse` with `Skill` emits `skill_executed`.
- Claude Code `UserPromptExpansion` emits `command_invoked`.
- Claude Code `PostToolUse` matching `mcp__.*` emits `mcp_tool_executed` with
  outcome `succeeded`.
- Claude Code `PostToolUseFailure` matching `mcp__.*` emits
  `mcp_tool_executed` with outcome `failed`.
- Codex `Stop` emits zero or more transcript-derived `skill_executed` events.
- Codex `PostToolUse` matching `mcp__.*` emits `mcp_tool_executed` with outcome
  `unknown`.
- Cursor `afterAgentResponse` emits zero or more transcript-derived
  `skill_executed` events.
- Cursor `afterMCPExecution` emits `mcp_tool_executed` with outcome `unknown`.

Claude Code and Codex names must match `mcp__<server>__<tool>`. The normalizer
removes the prefix, stores the first segment as the server, and preserves the
remaining text as the tool name. A malformed name produces no event. Cursor
requires a non-empty `tool_name` and leaves the server absent.

Claude Code command normalization keeps only `command_name`, `command_source`,
and the documented `slash_command` or `mcp_prompt` expansion type. An
unsupported expansion type produces no event rather than expanding the schema
with an unreviewed value.

Missing and negative durations are omitted. The adapters do not infer data from
URLs, server commands, tool payloads, results, or error strings.

### Identifier validation

Every external identifier is validated before enqueue. Validation applies to
the exact input: adapters do not trim, truncate, replace characters, or change
case. This avoids aliasing two harness values to one telemetry dimension.

The accepted profiles are:

| Fields | Length | Pattern |
| --- | --- | --- |
| `session_id` | 1–128 | `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$` |
| `skill_name`, `command_name` | 1–255 | `^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$` |
| `command_source` | 1–64 | `^[A-Za-z0-9_.-]{1,64}$` |
| `server_name`, `tool_name` | 1–128 | `^[A-Za-z0-9_.-]{1,128}$` |

Lengths count ASCII characters; the patterns reject non-ASCII text by design.
The colon in skill and command names preserves namespaced identifiers such as
`plugin-name:skill-name`; the 255-character limit leaves room for harness and
plugin namespaces while keeping the value finite. MCP names use the stricter
character set from the
[MCP tool-name specification](https://modelcontextprotocol.io/specification/2025-11-25/server/tools),
but enforce its recommendations as telemetry requirements.

Whitespace, control characters, path separators, shell metacharacters, Unicode
lookalikes, empty values, and values above the limit are invalid. If any
required identifier is invalid, the adapter emits no event. If an optional MCP
server name is present but invalid, the whole event is dropped rather than
silently removing or rewriting the server identity.

## Hook installation

The CLI-managed global hook files gain these owned entries:

- Claude Code:
  - existing `PreToolUse` group with matcher `Skill`;
  - `UserPromptExpansion` group without a matcher;
  - `PostToolUse` group with matcher `mcp__.*`;
  - `PostToolUseFailure` group with matcher `mcp__.*`.
- Codex:
  - existing `Stop` group;
  - `PostToolUse` group with matcher `mcp__.*`.
- Cursor:
  - existing `afterAgentResponse` entry;
  - `afterMCPExecution` entry.

Every entry invokes the existing bare command:

```text
ai-agent-telemetry ingest --agent=<harness>
```

The unchanged command preserves the Codex execution-policy allowlist and avoids
new CLI surface. Merge functions own only handlers with the canonical command, a
known legacy command, or `_apm_source: ai-agent-telemetry`. They preserve
unrelated groups and handlers, remove duplicate owned handlers, and produce the
same file on repeated installation.

The retained APM hook package receives equivalent entries. A parity test
compares its commands and event coverage with the CLI-managed configuration.

The Codex file content changes, so Codex treats the hook definition as changed
and requires the user to review the new hash. Installation and release
documentation must tell the user to restart Codex and review the telemetry hook.

## Privacy boundary

All event types pass through the existing repository allowlist before enqueue.
The policy may use `repo_dir` and local git remotes to select an allowed
normalized remote, but only `repo.remote` is serialized.

The expanded allowlist permits only values that pass the identifier profiles
or their fixed enums:

- harness name;
- session ID;
- normalized repository remote;
- skill name;
- command name, source, and expansion type;
- MCP server name when documented by the harness;
- MCP tool name;
- bounded MCP outcome;
- non-negative MCP duration;
- existing anonymous resource attributes.

The following fields remain excluded even when a hook provides them:

- prompts and expanded prompt text;
- command arguments;
- tool inputs and results;
- error messages and stack traces;
- local paths and transcript paths;
- MCP URLs and server launch commands;
- tool-call and turn IDs;
- model identifiers;
- user email;
- arbitrary unrecognized fields.

Command, skill, server, and tool names are identifiers required for the intended
usage analytics. The limits bound the size and shape of each dimension value;
the collector remains responsible for operational limits on the number of
distinct valid identifiers. These names are not treated as content fields. A
new ADR extends ADR 0004 with this event-specific allowlist and records that any
future content or identity field requires another explicit privacy decision.

## Error handling

The ingest command remains fail-open and always exits `0` for hook-originated
errors:

- malformed JSON emits no event;
- an unsupported agent or hook event emits no event;
- a missing required field emits no event;
- an invalid required or present optional identifier emits no event;
- an unrecognized MCP name emits no event rather than a guessed identity;
- an unavailable optional field is omitted;
- an event outside repository policy is dropped before enqueue;
- enqueue, rotation, CA, and flush failures are written to stderr without
  failing the harness turn;
- collector failure leaves unsent files in the outbox.

The flush path validates an event before mapping it to OTLP. Invalid versioned
files remain in the outbox. No tool-call ID is collected for deduplication;
documented post-execution hooks are treated as the authoritative signal.

## Testing

### Event and outbox tests

- Round-trip every new event type.
- Compare serialization with canonical JSON fixtures for all three version 1
  events and the legacy event.
- Reject an unknown schema version, event name, outcome, or mismatched payload.
- Reject unknown envelope and payload fields, explicit `null` values, and
  invalid timestamp formats.
- Decode a legacy skill-event file.
- Reject a legacy skill event whose identifiers fail the version 1 rules.
- Flush a mixed legacy and versioned batch in order.
- Apply repository policy to every event type.
- Preserve local-only `repo_dir` behavior.

### Adapter tests

- Parse Claude Code command expansion without retaining prompt or arguments.
- Parse Claude Code MCP success and failure, including optional duration.
- Parse Codex MCP completion and reject non-MCP tools.
- Parse Cursor MCP completion without inventing a server name.
- Cover malformed JSON, missing names, unsupported expansion types, and invalid
  duration.
- Cover identifier length boundaries and reject empty, oversized, non-ASCII,
  whitespace, control-character, path-separator, and shell-metacharacter names.
- Assert for each rejected identifier that its value reaches neither outbox JSON
  nor serialized OTLP.
- Seed fixtures with prompts, arguments, results, errors, paths, URLs, model
  names, and email addresses.
- Assert that none of those sensitive fixture values reach normalized events.

### Hook tests

- Merge every target into an empty configuration.
- Preserve unrelated event groups and handlers.
- Replace legacy owned handlers without changing user handlers.
- Reinstall idempotently.
- Report `installed`, `missing`, and `invalid` status for the complete managed
  hook set.
- Keep the Codex execution-policy command exact.
- Keep CLI-managed hooks and the APM package in parity.

### OTLP tests

- Decode the exported OTLP request and assert body, attribute names, value
  types, and optional fields.
- Verify that legacy `skill_executed` output is unchanged.
- Assert that sensitive fixture values do not occur in outbox JSON or serialized
  OTLP requests.
- Retain collector-error, TLS, lock, and outbox-recovery coverage.

The current `go-build.yaml` provides the required platforms. Ubuntu runs
formatting, vet, build, and all Go tests. Focused hook tests run on macOS and
Windows. No workflow change is planned unless implementation reveals a
platform-only failure.

## Documentation

Implementation updates:

- `README.md` to describe multiple event types and the exact Data allowlist;
- `docs/agent-integration.md` with the hook and capability matrix;
- `docs/cli.md` with the versioned outbox model and routing behavior;
- a new ADR extending `docs/adr/0004-event-schema-and-privacy.md`;
- the retained APM package README when its event coverage changes.

Installation remains concise. Codex-specific text is limited to the restart and
trust action required after the hook definition changes.

## Delivery sequence

1. Merge the skill-detection hotfix PR #17.
2. Rebase `feat/telemetry-events` onto the resulting `main`.
3. Execute the implementation plan with test-first changes.
4. Run all local tests and the cross-platform CI jobs.
5. Run a separate code review before opening the feature PR.
6. Validate available harness events end to end against a test collector query.
7. Release the change as a minor version because it adds event types without
   changing existing skill records.

Token, cost, and usage attribution follow in a separate design and PR.
