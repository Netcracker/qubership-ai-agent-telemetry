# Lifecycle installer

Install and manage the baseline developer tools with one Go lifecycle CLI. The canonical bootstraps are `install.sh`
and `install.ps1`; both verify a release binary and run the same `install`, `update`, or `uninstall` command.

## Install

macOS or Linux:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1')))"
```

The default selection installs the managed CLI, APM and `qubership-global-essentials`, AI agent telemetry, and global
Git hooks. It targets Claude Code, Cline, Codex, and Cursor.

## Select components and harnesses

Components are `apm`, `telemetry`, and `git-hooks`. Harnesses are `claude`, `cline`, `codex`, and `cursor`. `all` is
the default for both selections. Use lowercase, double-dash options in every shell.

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh \
  | sh -s -- --components apm,telemetry --harnesses claude,cursor
```

```powershell
$release = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download'
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod '$release/install.ps1'))) --components 'apm,telemetry' --harnesses 'claude,cursor'"
```

`--skip` subtracts components from the selected set. `--skip all` is invalid because it would leave no component; use
`update --cli-only` for an explicit CLI-only update.

```sh
ai-agent-telemetry install --skip git-hooks
ai-agent-telemetry install --components telemetry --harnesses codex
```

The lifecycle keeps harness selection separate from APM deployment targets. Selecting `cline` installs the global
Cline file hook and deploys skills through APM's `agent-skills` target. Cline has no native APM target. The same global
hook covers the Cline VS Code and JetBrains extensions, compatible VS Code hosts, and Cline CLI.

Install and update accept `--force-git-hooks` to replace an unrelated global `core.hooksPath`. They also accept
`--non-interactive`, which disables prerequisite and telemetry prompts.

## Noninteractive telemetry

Set a collector endpoint before selecting telemetry in noninteractive mode. The token is optional:

```sh
AI_AGENT_TELEMETRY_ENDPOINT=https://collector.example/v1/logs \
AI_AGENT_TELEMETRY_TOKEN=<token> \
ai-agent-telemetry install --non-interactive
```

Existing saved configuration satisfies the same requirement. A missing endpoint fails preflight before the managed
CLI or any component changes. Excluding `git-hooks` also excludes its Git and Java prerequisite checks.

## Update

Update the managed CLI and all components, or select a subset:

```sh
ai-agent-telemetry update
ai-agent-telemetry update --components telemetry --harnesses claude,codex
ai-agent-telemetry update --cli-only
```

Update refreshes selected existing components and installs selected missing components. `--cli-only` skips component
and prerequisite preflight. It cannot be combined with `--components`, `--skip`, `--harnesses`,
`--force-git-hooks`, or `--non-interactive`.

On Windows, direct update verifies a new release, hands control to the new image, and replaces the managed executable
before component changes. A helper removes the exact renamed old image after the old process exits. If bounded cleanup
retries fail, stderr prints the exact path that you may remove manually after both update processes exit.

## Uninstall and purge

Full uninstall removes every component and then removes the managed CLI and its installer-owned `PATH` mutation:

```sh
ai-agent-telemetry uninstall
```

A partial uninstall preserves the CLI unless you pass `--remove-cli`. The flag requires telemetry in the final
selection so hook cleanup cannot leave active hooks without their executable.

```sh
ai-agent-telemetry uninstall --components telemetry
ai-agent-telemetry uninstall --components telemetry --remove-cli
```

Normal uninstall preserves telemetry configuration, credentials, repository policy, delivery settings, diagnostics,
offsets, buffered events, and machine identity. Add `--purge` to remove the telemetry-specific configuration and cache
after successful hook cleanup:

```sh
ai-agent-telemetry uninstall --purge
```

The CLI reverses only a `PATH` mutation proven by its ownership receipt. It preserves unrelated shell-profile and
Windows user-PATH content and never removes `~/.local/bin`, even when that directory is empty.

The installed Windows executable rejects full uninstall and partial uninstall with `--remove-cli` before making any
changes. Use the temporary bootstrap so the managed executable is not running:

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'))) uninstall --purge"
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'))) uninstall --components telemetry --remove-cli"
```

## Git and Java prerequisites

Git and Java 21 or newer are required only for `git-hooks`. Interactive mode asks once whether you installed or
updated missing tools, then checks again. Noninteractive mode fails without prompting. No component changes occur when
preflight fails.

The installer validates the global hooks clone, its expected origin, and its worktree before changing
`core.hooksPath`. It preserves unrelated or locally modified state unless ownership is proven. A missing
`CYBER_FERRET_PASSWORD` remains a warning; the installer does not collect or store it.

## Results and exit codes

The summary lists the managed CLI and selected components as `OK`, `SKIPPED`, or `FAILED`. Independent components
continue after an operational failure.

- `0`: every selected operation is `OK` or `SKIPPED`.
- `1`: preflight or at least one operation failed.
- `2`: command syntax, selection, or an option combination is invalid.

The old update-check, self-update, force-update, force, skip-config, and PowerShell named-parameter contracts were
removed. They are breaking changes, not supported aliases. Use `update`, `update --cli-only`, component selection, or
`--skip telemetry` for the corresponding new operation.
