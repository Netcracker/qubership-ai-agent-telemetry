# Cline `PostToolUse` hook probe design

- Date: August 5, 2026
- Status: Approved for a local experiment
- Scope: Cline VS Code Extension 4.1.3 on the research machine

## Goal

Prove that Cline invokes a project skill through its file-based `PostToolUse` hook. Preserve the complete hook input so
the project can map observed Cline fields to the `ai-agent-telemetry` event model in a later design.

This experiment does not call `ai-agent-telemetry ingest`, write to its outbox, configure native OpenTelemetry, or send
data to the backend.

## Success criteria

The experiment succeeds when one captured JSON file meets every condition below:

- `hookName` is `PostToolUse`.
- `postToolUse.toolName` is `skills` or `use_skill`.
- `postToolUse.parameters` identifies `cline-hook-probe`.
- `postToolUse.success` is `true`.
- The test skill returns its expected sentinel response in Cline.
- The hook returns `{"cancel":false}` and does not interrupt the Cline task.

The raw capture must remain on the local machine. No raw field or value is added to the repository, copied to chat, or
sent over the network.

## Preconditions

Before installation, verify these conditions again:

- Cline VS Code Extension 4.1.3 is installed as `saoudrizwan.claude-dev`.
- `~/Documents/Cline/Hooks/PostToolUse` does not exist.
- Cline hooks are enabled. An unset `hooksEnabled` setting counts as enabled because Cline defaults it to `true`.
- `/bin/sh`, `/bin/cat`, `/bin/chmod`, `/bin/mkdir`, `/bin/mv`, and `/usr/bin/mktemp` are available.
- The repository does not contain `.cline/skills/cline-hook-probe`.

Abort before writing anything if the hook path or temporary skill path becomes occupied. Do not overwrite, rename, or
wrap an existing file.

## Artifacts

The experiment creates three local artifacts.

### Capture directory

Create a persistent, user-private run directory:

```text
~/.cache/ai-agent-telemetry/research/cline/<UTC-run-id>/
```

The run ID uses `YYYYMMDDTHHMMSSZ`. The directory mode is `0700`.

Each hook invocation produces one file:

```text
event.<random-suffix>.json
```

Each event file has mode `0600`. Separate files avoid interleaved writes if Cline runs hooks concurrently.

### Global hook

Install an extensionless executable file at:

```text
~/Documents/Cline/Hooks/PostToolUse
```

The hook contains the absolute capture directory for this run. It performs these operations:

1. Set `umask 077`.
2. Allocate a unique temporary file inside the capture directory.
3. Copy stdin to that file without parsing or filtering it.
4. Rename the complete file to the `.json` name.
5. Print `{"cancel":false}` to stdout.

The hook does not print the payload to stdout or stderr. It does not call network tools or the telemetry CLI.

If file creation or writing fails, the hook consumes stdin, records a short diagnostic on stderr, and still prints
`{"cancel":false}`. A capture failure must not fail the Cline tool call.

Use mode `0700` for the probe hook. Cline requires the owner execute bit on macOS and Linux; other users do not need
access.

### Test skill

Create a project-scoped skill at:

```text
.cline/skills/cline-hook-probe/SKILL.md
```

The skill has this behavior:

- It performs no file, shell, network, MCP, or editor operation.
- It returns the exact text `CLINE_HOOK_PROBE_OK`.
- It contains no telemetry instruction.

The project-scoped location keeps the stimulus visible and limits it to this repository.

## Captured data

The probe saves the complete JSON object that Cline writes to hook stdin. Expected fields include:

```text
clineVersion
hookName
timestamp
taskId
workspaceRoots
userId
model
postToolUse.toolName
postToolUse.parameters
postToolUse.result
postToolUse.success
postToolUse.executionTimeMs
```

The experiment intentionally retains all values. The payload can contain user identifiers, absolute paths, model
details, tool parameters, and tool results. The access restrictions and local-only handling are part of the experiment
contract, not optional cleanup.

## Procedure

### 1. Record the baseline

Record these facts without copying private values into the repository:

- extension version;
- hook path absence;
- temporary skill path absence;
- capture directory absence;
- current Git status.

### 2. Create the capture directory

Create the run directory with mode `0700`. Resolve it to an absolute path before writing the hook. Do not use an
unresolved environment variable inside the hook.

### 3. Install the hook

Write the hook to a temporary sibling file, apply mode `0700`, and move it to `PostToolUse`. This prevents Cline from
discovering a partially written script.

