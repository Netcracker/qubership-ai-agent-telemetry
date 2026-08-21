# Cursor Nested-Repository Attribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attribute Cursor skill-use telemetry to the one repository touched in an aggregate workspace response, while permitting explicit remote-less fallback.

**Architecture:** Extend the Cursor transcript scanner to return skill names plus a narrow set of filesystem evidence. Resolve each evidence path to its nearest local Git worktree and use the result only when exactly one repository is identified. Interpret a sole `*` repository policy as unrestricted, retaining unattributed events without serializing local paths.

**Tech Stack:** Go 1.26, standard library JSON/path/file APIs, Git CLI, existing Go tests.

## Global Constraints

- Collect only allowlisted tool-input path fields: `path`, `file_path`, `target_file`, `target_directory`, `working_directory`, and `cwd`.
- Do not inspect command strings, patches, prompts, response text, or arbitrary JSON fields.
- Do not serialize local paths or Git roots.
- Preserve Claude, Codex, default `github.com/Netcracker/*`, and ordinary allowlist behavior.
- Do not duplicate events when the response touches several paths or loads several skills.

---

### Task 1: Add unrestricted policy coverage

**Files:**
- Modify: `policy_test.go`
- Modify: `policy.go`

**Interfaces:**
- Produces: `telemetryPolicy.unrestricted() bool`
- Produces: `eventAllowedRemote` retention of an empty remote for explicit unrestricted scope.

- [ ] **Step 1: Write failing policy tests**

Add tests asserting `telemetryPolicy{RepoAllowList: []string{"*"}}` retains an event with no
remote and reports `repoScope() == "all"`, while a normal allowlist still drops it.

- [ ] **Step 2: Run the focused test**

Run: `go test ./... -run 'Test(UnrestrictedPolicy|PolicyWithAllowlistDropsUnknownRemote)'`

Expected: FAIL because `*` is treated as a canonical pattern and the event is dropped.

- [ ] **Step 3: Implement the minimal policy change**

Add `unrestricted()` that returns true only for a single trimmed `*` entry. In `repoScope`,
render that policy as `all`; in `eventAllowedRemote`, retain the normalized remote (including an
empty one) when unrestricted.

- [ ] **Step 4: Re-run focused tests**

Run: `go test ./... -run 'Test(UnrestrictedPolicy|PolicyWithAllowlistDropsUnknownRemote)'`

Expected: PASS.

### Task 2: Collect Cursor transcript path evidence

**Files:**
- Modify: `transcript_cursor_test.go`
- Modify: `transcript_cursor.go`

**Interfaces:**
- Produces: `cursorTranscriptScan { Skills []string; Paths []string; End int64 }`
- Consumes: transcript JSONL since an offset.

- [ ] **Step 1: Write failing scanner tests**

Add a test containing a skill `Read`, normal `Read` path, `ApplyPatch.target_file`, and
`Shell.working_directory`. Assert skill-body paths are excluded and only the three operation
paths are returned, in first-seen order.

- [ ] **Step 2: Run the focused test**

Run: `go test ./... -run TestScanCursorTranscriptCollectsOperationPaths`

Expected: FAIL because `scanCursorTranscript` returns only skill names and an offset.

- [ ] **Step 3: Implement the scanner result**

Decode tool input as `map[string]json.RawMessage`; read only the six allowlisted string keys;
exclude a path when `skillNameInPath` accepts it; deduplicate paths and preserve existing skill
detection, manual attachment, and byte-offset behavior.

- [ ] **Step 4: Re-run focused tests**

Run: `go test ./... -run 'TestScanCursorTranscript'`

Expected: PASS.

### Task 3: Infer a single nested Git repository

**Files:**
- Modify: `transcript_cursor_test.go`
- Modify: `transcript_cursor.go`

**Interfaces:**
- Produces: `cursorEvidenceRepo(paths, workspaceRoots []string) string`
- Uses: `git -C <directory> rev-parse --show-toplevel`

- [ ] **Step 1: Write failing integration tests**

Build an aggregate temporary directory with two initialized Git repositories. Assert paths inside
one repository select that root, paths spanning both return empty, and a skill-only transcript
does not fall back to the aggregate root.

- [ ] **Step 2: Run the focused test**

Run: `go test ./... -run TestCursorTranscriptEventsInferNestedRepository`

Expected: FAIL because events currently use `workspace_roots[0]`.

- [ ] **Step 3: Implement nearest-root inference**

Clean absolute paths; resolve relative paths only with one workspace root; reject paths outside
all workspace roots. For each candidate, start at its existing directory or nearest existing
parent, resolve its Git top-level, reject roots outside workspace roots, deduplicate roots, and
return the root only when one remains.

- [ ] **Step 4: Use the inferred root for event construction**

Pass the inferred root to the existing remote resolver and set `RepoDir` to it. Leave both fields
empty for zero or multiple inferred roots.

- [ ] **Step 5: Re-run focused tests**

Run: `go test ./... -run 'TestCursorTranscriptEvents(InferNestedRepository|AmbiguousRepository|ReadsFileAndResolvesRemote)'`

Expected: PASS.

### Task 4: Verify the complete sender behavior

**Files:**
- Modify: `transcript_cursor_test.go`
- Modify: `policy_test.go`

- [ ] **Step 1: Add a privacy regression test**

Create an event from a nested repository path, apply policy, marshal it, and assert neither the
workspace path nor the Git root is present in the serialized event.

- [ ] **Step 2: Run all tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 3: Format and lint the changed Go files**

Run: `gofmt -w transcript_cursor.go transcript_cursor_test.go policy.go policy_test.go && go test ./...`

Expected: no formatting changes remaining and PASS.
