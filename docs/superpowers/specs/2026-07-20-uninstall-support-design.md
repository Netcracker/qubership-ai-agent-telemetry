# Telemetry and developer-tool uninstall design

`ai-agent-telemetry` gains an ownership-aware hook removal command and a receipt that records complete hook removal.
The Qubership developer installers gain an uninstall mode that composes this command with removal of the managed APM
package and global Git hooks.

Status: implemented design.

## Problem

The telemetry CLI can install or repair global Claude, Codex, and Cursor hooks, but it cannot remove them. The
Qubership developer installers can install three workstation components, but they have no inverse operation.

Manual removal is unsafe because the native harness files can contain unrelated user settings and hooks. A complete
developer-tool cleanup also spans several owners: APM manages package deployments, the telemetry CLI manages native
telemetry hooks, and the bootstrap manages a global Git hooks clone and `core.hooksPath`.

The uninstall path must remove Qubership-managed behavior without deleting shared tools or unrelated user data. It
must also offer an explicit purge mode for telemetry credentials and buffered events.

## Selected approach

Extend the existing lifecycle entry points:

```text
ai-agent-telemetry hooks uninstall [--target=<claude,codex,cursor>]

qubership-dev-install.sh --uninstall [--purge] [--components <list>] [--skip <list>]
qubership-dev-install.ps1 -Uninstall [-Purge] [-Components <list>] [-Skip <list>]
```

The Go CLI remains the only implementation that edits native harness JSON. Each global installer adds uninstall
handlers to its existing component registry and retains the current independent-component execution model.

Three alternatives were rejected:

- Separate `qubership-dev-uninstall` scripts would duplicate option parsing, component metadata, summaries, release
  assets, and cross-platform tests.
- A top-level `ai-agent-telemetry uninstall` command would couple the telemetry binary to APM and the unrelated global
  Git hooks repository. It would also require the running Windows executable to remove itself.
- Shell and PowerShell implementations of native hook removal would duplicate ownership and JSON merge rules already
  enforced by the Go CLI.

## Telemetry hook uninstall

`ai-agent-telemetry hooks uninstall` removes telemetry-owned entries from every supported harness by default. An
explicit `--target` accepts the same comma-separated subset as `hooks install`. The values `all` and `none` remain
invalid with this flag; omitting the flag selects all targets.

For Claude and Codex, the command removes owned handlers from the supported telemetry event groups. For Cursor, it
removes owned entries from the supported telemetry event arrays. Ownership uses the same rules as installation:

- the canonical `ai-agent-telemetry ingest` command;
- a recognized legacy telemetry command for Codex or Cursor;
- the `_apm_source: ai-agent-telemetry` marker.

The merge removes empty telemetry-created groups and event arrays when it can identify them without ambiguity. It
preserves unrelated handlers, groups, events, root properties, and JSON files. An otherwise empty native file can
therefore remain after uninstall; the command does not infer ownership of the whole file.

Codex installation also creates `~/.codex/rules/ai-agent-telemetry.rules`. Uninstall deletes this dedicated file when
its contents match the canonical telemetry execution policy. If the file differs, uninstall preserves it and emits a
warning because the user may have modified it. Missing native files and a missing Codex rule are silent no-ops.

After successfully removing all supported targets, the CLI atomically writes a versioned tombstone receipt at
`<state-base>/ai-agent-telemetry/hooks-uninstalled`. The state base is `$XDG_STATE_HOME` when set and
`~/.local/state` otherwise on every platform. The receipt contains no credentials, paths, hook contents, or event
data. Version 1 has this exact content:

```text
version=1
state=uninstalled
```

Missing or different content is not valid proof of cleanup. A full uninstall fails if it cannot persist the receipt
because the global bootstrap could not safely recognize the result after deleting the binary.

Any command that attempts to install a nonempty hook target set removes the receipt before changing native files. This
includes both `configure` and `hooks install`, so standalone and global installation paths cannot leave a stale
uninstalled receipt. A subset uninstall does not create the receipt. A request that covers the complete canonical
target set can create it after every target succeeds.

Receipt invalidation is a fail-closed prerequisite for hook installation. A missing receipt is a successful no-op. Any
other removal error exits with code `1` before legacy APM cleanup or native hook updates begin. `configure` does not
roll back machine settings written before the hook phase, but the failed invalidation prevents it from changing any
native hook file. This ordering ensures that a receipt that could not be invalidated can never authorize a later
binary-free uninstall.

