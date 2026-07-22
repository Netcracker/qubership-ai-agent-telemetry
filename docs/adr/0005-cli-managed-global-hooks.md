# CLI-managed global hooks

> **Superseded installer details:** CLI-managed global hooks remain active. The unified Go lifecycle now runs hook
> installation and removal through component selection, preflight, and ownership-aware uninstall. The original text
> below remains unchanged as historical context.

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

The original installation flow set up the binary and machine configuration first, then required a
separate project-level hook step for each repository and harness. A user could finish the machine
setup and still have no telemetry until those additional steps were complete.

Project-level installation also left existing machines behind during upgrades. The installers
skipped `configure` when an endpoint existed, so they had no operation that could migrate or
repair hooks without prompting for collector settings again.

Each harness has a native user configuration with different JSON structure. Existing files may
contain unrelated settings and user hooks that installation must preserve. A malformed file must
not be overwritten, and one harness failure must not prevent independent harnesses from updating.

## Decision

The `ai-agent-telemetry` CLI owns global telemetry hook registration and required harness policy
files. It writes these user-level files on every supported operating system:

| Harness | Global file | Registration |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` | `PreToolUse`, matcher `Skill` |
| Codex | `~/.codex/hooks.json`, `~/.codex/rules/ai-agent-telemetry.rules` | `Stop` and its execution policy |
| Cursor | `~/.cursor/hooks.json` | `afterAgentResponse`, with numeric top-level `version` |

`ai-agent-telemetry configure` installs all supported hooks by default after it writes machine
configuration. `--hooks=all`, `--hooks=none`, or a comma-separated target list controls that step.
`ai-agent-telemetry hooks install` is the noninteractive repair path. It can select targets with
`--target=<list>` and never reads or changes the endpoint, token, CA, or repository policy.

Before either command installs a nonempty set of CLI-managed hooks, the CLI checks the global APM manifest at
`~/.apm/apm.yml` for the exact legacy telemetry package dependency. When it finds the dependency, it asks APM to
uninstall it globally. The CLI does not inspect or edit repository-local APM manifests.

Cleanup is best effort. Read, parse, executable lookup, and uninstall failures produce warnings on `stderr` but do not
change the command exit code. The CLI continues canonicalizing the requested native hooks, and configuration and hook
installation results determine success. Because `configure` writes machine configuration first, that configuration
remains written after a cleanup warning or a later hook failure.

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

The CLI reports registration and required policy state, not execution or trust. `status` reports
each hook as `installed`, `missing`, or `invalid`; `selftest` proves collector delivery. Harnesses
must be fully restarted after hook changes. The CLI does not modify private harness trust state.
Users inspect and approve the telemetry command when prompted.

The `agent-packages/ai-agent-telemetry` APM hook package remains as a compatibility surface for existing
repository-local consumers. Automatic cleanup removes only its global dependency when possible; it does not remove
the package from project manifests. New setups use the machine-wide hooks installed with the CLI. A parity test keeps
the package's command strings aligned with the CLI.

This ADR supersedes the APM-first installation assumptions in earlier design and decision records.
Those historical records remain unchanged because they document the constraints that led here.

## Consequences

- One installer run configures telemetry across repositories for Claude Code, Codex, and Cursor.
- Updating an already configured binary refreshes hooks without collector prompts.
- Native user configuration remains under user control; malformed files require explicit repair.
- Legacy global APM cleanup can warn and leave the dependency installed without blocking native hook installation.
- Hook status and collector delivery remain separate checks.
- Harness trust remains a user decision, so installation may require review after a command change.
- Existing repository-local APM consumers can migrate without an immediate package removal.
