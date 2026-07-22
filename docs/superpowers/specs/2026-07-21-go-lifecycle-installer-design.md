# Go lifecycle installer and Cobra CLI design

## Summary

Replace the duplicated POSIX and PowerShell developer-tool installers with one Go lifecycle implementation. The
release bootstrap scripts only download, verify, execute, and remove a temporary `ai-agent-telemetry` binary. That
binary installs or uninstalls its managed copy and the complete Qubership developer baseline.

The same change migrates the full CLI to Cobra. Install, uninstall, telemetry commands, hook management, and completion
generation share one command tree, one flag parser, and one error contract.

## Goals

- Keep platform bootstrap scripts small enough to audit as delivery code.
- Implement component selection, installation, configuration, verification, uninstall, purge, and summaries once in
  Go.
- Provide one explicit update command that refreshes the managed CLI and the selected developer baseline.
- Make the managed CLI available regardless of which optional components the user selects.
- Support safe full and partial uninstall when no managed telemetry binary is present.
- Replace the handwritten command router, help registry, and inconsistent flag parsers with Cobra.
- Preserve the existing no-argument global installer instructions during the transition.
- Test lifecycle behavior without changing a developer's real home directory or global tools.

## Non-goals

- Do not add Viper or replace the existing environment and file configuration model.
- Do not use the `cobra-cli` source generator or adopt its package-level command layout.
- Do not install shell completion into user profiles.
- Do not remove the APM CLI, a shared APM marketplace registration, Java, Git, or PATH entries that the installer does
  not own.
- Do not preserve the behavior of the old telemetry-only `--force` and `--skip-config` installer options.
- Do not retain two lifecycle implementations during a transition period.

## Public commands

The Go CLI exposes this command tree:

```text
ai-agent-telemetry
├── install
├── update
├── uninstall
├── configure
├── hooks
│   ├── install
│   └── uninstall
├── status
├── selftest
├── ingest
├── flush
├── version
└── completion
    ├── bash
    ├── zsh
    ├── fish
    └── powershell
```

The lifecycle commands are:

```text
ai-agent-telemetry install [options]
ai-agent-telemetry update [options]
ai-agent-telemetry uninstall [options]
```

All three commands accept `--components <list>` and `--skip <list>`. The supported components are `apm`, `telemetry`,
and `git-hooks`; `all` selects the full set and is the default for every lifecycle command. Install and update also
accept `--harnesses`, `--force-git-hooks`, and `--non-interactive`. Uninstall accepts `--purge` and `--remove-cli`.
Harness selection and install-only options are invalid during uninstall.

Install ensures that the selected baseline exists. Update refreshes the managed CLI and every selected component;
`all` is its default selection. If a selected component is absent, update installs it. Update uses the same preflight,
harness selection, ownership checks, component ordering, and summary contract as install. There is no force-update
mode and no separate command that only checks or updates the telemetry executable.

Update also accepts `--cli-only`. This mode updates only the managed CLI and skips component and prerequisite
preflight. It is mutually exclusive with `--components`, `--skip`, `--harnesses`, `--force-git-hooks`, and
`--non-interactive`. An empty component selection through `--skip all` is invalid; `--cli-only` is the explicit
human-facing operation.

Install applies the final normalized `--harnesses` selection to both APM deployment targets and telemetry native-hook
targets. A harness excluded from the selection is not changed by either component. Uninstall removes telemetry-owned
hooks from every supported harness and therefore rejects `--harnesses` rather than leaving a selected hook active.

`--purge` is valid only when the final component selection contains `telemetry`. Combining purge with a selection that
excludes telemetry is a usage error with exit code `2` and causes no changes.

Full uninstall removes the managed CLI without requiring `--remove-cli`. A partial uninstall preserves it unless
`--remove-cli` is present. The flag is valid only when the final component selection contains `telemetry`; otherwise,
active telemetry hooks could be left without their executable. A telemetry hook cleanup failure prevents CLI removal.

The managed binary is infrastructure, not a selectable component. For example, `--components telemetry` installs the
managed CLI, configures telemetry, and installs native hooks. `--skip telemetry` leaves the managed CLI installed but
does not change telemetry configuration or hooks.

