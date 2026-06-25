# CLI invocation via bare binary name on PATH

## Status
Accepted
#### Date
2026-06-22
#### Owner
denifilatoff
#### Participants and approvers
Denis Filatov (@denifilatoff)
#### Related ADRs
- [0001-skill-detection-via-hooks-and-transcripts.md](0001-skill-detection-via-hooks-and-transcripts.md) —
  the hooks that call the binary
- [0003-config-cache-dirs-xdg.md](0003-config-cache-dirs-xdg.md) — where the binary's config lives

## Context

The original design called the CLI through **bootstrap wrapper scripts** (`bootstrap.sh`, `bootstrap.ps1`,
`bootstrap.bat`) bundled inside the APM package. The hook command was
`sh ./scripts/bootstrap.sh ingest --agent=<name>`, with a `|| .bat` fallback for Windows. The scripts located a
cached binary or downloaded one from a pinned GitHub release tag, then forwarded the arguments. This gave
automatic version pinning and a download-on-first-use experience.

The script-in-hook model broke down on Windows, where each harness runs hooks through a different shell:

1. **Claude Code on Windows — Git Bash.** The `sh … || …bootstrap.bat` fallback used backslash paths. Git Bash
   strips backslashes, so the `.bat` path resolved to `bootstrapbat: command not found` (exit 127). A
   forward-slash workaround (`./scripts/bootstrap.bat`) was verified to work, but it was a fragile hack — the
   command relied on Git Bash executing a `.bat` file via a POSIX-style path, which is not a documented or
   guaranteed behavior.

2. **Cursor on Windows — PowerShell 5.1.** Cursor runs project hooks through PowerShell 5.1 (verified by
   inspecting `extensionHostProcess.js` → `$executeHookDirect` → `powershell.exe`). PowerShell 5.1 treats `||`
   as a parse error (pipeline-chain operators were added only in PowerShell 7). The entire
   `sh … || …bootstrap.bat` command fails at parse time before any script runs. Forward-slash paths do not
   help — the blocker is the shell, not backslashes.

3. **Codex on Windows — `commandWindows` field.** Codex supports a `commandWindows` field in its hook schema
   that routes to `cmd.exe` via PowerShell. The field is the right native mechanism, but APM (v0.21.0) neither
   deploys the script it references nor rewrites its path. Root cause: `HOOK_COMMAND_KEYS` in APM's
   `hook_integrator.py` (lines ~492–499) lists `command`, `bash`, `powershell`, `windows`, `linux`, `osx` but
   not `commandWindows`. The `_rewrite_hooks_data` method iterates only those keys, so `commandWindows` is
   never parsed for script references. A clean `apm install` produces a Codex Windows hook that points at a
   `bootstrap.bat` that does not exist in the deploy tree. Filed as a bug report draft; the upstream fix is not
   in our control.

4. **APM deploys only the script referenced by the `command` key.** The `bootstrap.sh` file is deployed
   because `command` references it. The sibling `bootstrap.ps1` and `bootstrap.bat` are never copied into the
   deploy tree — they are unreferenced by any key APM recognizes. On Windows, the `.bat` fallback only
   "worked" when the file happened to exist from a prior manual state, not from a clean install.

The full analysis is in
[the Windows hook-shell decision](../superpowers/decisions/2026-06-22-windows-hook-shell-and-paths.md).

## Decision

We will install the CLI binary to `~/.local/bin/ai-agent-telemetry` (`.exe` on Windows) and ensure that directory
is on `PATH`. The hook command becomes `ai-agent-telemetry ingest --agent=<name>` — a bare binary name with no
shell-specific wrapper, no fallback operator, and no per-harness script to deploy.

A configure skill (`ai-agent-telemetry-configure`) guides the user through the one-time install by running the
existing release scripts (`bootstrap.sh` / `bootstrap.ps1`) and CLI commands (`configure`, `status`,
`selftest`). The scripts download the release binary, verify its SHA-256 checksum against the published
`SHA256SUMS`, place it in `~/.local/bin`, and add that directory to `PATH`. An `update-check` subcommand
compares the installed version against the latest GitHub release and prints an advisory verdict; applying the
update is a separate, explicit re-run of the installer script with `--force`.

### Justification

A bare binary name is shell-agnostic. Git Bash, PowerShell 5.1, PowerShell 7, and `cmd.exe` all resolve it the
same way — through `PATH`. This eliminates every Windows shell trap at once:

- No `||` operator that PowerShell 5.1 rejects.
- No backslash-vs-forward-slash path ambiguity in Git Bash.
- No `commandWindows` field that APM does not deploy.
- No per-OS script to bundle, deploy, and maintain.

The bootstrap scripts' auto-download capability is replaced by a one-time setup step. The tradeoff is
that the binary no longer self-updates on every hook call. The `update-check` subcommand and the configure
skill cover this gap: the skill calls `update-check` and offers the update with consent, but auto-updating on
the hot hook path is deliberately avoided — a network fetch and binary swap on every agent turn risks failing
the turn.

The old bootstrap scripts pinned a release tag (e.g., `v0.1.0`) and downloaded into a per-version cache under
`LOCALAPPDATA` or `~/Library/Caches`. Running `status` through them reports a stale binary and a misleading
version. The on-`PATH` model has one binary, one version, and one `status` output across all harnesses.

The follow-up decisions (SHA-256 verification, `update-check`, `os.type` attribute, UTF-8 BOM stripping) are in
[the on-PATH lifecycle record](../superpowers/decisions/2026-06-23-on-path-binary-lifecycle.md).

## Consequences

- **Positive.** One hook command works across every harness and OS. The APM `commandWindows` bug is no longer a
  blocker for telemetry. The configure skill handles install and updates in a single, auditable flow.
- **Negative.** The binary does not self-update. A machine can run an old build until the user invokes the
  configure skill or manually re-runs the installer. There is no scheduled or hook-triggered update check
  yet.
- **Neutral.** The bootstrap scripts (`bootstrap.sh`, `bootstrap.ps1`) are retained as the installer mechanism
  (download, verify, place on `PATH`), but they are no longer called from hooks. They run once at setup
  time, not on every agent turn. The old locate-or-download-per-turn model is retired.
- **Supply-chain hardening.** The installer verifies SHA-256 checksums before placing the binary on `PATH`. On
  hosts without a SHA-256 tool, the POSIX installer warns and proceeds — integrity is best-effort, availability
  is not sacrificed.
