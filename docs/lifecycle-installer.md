# Lifecycle installer

The lifecycle installer manages the `ai-agent-telemetry` CLI, machine configuration, native harness hooks, and an
optional configure skill. It does not manage unrelated developer tools.

## Install

Run the release bootstrap on macOS or Linux:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh
```

Run the PowerShell bootstrap on Windows:

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1')))"
```

The bootstrap downloads the platform binary and `SHA256SUMS`, verifies the binary, and runs
`ai-agent-telemetry install`. Install then:

1. resolves the collector endpoint and optional token before mutation;
2. installs the managed CLI under `~/.local/bin` and records any installer-owned `PATH` change;
3. writes the telemetry configuration; and
4. registers native hooks for the selected harnesses.

Install selects Claude Code, Cline, Codex, and Cursor by default. Use `--harnesses` for a subset:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh \
  | sh -s -- --harnesses claude,codex
```

The option accepts comma-separated or repeated values. `all` selects every supported harness.

For unattended installation, provide `AI_AGENT_TELEMETRY_ENDPOINT` and the optional
`AI_AGENT_TELEMETRY_TOKEN`, then pass `--non-interactive`. A missing endpoint stops preflight before the managed CLI,
configuration, or hooks change.

## Optional configure skill

If APM is already on `PATH` and the CLI reports a release tag, install also installs
`agent-packages/ai-agent-telemetry-configure` globally for the selected harnesses. Cline maps to APM's
`agent-skills` target. Update refreshes the skill to the CLI release, and uninstall removes it when the global manifest
contains the package.

The installer does not install APM. A missing APM executable or unavailable release tag skips skill installation. An
APM command failure produces a `WARN` result but does not fail a working managed CLI and native-hook lifecycle.

## Update

Run:

```sh
ai-agent-telemetry update
ai-agent-telemetry update --harnesses claude,codex
```

Update downloads and verifies the release when needed, installs the managed CLI, refreshes the selected hooks, and
preserves telemetry configuration, machine identity, repository and path policy, certificates, delivery settings,
diagnostics, offsets, and buffered events.

Before writing native hooks, the CLI checks `~/.apm/apm.yml` for the exact global legacy dependency:

```text
Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
```

If the dependency is absent, APM is not required. If it is present, the CLI must remove it through APM before it writes
native hooks. An unreadable or invalid global manifest, a missing APM executable, or a failed removal stops migration
with a nonzero result. The diagnostic prints these recovery commands:

```sh
apm uninstall -g Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
ai-agent-telemetry update
```

This migration reads only the global APM manifest. It does not edit a repository-local manifest or remove the retained
compatibility package from consumer repositories.

On Windows, direct update hands control to the verified new binary before replacing the managed executable. The old
process returns the new process's exit code. If stale-image cleanup exhausts its retries, the diagnostic names the exact
file that you can remove after both update processes exit.

## Uninstall and purge

Run normal uninstall to remove native telemetry hooks, the optional configure skill when available, the managed CLI,
and its receipt-owned `PATH` entry:

```sh
ai-agent-telemetry uninstall
```

Normal uninstall preserves telemetry configuration, credentials, repository and path policy, delivery settings,
diagnostics, offsets, buffered events, and machine identity. A later install can resume the same configuration.

Use purge only when you also want to remove telemetry configuration and cache:

```sh
ai-agent-telemetry uninstall --purge
```

Purge runs after native hook cleanup. It removes the telemetry configuration and cache directories, including the
machine ID and outbox, but preserves their shared parent directories.

The installed Windows executable cannot remove itself. Use the temporary release bootstrap for uninstall:

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'))) uninstall"
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'))) uninstall --purge"
```

Uninstall removes only telemetry-owned hook entries and exact owned files. A modified Cline hook with the telemetry
ownership marker blocks managed CLI removal so the hook cannot lose its executable. Follow the
[manual conflict-resolution procedure](manual-uninstall.md), then rerun the same uninstall command.

## Clean up tools installed by version 1.2.0 or earlier

Cleanup of tools from the old lifecycle is voluntary and separate from telemetry. Run the pinned old bootstrap only if
you want to remove those old installations:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.sh | sh -s -- uninstall --components apm,git-hooks
```

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.ps1'))) uninstall --components apm,git-hooks"
```

Normal install, update, and uninstall never run these commands. The current CLI does not accept the old component
options.

## Results and exit codes

The lifecycle prints one result per operation:

- `OK` means the operation completed or was already in the requested state.
- `SKIPPED` means an optional operation was unavailable or a prerequisite operation failed.
- `WARN` means an optional configure-skill operation failed while the core telemetry lifecycle succeeded.
- `FAILED` means a required operation failed.

Usage errors return `2`. Operational failures return `1`. Successful lifecycles, including optional `SKIPPED` or
`WARN` results, return `0`.

The installer records only its own `PATH` mutation. Removal reverses that exact change and preserves unrelated shell
profile or Windows user-PATH content. It never removes `~/.local/bin`, even when the directory becomes empty.