The root command and incomplete parent commands print their help and return exit code `0`. Unknown commands, invalid
arguments, and invalid option combinations return exit code `2`.

## Cobra migration

`newRootCommand(deps)` constructs a fresh Cobra tree for every invocation. Command factories receive explicit
dependencies and do not store parsed flag values in package-level variables. Tests use `SetArgs`, `SetIn`, `SetOut`,
and `SetErr` against a new tree.

Commands use `RunE` and typed errors. The entry point maps usage and validation errors to exit code `2` and operational
errors to exit code `1`. Only hook-driven ingest has an intentional never-fail contract. The root sets `SilenceUsage`
and `SilenceErrors`; the application decides when to render usage and diagnostics.

The root command and every incomplete parent, including `hooks` and `completion`, use Cobra's discovery behavior. They
print that command's help and return success. Explicit help follows the same contract. Invalid children or arguments
remain usage errors.

Flags remain local until two command families require the same option. The migration does not create persistent flags
for hypothetical future reuse. Cobra-generated help becomes canonical, and tests assert commands, options, important
diagnostics, and exit codes rather than the previous byte-for-byte layout.

Every enum-like flag lists its accepted values in generated help. Validation errors for unknown components,
harnesses, or hook targets repeat the applicable values so a caller does not need completion or source access to
recover. Help, validation, and completion consume shared ordered value lists to prevent those surfaces from drifting.

Unknown commands retain Cobra's edit-distance suggestions while preserving the single-diagnostic contract. The
application includes any suggestion in the one stderr diagnostic and does not re-enable Cobra's automatic error or
usage rendering.

The `ingest` command disables Cobra flag parsing and retains its raw-argument validator. In particular,
`ingest --agent=codex` rejects every trailing argument. This preserves the exact command prefix approved by Codex
execution policy and prevents an approved hook command from accepting an endpoint or credential redirect.

Cobra exposes completion generation for Bash, Zsh, Fish, and PowerShell. The command writes a script to stdout and
does not modify shell profiles. Component, skip, harness, `configure --hooks`, and hook-target flags register value
completion functions. Comma-separated completion preserves the already typed prefix, omits duplicate values,
suggests only valid remaining values, and disables file completion. `configure --hooks` also completes `all` and
`none`. The `completion` parent exposes shells as subcommands, so Cobra completes their names without a separate
positional-value implementation.

Viper is excluded because endpoint, token, repository policy, delivery settings, and CA configuration already have
explicit environment and file precedence. Adding implicit config discovery or environment binding would change that
contract. The `cobra-cli` generator is excluded because it is a development scaffolder, not a runtime dependency, and
its package-level pattern conflicts with fresh command trees and injected dependencies.

## Bootstrap contract

`scripts/install.sh` and `scripts/install.ps1` are the canonical bootstrap files. Each bootstrap performs only these
steps:

1. Detect the operating system and architecture.
2. Resolve the stamped or explicitly overridden release version and base URL.
3. Download the matching binary and `SHA256SUMS` into a private temporary directory.
4. Verify the binary against the checksum from the same release.
5. Execute the temporary binary with the normalized lifecycle command and original options.
6. Return the binary's exit code and remove the temporary directory.

With no arguments, the bootstrap invokes `ai-agent-telemetry install`. If the first argument is `install`, `update`,
or `uninstall`, it passes that command through. If the first argument is an option, the bootstrap prefixes `install`.
Examples:

```bash
curl -fsSL <release>/install.sh | sh
curl -fsSL <release>/install.sh | sh -s -- --components telemetry
curl -fsSL <release>/install.sh | sh -s -- update
curl -fsSL <release>/install.sh | sh -s -- uninstall --purge
```

```powershell
& { $(irm '<release>/install.ps1') }
& { $(irm '<release>/install.ps1') } --components telemetry
& { $(irm '<release>/install.ps1') } update
& { $(irm '<release>/install.ps1') } uninstall --purge
```

The bootstrap contains no component registry, prerequisite policy, install or uninstall handler, configuration logic,
or result summary. It does not invoke the managed binary. Running a temporary executable lets Go remove or replace the
managed Windows executable directly.

Download or checksum failure prevents execution. The bootstrap reports the failed delivery step without printing
response bodies or sensitive environment values. Temporary cleanup runs after success, command failure, or an
interrupt that the host shell can trap.

