# Manual uninstall and legacy cleanup

## Remove tools installed by version 1.2.0 or earlier

Use this voluntary cleanup only for tools installed by the old lifecycle. The pinned bootstrap runs the old uninstall
contract without passing obsolete options to the current CLI:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.sh | sh -s -- uninstall --components apm,git-hooks
```

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/v1.2.0/install.ps1'))) uninstall --components apm,git-hooks"
```

Normal install, update, and uninstall never run this cleanup. Review the old lifecycle summary before removing tools
that you may still use independently.

## Replace a recognized legacy hook

The CLI reports a Cline hook as `outdated` only when its bytes exactly match a supported legacy template embedded in
the CLI. Neither the user nor an agent needs to inspect or recognize the script. Replace that hook with two explicit
operations:

```sh
ai-agent-telemetry hooks uninstall --target=cline
ai-agent-telemetry hooks install --target=cline
```

The first command removes the recognized legacy template. The second creates the current template at the now-missing
path. Do not use this procedure for `invalid`, unrecognized, modified, symbolic-link, or non-regular entries. Preserve
those entries and resolve their ownership first.

## Resolve a modified hook during uninstall

Use this procedure when uninstall preserves the Cline `PostToolUse` file because it contains the telemetry ownership
comment but no longer matches a generated version.

The installer never deletes a modified hook. The file may contain commands that you added and still need. Inspect it,
remove the telemetry-owned parts, and rerun the original uninstall command.

Every edit and file-removal command in this guide is an instruction for the user. The CLI displays the guidance but
never edits an unrecognized hook and never runs `rm` or `Remove-Item` itself.

## Identify the file

On macOS and Linux, inspect:

```text
~/Documents/Cline/Hooks/PostToolUse
```

On Windows, inspect:

```text
~/Documents/Cline/Hooks/PostToolUse.ps1
```

Uninstall reports the resolved path. Use that reported path if your home or Documents directory differs from the
examples above.

## Keep your commands

If the hook contains commands that you want to keep, edit the file manually. Remove both telemetry-owned parts:

1. Remove the line or command that invokes:

   ```text
   ai-agent-telemetry ingest --agent=cline
   ```

2. Remove the exact ownership comment:

   ```text
   # Managed by ai-agent-telemetry. Do not edit.
   ```

Keep the shebang, your commands, and any control flow that your commands require. The installer does not edit the file
because it cannot safely parse POSIX shell or PowerShell.

Review the result before continuing. The file must no longer invoke `ai-agent-telemetry`, and it must no longer contain
the telemetry ownership comment.

Rerun the same uninstall command that reported the conflict. The installer now treats the remaining hook as user-owned,
preserves it, and completes removal of the managed CLI and its receipt-owned `PATH` entry. If the original command used
`--purge`, the repeated command also removes telemetry configuration and cache.

## Remove the whole hook

Delete the entire file only if you no longer need any command in it.

On macOS and Linux:

```sh
rm -- "$HOME/Documents/Cline/Hooks/PostToolUse"
```

On Windows PowerShell:

```powershell
Remove-Item -LiteralPath "$HOME\Documents\Cline\Hooks\PostToolUse.ps1"
```

Rerun the original uninstall command after removing the file.

## Why uninstall stops

Automatic deletion requires a byte-for-byte match with a generated telemetry hook. In any other regular file, the
ownership comment classifies the file as a managed-hook conflict, but it does not authorize deletion. Uninstall keeps
the managed CLI and telemetry data until you resolve the conflict so that an active telemetry invocation does not lose
its executable.
