# Cline hook installation and removal research

Research snapshot from August 7, 2026. This document compares ownership and lifecycle strategies for Cline file hooks.
It supports [ADR 0008](../../adr/0008-cline-hook-installation-and-removal.md).

## Summary

Cline discovers one canonical file for each event in each hook directory. On POSIX systems, the `PostToolUse` file is
extensionless and executable. On Windows, the file is `PostToolUse.ps1`. Cline can run hooks from global and workspace
directories together, but it does not discover multiple arbitrarily named files for one event in a single directory.

The community integrations reviewed here use a single owned event file. They create or refresh the file when they can
prove ownership and preserve an existing foreign file. Their ownership checks range from a marker substring to strict
script-shape validation. None of the reviewed Cline installers stores a content hash.

The selected approach for `ai-agent-telemetry` is stricter:

- create the canonical hook only when its path is free;
- treat an exact current template as an idempotent installation;
- never rewrite another existing file during installation;
- remove only an exact current or explicitly supported legacy template;
- preserve every unknown file, symbolic link, and non-regular file;
- use the ownership comment only to classify a modified managed hook as incomplete cleanup;
- preserve an unknown file without that comment as user-owned content and continue uninstall;
- do not parse shell or PowerShell, store a hash receipt, or install a dispatcher.

The marker never authorizes deletion. A user who keeps custom hook commands removes the telemetry invocation and marker
manually, then reruns uninstall. The second run preserves the remaining file and completes telemetry removal.

## Cline's hook model

