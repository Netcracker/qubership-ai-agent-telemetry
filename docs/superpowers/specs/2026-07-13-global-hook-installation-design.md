# Global hook installation design

Date: July 13, 2026

Status: approved for implementation

## Goal

Install machine-wide telemetry hooks without requiring APM. A clean installation must configure telemetry and register
hooks for Claude Code, Codex, and Cursor. Updating an already configured installation must refresh those hooks without
prompting for the collector endpoint or token again.

The installation must work on Linux, macOS, and Windows, preserve unrelated harness settings, and remain safe to run
more than once.

## User experience

`ai-agent-telemetry configure` installs all three supported hooks by default after writing the telemetry configuration.
The optional `--hooks` flag accepts `all`, `none`, or a comma-separated subset of `claude`, `codex`, and `cursor`:

```sh
ai-agent-telemetry configure
ai-agent-telemetry configure --hooks=claude,codex
ai-agent-telemetry configure --hooks=none
```

The CLI also exposes a noninteractive repair and update path:

```sh
ai-agent-telemetry hooks install
ai-agent-telemetry hooks install --target=claude,codex
```

`hooks install` defaults to all supported harnesses. It never reads or changes the collector endpoint, token,
repository policy, or CA certificate.

Unknown targets, malformed target lists, unsupported hook actions, and unknown `configure` flags are usage errors. The
CLI reports the offending value and exits with code 2 without changing hook files.

## Installer behavior

The POSIX and PowerShell installers keep configuration prompts separate from hook maintenance:

1. Install or update the verified binary.
2. Add the binary directory to the user `PATH` when needed.
3. If the collector endpoint is missing, run `ai-agent-telemetry configure`. This writes the configuration and installs
   every supported hook.
4. If the endpoint already exists, run `ai-agent-telemetry hooks install`. This refreshes every supported hook without
   prompting for configuration values.

`--skip-config` and `-SkipConfig` skip both configuration and hook installation. This preserves their role as the
escape hatch for callers that do not want the installer to change user configuration.

This update path is required for migration. Existing installations already have an endpoint, so changing only
`configure` would leave their APM-installed hooks unchanged.

## Hook locations and definitions

The CLI resolves the user's home directory with Go's platform APIs and uses `filepath` operations. It writes the same
home-relative locations on every supported operating system:

