# CLI-managed global hooks

## Status

Accepted

**Date:** 2026-07-13

**Owner:** Qubership AI Agent Telemetry maintainers

**Related ADRs:**

- [0001-skill-detection-via-hooks-and-transcripts.md](0001-skill-detection-via-hooks-and-transcripts.md) defines
  the harness events and transcript detection.
- [0002-bare-binary-on-path.md](0002-bare-binary-on-path.md) defines the portable command used by every hook.
- [0003-config-cache-dirs-xdg.md](0003-config-cache-dirs-xdg.md) defines the machine-level configuration paths.

## Context

The original installation flow used APM to deploy a hook into every repository. That made hook
registration depend on a project package manager even though the binary, endpoint, token, outbox,
and repository policy were already machine-level resources.

Project-level installation also left existing machines behind during upgrades. The installers
skipped `configure` when an endpoint existed, so they had no operation that could migrate or
repair hooks without prompting for collector settings again.

Each harness has a native user configuration with different JSON structure. Existing files may
contain unrelated settings and user hooks that installation must preserve. A malformed file must
not be overwritten, and one harness failure must not prevent independent harnesses from updating.

## Decision

The `ai-agent-telemetry` CLI owns global telemetry hook registration. It writes these native user
files on every supported operating system:

| Harness | Global file | Registration |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` | `PreToolUse`, matcher `Skill` |
| Codex | `~/.codex/hooks.json` | `Stop` |
| Cursor | `~/.cursor/hooks.json` | `afterAgentResponse`, with numeric top-level `version` |

`ai-agent-telemetry configure` installs all supported hooks by default after it writes machine
configuration. `--hooks=all`, `--hooks=none`, or a comma-separated target list controls that step.
`ai-agent-telemetry hooks install` is the noninteractive repair path. It can select targets with
`--target=<list>` and never reads or changes the endpoint, token, CA, or repository policy.

Each native adapter recognizes only canonical telemetry commands, retained APM provenance, and
explicitly supported legacy commands. It removes duplicate owned entries and preserves unrelated
top-level fields, events, groups, handlers, and extension fields. Repeated installation is
idempotent.

Updates use atomic file replacement. New POSIX directories use mode `0700`, new files use mode
`0600`, and existing file modes are preserved. Invalid JSON or an incompatible native structure
is left byte-for-byte unchanged. Installation continues for other requested harnesses and reports
all failures before returning a nonzero exit code.

The platform installers choose between configuration and repair. A missing endpoint invokes
`configure`; an existing endpoint invokes `hooks install`. Their skip-config options skip both
configuration and hook writes.

The CLI reports registration, not execution or trust. `status` reports each hook as `installed`,
`missing`, or `invalid`; `selftest` proves collector delivery. Harnesses must be fully restarted
after hook changes. The CLI does not modify Codex's private trust state. Codex users inspect and
approve `ai-agent-telemetry ingest --agent=codex` when prompted.

The `agent-packages/ai-agent-telemetry` APM hook package remains as a legacy compatibility surface
for existing consumers. New installations do not depend on it. A parity test keeps its three
command strings aligned with the CLI.

This ADR supersedes the APM-first installation assumptions in earlier design and decision records.
Those historical records remain unchanged because they document the constraints that led here.

## Consequences

- One installation registers telemetry across repositories for Claude Code, Codex, and Cursor.
- Updating an already configured binary refreshes hooks without collector prompts.
- Native user configuration remains under user control; malformed files require explicit repair.
- Hook status and collector delivery remain separate checks.
- Harness trust remains a user decision, so installation may require review after a command change.
- Existing APM consumers can migrate without an immediate package removal.