On POSIX systems, the bootstrap connects the temporary binary's stdin to `/dev/tty` when a controlling terminal is
available. This prevents `curl | sh` from passing the script pipe or EOF to an interactive Go prompt. If no controlling
terminal exists, the Go preflight may continue only when the selected operation needs no input. A required prompt
without a terminal fails before any managed file or component changes. PowerShell passes its console input stream to
the temporary binary and follows the same Go preflight contract.

## Managed binary lifecycle

Install copies the running temporary executable to the platform's fixed managed path:

- `~/.local/bin/ai-agent-telemetry` on POSIX systems;
- `~/.local/bin/ai-agent-telemetry.exe` on Windows.

The copy uses a same-directory temporary file, executable permissions on POSIX, and an atomic replacement. The binary
is installed before optional components so a failed component can be repaired without downloading the bootstrap
again. When the running executable already resolves to the managed path, install records the CLI as unchanged instead
of replacing the running file.

Install adds the managed directory to PATH only when necessary. On POSIX systems, it writes one marked, exact block to
the selected shell profile. On Windows, it adds one exact entry to the user PATH. A small installer-owned lifecycle
receipt beside the managed executable records which PATH mutation the installer made. Keeping the receipt outside the
telemetry configuration and cache roots prevents purge from deleting ownership evidence before CLI cleanup. The
receipt contains no credentials or telemetry settings and is written atomically. If the receipt cannot be persisted
after a new PATH mutation, install rolls back that exact mutation and reports failure.

After adding or removing the Windows user PATH entry, the CLI broadcasts `WM_SETTINGCHANGE` with `Environment` through
`SendMessageTimeout(HWND_BROADCAST, ...)`. Failure to broadcast is a lifecycle failure rather than a false success. The
CLI restores the prior registry value and broadcasts the restored state; diagnostics retain both the primary failure
and any rollback failure. Registry access and notification are injected independently in unit tests.

CLI removal deletes the fixed managed executable and reverses only the PATH mutation recorded by the receipt. It also
recognizes and removes the exact marked block written by the previous POSIX installer. Without ownership evidence, it
preserves a Windows PATH entry and any unrelated POSIX profile content. It never removes the shared `~/.local/bin`
directory, including when the directory is empty. It deletes the lifecycle receipt after successful CLI and PATH
cleanup. The receipt records PATH ownership only; it does not track directory or update-artifact ownership.

A direct update resolves the latest release, downloads the platform asset and checksums to a private temporary path,
and verifies the asset before lifecycle preflight or component changes. It reports the CLI as unchanged when the
installed version is current and runs the selected update with that version. When a newer version exists, the running
binary transfers update orchestration to the verified new binary. The new binary performs preflight, installs itself
at the managed path, updates components, prints the summary, and determines the command's exit code. The old binary
does not apply component lifecycle logic.

On POSIX systems, the old process starts the verified runner with the same standard streams, waits for it, and returns
its exit code. The runner atomically replaces the managed path before component changes. The running old image does not
block that replacement.

On Windows, the old process starts the verified temporary binary with the existing console or redirected standard
handles and waits for its exit code. The new runner parses options and completes read-only preflight before changing
the managed installation. It then stages a copy of itself on the managed volume and chooses a collision-resistant
`.ai-agent-telemetry.exe.update-old-<pid>-<nonce>` sibling path. The rename fails rather than overwriting any existing
path. After renaming the running old executable, the runner installs the staged copy at the canonical path. If
canonical installation fails, the new runner restores the original managed path before component changes and returns
code `1`. Failure to start the new runner leaves the managed installation unchanged.

The renamed Windows image cannot be deleted until the old process exits. A narrowly scoped helper removes that stale
file afterward. The runner passes the helper the one exact path created by the current update. The helper performs
bounded retries and never enumerates or matches sibling names. Helper startup failure emits a warning with the exact
leftover path. If all deletion retries fail, the helper reports that path to stderr and leaves it for manual cleanup.
Later invocations do not delete it automatically. The canonical executable and component results are already
synchronous, so stale-image cleanup is housekeeping rather than a lifecycle state. The design does not add `PENDING`,
a durable update state machine, or delayed success reporting.

