# CLI help design

## Context

The CLI lists command names when invoked without arguments, but it does not explain what each command does or which
arguments it accepts. Nested commands are harder to discover: `ai-agent-telemetry hooks` reports a missing action, and
`ai-agent-telemetry hooks help` reports an unknown action.

## Command catalog

Keep the existing hand-written dispatcher and add one centralized catalog for public command metadata and help text.
The catalog covers `configure`, `hooks`, `status`, `selftest`, `ingest`, `flush`, `update-check`, `self-update`, and
`version`. The dispatcher continues to call the existing command implementations and parsers.

The root help lists every public command with a one-line description. Command help includes a short description, usage,
supported options, and accepted values where applicable. Hook target help names `claude`, `codex`, and `cursor`, plus
`all` or `none` only for options that support those values.

## Invocation forms

The root accepts these equivalent forms:

```text
ai-agent-telemetry help
ai-agent-telemetry -h
ai-agent-telemetry --help
```

Each public command accepts these equivalent forms:

```text
ai-agent-telemetry help <command>
ai-agent-telemetry <command> help
ai-agent-telemetry <command> -h
ai-agent-telemetry <command> --help
```

`hooks` documents its `install` action and `--target=<list>` option. The explicit
`ai-agent-telemetry hooks install --help` form also displays the `hooks` command help.

## Exit behavior

Explicit help exits with code 0 and does not read configuration, create an outbox, install hooks, access the network, or
prompt for input.

Invalid input keeps exit code 2. An incomplete nested command, such as `ai-agent-telemetry hooks`, prints a concise error
followed by the relevant command help. Unknown root commands and unknown help topics print an error followed by root
help. Existing runtime failures retain their current exit codes.

The hook ingestion path keeps its hook-safe behavior. Invalid `ingest` arguments continue to exit with code 0 so a
telemetry hook cannot fail an agent turn.

## Implementation boundaries

Do not add a CLI framework or a new dependency. Add small formatting and lookup helpers around the existing `run`
dispatcher. Keep command execution in the existing switch and preserve the existing parsers except where help must be
recognized before command execution.

## Documentation and verification

Update the CLI reference to mention the built-in help forms. Add table-driven tests for root help, per-command help,
nested hook help, unknown topics, exit codes, and side-effect-free help execution. Existing Go test jobs exercise the
same code on Linux, macOS, and Windows, so this change does not require another CI job.
