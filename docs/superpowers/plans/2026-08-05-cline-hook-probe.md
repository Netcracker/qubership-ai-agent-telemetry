# Cline `PostToolUse` Hook Probe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove that Cline VS Code Extension reports a project skill invocation through `PostToolUse` while retaining
the complete raw hook payload locally for later event mapping.

**Architecture:** Install one temporary global Cline hook that copies each stdin payload to a private per-run directory
and always returns `{"cancel":false}`. Invoke one inert project skill, derive a redacted observation from the matching
event, then remove the active hook and skill while retaining the private raw files.

**Tech Stack:** POSIX `sh`, Cline VS Code Extension 4.1.3, Cline file-based hooks, Cline project skills, `jq`, Git

## Global Constraints

- Run only against Cline VS Code Extension 4.1.3 installed as `saoudrizwan.claude-dev`.
- Do not overwrite an existing `~/Documents/Cline/Hooks/PostToolUse` file.
- Do not overwrite an existing `.cline/skills/cline-hook-probe` directory.
- Keep raw payloads outside the repository under `~/.cache/ai-agent-telemetry/research/cline/`.
- Keep the run directory at mode `0700` and event files at mode `0600`.
- Preserve the complete stdin payload without filtering or normalization.
- Do not print, upload, attach, paste, index, or transmit raw payload contents.
- Do not invoke `ai-agent-telemetry`, OTLP, transcript parsing, or native Cline telemetry during this experiment.
- The hook must remain fail-open and print `{"cancel":false}` even when capture fails.
- Remove only artifacts whose exact contents still match the files installed by this experiment.
- Keep the private capture directory after active probe cleanup.

---

## File map

- Create temporarily: `~/Documents/Cline/Hooks/PostToolUse`
  - Captures each Cline `PostToolUse` stdin payload and returns a non-cancelling response.
- Create temporarily: `.cline/skills/cline-hook-probe/SKILL.md`
  - Provides an inert, deterministic skill stimulus.
- Create persistently outside Git: `$RUN_DIR/event.*.json`
  - Retains the exact raw hook payload for later mapping.
- Create after a successful or failed run:
  `docs/superpowers/research/2026-08-05-cline-hook-probe-result.md`
  - Records only the approved redacted facts and cleanup evidence.

### Task 1: Establish the baseline and private capture directory

**Files:**

- Verify: `~/Documents/Cline/Hooks/PostToolUse`
- Verify: `.cline/skills/cline-hook-probe`
- Create: `$RUN_DIR`

**Interfaces:**

- Consumes: the preconditions in
  `docs/superpowers/specs/2026-08-05-cline-hook-probe-design.md`.
- Produces: one absolute `RUN_DIR` value with mode `0700`, used verbatim by Task 2.

- [ ] **Step 1: Record a fresh read-only baseline**

  Run:

  ```bash
  git status --short --branch
  test ! -e "$HOME/Documents/Cline/Hooks/PostToolUse"
  test ! -e .cline/skills/cline-hook-probe
  test -x /bin/sh
  test -x /bin/cat
  test -x /bin/chmod
  test -x /bin/mkdir
  test -x /bin/mv
  test -x /usr/bin/mktemp
  test -x /usr/bin/jq
  ```

  Expected: Git reports only the documentation state expected at execution time, and every `test` exits zero. Stop
  before writing if either probe path exists.

- [ ] **Step 2: Reconfirm the installed Cline identity and hook setting**

  Read the installed extension metadata and Cline global state without printing unrelated values. Confirm:

  ```text
  extension ID: saoudrizwan.claude-dev
  version: 4.1.3
  hooksEnabled: true or absent
  ```

  Expected: all three conditions match. If `hooksEnabled` is explicitly `false`, stop instead of changing it.

- [ ] **Step 3: Allocate the capture directory**

  Compute the run ID once in UTC using `date -u +%Y%m%dT%H%M%SZ`. Create exactly:

  ```text
  ~/.cache/ai-agent-telemetry/research/cline/$RUN_ID
  ```

  Create parent directories as needed, set the run directory mode to `0700`, resolve the resulting absolute path, and
  retain it as `RUN_DIR` for the remaining tasks.

- [ ] **Step 4: Verify the empty private baseline**

  Run a metadata-only listing of `RUN_DIR` and inspect its numeric mode.

  Expected: mode `0700`, current-user ownership, and no files.

### Task 2: Install the fail-open capture hook

**Files:**

- Create temporarily: `~/Documents/Cline/Hooks/PostToolUse`
- Create temporarily during atomic installation: `~/Documents/Cline/Hooks/.PostToolUse.cline-probe.tmp`

**Interfaces:**

- Consumes: the absolute `RUN_DIR` produced by Task 1.
- Produces: an executable hook that creates `event.*.json` files and writes one JSON response to stdout.