A bootstrap runner is already executing the requested release. It installs itself at the managed path before running
the selected component updates and does not need version handoff.

Release packaging stamps only the bootstrap's default binary version. An explicit
`AI_AGENT_TELEMETRY_INSTALL_VERSION` value always takes precedence in both the POSIX and PowerShell bootstrap, including
in the staged release assets.

The final normalized component set, not the presence of a selection flag, determines whether uninstall is full. A set
equal to `apm,telemetry,git-hooks` is full, including `--components all` or an explicit list of all three components. A
proper subset is partial. Full uninstall removes the managed copy and its owned PATH mutation after all selected
destructive steps. Partial uninstall leaves them installed unless `--remove-cli` is present. If native telemetry hook
cleanup fails, either form preserves the managed copy and PATH mutation to avoid leaving active hooks that reference a
missing executable.

The temporary runner makes repeated hook uninstall independent of installed state. Native hook cleanup does not use
an uninstall tombstone: every invocation inspects and edits current native files directly. The lifecycle receipt is
limited to ownership of installer-created PATH changes and does not claim that hook cleanup has completed.

The documented Windows entry point for full uninstall or any use of `--remove-cli` is the bootstrap. A temporary
bootstrap runner can remove the managed Windows executable directly and report the synchronous result. The installed
Windows executable rejects either operation during preflight, before component, PATH, receipt, or data changes, and
prints the exact bootstrap command to run. Partial uninstall without `--remove-cli` remains valid from the installed
executable. POSIX systems unlink the running managed executable directly for full or explicit CLI removal.

## Component lifecycle

### APM

Install locates `apm` and invokes the official platform installer when the command is absent. Go downloads the vendor
installer to a temporary file and invokes the platform shell explicitly; vendor installation code is not copied into
this repository. A completed installer that does not make `apm` discoverable is a component failure.

Install ensures that the APM CLI, `qubership-ai-packages` marketplace, and `qubership-global-essentials` package exist.
Update refreshes the APM CLI and marketplace metadata, installs or updates the package globally for the selected
harnesses, compiles global primitives, and verifies the global dependency state.

Uninstall checks the global APM manifest before invoking `apm uninstall -g`. An absent manifest or package is
`SKIPPED`. The component preserves the APM CLI and the `qubership-ai-packages` marketplace because this installer has
no ownership evidence for either shared resource.

### Telemetry

Install applies configuration through the existing deterministic Go functions and installs native hooks for the final
normalized harness selection through the existing ownership-aware merge functions. It does not invoke the installed
CLI as a subprocess. Existing legacy APM telemetry cleanup remains an install-time best-effort compatibility
operation.

Interactive preflight resolves an endpoint from the environment or existing config. If neither contains an endpoint,
it prompts before any changes and keeps the answer in memory until the telemetry component runs. Empty input cancels
installation before changes. The token remains optional; a new interactive configuration may collect it without echo.

With `--non-interactive`, telemetry reads the endpoint and optional token from the environment and existing config and
never prompts. A missing endpoint fails the lifecycle preflight before the managed CLI or any component is changed.
The CA, repository policy, and delivery settings retain their existing configured values or defaults and do not become
new mandatory inputs.

Update preserves telemetry configuration and re-canonicalizes native hooks for the selected harnesses with the latest
managed command form. It follows the same interactive or non-interactive telemetry preflight as install.

Uninstall removes only telemetry-owned native handlers, empty telemetry-created groups that can be identified safely,
and the canonical Codex execution-policy rule. Unrelated native properties, handlers, files, and modified Codex rules
remain unchanged. A malformed native file fails that target without rewriting it; other targets still run. Codex
policy cleanup runs only after its native hook file was cleaned successfully, so a retained hook never loses its
required canonical policy during a failed uninstall.

Normal uninstall preserves telemetry configuration, credentials, repository policy, delivery settings, diagnostics,
offsets, buffered events, and machine identity. When telemetry is selected, `--purge` removes the package-specific
configuration and cache directories after successful hook cleanup. It does not remove their shared parent directories.

### Global Git hooks

