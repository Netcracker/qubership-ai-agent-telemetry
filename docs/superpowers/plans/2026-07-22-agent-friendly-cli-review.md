# Agent-friendly CLI review implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PR #24's CLI flags, migration failures, completion, typo recovery, and POSIX tests predictable on every
supported platform.

**Architecture:** Ordered helper functions provide one source for help, validation, and completion values. Hidden Cobra
commands return migration diagnostics without aliases or side effects. Positional validation augments Cobra usage errors
with command suggestions while retaining the application's single-diagnostic renderer.

**Tech Stack:** Go 1.26, Cobra v1.9.1, POSIX filesystem tests, and the standard Go test toolchain.

## Global constraints

- Keep `SilenceErrors` and `SilenceUsage`; the application renders one diagnostic and no usage for invalid input.
- Return `2` for enum validation, removed commands, and unknown commands.
- Keep removed commands as hidden diagnostics, not runtime aliases.
- Derive help, validation, and completion from shared ordered values.
- Keep `all` and `none` invalid for explicit `hooks install --target` and `hooks uninstall --target`.
- Do not change production POSIX symlink canonicalization.
- Keep developer-facing text in American English and wrap Markdown body lines at 120 characters.
- Preserve the pre-existing untracked `.local/` directory.

---

### Task 1: Make enum flags self-describing

**Files:**

- Modify `selection.go`: add ordered value helpers and valid-value diagnostics.
- Modify `cli_commands.go`: reuse values in help and completion; register `configure --hooks` completion.
- Test `cli_test.go`: cover help, invalid values, and configure completion.

**Interfaces:**

- Produces `componentFlagValues(bool) []string`, `harnessFlagValues(bool) []string`, and
  `hookFlagValues(bool) []string`.
- Produces `enumValuesDescription([]string) string`.
- Completion retains CSV prefix handling and `cobra.ShellCompDirectiveNoFileComp`.

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestLifecycleEnumFlagsDescribeAcceptedValues(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"install", "--help"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	for _, want := range []string{
		"all, apm, telemetry, git-hooks",
		"all, claude, codex, cursor",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help = %q, want %q", out.String(), want)
		}
	}
}

func TestUnknownLifecycleEnumListsAcceptedValues(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "--components", "bogus"},
			"valid components: all, apm, telemetry, git-hooks"},
		{[]string{"install", "--harnesses", "bogus"},
			"valid harnesses: all, claude, codex, cursor"},
	} {
		var errOut bytes.Buffer
		code := execute(tt.args, appDeps{ErrOut: &errOut, Home: t.TempDir})
		if code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Fatalf("args = %v, code = %d, stderr = %q", tt.args, code, errOut.String())
		}
	}
}