| Harness | Global configuration | Hook |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` | `PreToolUse`, matcher `Skill` |
| Codex | `~/.codex/hooks.json` | `Stop` |
| Cursor | `~/.cursor/hooks.json` | `afterAgentResponse`, with top-level `version: 1` |

Every hook invokes the binary by its bare name:

```text
ai-agent-telemetry ingest --agent=<harness>
```

The bare command keeps the definition portable across POSIX shells, PowerShell, and `cmd.exe`. The installers remain
responsible for adding `~/.local/bin` to the user `PATH`.

## Merge and ownership rules

Each harness adapter owns its native JSON shape, while a shared file layer handles reading and atomic writes. An
adapter must:

- preserve every unrelated top-level field, event, matcher group, handler, and unknown extension field;
- recognize the canonical telemetry command and the known APM-generated telemetry entry;
- remove duplicate owned entries;
- replace a recognized legacy telemetry entry with one canonical entry;
- add the canonical entry when no owned entry exists; and
- produce the same JSON structure on every subsequent run.

Recognition is deliberately narrow. It covers the current bare telemetry command, the APM provenance attached to the
`ai-agent-telemetry` package entry, and legacy commands explicitly listed in regression fixtures. It does not remove a
user hook merely because its command contains a similar substring.

Claude Code may already have a `PreToolUse` matcher group for `Skill`. The adapter reuses that group when possible and
adds only the telemetry handler. Codex preserves other `Stop` groups and handlers. Cursor preserves other
`afterAgentResponse` commands and ensures that the required numeric top-level `version` remains present.

The retained APM hook package is a compatibility surface, not an installation dependency. Its README marks the package
as legacy, and a parity test verifies that its three hook commands match the CLI's canonical definitions.

## File safety and errors

The installer creates missing parent directories and JSON files. On POSIX systems, new directories use mode `0700`
and new files use mode `0600`; Windows uses the platform's access-control behavior. Existing POSIX file permissions are
preserved.

Writes use a temporary file in the destination directory followed by a platform-safe replacement. Native Windows tests
must exercise replacement of an existing file, not only creation of a new file.

If an existing file contains invalid JSON or has an incompatible structural type, the adapter leaves it byte-for-byte
unchanged and reports an actionable error with the file path. Installation continues for the other requested
harnesses, then exits nonzero with a summary of every failed target. Successfully updated hook files are not rolled
back because they are independent user configurations and each update is safe to rerun.

`configure` writes the telemetry configuration and then installs hooks. A hook failure does not discard valid endpoint,
token, repository-policy, or CA changes, but `configure` exits nonzero so the incomplete setup is visible.

## Status and trust

`ai-agent-telemetry status` adds a `hooks` section with one state per supported harness:

- `installed`: one canonical telemetry hook is present;
- `missing`: the file or telemetry entry is absent; or
- `invalid`: the file cannot be parsed or has an incompatible shape.

Status remains read-only and does not treat an absent harness executable as an error. Hooks are intentionally installed
before a harness exists. Verbose status includes the native file path and parse error when applicable.

The CLI reports hook registration, not harness trust. It does not edit Codex's private trust state or claim that a hook
has executed. After installation, documentation tells the user to fully restart each installed harness, inspect the
command, and approve or trust it if prompted. The Codex instructions name the exact command to approve:
`ai-agent-telemetry ingest --agent=codex`.

`selftest` continues to verify delivery to the collector. It cannot prove that a harness loaded or invoked its hook, so
the documentation distinguishes the delivery check from hook registration and trust.

## Documentation changes

The root TL;DR contains the complete required path:

1. Run the platform installer.
2. Run `configure` only when the installer did not complete interactive configuration.
3. Run `status` and `selftest`.
4. Fully restart each installed harness.
5. Review and trust the telemetry command when the harness prompts.

The main installation guide and CLI reference document `--hooks`, `hooks install`, hook status, global file locations,
restart behavior, trust, repair, and `--skip-config`. APM is removed from the primary installation flow. The retained
APM package documentation labels it as a legacy compatibility option and directs new installations to the CLI.

The setup skill is updated to diagnose and repair the CLI-managed global hooks instead of instructing users to run APM
for project-level hooks.

## Test and CI coverage

Go tests cover:

- target parsing, defaults, and usage errors;
- clean creation of all three native files;
- preservation of unrelated settings and unknown fields;
- canonicalization of current and known legacy APM entries;
- duplicate removal and repeat-run idempotence;
- malformed JSON and incompatible structures;
- aggregated partial failures;
- status states and verbose diagnostics; and
- secure permissions where the operating system exposes POSIX modes.

Installer tests cover both paths: a clean install calls `configure`, while an update with an existing endpoint calls the
noninteractive hook installer. Skip-config tests prove that neither operation runs.

CI runs the Go hook tests natively on Linux, macOS, and Windows. The Windows job verifies paths and replacement of
existing files under a temporary `USERPROFILE`. Installer tests continue to use local release assets and are extended
where needed to assert the new call sequence on POSIX and PowerShell.

Existing build, formatting, vet, Super-Linter, and link checks remain required. Workflow path filters must classify the
new hook implementation and its tests as relevant changes.

## Pull request delivery

Before the pull request is created, a separate subagent reviews the complete diff for correctness, cross-platform
behavior, configuration preservation, documentation accuracy, and missing tests. Confirmed findings are fixed and the
relevant verification is rerun.

The pull request description stays concise and uses three sections: why APM-free global installation is needed, what
changed in the CLI and installers, and how the change was verified.
