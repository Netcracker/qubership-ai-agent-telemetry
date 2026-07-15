# Qubership developer installer

Install the baseline developer tools with one command. The installer configures the selected components and prints a
summary when it finishes.

This bootstrap is separate from the AI agent telemetry installer. It calls that installer as one of its components but
does not replace it.

## TL;DR

Install all components for Claude Code, Codex, and Cursor, and force their update operations.

macOS or Linux:

```sh
curl -fsSL \
  https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.sh \
  | sh -s -- --force-update
```

Windows Command Prompt:

```bat
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$u='https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.ps1';$s=irm $u;iex ('& {'+$s+'} -ForceUpdate')"
```

## Normal update behavior

To use the normal update behavior, omit the force-update option.

macOS or Linux:

```sh
curl -fsSL \
  https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.sh \
  | sh
```

Windows Command Prompt:

```bat
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$u='https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/qubership-dev-install.ps1';$s=irm $u;iex ('& {'+$s+'}')"
```

Both forms update existing APM and telemetry CLIs before configuration. This ensures the commands used by the
bootstrap are available.

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
| `--force-update` | `-ForceUpdate` | Force update operations for every selected component. |
| `--non-interactive` | `-NonInteractive` | Fail instead of prompting for missing prerequisites. |
| `-h`, `--help` | `-Help` | Print command help. |

Existing APM and telemetry CLIs are updated before configuration on every run. `--force-update` also performs these
component-specific operations:

- APM refreshes marketplace metadata and the installed global package.
- Telemetry forces the release installation even when no existing binary is detected. Existing binaries select this
  mode automatically.
- Global Git hooks run `git pull --ff-only`. Local changes or divergent history cause this component to fail; the
  installer does not discard them.

The telemetry component installs the binary first, then configures hooks through `ai-agent-telemetry`. It therefore
requires a telemetry release that supports CLI-managed global hooks.

## Git and Java prerequisites

Git and Java 21 or newer are required only for the `git-hooks` component. If Git is missing, or if Java is missing,
older than version 21, or cannot report its specification version, the interactive installer asks once whether you
installed or updated the required tools in another terminal. It then checks again. A negative response or failed
second check stops the bootstrap before it changes any component.

If `core.hooksPath` already points somewhere else, the installer leaves it unchanged and marks `git-hooks` as
`SKIPPED`. Pass `--force-git-hooks` or `-ForceGitHooks` to replace it explicitly.

Before activating an existing hooks directory, the installer verifies that it is a clean Git clone with the expected
`origin`. A different repository, an ordinary directory, or local changes cause `git-hooks` to fail without changing
`core.hooksPath`.

## CyberFerret password

CyberFerret reads its password from `CYBER_FERRET_PASSWORD`; it does not accept a password option. The installer does
not collect or store this value. When it is missing, installation continues with a warning.

On Linux with Bash, save it without putting the password in shell history, then sign out and back in:

```bash
read -rsp 'CyberFerret password: ' password; echo
printf '\nexport CYBER_FERRET_PASSWORD=%q\n' "$password" >> "$HOME/.profile"
unset password
```

If you use another shell, save the export in that shell's login file instead.

On macOS with the default Zsh, set it for applications started later in the current login session:

```zsh
read -s 'password?CyberFerret password: '; echo
launchctl setenv CYBER_FERRET_PASSWORD "$password"
unset password
```

Run the macOS command again after signing in or restarting. Start or restart the terminal and IDE after setting the
variable.

On Windows PowerShell, save it for the current Windows user, then restart the terminal and IDE:

```powershell
$password = Read-Host 'CyberFerret password' -AsSecureString
$value = [Net.NetworkCredential]::new('', $password).Password
[Environment]::SetEnvironmentVariable('CYBER_FERRET_PASSWORD', $value, 'User')
Remove-Variable password, value
```

## Automation

Non-interactive mode requires telemetry to have been configured already. If it is not configured, the component fails
with an instruction to run `ai-agent-telemetry configure`; it never waits for endpoint or token input.

Exit codes are:

- `0`: every selected component is `OK` or `SKIPPED`;
- `1`: a prerequisite check or at least one component failed;
- `2`: an option, component, harness, or platform value is invalid.

An individual component failure does not stop independent components. The final summary lists `OK`, `SKIPPED`, or
`FAILED` for every selected component.
