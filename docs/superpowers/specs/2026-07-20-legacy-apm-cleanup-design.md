# Automatic legacy APM cleanup design

`ai-agent-telemetry` attempts to remove its legacy global APM dependency before it installs CLI-managed hooks. The
cleanup lives in the cross-platform Go binary so every installation path applies the same compatibility rule.

Status: proposed design.

## Problem

Existing installations can retain this global dependency in `~/.apm/apm.yml`:

```text
Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
```

The dependency registers telemetry hooks through APM. New releases register hooks through the
`ai-agent-telemetry` CLI instead. If the dependency remains, a later `apm compile -g` can restore the obsolete hooks and
leave both registration mechanisms active.

The cleanup must work for every command or installer that activates CLI-managed hooks. Platform-specific installers
must not implement their own APM manifest parsing.

## Selected approach

The Go CLI performs an automatic compatibility cleanup immediately before installing a nonempty set of hooks. Both
`ai-agent-telemetry configure` and `ai-agent-telemetry hooks install` use this path.

This approach establishes one invariant: installing CLI-managed telemetry hooks first removes the known APM-managed
registration when possible, then canonicalizes the requested native hook files regardless of the cleanup result.

Three alternatives were rejected:

- An explicit migration flag would require every caller to know when cleanup is necessary. Older or standalone
  installers could omit it.
- A shared script helper would remain outside the hook owner and add another platform-facing artifact to release and
  test.
- `apm deps list -g` would avoid reading the manifest directly, but its output is designed for people rather than
  machine parsing. It also requires an available APM executable before the CLI can determine whether cleanup is needed.
  Calling `apm uninstall` unconditionally is not suitable because it fails on clean systems with no manifest or package.

## Cleanup behavior

The cleanup follows these steps:

1. Read `<home>/.apm/apm.yml`.
2. Decode the top-level `dependencies` sequence as YAML.
3. Normalize each string dependency by trimming whitespace and removing an optional revision suffix that starts with
   `#`.
4. Compare the normalized value with the legacy package path using case-insensitive equality.
5. Locate `apm` on `PATH` when a matching dependency is present.
6. Run `apm uninstall -g <legacy-package>`.
7. Report any cleanup failure as a warning.
8. Install and canonicalize the requested CLI-managed hooks regardless of the cleanup result.

Plain, single-quoted, and double-quoted YAML scalars follow the same parsing path. Revision pins and trailing comments
are accepted. Near matches and values under unrelated YAML keys are ignored.

The operation is idempotent. A missing manifest or dependency is a silent no-op. `configure --hooks=none` does not run
cleanup because it does not install CLI-managed hooks.

## Failure handling

Cleanup is best effort. An unreadable or malformed manifest, a missing `apm` executable, or a failed
`apm uninstall` produces a warning and does not block hook installation. The command exit code depends on configuration
and hook installation, not cleanup. A successful hook installation therefore returns exit code `0` after any cleanup
warning.

This policy lets native hook canonicalization repair known APM entries after a partial uninstall. The remaining global
dependency can restore obsolete entries during a later `apm compile -g`, so the warning remains actionable.

All cleanup warnings go to `stderr`. A manifest read or parse warning states:

```text
Legacy APM cleanup could not verify or remove the telemetry dependency: <reason>
```

A missing-executable warning names `apm` and states that automatic cleanup could not run. An uninstall warning includes
the command context and a combined copy of the subprocess standard output and standard error. The diagnostic includes
at most 4 KiB of subprocess output and marks truncated output. Successful cleanup does not replay subprocess output.

`configure` writes machine configuration before it installs hooks. A cleanup warning does not roll back the endpoint,
token, CA, repository policy, or delivery settings, and it does not change the command exit code. A later hook failure
keeps the existing partial-result behavior: configuration remains written, and `configure` returns exit code `1`.

## Code boundaries

A focused Go source file owns legacy APM manifest detection and command execution. Its testable core takes the home
directory as a string argument plus ordinary `lookPath` and `runCommand` function values and an `io.Writer` for
warnings. Production wiring uses `os.UserHomeDir`, `exec.LookPath`, `exec.Command`, and `os.Stderr`.

`installHooks` remains responsible for harness configuration files. A small orchestration function runs cleanup and
then delegates to `installHooks`. The `configure` and `hooks install` command paths both use this function.

`gopkg.in/yaml.v3` becomes a direct Go dependency. A typed top-level manifest structure limits matching to
`dependencies` and avoids platform-specific line parsing.

The POSIX and PowerShell installers continue to install or update the telemetry binary and invoke its public commands.
They contain no legacy package constant, YAML matching, or direct `apm uninstall` call.

## Tests

Go unit tests cover:

- missing manifest and absent dependency no-ops;
- plain, quoted, revision-pinned, and commented dependency forms;
- near-match and unrelated-list rejection;
- unreadable and malformed manifest warnings;
- missing `apm` warnings;
- exact uninstall arguments;
- uninstall failure diagnostics, combined-output truncation, and `stderr` routing;
- suppression of subprocess output after successful cleanup;
- cleanup-attempt-before-hook-install ordering;
- hook installation after every cleanup warning;
- cleanup suppression for an empty hook target list.

Command tests verify that `configure` and `hooks install` use the cleanup-aware orchestration path. They also verify
that a cleanup warning preserves configuration, permits hook installation, and does not change a successful exit code.
Installer black-box tests verify component orchestration and CLI invocation without duplicating the YAML case matrix.

## Documentation

User-facing installation documentation states that installing CLI-managed hooks attempts to remove the legacy APM
telemetry package when APM is available. It also describes the warning shown when automatic cleanup cannot run.

The pull request records the user-visible problem, the centralized cleanup behavior, standalone-installer coverage,
and the verification commands.