- [ ] **Step 1: Materialize the hook source with the absolute run directory**

  Create a temporary source file outside the repository with the following content. Replace `__ABSOLUTE_RUN_DIR__`
  once with the literal absolute `RUN_DIR`; the installed file must not contain the token or an environment-variable
  reference.

  ```sh
  #!/bin/sh
  set -u
  umask 077

  capture_dir='__ABSOLUTE_RUN_DIR__'
  tmp_file=$(/usr/bin/mktemp "${capture_dir}/event.XXXXXXXX.tmp")
  if [ -z "${tmp_file}" ]; then
      /bin/cat >/dev/null
      printf '%s\n' 'cline hook probe: unable to allocate capture file' >&2
      printf '%s\n' '{"cancel":false}'
      exit 0
  fi

  if /bin/cat >"${tmp_file}"; then
      final_file="${tmp_file%.tmp}.json"
      if ! /bin/mv "${tmp_file}" "${final_file}"; then
          printf '%s\n' 'cline hook probe: unable to finalize capture file' >&2
      fi
  else
      printf '%s\n' 'cline hook probe: unable to write capture file' >&2
  fi

  printf '%s\n' '{"cancel":false}'
  exit 0
  ```

- [ ] **Step 2: Validate the materialized hook before installation**

  Run `/bin/sh -n` against the temporary source. Confirm the file contains the literal `RUN_DIR`, does not contain
  `__ABSOLUTE_RUN_DIR__`, and contains no calls to `curl`, `wget`, `ai-agent-telemetry`, or OTLP endpoints.

  Expected: syntax validation passes and all content checks pass.

- [ ] **Step 3: Install atomically without replacing existing content**

  Recheck that `~/Documents/Cline/Hooks/PostToolUse` and the temporary sibling path are absent. Create the `Hooks`
  directory if needed, copy the validated source to `.PostToolUse.cline-probe.tmp` with mode `0700`, then move that
  sibling to `PostToolUse`.

  Expected: `PostToolUse` is a regular non-symlink file owned by the current user with numeric mode `0700`.

- [ ] **Step 4: Verify installation ownership evidence**

  Compute and retain the SHA-256 digest of the installed hook. Compare the installed bytes with the materialized source.

  Expected: the byte comparison succeeds. The digest is used as the deletion guard in Task 5.

- [ ] **Step 5: Smoke-test the hook without Cline**

  Pipe this synthetic object to the installed hook:

  ```json
  {"probe":"preflight"}
  ```

  Expected stdout:

  ```json
  {"cancel":false}
  ```

  Expected capture: one valid JSON file at mode `0600` whose exact content is the synthetic object. Remove only this
  identified synthetic capture file after comparing its bytes with the input. Confirm `RUN_DIR` is empty again before
  the Cline action.

### Task 3: Install and invoke the inert project skill

**Files:**

- Create temporarily: `.cline/skills/cline-hook-probe/SKILL.md`

**Interfaces:**

- Consumes: the verified hook from Task 2.
- Produces: one Cline task displaying `CLINE_HOOK_PROBE_OK` and at least one raw hook event in `RUN_DIR`.

- [ ] **Step 1: Install the temporary project skill atomically**

  Materialize the following exact source in a temporary file outside the repository. Recheck that the target directory
  is absent, create it, copy the source to a temporary sibling of `SKILL.md`, and move the complete sibling to
  `SKILL.md`:

  ```markdown
  ---
  name: cline-hook-probe
  description: Return a deterministic sentinel for the local Cline hook experiment.
  ---

  # Cline hook probe

  Return exactly `CLINE_HOOK_PROBE_OK`.

  Do not invoke tools, edit files, run commands, access the network, or add any other text.
  ```

- [ ] **Step 2: Verify the worktree delta and skill ownership evidence**

  Confirm the temporary skill is the only new active experiment artifact inside the repository. Compute and retain its
  SHA-256 digest for the deletion guard in Task 5.

  Expected: no tracked file changes were introduced by skill installation.

- [ ] **Step 3: Reconfirm the empty event baseline**

  List filenames and metadata in `RUN_DIR` without printing contents.

  Expected: no `event.*.json` or `.tmp` files.

- [ ] **Step 4: Invoke the skill in a new Cline task**

  Open this repository in the already installed Cline VS Code Extension and submit exactly:

  ```text
  Use the cline-hook-probe skill. Follow it exactly and do not invoke any unrelated tool.
  ```

  If Cline does not resolve the skill by name but exposes `/cline-hook-probe`, stop that task and use the slash command
  once in a new task. Do not mix the two stimuli in one task.

- [ ] **Step 5: Record the visible result without copying private data**

  Expected: Cline displays exactly `CLINE_HOOK_PROBE_OK`, completes without a hook error, and remains responsive. Record
  only whether those three conditions were met.

### Task 4: Validate and summarize the captured event

**Files:**