Every native file update remains atomic. Running uninstall repeatedly produces no additional changes. A malformed or
structurally invalid native file fails that target without rewriting it. The command continues with the other
requested targets and exits with code `1` if any target fails. Argument errors exit with code `2`.

Hook uninstall does not inspect or change APM dependencies. Automatic legacy APM cleanup remains an install-time
compatibility operation. The global developer uninstall owns removal of its current APM package.

## Global installer command contract

The existing POSIX and PowerShell scripts add `--uninstall` or `-Uninstall`. All components remain selected by
default, and `--components` and `--skip` retain their existing selection behavior.

`--purge` or `-Purge` requires uninstall mode. It adds destructive removal of telemetry configuration and cached
events. It does not broaden ownership checks for shared paths, APM marketplaces, or modified Git repositories.

Harness selection is not supported in uninstall mode. Removing the telemetry binary while leaving selected native
hooks would create broken commands, while APM package uninstall already applies across every deployed target. An
explicit `--harnesses` or `-Harnesses` therefore exits with code `2` before changing any component.

Install-only options such as force update, force Git hooks replacement, and non-interactive prerequisite handling are
also invalid in uninstall mode. Help documents the two modes and their valid options.

The uninstall lifecycle runs selected components independently. A component failure does not prevent later
components from running. The summary reports `OK`, `SKIPPED`, or `FAILED` for each selected component. Exit codes are:

- `0` when every selected component is `OK` or `SKIPPED`;
- `1` when at least one selected component fails;
- `2` for an invalid option or selection.

Uninstall does not require Java. The `git-hooks` component still requires Git because it must inspect global Git
configuration and validate repository ownership.

## Component removal

### APM baseline

The APM component runs:

```text
apm uninstall -g qubership-global-essentials@qubership-ai-packages
```

Before invoking APM, the component checks for `~/.apm/apm.yml`. A missing global manifest proves that no global package
entry remains and marks the component as `SKIPPED`. When the manifest exists, a missing APM command or failed uninstall
marks the component as failed. APM treats an absent package in an existing manifest as a successful no-op.

APM owns removal from the global manifest, lockfile, module tree, deployed primitives, and native hooks. The bootstrap
does not parse these files or run `apm compile -g` after uninstall. Other selected components continue after an APM
failure.

Both normal uninstall and purge preserve the `qubership-ai-packages` marketplace registration. The existing installer
did not record whether the registration predated the bootstrap, and an installer-owned receipt would still not prove
that no other global package uses it. Users who have verified that the marketplace is unused can remove it explicitly
with `apm marketplace remove qubership-ai-packages --yes`.

The bootstrap never removes the APM CLI. It can predate the bootstrap or come from Homebrew, pip, Scoop, or another
installation method, so the current installation has no reliable ownership evidence.

### AI agent telemetry

The telemetry component resolves the managed binary under `~/.local/bin` first, then falls back to an
`ai-agent-telemetry` command on `PATH`. It invokes `hooks uninstall` for all harnesses before deleting any binary.

If hook removal fails, the component preserves the binary so the user can repair the native files and retry. If no
telemetry command is available, a valid hook-removal receipt proves that cleanup already succeeded. Without a receipt,
the component fails when any native harness hook file or Codex rule exists rather than assuming it contains no
telemetry entry. If none of those files exists, the script atomically writes the same version 1 receipt and continues.

After successful hook removal, the component deletes only the fixed release binary owned by this repository:

- `~/.local/bin/ai-agent-telemetry` on POSIX systems;
- `~/.local/bin/ai-agent-telemetry.exe` on Windows.

A telemetry command resolved elsewhere on `PATH` can perform hook cleanup, but the bootstrap does not delete that
external executable.

Normal uninstall preserves `<config-base>/ai-agent-telemetry` and `<cache-base>/ai-agent-telemetry`. Purge deletes both
package directories after successful hook removal. This removes the endpoint, token, CA certificate, repository
policy, machine ID, delivery settings, offsets, diagnostics, and buffered events. It preserves the parent XDG or
home-relative directories.

Purge preserves the hook-removal receipt. This nonsensitive tombstone is the minimum state required to distinguish a
completed uninstall from native files that still need inspection when the binary is gone.

