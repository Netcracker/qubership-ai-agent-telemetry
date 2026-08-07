# Record Cline MCP tool executions

## Status

Accepted

<!-- markdownlint-disable MD001 -->

#### Date

2026-08-07

#### Owner

denifilatoff

#### Participants and approvers

Denis Filatov (@denifilatoff)

#### Related ADRs

- [0006-generic-event-schema-and-privacy.md](0006-generic-event-schema-and-privacy.md) defines the shared MCP event and
  privacy allowlist.
- [0007-cline-harness-support.md](0007-cline-harness-support.md) defines Cline hook ownership, skill detection, and
  repository attribution.

<!-- markdownlint-enable MD001 -->

## Context

Cline's existing `PostToolUse` hook includes the data needed to record completed MCP tool calls. The classic
`use_mcp_tool` wrapper supplies explicit server and tool names. Newer SDK clients expose MCP tools as
`<server>__<tool>`, but Cline may sanitize or truncate the original names and append an eight-character SHA-1 suffix.

The same payload can include arbitrary MCP arguments, results, and errors. These values are private content and are
outside the telemetry allowlist. Cline replaces slash-command tokens with expanded instructions before its available
prompt hook runs, so that hook cannot provide an exact command identity.

## Decision

We will emit `mcp_tool_executed` from Cline's existing post-tool hook. The event contains the exact server and tool
names, `succeeded` or `failed` from Cline's boolean success field, and a non-negative integer duration when Cline
provides one.

The adapter will accept explicit names from `use_mcp_tool`. It will accept a direct SDK name only when the value has
exactly one separator, is no longer than Cline's 64-character limit, contains valid MCP identifiers, and has no
hash-shaped suffix. Ambiguous names will produce no event.

The adapter will decode only the fields needed for event identity, outcome, duration, session, and repository
attribution. It will not decode MCP arguments, results, errors, prompts, model data, or user identity. Cline command
invocations remain unsupported until Cline exposes the original command metadata through a native hook.

### Justification

The existing hook reports completion, exact success state, and duration without requiring another machine-wide file.
The classic wrapper carries exact MCP identity and therefore needs no inference.

Reporting a sanitized SDK name as the original would create incorrect server and tool dimensions. Conservative
rejection keeps the dataset trustworthy at the cost of missing ambiguous calls. Reading prompt content, workflow
files, session databases, or logs would weaken the privacy boundary and couple the adapter to undocumented storage.

## Consequences

- Cline contributes MCP call counts, success rates, failure rates, and latency to the shared telemetry dataset.
- The existing global `PostToolUse` installation requires no lifecycle migration.
- Classic wrapper calls have exact server and tool attribution.
- Ambiguous SDK names are omitted, including legitimate names that resemble Cline's transformed output.
- MCP arguments, results, and errors remain outside telemetry.
- Cline `command_invoked` remains unsupported because the available hook does not retain the command identity.