- Read privately: `$RUN_DIR/event.*.json`
- Create: `docs/superpowers/research/2026-08-05-cline-hook-probe-result.md`

**Interfaces:**

- Consumes: raw JSON files created by Task 3 and the visible Cline result.
- Produces: a redacted result document with field names, JSON types, success evidence, and no private values.

- [ ] **Step 1: Inventory capture files without displaying contents**

  List only filenames, owner, numeric mode, byte count, and modification time. Check for `.tmp` files.

  Expected: one or more `event.*.json` files at mode `0600`; no incomplete `.tmp` file. Retain all Cline-created event
  files even when more than one exists.

- [ ] **Step 2: Validate JSON privately**

  Run `jq -e .` for each event with stdout redirected away from chat and logs.

  Expected: every final event file is one valid JSON object. Stop analysis and preserve all files if validation fails.

- [ ] **Step 3: Locate the probe event by predicates**

  Use a local `jq -e` predicate that requires all of the following without printing matching values:

  ```text
  .hookName == "PostToolUse"
  .postToolUse.toolName == "skills" or .postToolUse.toolName == "use_skill"
  serialized .postToolUse.parameters contains "cline-hook-probe"
  .postToolUse.success == true
  ```

  Expected: exactly one event matches. If none matches, apply the retry sequence from the design once. If multiple
  events match, retain them all and report the count instead of choosing one arbitrarily.

- [ ] **Step 4: Derive a redacted schema inventory**

  From the matching object, derive only:

  - sorted top-level field names and their JSON types;
  - sorted `postToolUse` field names and their JSON types;
  - observed hook name and tool name;
  - `postToolUse.parameters` field names when it is an object, without their values;
  - whether parameters contain `cline-hook-probe`;
  - success boolean;
  - whether the visible response matched the sentinel;
  - event count and matching-event count.

  Do not extract or print `taskId`, `userId`, workspace paths, model values, arbitrary parameter values, or result text.

- [ ] **Step 5: Write the redacted result document**

  Create `docs/superpowers/research/2026-08-05-cline-hook-probe-result.md` in English with these sections and fields:

  - H1: `Cline PostToolUse Hook Probe Result`, with `PostToolUse` in code formatting.
  - Metadata: date, extension ID and version, `PASS` or `FAIL`, and the literal absolute `RUN_DIR` path.
  - `Observed behavior`: approved booleans, counts, hook name, tool name, and sentinel confirmation.
  - `Observed schema`: sorted field names and JSON types only.
  - `Cleanup`: hook removal, skill removal, retained-directory permissions, and repository status.

  Do not copy any raw JSON object or private field value into the document.

### Task 5: Remove active artifacts and verify retained evidence

**Files:**

- Remove after guarded comparison: `~/Documents/Cline/Hooks/PostToolUse`
- Remove after guarded comparison: `.cline/skills/cline-hook-probe/SKILL.md`
- Retain: `$RUN_DIR/event.*.json`
- Modify: `docs/superpowers/research/2026-08-05-cline-hook-probe-result.md`

**Interfaces:**

- Consumes: hook and skill SHA-256 digests from Tasks 2 and 3.
- Produces: no active probe, retained private evidence, and final cleanup evidence in the result document.

- [ ] **Step 1: Guard hook removal by exact identity**

  Confirm `PostToolUse` is still a regular non-symlink file and its SHA-256 digest equals the installation digest. If
  either check fails, leave it in place and record the mismatch. Otherwise remove exactly that file.

- [ ] **Step 2: Guard skill removal by exact identity**

  Confirm `SKILL.md` still has the installation digest. If it changed, leave the entire skill directory in place and
  record the mismatch. Otherwise remove `SKILL.md`, then remove only the now-empty `cline-hook-probe` directory.

- [ ] **Step 3: Verify final filesystem state**

  Confirm:

  ```text
  ~/Documents/Cline/Hooks/PostToolUse is absent
  .cline/skills/cline-hook-probe is absent
  RUN_DIR is present at mode 0700
  each final event file is present at mode 0600
  no incomplete .tmp capture exists
  ```

- [ ] **Step 4: Finalize the cleanup section**

  Update the result document with the verified cleanup facts. If any guard failed, set the overall outcome to `FAIL`
  even when event capture succeeded, because the experiment contract includes safe cleanup.

- [ ] **Step 5: Validate repository documentation**

  Run:

  ```bash
  git diff --check
  codespell docs/superpowers/research/2026-08-05-cline-hook-probe-result.md
  ```

  Check that Markdown body lines are at most 120 characters, there is exactly one H1, and every fenced code block has a
  language tag.

- [ ] **Step 6: Commit the redacted result**

  Stage only the result document and any separately approved plan bookkeeping. Commit with:

  ```bash
  git commit -m "docs(cline): record hook probe result"
  ```

  Do not stage the raw capture directory, active probe files, generated Cline state, or unrelated worktree changes.