func TestConfigureHooksCompletionUsesDocumentedValues(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"__complete", "configure", "--hooks", ""}, appDeps{
		Out: &out, ErrOut: &errOut, Home: t.TempDir,
	})
	if code != 0 {
		t.Fatalf("code = %d; stderr = %q", code, errOut.String())
	}
	for _, want := range []string{"all", "none", "claude", "codex", "cursor", ":6"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("completion = %q, want %q", out.String(), want)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./... -run 'TestLifecycleEnum|TestUnknownLifecycleEnum|TestConfigureHooksCompletion' -count=1
```

Expected: the three missing discovery behaviors fail.

- [ ] **Step 3: Add and reuse ordered values**

Add to `selection.go`:

```go
func componentFlagValues(includeAll bool) []string {
	values := []string{string(componentAPM), string(componentTelemetry), string(componentGitHooks)}
	if includeAll {
		values = append([]string{"all"}, values...)
	}
	return values
}

func harnessFlagValues(includeAll bool) []string {
	values := []string{string(hookClaude), string(hookCodex), string(hookCursor)}
	if includeAll {
		values = append([]string{"all"}, values...)
	}
	return values
}

func hookFlagValues(includeSelectors bool) []string {
	values := harnessFlagValues(false)
	if includeSelectors {
		values = append([]string{"all", "none"}, values...)
	}
	return values
}

func enumValuesDescription(values []string) string {
	return strings.Join(values, ", ")
}
```

Use these helpers in lifecycle help, component and harness errors, lifecycle completion, hook-target help and errors,
and `configure --hooks` completion. Command-specific errors must list only values valid for that command.

- [ ] **Step 4: Verify GREEN and related coverage**

Run:

```bash
go test ./... -run 'TestLifecycleEnum|TestUnknownLifecycleEnum|TestConfigureHooksCompletion|TestCompletion|TestNormalize' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add selection.go cli_commands.go cli_test.go
git commit -m "feat(cli): expose enum values in discovery surfaces"
```

### Task 2: Guide removed commands and command typos

**Files:**

- Modify `cli.go`: register hidden migration commands and augment parent-command argument errors.
- Test `cli_test.go`: cover migration guidance, hidden help, no side effects, and typo suggestions.

**Interfaces:**

- Produces `newLegacyMigrationCommand(string, string) *cobra.Command`.
- Produces `withCommandSuggestions(*cobra.Command, []string, error) error`.

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestRemovedCommandsReturnActionableMigrationErrors(t *testing.T) {
	for _, tt := range []struct{ command, hint string }{
		{"update-check", "use update"},
		{"self-update", "use update --cli-only"},
	} {
		var out, errOut bytes.Buffer
		code := execute([]string{tt.command}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
		if code != 2 || !strings.Contains(errOut.String(), tt.hint) || out.Len() != 0 {
			t.Fatalf("%s: code = %d, stdout = %q, stderr = %q", tt.command, code, out.String(), errOut.String())
		}
	}
}

func TestUnknownCommandIncludesSuggestionWithoutUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute([]string{"instll"}, appDeps{Out: &out, ErrOut: &errOut, Home: t.TempDir})
	if code != 2 || !strings.Contains(errOut.String(), "Did you mean this?") ||
		!strings.Contains(errOut.String(), "install") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
	}
	if strings.Contains(errOut.String(), "Usage:") || out.Len() != 0 {
		t.Fatalf("unknown command rendered usage: stdout = %q, stderr = %q", out.String(), errOut.String())
	}
}
```

Also assert that root help omits both migration commands and use lifecycle dependencies that fail the test if called.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./... -run 'TestRemovedCommands|TestUnknownCommandIncludesSuggestion' -count=1
```

Expected: generic unknown-command output for removed commands and no suggestion for `instll`.

- [ ] **Step 3: Implement hidden diagnostics and suggestions**

Add:

```go
func newLegacyMigrationCommand(name, replacement string) *cobra.Command {
	return &cobra.Command{
		Use:    name,
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			return usageError{err: fmt.Errorf("%s is no longer supported; use %s", name, replacement)}
		},
	}
}

