# Generic event schema and privacy allowlist

## Status

Accepted

**Date:** 2026-07-17

**Related ADRs:**

- [ADR 0004](0004-event-schema-and-privacy.md), the accepted historical decision for the original skill event and
  privacy boundary

## Context

The CLI originally persisted and exported only `skill_executed`. Command invocation and MCP execution analytics need
two more event types without weakening the privacy boundary established by ADR 0004. Harness hook payloads can contain
prompts, arguments, tool inputs and results, errors, paths, identifiers, and user information that the analytics do not
need.

The outbox also needs to accept events written before this expansion. The new schema must distinguish payload types,
reject accidental fields, preserve existing skill records, and let old buffered skill events flush after an upgrade.

## Decision

Use a typed, versioned event envelope with `schema_version`, `event_name`, optional `event_id`, `agent`, `session_id`,
optional `repo_remote`, `ts`, and one direct event-specific `payload` object. Version `1` supports `skill_executed`,
`command_invoked`, and `mcp_tool_executed`. Optional fields are omitted, never encoded as `null`, and the decoder
rejects unknown, duplicate, mismatched, or explicitly null fields. Writers emit only version `1`.

Each payload has a fixed shape:

- `skill_executed` contains `skill_name`;
- `command_invoked` contains `command_name`, `command_source`, and `expansion_type`;
- `mcp_tool_executed` contains `tool_name` and `outcome`, plus optional `server_name` and `duration_ms`.

Only `server_name` and `duration_ms` are optional in the MCP payload. `event_id` and `repo_remote` are optional envelope
fields so buffered version 1 events written before event IDs were introduced remain readable.

Readers also accept the exact legacy unversioned skill shape with `agent`, `session_id`, optional `repo_remote`,
`skill`, and `ts`. They map it to `skill_executed` and apply version 1 validation. Existing files are not rewritten.

The CLI generates a UUID v7 when it enqueues an event and stores it as `event_id`. Every delivery attempt for the same
outbox file exports that value as `event.id`. Different events receive different identifiers. UUID v7 embeds the event
time in milliseconds. Its random portion comes from `crypto/rand` and contains no user, machine, repository, session,
or event payload data.

Older outbox entries without an ID, and entries with an untrusted malformed ID, receive a deterministic UUID v7
fallback. The fallback uses the persisted event timestamp and derives its random portion only from the opaque outbox
filename. This keeps retries stable without exporting arbitrary stored content. VictoriaLogs does not deduplicate
records automatically by `event.id`; backend processing or analytics queries must use the attribute to collapse repeat
deliveries.

Apply strict profiles to every external identifier before enqueue:

| Fields | Length | Pattern |
| --- | --- | --- |
| `session_id` | 1–128 | `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$` |
| `skill_name`, `command_name` | 1–255 | `^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$` |
| `command_source` | 1–64 | `^[A-Za-z0-9_.-]{1,64}$` |
| `server_name`, `tool_name` | 1–128 | `^[A-Za-z0-9_.-]{1,128}$` |

Validation uses the exact input. Adapters do not trim, truncate, replace characters, or change case. Invalid required
identifiers drop the event. An invalid present optional server name also drops the event. Expansion type, MCP outcome,
and duration use fixed enums or bounds rather than arbitrary values.

Reserve one internal exception for the delivery probe. `agent=selftest` is valid only with `event_name=skill_executed`,
`skill_name=__selftest__`, a generated lowercase UUID v4 session ID, and no repository value or local repository
directory. Adapters accept only `claude`, `codex`, and `cursor`; they cannot select the exception. Legacy reads accept
only the same exact reserved pair.

Apply collection policy to every harness event before enqueue:

```text
collect = !disabled && (repository_allowed || path_allowed)
```

Repository policy can use the local working directory and all git remotes to select an allowed normalized remote. Path
policy can use harness workspace roots when repository attribution is missing or disallowed. Only `repo.remote` is
serialized, and a path-only match retains a safe normalized remote when one is available. Local paths and path rules
stay on the machine. An event can retain an empty remote when Git attribution is unavailable. The selftest probe
bypasses collection policy because it tests machine delivery rather than repository activity. Path authorization does
not choose an operation-specific root; that work remains [issue 66].

Keep these content and identity fields excluded even when a hook supplies them:

- prompts and expanded prompt text;
- command arguments;
- tool inputs, results, errors, and stack traces;
- local paths and transcript paths;
- MCP URLs and server launch commands;
- tool-call and turn IDs;
- model identifiers and user email;
- arbitrary unrecognized fields.

Names that pass the profiles are analytics dimensions, not content. The backend remains responsible for limiting the
number of distinct valid identifiers and detecting excessive cardinality.

Token, cost, and usage attribution remain a separate decision. Those signals have different privacy and attribution
properties across harnesses and are not part of this schema expansion.

[issue 66]: https://github.com/Netcracker/qubership-ai-agent-telemetry/issues/66

## Consequences

- Existing `skill_executed` OTLP bodies and attributes remain compatible, while consumers can distinguish the new
  command and MCP event bodies.
- A mixed outbox can flush valid legacy and version 1 entries without a migration; invalid files remain buffered.
- Delivery retries have a stable `event.id`, but storage and queries must use it explicitly to remove duplicates.
- Invalid or unsupported hook data produces no event and cannot fail an agent turn.
- The typed payloads prevent newly decoded harness fields from entering storage by accident.
- Analytics cannot use excluded content, user identity, token usage, or inferred outcomes.
- The backend must enforce operational cardinality limits even though every individual identifier is bounded.