The bootstrap does not remove `~/.local/bin` from PATH or edit shell profiles. The directory is shared, and existing
installations do not record whether the PATH entry was added for telemetry or another tool.

### Global Git hooks

The Git hooks component computes the same managed clone and `hooks-global` paths as installation. It unsets global
`core.hooksPath` only when the current value resolves to that managed hooks directory. An absent value or an unrelated
value is preserved.

The component deletes the clone only when it is a Git worktree with the configured repository as its `origin` and has
no tracked or untracked changes. An unexpected directory, unexpected origin, or local change marks the component as
failed and preserves the directory. Purge does not override these checks.

Configuration deactivation and clone validation are independent. If `core.hooksPath` points to the managed directory,
the component unsets it even when the clone cannot be deleted. This prevents Git from continuing to execute a broken
or untrusted managed path while preserving files that require manual review.

## Failure handling and diagnostics

Warnings and component errors go to `stderr`; lifecycle progress and the final summary retain their existing streams.
Diagnostics name the component, failed operation, and preserved path. Commands do not replay successful subprocess
output beyond the lifecycle messages already shown by the installers.

Destructive steps follow dependency order. Native hooks are removed before the telemetry binary, and the binary is
removed before optional telemetry data. Git configuration is deactivated before the managed clone is deleted. A
failure stops later destructive steps within that component but does not roll back earlier successful steps.

The scripts are idempotent. Missing managed binaries, directories, package entries, and configuration values are
successful no-ops when the remaining state can be verified safely. The hook-removal receipt and the global APM
manifest precheck make repeated telemetry and APM removal successful without parsing native JSON or APM YAML in the
platform scripts.

## Code boundaries

The Go hook layer adds removal merge functions beside the existing install merge functions and reuses ownership
predicates, atomic file updates, target parsing, and result aggregation. A focused state helper owns receipt path
resolution, atomic writes, validation, and invalidation. Command routing and help distinguish the `install` and
`uninstall` actions without adding a top-level uninstall command.

Each global script extends its component registry with uninstall handlers. Shared selection, result recording, and
summary logic remain common to install and uninstall modes. Platform-specific filesystem and PATH handling stays in
the corresponding script.

The standalone telemetry release installers remain installation-only. The global developer installers delete the
known telemetry release binary after the Go CLI has removed hooks.

## Tests

Go tests cover:

- parsing and help for `hooks uninstall` and target subsets;
- canonical, legacy, and APM-marked entry removal for every harness;
- preservation of unrelated handlers, groups, events, properties, and files;
- removal of empty telemetry-created containers;
- exact-match Codex rule deletion and modified-rule preservation warnings;
- full-uninstall receipt creation, validation, and install-time invalidation;
- receipt write failure handling and subset-uninstall suppression;
- fatal invalidation failure before legacy APM cleanup or native hook updates;
- preservation of already-written `configure` settings after an invalidation failure;
- missing-file and repeated-uninstall no-ops;
- malformed and structurally invalid JSON without mutation;
- continuation across target failures and aggregate exit status;
- confirmation that hook uninstall does not invoke legacy APM cleanup.

POSIX and PowerShell black-box tests cover:

- uninstall and purge option validation;
- component and skip selection, ordering, summaries, and exit codes;
- rejection of explicit harness and install-only options in uninstall mode;
- telemetry hook removal before binary deletion;
- binary preservation after hook failure;
- fixed-path binary deletion without deleting an external command;
- missing-binary handling with a valid receipt and without proof of hook removal;
- normal preservation and purge removal of telemetry config and cache;
- purge preservation of the hook-removal receipt;
- missing-manifest and missing-package APM no-ops;
- APM package removal and marketplace preservation in both modes;
- preservation of the APM CLI and shared PATH entries;
- exact managed `core.hooksPath` deactivation;
- clean expected-origin clone removal;
- preservation and failure reporting for modified or unexpected Git hooks directories;
- uninstall operation without Java;
- independent component continuation after a failure;
- repeated uninstall and purge runs.

## Documentation and release scope

`global-scripts/README.md` documents uninstall examples, purge data loss, preserved shared tools and marketplace
registration, the hook-removal receipt, ownership failures, and retry guidance. CLI help documents `hooks uninstall`
and its target behavior.

The existing release workflow continues to publish the two global scripts. No additional release asset or standalone
uninstaller is introduced.
