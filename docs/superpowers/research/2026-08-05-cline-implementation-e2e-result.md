# Cline harness implementation end-to-end result

- Date: August 5, 2026
- Repository branch: `docs/cline-vscode-hook-experiment`
- Development CLI version: `dev`
- Cline CLI: 3.0.50
- Cline VS Code Extension identifier: `saoudrizwan.claude-dev`
- Outcome: `PASS`

## Scope

This experiment verified the implemented Cline detector and managed global hook against two real clients:

1. Cline CLI.
2. The Cline VS Code Extension.

The clients used different probe skill names so their records could be queried independently. The experiment did not
capture raw hook payloads, prompts, tool results, user identifiers, model identifiers, or workspace paths.

## Setup

The development binary was built outside the repository and temporarily installed at the normal bare-command path:

```text
~/.local/bin/ai-agent-telemetry
```

The previous v1.2.0 binary was copied to a private temporary directory first. Its SHA-256 digest matched the restored
binary after the experiment.

The development CLI installed one global Cline hook:

```text
~/Documents/Cline/Hooks/PostToolUse
```

No file occupied that path before the experiment. `status --verbose` reported `cline: installed`, and the file had
mode `0755`. Two temporary project skills were added under `.cline/skills`:

- `cline-telemetry-cli-probe-20260805`
- `cline-telemetry-vscode-probe-20260805`

## Cline CLI result

Cline CLI discovered and invoked `cline-telemetry-cli-probe-20260805` exactly once through its `skills` tool. The
task returned `CLINE_TELEMETRY_CLI_PROBE_OK` and completed successfully. The global hook did not alter or block the
tool result.

VictoriaLogs returned exactly one matching record for:

```text
agent:cline AND skill.name:cline-telemetry-cli-probe-20260805
```

The record used `agent=cline`, `_msg=skill_executed`, the normalized repository identity, and the CLI conversation ID
as `session.id`.

## VS Code Extension result

The Cline VS Code Extension discovered and invoked `cline-telemetry-vscode-probe-20260805` exactly once through its
`use_skill` tool. Its UI showed the global `PostToolUse` hook and the response `{"cancel":false}`. The task returned
`CLINE_TELEMETRY_VSCODE_PROBE_OK` and completed successfully.

The event initially remained in the local outbox because the CLI probe had triggered the normal 60-second flush
throttle immediately beforehand. `status --verbose` reported one buffered event. An explicit `flush` sent it and
reduced the backlog to zero.

VictoriaLogs then returned exactly one matching record for:

```text
agent:cline AND skill.name:cline-telemetry-vscode-probe-20260805
```

The record used `agent=cline`, `_msg=skill_executed`, the normalized repository identity, and the numeric Cline task
ID as `session.id`.

## Stored field check

The VictoriaLogs JSON view showed only the event and resource fields expected by the existing schema:

- `_time`
- `_stream` and `_stream_id`
- `_msg`
- `agent`
- `event.id`
- `machine.id`
- `os.type`
- `repo.remote`
- `service.name`
- `service.version`
- `session.id`
- `severity`
- `skill.name`

Neither record contained a prompt, tool result, model, Cline user ID, workspace path, raw remote URL, tool arguments,
or arbitrary hook fields.

## Cleanup

- The hook matched the implementation-owned SHA-256 digest before guarded uninstall.
- The development CLI removed the exact managed hook.
- Both temporary probe skills and their directories were removed.
- The original v1.2.0 CLI was restored with its original SHA-256 digest.
- The local outbox was empty.
- The temporary development binary, backup, and cross-compiled test binaries were removed.
- No active Cline experiment artifact remained in the repository or global hook directory.