func withCommandSuggestions(cmd *cobra.Command, args []string, err error) error {
	if len(args) == 0 || !cmd.HasAvailableSubCommands() {
		return err
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	suggestions := cmd.SuggestionsFor(args[0])
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\nDid you mean this?\n\t%s", err, strings.Join(suggestions, "\n\t"))
}
```

Register `update-check` with replacement `update` and `self-update` with replacement `update --cli-only`. Call
`withCommandSuggestions` inside `usageArgs` before wrapping positional validation errors.

- [ ] **Step 4: Verify GREEN and routing behavior**

Run:

```bash
go test ./... -run 'TestRemovedCommands|TestUnknownCommand|TestRootDiscovery|TestLegacyOption|TestCobraExplicitHelp' -count=1
```

Expected: PASS; migration commands remain absent from help and return `2` without lifecycle calls.

- [ ] **Step 5: Commit**

```bash
git add cli.go cli_test.go
git commit -m "feat(cli): guide removed commands and command typos"
```

### Task 3: Make the POSIX symlink test portable

**Files:**

- Modify `managed_cli_posix_test.go`: reproduce a parent-path alias and canonicalize the expected receipt path.
- Do not modify `managed_cli_posix.go`.

**Interfaces:**

- The receipt must identify the exact regular file edited through the profile symlink.

- [ ] **Step 1: Extend the fixture and verify RED**

Create a physical root and alias before constructing `target` and `profile`:

```go
physicalRoot := t.TempDir()
aliasParent := t.TempDir()
root := filepath.Join(aliasParent, "root-alias")
if err := os.Symlink(physicalRoot, root); err != nil {
	t.Fatal(err)
}
```

Keep the existing raw `receipt.Profile != target` expectation and run:

```bash
go test ./... -run TestManagedCLIPosixUpdatesAndRemovesSymlinkTarget -count=1
```

Expected: FAIL because the receipt uses the physical canonical path while `target` contains `root-alias`.

- [ ] **Step 2: Canonicalize the expected target**

Replace the raw expected path with:

```go
canonicalTarget, err := filepath.EvalSymlinks(target)
if err != nil {
	t.Fatal(err)
}
if receipt.Profile != canonicalTarget {
	t.Fatalf("receipt profile = %q, want canonical edited target %q", receipt.Profile, canonicalTarget)
}
```

- [ ] **Step 3: Verify GREEN and commit**

Run the Step 1 test again. Expected: PASS on Linux and macOS.

```bash
git add managed_cli_posix_test.go
git commit -m "test(installer): canonicalize POSIX symlink target"
```

### Task 4: Run final verification

**Files:**

- Review all files changed by Tasks 1 through 3.
- Do not stage or modify `.local/`.

**Interfaces:**

- The review is complete only when focused CLI behavior, full Go tests, race tests, vet, formatting, and diff checks
  pass.

- [ ] **Step 1: Format and run focused tests**

Run:

```bash
gofmt -w cli.go cli_commands.go cli_test.go selection.go managed_cli_posix_test.go
go test ./... -run 'TestLifecycleEnum|TestUnknownLifecycleEnum|TestConfigureHooksCompletion|TestRemovedCommands|TestUnknownCommand|TestManagedCLIPosixUpdatesAndRemovesSymlinkTarget' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full Go gates**

Run:

```bash
gofmt -l *.go
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
```

Expected: no formatting output and every command exits `0`.

- [ ] **Step 3: Run CLI smoke tests**

Run the built CLI for `install --help`, invalid components, `update-check`, configure-hook completion, and `instll`.
Expected: documented values and hints appear on stderr or stdout as specified, without usage output on errors.

- [ ] **Step 4: Run repository integrity checks**

Run:

```bash
git diff --check
git status --short --branch
```

Expected: no whitespace errors; `.local/` remains untracked; only intended commits differ from the prior PR head.

- [ ] **Step 5: Check PR wording**

Confirm that the existing PR statement about actionable `update-check` and `self-update` diagnostics is true. Do not
edit the PR body unless another mismatch remains.

### Task 5: Preserve release-version override precedence

**Files:**

- Modify `scripts/install.sh` and `scripts/install.ps1`: separate the stampable default from the environment override.
- Modify `Makefile` and `.github/workflows/release.yaml`: stamp only the default-version assignment.
- Test the staged POSIX and PowerShell release assets with an explicit version override.

- [ ] Add failing staged-asset tests proving the current stamp erases the environment override.
- [ ] Introduce `DEFAULT_BINARY_VERSION` and `$DefaultBinaryVersion`; keep the environment value authoritative.
- [ ] Update both staging paths to replace only the default assignment and verify source plus staged installers.
- [ ] Commit with `fix(release): preserve installer version overrides`.

### Task 6: Activate Windows PATH changes

**Files:**

- Modify the Windows managed-CLI implementation: inject and invoke an environment-change notifier.
- Add a Windows implementation using `SendMessageTimeoutW` with `HWND_BROADCAST`, `WM_SETTINGCHANGE`, and
  `Environment`.
- Test addition, removal, notification failure, registry rollback, and rollback diagnostics.

- [ ] Add failing manager tests for notification and rollback semantics.
- [ ] Implement the notifier and wire it after successful registry writes.
- [ ] Cross-build Windows and run focused managed-CLI tests.
- [ ] Commit with `fix(installer): activate Windows PATH changes`.

### Task 7: Pin Git-hook updates to the verified origin

**Files:**

- Modify `component_git_hooks.go`: validate the checked-out branch upstream and fetch an explicit origin ref.
- Test a clean managed clone whose branch tracks another remote.

- [ ] Add a failing regression test proving implicit `git pull` accepts an unrelated upstream.
- [ ] Resolve branch configuration, require the verified `origin`, fetch the explicit merge ref, and fast-forward it.
- [ ] Run focused Git-hook component tests and commit with `fix(installer): pin Git hook updates to origin`.

### Task 8: Keep Codex policy when hook cleanup fails

**Files:**

- Modify `hooks_remove.go`: remove the Codex policy only after native hook cleanup succeeds.
- Test malformed or incompatible Codex hook content with a canonical managed policy rule.

- [ ] Add the failing regression test.
- [ ] Gate policy cleanup on successful native-file cleanup.
- [ ] Run focused hook-removal tests and commit with `fix(hooks): preserve Codex policy on cleanup failure`.

### Task 9: Complete small discovery fixes

**Files:**

- Modify `selection.go` and completion tests: treat both `all` and `none` as exclusive selectors.
- Modify `docs/cli.md`: document `hooks uninstall`, target values, and a removal example.

- [ ] Add failing completion assertions for exact `none` and `none,`.
- [ ] Prevent mixed completion candidates and avoid requesting a following CSV value after an exclusive selector.
- [ ] Update the CLI reference and commit with `docs(cli): complete hook removal discovery`.

### Task 10: Re-run complete verification and branch review

- [ ] Run formatting, focused tests, full tests, race tests, vet, Windows cross-builds, script suites, and diff checks.
- [ ] Audit the complete branch diff from `642ef619` through `HEAD` and confirm `.local/` remains untouched.
- [ ] Request a fresh whole-branch review and resolve every Critical or Important finding before handoff.