Read the installed file back and verify its checksum or exact contents. Confirm that it is a regular executable file,
not a symbolic link.

### 4. Install the test skill

Create `SKILL.md` through an atomic temporary-file move. Verify that only the temporary skill directory was added to
the repository worktree.

### 5. Establish an empty capture baseline

Confirm that the capture directory contains no event files before the Cline action. This separates the test invocation
from earlier activity.

### 6. Invoke the skill in Cline

Open this repository in VS Code and start a new Cline task. Invoke the skill explicitly:

```text
Use the cline-hook-probe skill. Follow it exactly and do not invoke any unrelated tool.
```

If Cline exposes the skill as a slash command, `/cline-hook-probe` is an acceptable explicit invocation. Use one method
per task so the evidence remains easy to attribute.

Wait until Cline displays `CLINE_HOOK_PROBE_OK` or reports an error.

### 7. Locate the matching event

List the captured files without printing their contents. Validate every file with `jq` locally, then select the event
whose tool name is `skills` or `use_skill` and whose parameters identify `cline-hook-probe`.

Do not paste the raw JSON into chat or a repository document.

### 8. Produce a redacted observation

Record only these derived facts in the experiment result:

- extension version;
- observed hook name;
- observed tool name;
- parameter field names;
- whether the expected skill name was present;
- success value;
- top-level and `postToolUse` field names;
- JSON value types for each field;
- whether the task completed and returned the sentinel;
- raw capture directory path.

Do not include `taskId`, `userId`, workspace paths, model values, parameter values other than the probe skill name, or
tool result contents other than confirmation that the sentinel matched.

### 9. Remove active probe artifacts

Remove only the exact hook and temporary skill created by this run. Verify ownership by comparing their contents before
removal. Stop if either file changed after installation.

Keep the private capture directory for the later event-mapping analysis. Report its path and permissions.

### 10. Verify cleanup

Confirm these conditions:

- `~/Documents/Cline/Hooks/PostToolUse` is absent.
- `.cline/skills/cline-hook-probe` is absent.
- the capture directory still has mode `0700`;
- event files still have mode `0600`;
- the repository contains only the previously approved documentation changes.

## Failure handling

### Hook file appears before installation

Abort. Preserve the file byte-for-byte and report the conflict.

### Cline does not invoke the hook

Check the following without changing the experiment scope:

1. Confirm the hook is executable and hooks are enabled.
2. Refresh Cline's hook view or reload the VS Code window once.
3. Repeat the explicit skill invocation in a new Cline task.
4. Inspect Cline's hook status and diagnostic error only after the second attempt fails.

Do not enable native OTLP or start transcript parsing as a fallback.

### Hook captures an unrelated tool only

Retain the raw files and repeat the test with the explicit slash command if available. Do not infer skill execution from
an unrelated `PostToolUse` event.

### Captured JSON is invalid or incomplete

Stop the experiment and retain the file. Check whether the hook process was interrupted or whether Cline wrote a
non-JSON payload. Do not repair the raw capture in place.

### Cline reports a hook error

Capture the error text without private payload data, remove the active hook, and verify that Cline works without it.
The experiment fails even if a raw file exists because the hook must remain fail-open.

### Cleanup ownership check fails

Leave the changed artifact in place and report the mismatch. Do not delete a file that no longer matches the installed
probe.

## Security boundary

The raw payload is more sensitive than production telemetry. It can contain data that the production adapter must
discard. Apply these rules throughout the experiment:

- Keep raw files outside the repository.
- Restrict directories to the current user.
- Do not upload, attach, paste, or index raw files.
- Do not send the payload to `ai-agent-telemetry`, OTLP, or any other network destination.
- Inspect values only when needed to identify the probe event.
- Build future test fixtures from a synthetic or redacted copy, never from the raw file.

## Out of scope

This experiment does not design or implement:

- the production Cline adapter;
- Cline hook installer ownership and conflict behavior;
- repository attribution for multi-root workspaces;
- subagent coverage;
- native Cline metrics or logs;
- backend dashboards or event fixtures;
- APM target mapping;
- subscriber rollout.

Those topics use the captured hook contract as an input to separate designs.

## Deliverables

The completed experiment produces:

1. A private directory containing the raw hook payload files.
2. A redacted result that states whether Cline invoked `cline-hook-probe` through `PostToolUse`.
3. An inventory of observed field names and JSON types for the later event-mapping design.
4. Evidence that the hook and temporary skill were removed after the run.
