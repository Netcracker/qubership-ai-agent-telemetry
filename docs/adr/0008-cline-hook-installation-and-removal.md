# Install and remove the Cline hook by exact content

## Status

Accepted

<!-- markdownlint-disable MD001 -->

#### Date

2026-08-07

#### Owner

denifilatoff

#### Participants and approvers

Denis Filatov (@denifilatoff)

#### Related ADRs

- [ADR 0002](0002-bare-binary-on-path.md) defines the command that the hook invokes.
- [ADR 0005](0005-cli-managed-global-hooks.md) defines machine-wide hook ownership.
- [ADR 0007](0007-cline-harness-support.md) selects Cline's global `PostToolUse` hook.
- [Cline hook installation and removal
  research](../superpowers/research/2026-08-07-cline-hook-installation-and-removal.md)
  compares the lifecycle options and community implementations.
- [Manual Cline hook conflict resolution](../manual-uninstall.md) defines the user procedure after a modified managed
  hook blocks uninstall.
- [Issue 57](https://github.com/Netcracker/qubership-ai-agent-telemetry/issues/57) records the incomplete-cleanup
  defect.

<!-- markdownlint-enable MD001 -->

## Context

Cline discovers one canonical file for each event in each hook directory. The global telemetry hook occupies
`~/Documents/Cline/Hooks/PostToolUse` on POSIX systems and `PostToolUse.ps1` on Windows. Another global integration may
need the same path, but Cline does not provide an array of global files for one event.

The telemetry installer must not overwrite user or third-party logic. It must also avoid removing the managed CLI while
a preserved telemetry hook may still invoke it. Issue 57 showed that preserving a modified hook while reporting cleanup
success leaves a broken active hook after full uninstall.

The first implementation in pull request 59 addressed that defect by tokenizing shell-like and PowerShell-like input
to detect a remaining `ai-agent-telemetry ingest --agent=cline` invocation. The tokenizer is not a complete parser for
either language. A complete implementation would add language-specific complexity unrelated to telemetry ingestion,
while still requiring continuous maintenance for new syntax and indirect invocation forms.

Cline's own multi-hook execution does not provide a reusable solution inside one global directory. A custom dispatcher
would have to replay stdin, parse and combine child JSON responses, implement timeout and failure rules, and behave the
same way on POSIX and Windows. If the dispatcher runs through `ai-agent-telemetry`, uninstall cannot remove the binary
without breaking every remaining child hook. A separate dispatcher would be a new product component with an independent
lifecycle.

Community Cline installers normally own one canonical event file and refuse to overwrite an unowned file.
Implementations use marker checks, generated-shape checks, or exact templates. The reviewed implementations do not use
content hashes or a shared dispatcher.

## Decision

We will manage the Cline hook through exact generated content and conservative conflict handling.

Installation is create-only:

1. If the canonical path is missing, create the hook exclusively with the platform mode required by Cline.
2. If the file exactly matches the current template, report an idempotent success without rewriting it.
3. If any other file, symbolic link, directory, or special entry occupies the path, preserve it and return a conflict.
4. Do not automatically rewrite an existing legacy managed template. The user can uninstall it and install the current
   template as two explicit operations.

The generated hook comments state that `ai-agent-telemetry` manages the entire file. They tell users not to append
another hook in place. To keep custom commands during uninstall, the user must remove the telemetry invocation and the
ownership comment first.

Uninstall is exact and fail-safe:

1. If the canonical path is missing, report an idempotent success.
2. If the file exactly matches the current template or an explicitly supported legacy template, remove it.
3. If a different regular file contains the exact `Managed by ai-agent-telemetry` ownership comment, preserve it and
   report incomplete Cline cleanup. The marker classifies the conflict but never authorizes deletion.
4. Preserve every other file, symbolic link, directory, or special entry as user-owned content without blocking the
   rest of telemetry uninstall.
5. Incomplete Cline cleanup prevents removal of the managed CLI, its receipt-owned `PATH` entry, telemetry
   configuration, and telemetry cache.
6. Continue independent cleanup for other selected harnesses and aggregate the Cline error.

The installer opens an existing entry without following symbolic links. On POSIX, it also uses nonblocking mode so a
FIFO cannot stop the process. It checks the type and reads the bytes through that same file descriptor.

Before deleting an exact template, uninstall moves the matched canonical entry to a unique path in the same directory
and compares its bytes again. This protects the expected concurrency boundary: another process replacing the canonical
path during uninstall. If a different regular entry was moved, uninstall restores it without overwriting a newly
occupied canonical path. If safe restoration is not possible, it retains the entry at the reported temporary
preservation path and returns an error.

The lifecycle does not claim protection from an adversarial process running as the same user that discovers and
replaces the random temporary path, or modifies the isolated file through an already open descriptor after comparison.
Portable filesystems do not provide an atomic compare-by-content-and-unlink operation. The temporary path is an
internal fail-safe for ordinary concurrent replacement, not a security boundary against the local account owner.

The error names the preserved hook path and says that the file does not match a managed version. It does not claim to
know whether the file was edited or replaced. It also names the managed CLI path that remains installed and explains
that a requested purge did not run.

The error directs the user to the
[manual conflict-resolution guide](https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/docs/manual-uninstall.md).
The guide covers two paths:

- If the user wants to keep commands added to the hook, remove only the telemetry invocation and the telemetry
  ownership comment, then rerun the original uninstall command. The second run treats the remaining file as user-owned
  and completes normal telemetry removal.
- If the user no longer needs any content in the hook, remove the whole file with the documented POSIX or PowerShell
  command, then rerun the original uninstall command.

The CLI will not parse POSIX shell or PowerShell to infer whether an unknown file invokes telemetry. It will not use a
content hash, ownership receipt, marker-authorized deletion, or a custom dispatcher.

The error also states that `~/.local/bin/ai-agent-telemetry` or its Windows `.exe` counterpart remains installed. The
user should rerun the lifecycle command instead of deleting that binary directly.

### Justification

Exact comparison is the smallest ownership proof that authorizes deletion. A content hash provides the same equality
result but adds sidecar state and recovery cases. A marker helps explain ownership to the user but does not prove that
the rest of the file is unchanged. Structural recognition and command tokenization accept more variations at the cost
of incomplete language parsing.

The ownership comment closes issue 57 without guessing about executable content. An ordinary edit leaves the comment
in place, so cleanup fails and the lifecycle keeps the CLI available. A user who removes the telemetry invocation also
removes that ownership claim. The next uninstall preserves the remaining commands as user-owned content and completes
the lifecycle.

A user can remove the ownership comment while leaving the telemetry invocation. No stateless exact-content check can
prevent that mistake without parsing the script. The manual procedure makes the required edit explicit and places that
decision with the person who changed the file.

The dispatcher option is rejected because correct fan-out requires processing Cline's JSON protocol. Keeping the
dispatcher after telemetry removal would keep telemetry code or a new executable installed indefinitely. Removing it
would break other child hooks. That lifecycle is broader and harder to remove than the single-file conflict it solves.

## Consequences

- Installation never changes an occupied Cline hook path unless the file already equals the current template.
- Uninstall never deletes user-modified or third-party hook content.
- Installation, status, and uninstall do not follow symbolic links or read non-regular entries.
- Uninstall isolates and rechecks an exact candidate so an ordinary concurrent replacement of the canonical path is
  restored or retained instead of deleted.
- A modified hook that retains the telemetry ownership comment prevents automatic removal of the CLI.
- The implementation has no shell or PowerShell tokenizer and no hash or receipt state for Cline hooks.
- Windows and POSIX use the same ownership rule despite different generated scripts and filenames.
- Updating a legacy hook requires an explicit uninstall and reinstall instead of an automatic rewrite.
- A foreign hook without the telemetry ownership comment is preserved and does not block telemetry removal.
- Users can keep their commands by removing only the telemetry invocation and ownership comment before rerunning
  uninstall.
- Manual conflict resolution is part of the supported lifecycle and must be covered by tests and user-facing
  instructions.
- The lifecycle tells users to rerun uninstall after removing a confirmed telemetry hook instead of deleting the
  binary directly. This lets the installer reverse its owned `PATH` change and apply `--purge` consistently.
