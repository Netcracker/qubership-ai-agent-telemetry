# Cline hook lifecycle implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace partial script parsing with exact-template deletion and ownership-comment conflict handling for Cline.

**Architecture:** The canonical Cline hook remains a generated platform-specific file. Installation creates only a
missing path and treats only the current exact template as idempotent. Uninstall deletes exact current or supported
legacy templates, blocks lifecycle cleanup for a mismatched regular file with the exact ownership comment, and
preserves every other entry as user-owned.

**Tech Stack:** Go, standard library filesystem APIs, table-driven Go tests, Markdown documentation.

## Global constraints

- Do not parse POSIX shell or PowerShell.
- Do not use hashes, ownership receipts, or a dispatcher.
- Never follow a Cline hook symbolic link when classifying ownership.
- Never delete a mismatched hook automatically.
- Keep POSIX and Windows behavior equivalent.
- Publish the result to the existing `fix/issue-57-cline-cleanup` branch and PR #59.

---

### Task 1: Lock the ownership contract with failing tests

**Files:**

- Modify: `hooks_cline_test.go`
- Modify: `hooks_remove_test.go`
- Modify: `lifecycle_test.go`

**Interfaces:**

- Consumes: `installClineHook`, `removeClineHook`, `runLifecycle`.
- Produces: executable coverage for create-only installation, marker-classified conflicts, user-owned preservation,
  and the two-run manual conflict-resolution workflow.

- [x] **Step 1: Replace the legacy migration expectation**

  Change the existing test so installation must preserve an exact legacy template, return a conflict, and leave its
  bytes unchanged on POSIX and Windows.

- [x] **Step 2: Replace parser-oriented removal cases**

  Add literal fixtures proving that a mismatched regular file with the exact ownership-comment line blocks cleanup,
  while the same executable content without that comment is preserved without an error.

- [x] **Step 3: Add the two-run lifecycle scenario**

  First run uninstall against a modified managed hook and assert that the hook, CLI, configuration, and cache remain.
  Replace the hook with user commands that contain neither the telemetry invocation nor ownership comment, rerun the
  same uninstall, and assert that the hook remains while telemetry-owned state is removed.

- [x] **Step 4: Verify RED**

  Run `go test ./... -run 'Test(InstallClineHookPreservesPreviousManagedContent|RemoveClineHookOwnership|RunLifecycleResolvesModifiedClineHookAfterManualEdit)'`.
  Expected: failures showing that legacy content is rewritten and marker-based classification is not implemented.

### Task 2: Implement exact ownership and actionable conflict reporting

**Files:**

- Modify: `hooks_cline.go`
- Modify: `hooks_cline_test.go`
- Modify: `hooks_remove_test.go`
- Modify: `lifecycle_test.go`

**Interfaces:**

- Consumes: `clineHookContent(goos)`, `clinePreviousHookContent(goos)`, `managedCLIPath(home, goos)`.
- Produces: `clineHookHasOwnerComment(data []byte) bool` and conservative install/uninstall behavior.

- [x] **Step 1: Make installation create-only**

  Remove automatic rewriting of `clinePreviousHookContent`. Preserve it and return the same occupied-path conflict as
  any other non-current content. Keep exact current content idempotent and retain safe mode repair for exact content.

- [x] **Step 2: Replace the command tokenizer**

  Delete `clineHookInvokesManagedCLI` and all tokenization helpers. Add a helper that compares complete text lines to
  the exact comment `# Managed by ai-agent-telemetry. Do not edit.` without following symbolic links.

- [x] **Step 3: Classify uninstall conflicts**

  Delete exact current and supported legacy templates. For a mismatched regular file with the comment, preserve it and
  return an error naming the hook, managed CLI, manual guide, and rerun requirement. Preserve an entry without the
  comment as user-owned and return success. Use separate warnings so a foreign entry is not called modified telemetry.

- [x] **Step 4: Verify GREEN**

  Run `go test ./... -run 'Test(InstallClineHookPreservesPreviousManagedContent|RemoveClineHookOwnership|RunLifecycleResolvesModifiedClineHookAfterManualEdit)'`.
  Expected: all selected tests pass.

- [x] **Step 5: Run focused Cline and lifecycle tests**

  Run `go test ./... -run 'Cline|Lifecycle'`. Expected: exit status 0.

### Task 3: Verify, review, and publish

**Files:**

- Verify: `hooks_cline.go`
- Verify: `hooks_cline_test.go`
- Verify: `hooks_remove_test.go`
- Verify: `lifecycle_test.go`
- Verify: `docs/adr/0008-cline-hook-installation-and-removal.md`
- Verify: `docs/manual-uninstall.md`
- Verify: `docs/superpowers/research/2026-08-07-cline-hook-installation-and-removal.md`

**Interfaces:**

- Consumes: repository test and lint commands, PR #59.
- Produces: a reviewed commit on the existing PR branch with completed GitHub checks.

- [x] **Step 1: Format and verify locally**

  Run `gofmt` on changed Go files, `go test ./...`, `go vet ./...`, Markdown lint on the changed documentation, and
  `git diff --check`.

- [ ] **Step 2: Run adversarial review**

  Give a separate agent the complete branch diff and ADR contract. Address every confirmed finding and repeat review,
  for no more than five total cycles.

- [ ] **Step 3: Commit and push**

  Stage only the Cline implementation, tests, ADR, research, manual guide, and this plan. Commit with Conventional
  Commits and push `fix/issue-57-cline-cleanup` to update PR #59.

- [ ] **Step 4: Drive CI to green**

  Inspect every PR check. For GitHub Actions failures, read the failing logs, apply only focused in-scope corrections,
  rerun local verification, commit, and push. Do not merge the PR.
