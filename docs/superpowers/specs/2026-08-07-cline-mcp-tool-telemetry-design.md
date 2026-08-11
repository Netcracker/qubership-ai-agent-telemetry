# Cline MCP tool telemetry design

## Goal

Emit `mcp_tool_executed` for Cline MCP tool calls with the same event schema used by Claude Code, Codex, and Cursor.
Use only Cline's existing post-tool hook. Do not inspect tool arguments, results, errors, prompts, or session storage.

## Scope

This change covers MCP tools exposed through either of Cline's supported tool-name shapes:

- the classic `use_mcp_tool` wrapper, whose parameters contain `server_name` and `tool_name`; and
- direct SDK tool names in the form `<server>__<tool>` when the original names can be recovered without guessing.

The existing Cline `skill_executed` behavior remains unchanged. Cline `command_invoked` remains out of scope because
Cline replaces a slash-command token with its instructions before the available prompt hook runs. The hook payload
does not retain the command name, source, or expansion type.

## Hook contract

The installed `PostToolUse` hook already forwards the required payload to
`ai-agent-telemetry ingest --agent=cline`. No additional hook file or lifecycle change is required.

The adapter accepts the existing client compatibility aliases:

| Meaning | Accepted field |
| --- | --- |
| Hook event | `hookName=PostToolUse` or `hookName=tool_result` |
| Tool name | `postToolUse.toolName` or `postToolUse.tool` |
| Outcome | `postToolUse.success` |
| Duration | `postToolUse.executionTimeMs` or `postToolUse.durationMs` |

`success` must be present and boolean. `true` maps to `succeeded`; `false` maps to `failed`. A non-negative integer
duration is included when present. Missing, negative, fractional, or overflowing duration values are omitted without
dropping an otherwise valid event. A null `executionTimeMs` is treated as absent, so a valid `durationMs` alias can
supply the duration.

## MCP identity

### Classic wrapper

For `use_mcp_tool`, the adapter reads only `postToolUse.parameters.server_name` and
`postToolUse.parameters.tool_name`. Both values must satisfy the existing MCP identifier contract. These fields carry
the original MCP identity, so the event includes both `mcp.server.name` and `mcp.tool.name`.

### Direct SDK tools

The newer Cline SDK exposes an MCP tool to hooks as `<server>__<tool>`. Cline may sanitize or truncate an unsupported
source name and append an eight-character SHA-1 suffix. A transformed value must not be reported as the original MCP
identity.

The adapter accepts a direct SDK name only when all of these conditions hold:

- it contains exactly one `__` separator;
- both components satisfy the existing MCP identifier contract;
- the complete hook tool name is no longer than Cline's 64-character transformation limit; and
- the tool component does not end with the transformation suffix shape `_[0-9a-f]{8}`.

These checks prefer false negatives over incorrect attribution. A legitimate tool component with a hash-shaped suffix
is not emitted because the hook cannot distinguish it from a transformed name.

## Attribution and privacy

MCP events reuse Cline's existing session and repository attribution:

- `taskId` becomes `session_id`;
- a single `workspaceRoots` entry supplies the repository directory and resolved remote;
- multiple roots are accepted only when they resolve to the same normalized remote; and
- no workspace root remains attributable when the list is absent.

The Cline payload allowlist does not decode MCP arguments, results, errors, user identifiers, model information, or
prompt content. Serialized telemetry contains only the event envelope, MCP server and tool names, outcome, and optional
duration.

## Adapter structure

`clineAdapter` first validates the hook envelope and session. It then routes the tool call and resolves repository
attribution only for a recognized identity:

1. Existing skill wrappers produce `skill_executed` only after a successful call.
2. `use_mcp_tool` produces an MCP event from its two explicit identity fields.
3. A reversible direct SDK name produces an MCP event from its two name components.
4. Other tools and ambiguous direct names produce no event and no repository lookup.

The adapter returns at most one event for a hook invocation. Existing event constructors and schema validation remain
authoritative for field limits and serialization.

## Verification

Tests cover successful and failed classic wrapper calls, direct SDK calls, both tool and duration aliases, omitted and
invalid durations, malformed identities, ambiguous transformed names, repository attribution, and privacy-sensitive
input fields. The backend smoke fixture gains one sanitized Cline MCP event so the ingestion path verifies the shared
schema.

Maintained documentation and ADR 0007 are updated to describe the new capability and the remaining command limitation.
The final gate is `go test ./...` from a clean worktree.
