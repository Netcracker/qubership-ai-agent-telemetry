# Qubership developer installer

Install the baseline developer tools with one command. The installer configures the selected components and prints a
summary when it finishes.

This bootstrap is separate from the AI agent telemetry installer. It calls that installer as one of its components but
does not replace it.

## Install the default toolset

The default run installs all components for Claude Code, Codex, and Cursor.

macOS or Linux:

```sh
curl -fsSL \
  https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.sh \
  | sh
```

Windows PowerShell:

```powershell
$release = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download'
$installer = irm "$release/qubership-dev-install.ps1"
iex "& { $installer }"
```

The installer adds or configures:

- APM and the global `qubership-global-essentials` package;
- AI agent telemetry and hooks for the selected agent harnesses;
- global Git hooks from [`exadmin/pre-commit-global`](https://github.com/exadmin/pre-commit-global).

## Select components and harnesses

Components are `apm`, `telemetry`, and `git-hooks`. Harnesses are `claude`, `codex`, and `cursor`. The value `all`
selects every known value.

macOS or Linux:

```sh
curl -fsSL \
  https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.sh \
  | sh -s -- --components apm,telemetry --harnesses claude,cursor
```

Windows PowerShell:

```powershell
$release = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download'
$installer = irm "$release/qubership-dev-install.ps1"
iex "& { $installer } -Components apm,telemetry -Harnesses claude,cursor"
```

Use `--skip` or `-Skip` to subtract components from the selected set:

```sh
qubership-dev-install.sh --skip git-hooks
```

```powershell
./qubership-dev-install.ps1 -Skip git-hooks
```

## Options

| POSIX | PowerShell | Behavior |
| --- | --- | --- |
| `--components <list>` | `-Components <list>` | Select components. The default is `all`. |
| `--skip <list>` | `-Skip <list>` | Exclude components from the selected set. |
| `--harnesses <list>` | `-Harnesses <list>` | Select agent harnesses. The default is `all`. |
| `--force-git-hooks` | `-ForceGitHooks` | Replace an unrelated global `core.hooksPath`. |
| `--force-update` | `-ForceUpdate` | Update selected components even when they are installed. |
| `--non-interactive` | `-NonInteractive` | Fail instead of prompting for missing prerequisites. |
| `-h`, `--help` | `-Help` | Print command help. |

`--force-update` performs the component-specific update:

- APM updates its CLI, marketplace metadata, and the installed global package.
- Telemetry replaces its installed binary with the latest release and refreshes selected hooks.
- Global Git hooks run `git pull --ff-only`. Local changes or divergent history cause this component to fail; the
  installer does not discard them.

## Git and Java prerequisites

Git and Java are required only for the `git-hooks` component. If either command is missing, the interactive installer
asks once whether you installed it in another terminal, then checks again. A negative response or failed second check
stops the bootstrap before it changes any component.

If `core.hooksPath` already points somewhere else, the installer leaves it unchanged and marks `git-hooks` as
`SKIPPED`. Pass `--force-git-hooks` or `-ForceGitHooks` to replace it explicitly.

`CYBER_FERRET_PASSWORD` is not collected by the installer. When it is missing, installation continues with a warning;
configure the environment variable before relying on CyberFerret checks.

## Automation

Non-interactive telemetry configuration uses the existing `AI_AGENT_TELEMETRY_*` environment variables. Do not pass
tokens on the command line.

Exit codes are:

- `0`: every selected component is `OK` or `SKIPPED`;
- `1`: a prerequisite check or at least one component failed;
- `2`: an option, component, harness, or platform value is invalid.

An individual component failure does not stop independent components. The final summary lists `OK`, `SKIPPED`, or
`FAILED` for every selected component.