Install retains the existing Git and Java 21 prerequisite policy. When `git-hooks` is selected, lifecycle preflight
checks both tools before the managed CLI or any component changes. Interactive mode asks once whether the user
installed or updated missing prerequisites and then repeats the complete check. A negative answer or failed recheck
aborts the operation without changes. `--non-interactive` fails the preflight without prompting. No Git or Java check
runs when `git-hooks` is excluded.

The component clones or updates the fixed global hooks repository, validates its origin and worktree state, manages
`core.hooksPath`, and preserves an unrelated configured path unless `--force-git-hooks` is present. A missing
`CYBER_FERRET_PASSWORD` remains a warning with configuration guidance.

Install clones the repository when it is absent and otherwise verifies the existing owned clone. Update resolves the
checked-out branch's upstream, requires that it belongs to the verified `origin`, fetches that explicit remote ref, and
fast-forwards to the fetched commit. It never relies on an unvalidated upstream through an implicit `git pull`. A
missing selected clone is installed.

Uninstall clears `core.hooksPath` only when it resolves to the managed clone. It deletes the clone only when the path,
origin, and worktree state prove ownership and no local modifications exist. A modified or ambiguous clone is
preserved and reported as a failure so the user can inspect it.

## Explicit flush behavior

`flush` is a human-invoked delivery command and reports whether the requested work completed. It opens the outbox,
acquires its lock, and lists events before validating delivery configuration. An empty locked outbox prints
`nothing to flush` and returns code `0` without resolving the endpoint or loading the configured CA. A nonempty outbox
then requires a valid endpoint and CA. Outbox open, lock, or listing failures return code `1`.

Flush returns code `0` when every queued event is delivered and removed. It returns code `1` for a missing endpoint, an
invalid CA, an unreadable or invalid queued event, a delivery or exporter shutdown failure, or failure to remove a
delivered event.

Flush never removes an event before successful delivery. Events that cannot be delivered or validated remain in the
outbox. A removal failure can cause at-least-once redelivery on the next run and is reported instead of being treated
as success. A busy flush lock returns code `1` when queued work exists because the explicit request did not run. Flush
continues past an unreadable or invalid event so that independent valid events can be delivered, then returns code `1`
with a bounded diagnostic summary for the retained failures.

The opportunistic flush performed by `ingest` remains fail-open as part of the hook command's never-fail contract.

## Ordering, results, and errors

Install, update, and uninstall parse and normalize selections before entering a read-only preflight. Install preflight
checks selected Git and Java prerequisites, resolves required telemetry input, and confirms terminal availability when
a prompt is required. A direct update verifies and transfers control to the latest binary before the new binary runs
the selected component preflight. CLI-only update skips component preflight. A preflight failure returns code `1`
before changing configuration or invoking a component installer.

After successful preflight, install processes selected operations in this order: managed CLI, APM, telemetry, and
global Git hooks. Update installs or confirms the managed CLI first, then uses the same component order. The verified
new binary orchestrates every selected update operation. Uninstall processes component cleanup before managed CLI and
PATH removal. Within a component, dependency-sensitive destructive steps retain their safe order, such as native hooks
before binary removal and Git configuration before clone deletion.

Component failures do not prevent independent later components from running. The summary reports the managed CLI and
each selected component as `OK`, `SKIPPED`, or `FAILED`. Exit codes are:

- `0` when every selected operation is `OK` or `SKIPPED`;
- `1` when an operational or component failure occurs;
- `2` when command syntax, option combinations, or selections are invalid.

Completed steps are not rolled back. Diagnostics name the component, failed operation, and actionable next step. A
failed subprocess diagnostic includes bounded combined output. Warnings and errors go to stderr; summaries and normal
command output go to stdout. Secrets and full downloaded response bodies are never included.

## Compatibility and release assets

The canonical documentation points only to `install.sh` and `install.ps1`. The first release containing this
architecture also publishes the old no-argument global installer URLs:

- `global-scripts/qubership-dev-install.sh` is a source symlink to `../scripts/install.sh`;
- `global-scripts/qubership-dev-install.ps1` is a one-line forwarding stub because Git symlink checkout is unreliable
  on Windows;
- release assets named `qubership-dev-install.sh` and `qubership-dev-install.ps1` are byte-identical copies of the
  canonical bootstrap assets.

