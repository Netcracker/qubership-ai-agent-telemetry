# Support Cline as a telemetry harness

## Status

Accepted

<!-- markdownlint-disable MD001 -->

#### Date

2026-08-06

#### Owner

denifilatoff

#### Participants and approvers

Denis Filatov (@denifilatoff)

#### Related ADRs

- [0001-skill-detection-via-hooks-and-transcripts.md](0001-skill-detection-via-hooks-and-transcripts.md) defines
  native event and transcript-based skill detection.
- [0002-bare-binary-on-path.md](0002-bare-binary-on-path.md) defines the command that native hooks run.
- [0005-cli-managed-global-hooks.md](0005-cli-managed-global-hooks.md) defines machine-wide hook ownership.
- [0006-generic-event-schema-and-privacy.md](0006-generic-event-schema-and-privacy.md) defines the event allowlist and
  privacy boundary.
- [0008-cline-hook-installation-and-removal.md](0008-cline-hook-installation-and-removal.md) supersedes the installation
  migration and removal rules in this record.
- [0009-cline-mcp-tool-telemetry.md](0009-cline-mcp-tool-telemetry.md) extends this harness with MCP tool telemetry.

<!-- markdownlint-enable MD001 -->

## Context

Cline has active users whose skill executions were missing from the telemetry dataset. Support for Claude Code,
Codex, and Cursor alone left a known gap in adoption data. The first required client is the Cline VS Code Extension.

Cline exposes skill execution through a native file hook. Its VS Code Extension sends `PostToolUse` with the
`use_skill` tool and `skill_name` parameter. Cline CLI uses a compatibility envelope with event `tool_result`, tool
`skills`, and parameter `skill`. Both clients run the global hook under `~/Documents/Cline/Hooks/`.

Cline supports one file per hook type. Installing a telemetry `PostToolUse` hook can therefore conflict with a file
owned by the user or another integration. Cline also creates a visible hook-status card before it starts the hook
process. Suppressing process output removes telemetry text from that card, but it does not remove the card itself.
Cline 4.1.4 has no separate setting that hides the card while leaving hooks enabled.

## Decision

We will support Cline as the fourth shipped telemetry harness. The lifecycle target is `cline`, and the event value is
`agent=cline`.

The CLI will install one machine-wide `PostToolUse` file for Cline. The POSIX path is
`~/Documents/Cline/Hooks/PostToolUse`; the Windows path adds the `.ps1` extension. The hook runs
`ai-agent-telemetry ingest --agent=cline`, discards stdout and stderr, and always exits with code `0`.

The Cline adapter will accept the native VS Code Extension payload and the CLI compatibility payload. It will emit one
`skill_executed` event only for a successful `use_skill` or `skills` call with a valid skill name. It will decode only
the fields needed for skill detection and repository attribution. Prompts, tool results, model data, user identity,
and local paths remain outside the event.

The installer owns only its exact hook content. The original decision allowed migration of the previous managed
version that printed `{"cancel":false}`. [ADR 0008](0008-cline-hook-installation-and-removal.md) supersedes that part:
installation is create-only, while uninstall removes exact current and supported legacy templates. It preserves
modified files, unrelated files, and symbolic links. It will not compose multiple `PostToolUse` scripts.

Selecting the Cline lifecycle target will deploy shared skills through APM's `agent-skills` target because APM has no
native `cline` target. The global hook also applies to Cline CLI and other Cline clients that use the same file-hook
contract. The VS Code Extension and CLI are the acceptance-tested clients for this change.

### Justification

We considered leaving Cline unsupported until another planned harness was added. That would avoid another adapter but
would continue omitting events from active Cline users.

We also considered reading Cline's session database or logs. The native hook already provides the skill name and
success state, so session parsing would add format coupling and expose more local data without improving detection.

A separate hook per Cline client would duplicate installation and parsing logic. Cline deliberately shares the global
file-hook contract, so one adapter can normalize the small payload difference between the extension and CLI.

We also considered printing a JSON success response. Cline accepts a successful empty response as
`cancel=false`. Empty output avoids showing telemetry JSON after every tool call. The remaining hook-status card is
owned by Cline's UI and cannot be removed by changing the hook response.

## Consequences

- Cline skill executions can use the same event schema, repository policy, outbox, and collector as the other
  harnesses.
- One lifecycle installation configures Cline alongside Claude Code, Codex, and Cursor.
- Cline command and MCP events remain unsupported. This decision adds only `skill_executed`.
- A pre-existing `PostToolUse` file blocks automatic installation until the owner resolves the conflict.
- Cline users still see the product's hook-status card for each invocation, although the hook adds no output to it.
- The adapter depends on Cline's documented file-hook and payload contracts. A breaking change requires an adapter
  update.
