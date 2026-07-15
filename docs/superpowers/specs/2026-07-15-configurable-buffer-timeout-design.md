# Configurable buffer capacity and flush timeout design

## Goal

Allow operators to configure the local telemetry buffer capacity and the ordinary flush timeout without rebuilding the
CLI. Preserve the existing behavior when neither setting is configured.

## Public interface

The CLI accepts two new environment variables:

- `AI_AGENT_TELEMETRY_BUFFER_CAP`: a positive decimal integer. The default is `100`.
- `AI_AGENT_TELEMETRY_FLUSH_TIMEOUT`: a positive Go duration such as `500ms`, `30s`, or `1m`. The default is `2s`.

The `configure` command accepts the same settings through either `--flag=value` or `--flag value` syntax:

```text
ai-agent-telemetry configure --buffer-cap=1000 --flush-timeout=30s
ai-agent-telemetry configure --buffer-cap 1000 --flush-timeout 30s
```

`configure` stores supplied values in `~/.config/ai-agent-telemetry/env` under their environment-variable names. An
omitted flag preserves the existing saved value. Process environment variables override saved values, and saved values
override defaults.

## Runtime model

A focused runtime-settings resolver loads both values once for each command invocation. It returns a typed settings
structure containing an integer buffer capacity and a `time.Duration` flush timeout.

The ingest path uses the effective buffer capacity when rotating the outbox and the effective flush timeout when sending
events. The explicit `flush` command uses the same effective flush timeout. The self-test retains its separate `10s`
timeout because it is a diagnostic command rather than an ordinary delivery attempt.

The status command keeps its compact output unchanged. `status --verbose` adds these effective values:

```text
configuration:
  buffer_cap: 1000
  flush_timeout: 30s
```

## Validation and error handling

`configure` rejects zero, negative, malformed, or missing values for either new flag. It returns exit code `2`, writes
nothing to the configuration file, and prints the configure help with a specific validation error.

At runtime, an invalid process environment or saved value cannot break an agent hook. The resolver writes a warning to
stderr and substitutes the corresponding default. Each warning names the setting, the invalid value, and the default
that the CLI selected.

The resolver applies precedence before validation. If the process environment contains an invalid value, it falls back
to the default rather than using the saved value. This behavior makes an explicit but invalid override visible and
predictable.

## Implementation boundaries

- `config.go` owns setting names, defaults, parsing, precedence, and runtime warning generation.
- `main.go` parses configure flags, resolves runtime settings once per command, and passes typed values to delivery
  code.
- `commands.go` persists supplied configure values and includes effective settings in verbose status reports.
- CLI help and operator documentation describe the flags, variables, defaults, precedence, and validation rules.

No generic configuration registry is introduced. The two settings share small parsing helpers and a typed structure,
which keeps the change testable without adding an abstraction for hypothetical settings.

## Testing

Unit tests cover:

- current defaults when neither source defines a value;
- process-environment precedence over the saved env file;
- valid integer and Go-duration parsing;
- warnings and default fallback for invalid runtime values;
- configure parsing in equals and space-separated forms;
- configure rejection of missing, malformed, zero, and negative values;
- persistence that preserves unrelated and omitted settings;
- effective values in verbose status output and their absence from compact status output;
- use of the configured buffer capacity by outbox rotation;
- use of the configured timeout by ordinary flush paths.

The full Go test suite and build must pass before the branch is pushed.