The forwarding stub contains no parsing or lifecycle behavior. Release checks compare the old and canonical asset
bytes. Removing the compatibility names requires a later PR with a documentation update; this change does not assign
an automatic expiration date.

The new documentation uses lowercase, double-dash, kebab-case options on every shell. Old telemetry-only options and
PowerShell named-parameter spellings do not retain behavior. Known legacy tokens fail with an actionable replacement,
for example:

```text
--force is no longer supported; use update --components telemetry
--skip-config is no longer supported; use --skip telemetry
-ForceUpdate is no longer supported; use update
```

Unknown options use Cobra's standard validation. No compatibility flag is silently translated to a different
lifecycle operation.

The removed `update-check` and `self-update` commands are hidden diagnostic commands rather than runtime aliases.
They return usage exit code `2` and direct the user to `update` and `update --cli-only`, respectively. They do not run
preflight, downloads, lifecycle operations, or any other side effect.

The release workflow stamps both bootstrap files with the release version, publishes the existing platform binary
matrix, publishes canonical and compatibility bootstrap names, and includes every asset in `SHA256SUMS`.

## Code boundaries

Lifecycle code uses focused units with explicit dependencies:

- command factories define Cobra syntax and translate flags into domain options;
- a lifecycle orchestrator handles selection, ordering, continuation, and summaries;
- a managed-binary service owns fixed-path installation and removal;
- APM, telemetry, and Git-hook components implement a common lifecycle result contract;
- download, subprocess, filesystem, user interaction, environment, and platform-path adapters isolate side effects.

Function values and small structs are preferred over one-method interfaces. Interfaces are introduced only when a
component has multiple implementations or a stable behavioral boundary. Platform-specific code uses Go build-tagged
files when behavior depends on operating-system APIs; ordinary path differences use `runtime.GOOS` and tested helper
functions.

Existing deterministic telemetry functions remain independent of Cobra. Command factories call application services;
they do not contain hook merges, config writes, package detection, or Git ownership rules.

## Testing

### Go tests

- Construct every command from `newRootCommand(deps)` and verify routing, help, input/output streams, flag validation,
  and exit-code classification.
- Preserve the exact Codex ingest argument restriction and never-fail hook behavior.
- Verify that explicit `flush` failures return code `1`, retain undelivered or invalid events, and report actionable
  diagnostics for endpoint, CA, locking, read, validation, delivery, shutdown, and removal failures.
- Verify that an empty locked outbox prints `nothing to flush` and returns code `0` without resolving an endpoint or
  loading CA configuration.
- Cover component and harness list normalization, duplicates, `all`, exclusions, invalid combinations, and partial
  selections.
- Assert that help and unknown-value diagnostics enumerate the accepted component, harness, and hook-target values
  from the same ordered lists used by completion.
- Cover `update --cli-only`, reject its incompatible options, reject `--skip all`, and prove that CLI-only update does
  not run telemetry, Git, Java, APM, or component preflight.
- Verify that `--components all` and an explicit complete set trigger full uninstall. Verify that every proper subset
  preserves the managed CLI unless valid `--remove-cli` is present.
- Reject `--purge` when the final selection excludes telemetry before any lifecycle side effect.
- Reject `--remove-cli` when the final selection excludes telemetry, and preserve the CLI and PATH when telemetry hook
  cleanup fails.
- Apply harness selection identically to APM targets and telemetry hooks, and preserve every excluded harness file.
- Verify that root, `hooks`, and `completion` without a child print the relevant help and return code `0`. Unknown
  commands, extra arguments, and invalid options return code `2`.
- Verify actionable code-2 diagnostics for the removed `update-check` and `self-update` commands without invoking any
  lifecycle dependency. Verify that a close command typo returns Cobra's suggestion in the single stderr diagnostic.
- Cover interactive and non-interactive telemetry preflight, optional token handling, missing endpoint failure, and no
  writes before preflight succeeds.
- Cover Git and Java preflight before managed CLI, APM, telemetry, or Git-hook changes.
- Cover managed binary install, replacement, permission failure, full removal, explicit partial removal, and
  telemetry-cleanup failure preservation.
- Cover owned POSIX profile and Windows user-PATH addition and removal, receipt persistence, rollback after receipt
  failure, exact legacy POSIX block removal, unrelated entry preservation, repeated uninstall, and unconditional
  preservation of the managed directory.