Cline's hook discovery code resolves one canonical path for every event in every configured hook directory. It looks
for an extensionless event name on POSIX systems and `<Event>.ps1` on Windows. See Cline's
[`refreshHooks.ts`](https://github.com/cline/cline/blob/7348ba18478d34f3369116dba64b0659d912ac4d/apps/vscode/src/core/controller/file/refreshHooks.ts).

Cline can combine matching hooks from its global and workspace directories. Its `CombinedHookRunner` sends the same
JSON payload to each process, runs them concurrently, and combines their results. Cancellation is a logical OR,
`contextModification` values are joined with blank lines, and error messages are joined with newlines. Each process has
a 30-second timeout. See Cline's
[`hook-factory.ts`](https://github.com/cline/cline/blob/7348ba18478d34f3369116dba64b0659d912ac4d/apps/vscode/src/core/hooks/hook-factory.ts).

This native composition does not provide multiple global files for one event. A third-party global integration must
either own the canonical file, use a different Cline scope, or introduce its own composition layer.

## Project history and defect

[ADR 0007](../../adr/0007-cline-harness-support.md) selected a global `PostToolUse` file and exact-content ownership.
The initial implementation preserved a modified file during uninstall but returned success. The lifecycle could then
remove the managed CLI, its owned `PATH` entry, and, with `--purge`, telemetry data. The remaining hook continued to
invoke the missing CLI and suppressed the failure.

The defect is recorded in
[issue 57](https://github.com/Netcracker/qubership-ai-agent-telemetry/issues/57) and the original
[post-merge review](https://github.com/Netcracker/qubership-ai-agent-telemetry/pull/39#discussion_r3731421475).
[Pull request 59](https://github.com/Netcracker/qubership-ai-agent-telemetry/pull/59) fixed the reported reproduction by
tokenizing POSIX shell and PowerShell-like input to detect a remaining telemetry invocation.

That parser is not a complete parser for either language. Adding more token rules can reduce known false negatives but
cannot establish correctness for shell aliases, functions, variables, `eval`, nested command strings, PowerShell AST
forms, or future syntax. The parser also has no role in telemetry ingestion. Its only purpose is to decide whether
uninstall may delete the CLI. That is too much language-specific machinery for an ownership check.

## Community implementations

### nah

[`nah`](https://github.com/manuelschipper/nah/blob/742ab24d399c157195096ca6051d8fc32c23e7c3/crates/nah-cli/src/commands/cline_installation.rs)
installs `PreToolUse` in both the extension and CLI global directories. It locks installation, rejects symbolic links,
and performs atomic replacement.

`nah` recognizes ownership by validating the complete script shape. It checks the marker, expected lines, command
form, binary suffix, and optional failure-policy argument. Installation rejects an unowned file. Uninstall checks both
destinations before deleting either one, so one conflict prevents partial removal.

This is stricter than a marker check, but it is still a small parser for generated script forms. It accepts intended
variations such as the executable path and failure mode.

### numbat

[`numbat`](https://github.com/perplexityai/numbat/blob/ee5728a4e3406c4f32a2a05a00681153816ca2d3/internal/hook/install_cline.go)
installs canonical files for several Cline events. Before writing, it checks every destination and refuses the whole
installation if any existing file is not owned. Writes are atomic.

The shared
[`textHookFileOwnership`](https://github.com/perplexityai/numbat/blob/ee5728a4e3406c4f32a2a05a00681153816ca2d3/internal/hook/install.go#L1440-L1458)
helper treats a file as owned when its contents contain both required marker strings. Uninstall removes every matching
file and leaves nonmatching files in place.

This approach is simple, but a rewritten file that retains both markers is still considered removable.

### agentlogs

[`agentlogs`](https://github.com/agentlogs/agentlogs/blob/466af1b68c50b53752cda7584c383cccf41d94b6/packages/cli/src/commands/cline/install.ts)
creates canonical `PostToolUse`, `TaskComplete`, and `TaskCancel` files. If a destination contains
`# agentlogs-managed`, installation overwrites it. Otherwise, installation skips the file.

No Cline uninstall implementation was present at the reviewed revision. The ownership marker controls updates only.

### Rampart

[`Rampart`](https://github.com/peg/rampart/blob/4b203701e69fb18169be9f111202d92739295360/cmd/rampart/cli/cline.go)
installs canonical `PreToolUse` and `PostToolUse` files. It rejects symbolic links and foreign files, writes atomically,
and can migrate an older directory-based layout. Its `--force` option does not overwrite another owner's hook.

Rampart recognizes a current hook through a managed marker plus the `hook --format cline` invocation. Uninstall removes
matching files and reports nonmatching files as skipped. A user-edited file can still be removed if those two strings
remain.

### Tokenjuice beta integration

The
[`tokenjuice` Cline guide](https://github.com/vincentkoc/tokenjuice/blob/49bdcf1755833ff1e02e44e6e7fe91c0fb44c16e/docs/cline-integration.md)
describes an arbitrarily named script followed by manual registration in the Cline Hooks UI. The reviewed upstream
Cline discovery code does not support arbitrary filenames in the file-hook directories. This beta guide is not a
reliable lifecycle model without a version-pinned runtime test.

## Ownership strategies

### Exact generated content

The installer keeps the current template and any explicitly supported legacy templates in code. It compares the file
bytes directly.

Advantages:

- no parser or external state;
- a user edit always prevents automatic deletion;
- behavior is deterministic on POSIX and Windows;
- a template can be reviewed and tested as a complete artifact.

Costs:

- harmless changes such as a comment or line-ending conversion require manual cleanup;
- supported legacy versions must remain explicit;
- an unknown file cannot be classified as modified managed content or foreign content.

This is the selected ownership test.

### Marker-based ownership

A comment or inert command argument identifies the producer. `numbat`, `agentlogs`, and Rampart use variants of this
approach.

Markers make upgrades easy, but they do not prove that the rest of the file is unchanged. Deleting any file that still
contains a marker can remove user logic added after installation. A marker remains useful as a warning to users, but it
is not sufficient authorization for deletion.

### Structural script recognition

The installer recognizes an allowlist of script forms. `nah` uses this approach.

Structural recognition can accept path and version differences without requiring byte equality. It also introduces a
language-specific recognizer whose accepted grammar must remain smaller than the shell grammar. The complexity is not
justified for a three-line telemetry hook.

### Content hash receipt

The installer computes a digest when it creates the hook and stores that digest in a sidecar receipt. Uninstall hashes
the file again and removes it only when the values match.

For this small generated file, a digest answers the same question as direct byte comparison. It adds receipt creation,
atomicity, migration, corruption, and loss cases. An inline hash cannot cover the whole file without defining an
exclusion and parsing rule. This option is rejected.

### Ownership receipt without a hash

A sidecar receipt can record that telemetry created a path. It helps distinguish a later replacement from a file that
blocked installation from the start. It still cannot authorize deletion after the contents change, so exact comparison
remains necessary.

The receipt reduces some false-positive cleanup failures but adds persistent state and reconciliation rules. The chosen
policy accepts manual resolution instead.

### Dispatcher and child hook directory

A canonical root hook can dispatch the input to files such as `99-ai-agent-telemetry` in a child directory. Removing
the telemetry child leaves the root dispatcher in place and allows other children to continue.

A general dispatcher must implement Cline's hook contract, not just execute files:

1. Read the JSON input once and replay it to every child.
2. Capture stdout separately for every child.
3. Parse each child response as JSON.
4. Combine cancellation, context modifications, and errors.
5. Define ordering or concurrency, timeouts, malformed-output handling, and failure policy.
6. Provide equivalent behavior on POSIX and Windows.

Bash and PowerShell wrappers are poor places for this protocol. Putting the dispatcher in `ai-agent-telemetry` makes
the telemetry binary a permanent dependency of all child hooks. Uninstall can no longer remove the binary while the
dispatcher remains active. A separate dispatcher executable avoids that dependency but creates a new product component
with its own installation, update, ownership, security, and removal lifecycle.

No reviewed Cline community integration implements a shared `.d`-style dispatcher. The approach solves composition by
turning telemetry into a hook platform. It is rejected for this project.

### Native composition across Cline scopes

Cline already combines global and workspace hooks. A project can coexist with telemetry by placing its hook in a
workspace directory while telemetry owns the global file. This does not solve conflicts between two global
integrations, and execution across directories is concurrent rather than ordered.

The installer should not move a third-party global hook into a workspace or choose a workspace on the user's behalf.

## Selected lifecycle

### Installation

The installer uses exclusive creation for the canonical global path. The current exact template is an idempotent
success. Every other existing file, symbolic link, or non-regular entry is preserved and reported as a conflict.
Installation does not rewrite a supported legacy template; uninstall and reinstall perform that migration explicitly.

The generated comments state that the file is managed by `ai-agent-telemetry`, must not be edited, and cannot contain
additional hooks. They direct users with another `PostToolUse` requirement to Cline's workspace hook scope or manual
conflict resolution.

### Removal

Uninstall removes only an exact current or supported legacy template. A missing file is an idempotent success. Every
unknown entry is preserved. A regular file with the exact telemetry ownership comment makes Cline cleanup incomplete.
An unknown entry without that comment is treated as user-owned and does not block the remaining telemetry lifecycle.

The error reports:

- the exact hook path that was preserved;
- that its contents do not match a managed version;
- that the managed CLI and requested telemetry purge were not removed;
- a link to the manual conflict-resolution procedure;
- the command to rerun uninstall;
- the managed CLI and telemetry data that remain until the conflict is resolved.

If the user wants to keep custom commands, the manual procedure removes only the telemetry invocation and the
`Managed by ai-agent-telemetry` comment. The user then reruns the original uninstall command. The remaining file no
longer claims telemetry ownership, so the lifecycle preserves it and completes removal.

If the user wants to remove the whole hook, the POSIX command is:

```sh
rm -- "$HOME/Documents/Cline/Hooks/PostToolUse"
```

On Windows, the command is:

```powershell
Remove-Item -LiteralPath "$HOME\Documents\Cline\Hooks\PostToolUse.ps1"
```

The full procedure is in [manual uninstall](../../manual-uninstall.md). Rerunning the lifecycle is safer than deleting
the managed binary manually because the lifecycle also reverses its receipt-owned `PATH` change.

## Fit with issue 57 and community practice

The selected behavior closes the issue 57 reproduction. Appending a comment changes the bytes while leaving the
ownership comment in place. Cline cleanup therefore fails, and the lifecycle preserves the CLI, its owned `PATH`
entry, configuration, and cache. No command parser is required.

The approach follows the common community rule that installers do not overwrite foreign canonical files. It is more
conservative on deletion than marker-based projects because it requires exact generated content. The extra manual step
is a direct result of Cline's one-file-per-event global hook model.

## Implementation in pull request 59

The final pull request 59 implementation removes `clineHookInvokesManagedCLI` and its POSIX and PowerShell tokenizer.
Installation creates only a missing canonical path and treats only the exact current template as idempotent. Uninstall
deletes exact current or supported legacy templates, classifies a mismatched regular file through the exact ownership
comment, and preserves every other entry as user-owned.

Installation, status, and uninstall open entries without following symbolic links, then check the type and read bytes
through the same descriptor. POSIX uses nonblocking mode so a FIFO cannot stop the process. For deletion, uninstall
moves an exact candidate to a unique path in the same directory and rechecks the isolated entry. An ordinary concurrent
replacement of the canonical path is restored without overwriting a new entry or retained at a reported temporary
preservation path if restoration is not safe.

This is a fail-safe for expected concurrent writes to the canonical hook path. It is not a security boundary against
an adversarial process running as the same user that tracks the random temporary path or writes through an open file
descriptor after comparison. Portable filesystems provide no atomic compare-by-content-and-unlink operation.

Ownership-comment classification decodes plain UTF-8, UTF-8 with a byte-order mark, and BOM-declared UTF-16LE or
UTF-16BE so normal PowerShell editor output remains recognizable. A trailing incomplete UTF-16 code unit does not hide
complete preceding lines. This decoding does not relax deletion: only the raw bytes of a current or explicitly
supported legacy template authorize removal.

Lifecycle tests cover the blocking conflict and the supported two-run procedure: the first uninstall preserves the
CLI and telemetry data; after the user removes the telemetry invocation and ownership comment, the second uninstall
preserves the remaining user hook and completes telemetry cleanup.