- Reject direct Windows full uninstall and `--remove-cli` before any side effect, and include the exact bootstrap
  command in the diagnostic. Cover direct POSIX unlink and removal by a temporary bootstrap runner on both platforms.
- Cover direct update handoff to a verified new binary and prove that the old binary never invokes component lifecycle
  operations. Verify inherited standard streams, new-version preflight and migration behavior, child exit-code
  propagation, and no handoff when the installed version is current.
- Cover Windows same-volume staging, running-image rename, canonical installation, child startup, rollback before
  component changes, collision refusal, exact-path helper invocation, bounded deletion retries, and leftover-path
  diagnostics. Verify that later invocations never scan or delete stale-looking siblings and no `PENDING` state or
  durable update state is written.
- Cover fresh install, update, repeated install, missing dependency, malformed state, and subprocess failure for every
  component. Verify that update refreshes each selected existing component and installs each selected missing
  component.
- Cover full uninstall, repeated uninstall, partial uninstall, purge, ownership ambiguity, and partial result
  summaries.
- Verify that telemetry uninstall removes owned entries and preserves unrelated native content.
- Verify APM manifest states and preservation of the CLI and marketplace.
- Verify Git clone origin, worktree, global configuration, Java version, and password-warning policies.
- Generate Bash, Zsh, Fish, and PowerShell completion output without modifying user files. Exercise Cobra's
  `__complete` path and assert actual component, skip, harness, `configure --hooks`, hook-target, and shell candidates.
  Cover comma-separated prefixes, duplicate suppression, invalid prefixes, and the no-file-completion directive.
- On POSIX, compare symlink-target receipts with a `filepath.EvalSymlinks`-canonicalized expected path so the suite
  accepts macOS's `/var` to `/private/var` filesystem alias without weakening the production ownership check.

Tests use temporary home and XDG roots, fake executables, local HTTP servers, injected stdin, and captured stdout and
stderr. They never mutate the developer's global APM manifest, Git configuration, native harness files, or managed
binary.

### Bootstrap and integration tests

- Serve local release fixtures and verify OS/architecture asset selection and checksum enforcement.
- Stage release installers with a stamped default version, then verify that
  `AI_AGENT_TELEMETRY_INSTALL_VERSION` still takes precedence for both POSIX and PowerShell assets.
- Verify no-argument defaulting to `install`, explicit `install`, `update`, and `uninstall`, option-prefix defaulting,
  exact argument forwarding, exit-code propagation, and temporary cleanup.
- Pipe a locally served bootstrap through `curl | sh` under a pseudoterminal, answer a Go prompt through the controlling
  terminal, and verify the runner receives the answer instead of script-pipe input or EOF.
- Verify that piped execution without a controlling terminal fails before changes when input is required and succeeds
  when existing state satisfies every required input, including under `--non-interactive`.
- Run POSIX bootstrap tests on Ubuntu and macOS.
- Run PowerShell bootstrap tests under Windows PowerShell 5.1 and PowerShell 7.
- Verify full Windows uninstall and `--remove-cli` through the temporary runner, including synchronous managed-file,
  PATH, and receipt removal.
- Run a Windows console integration test in which direct update hands a prompt from the installed old binary to the
  new temporary runner and propagates the runner's output and exit code.
- Verify the compatibility shell symlink, PowerShell forwarding stub, and byte-identical release assets.
- Cross-build every supported target and run existing global-hook smoke tests on macOS and Windows.

### Final lifecycle regression coverage

- Verify Windows user PATH addition and removal broadcast `WM_SETTINGCHANGE`; notification failure restores the prior
  registry value and reports rollback failures without claiming success.
- Verify Git-hook update rejects a clean managed clone whose current branch tracks a remote other than the validated
  `origin`, without fetching or changing hook content.
- Verify Codex uninstall preserves the execution-policy rule when native hook-file cleanup fails.
- Verify `none` is an exclusive `configure --hooks` completion result for both `none` and `none,` prefixes.

### Test migration

The implementation removes tests that assert deleted contracts instead of keeping compatibility branches to satisfy
them. Replace those tests with the new command-level and component-level coverage described above:

- remove handwritten-router and byte-for-byte help tests after Cobra help becomes canonical;
- replace `update-check`, `self-update`, and `--force-update` tests with unified `update` routing, selection, download,
  checksum, new-version handoff, CLI-only, replacement, and component-refresh tests;
- replace incomplete-parent exit-code `2` assertions with help-and-success assertions;
- replace flush never-fail assertions with strict explicit-flush failure and outbox-retention assertions, while
  retaining fail-open tests for ingest and its opportunistic delivery;
- replace tests that preserve installer-owned PATH mutations with ownership, receipt, removal, and unrelated-content
  preservation tests;
- replace empty managed-directory deletion tests with unconditional directory-preservation assertions;
- replace wildcard or later-invocation stale-image cleanup tests with exact current-helper target, retry, collision,
  and unrelated-sibling preservation tests;
- retain Windows full-uninstall rejection coverage for direct managed execution and replace post-exit uninstall-helper
  tests with synchronous temporary-runner removal tests;
- delete duplicated POSIX and PowerShell component-behavior tests when equivalent Go service tests cover the same
  policy, but retain bootstrap transport, quoting, stdin, checksum, platform selection, forwarding, and cleanup tests;
- strengthen completion tests from nonempty-script checks to candidate and directive assertions, while retaining one
  generation smoke test for every supported shell.

Deleting an obsolete test is valid only when its behavior is intentionally removed above or equivalent coverage moves
to the Go lifecycle suite. The implementation plan must map each deleted installer test to its replacement or state
that the old contract no longer exists.

## Documentation and rollout

Update the root README, CLI reference, global installer guide, release contract, and agent configuration guidance to
use the canonical bootstrap and Cobra commands. Explain that the bootstrap always installs the CLI and that
`telemetry` selects configuration and hooks rather than the executable.

Documentation must present `install`, `update`, and `uninstall` as the complete lifecycle. Remove examples and option
tables for `update-check`, `self-update`, `--force-update`, and PowerShell-style named parameters. Document that update
refreshes all selected components and installs selected missing components. Include CLI-only update examples and its
option restrictions. Document full uninstall, partial uninstall, `--remove-cli`, `--purge`, owned PATH cleanup, and
data preservation. State that uninstall preserves `~/.local/bin` even when it is empty. Windows instructions must
distinguish direct update handoff from bootstrap-required CLI removal, describe manual cleanup diagnostics for a rare
stale-image deletion failure, and include copyable bootstrap commands in both uninstall cases.

Completion documentation must show generation commands for all four shells and explain that generation does not edit
profiles. Flush documentation must distinguish strict explicit delivery from fail-open hook ingestion and state that
an empty outbox is a successful no-op while failed delivery retains queued data. Release and migration notes must call
the removed commands and flags breaking changes and point to `update` rather than carrying runtime aliases.

The CLI reference must list `hooks uninstall`, document its target values, and include a removal example.

The new PR starts from `main` and contains the complete architecture, including uninstall and purge. It does not
depend on the draft uninstall PR. After the new PR reaches functional parity and passes CI, close the draft PR as
superseded and link to the replacement.

## Rejected alternatives

- Keeping lifecycle logic in both POSIX and PowerShell preserves duplicate behavior and test matrices.
- Installing the managed binary before invoking it requires a platform helper or bootstrap logic to delete a running
  Windows executable.
- Updating external components before handing control to the downloaded release would apply old lifecycle migrations.
- Reporting direct Windows CLI removal as successful after starting a deletion helper would make its summary and exit
  code unverifiable. The bootstrap provides synchronous removal without a persistent launcher.
- Tracking asynchronous Windows cleanup as `PENDING` or in the lifecycle receipt would add durable state for a
  noncanonical stale file. The current update helper already knows the exact path it created and can leave a failed
  cleanup for explicit manual removal.
- Publishing a separate `qubership-dev` manager introduces another release artifact and version lifecycle without a
  distinct product boundary.
- Migrating only new commands to Cobra leaves two routers, two help systems, and inconsistent flag behavior.
- Adding Viper changes configuration precedence without solving a requirement in this migration.
- Using `cobra-cli` scaffolding introduces package-level command state that conflicts with isolated command tests.
