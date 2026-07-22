# Go lifecycle installer and Cobra CLI implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace lifecycle logic in POSIX and PowerShell installers with one tested Go implementation, add complete
install/update/uninstall support, and migrate the entire CLI to Cobra.

**Architecture:** Thin release bootstraps download and verify a temporary platform binary, then forward lifecycle
arguments and streams. Cobra command factories call a lifecycle orchestrator composed from managed-CLI, APM,
telemetry, and global-Git-hook services. Direct update hands control to the verified new binary before component
preflight; Windows uses running-image rename for update and the bootstrap for synchronous CLI removal.

**Tech Stack:** Go 1.26, Cobra v1.9.1, POSIX `sh`, Windows PowerShell 5.1 and 7, Git CLI, APM CLI, JSON, YAML, GitHub
Actions, and ShellCheck.

## Global constraints

- Keep all lifecycle policy in Go; bootstrap scripts only detect, download, verify, execute, and clean temporary files.
- Keep Viper and the `cobra-cli` generator out of the dependency graph.
- Build a fresh Cobra tree for each invocation and keep command state out of package globals.
- Return `0` for success and discovery help, `1` for operational failure, and `2` for syntax or validation failure.
- Keep only `ingest` fail-open. Explicit `flush`, install, update, uninstall, and self-test failures are nonzero.
- Select `apm`, `telemetry`, and `git-hooks` by default; select `claude`, `codex`, and `cursor` by default.
- Apply harness selection identically to APM targets and telemetry hook targets.
- Complete all selected independent components and summarize each as `OK`, `SKIPPED`, or `FAILED`.
- Run Git and Java 21 preflight only when `git-hooks` is selected; run all preflight before component changes.
- Preserve unrelated native hooks, APM state, Git configuration, repositories, PATH entries, and user files.
- Never delete `~/.local/bin`, even when it is empty.
- Store only installer-owned PATH mutation data in the lifecycle receipt beside the managed executable.
- Make normal telemetry uninstall preserve configuration and data; make `--purge` remove package config and cache.
- Make direct Windows CLI removal fail before side effects and print the exact bootstrap command.
- Do not add `PENDING`, an update state machine, wildcard stale-image cleanup, or runtime aliases for removed commands.
- Keep developer-facing text in American English and wrap Markdown body lines at 120 characters.

---

## File map

- Create `cli.go`: Cobra root construction, dependency injection, exit-code classification, and process entry point.
- Create `cli_commands.go`: factories for existing telemetry commands, hooks, completion, and lifecycle commands.
- Create `cli_test.go`: command routing, help, streams, exit codes, completion candidates, and legacy-option failures.
- Delete `help.go`: remove the handwritten help registry after Cobra coverage is green.
- Modify `main.go` and `main_test.go`: retain deterministic application helpers and remove handwritten routing tests.
- Modify `flush.go` and `flush_test.go`: strict explicit flush and fail-open ingest delivery contracts.
- Create `lifecycle.go` and `lifecycle_test.go`: actions, normalized options, preflight, ordering, results, and
  summaries.
- Create `selection.go` and `selection_test.go`: component and harness parsing plus comma-separated completion.
- Create `managed_cli.go` and `managed_cli_test.go`: managed paths, PATH receipt, copy, removal, and purge-independent
  state.
- Create `managed_cli_posix.go` and `managed_cli_posix_test.go`: profile blocks, atomic replacement, and direct unlink.
- Create `managed_cli_windows.go` and `managed_cli_windows_test.go`: user PATH, running-image rename, rollback, and
  helpers.
- Refactor `update.go` and `update_test.go`: verified release client and new-version lifecycle handoff.
- Create `component_apm.go` and `component_apm_test.go`: official installer invocation and APM package lifecycle.
- Create `component_telemetry.go` and `component_telemetry_test.go`: configuration preflight, hooks, and purge.
- Create `component_git_hooks.go` and `component_git_hooks_test.go`: Git/Java preflight and owned clone lifecycle.
- Create `hooks_remove.go` and `hooks_remove_test.go`: ownership-aware native hook and Codex-rule removal.
- Modify `hooks_*.go`, `hooks.go`, `codex_rule.go`, and their tests: expose target-specific removal merges.
- Replace `scripts/install.sh` and `scripts/install.ps1`: canonical thin bootstraps.
- Create `scripts/install_test.sh` and `scripts/install.Tests.ps1`: canonical bootstrap transport tests without
  compatibility aliases or duplicated component cases covered by Go.
- Modify `.github/workflows/go-build.yaml`, `.github/workflows/installer-tests.yaml`,
  `.github/workflows/bootstrap-tests.yaml`, `.github/workflows/release.yaml`, and `Makefile`: test and publish the new
  assets.
- Modify `README.md`, `docs/cli.md`, `docs/release.md`, `docs/agent-integration.md`,
  `docs/lifecycle-installer.md`, and the affected ADRs and agent-package READMEs: document the new lifecycle and
  breaking CLI changes.

### Task 1: Migrate the complete existing CLI to Cobra

**Files:**

- Modify: `go.mod`
- Modify: `go.sum`
- Create: `cli.go`
- Create: `cli_commands.go`
- Create: `cli_test.go`
- Modify: `main.go`
- Modify: `main_test.go`
- Delete: `help.go`

**Interfaces:**

- Produces: `newRootCommand(deps appDeps) *cobra.Command` and `execute(args []string, deps appDeps) int`.
- Produces: `usageError` for exit code `2`; every other returned error maps to exit code `1`.
- Preserves: `ingest --agent=codex` rejects every trailing argument while returning `0` from hook execution.

- [ ] **Step 1: Add failing Cobra routing and discovery tests**

Create table-driven tests that construct a fresh root, call `SetArgs`, `SetIn`, `SetOut`, and `SetErr`, and assert:

```go
tests := []struct {
    args     []string
    wantCode int
    contains string
}{
    {nil, 0, "Available Commands:"},
    {[]string{"hooks"}, 0, "install"},
    {[]string{"completion"}, 0, "powershell"},
    {[]string{"unknown"}, 2, "unknown command"},
    {[]string{"version"}, 0, version},
}
```

Add focused tests for configure flags, status `--verbose`, self-test, raw ingest arguments, explicit help, and fresh
tree isolation.

- [ ] **Step 2: Run the Cobra tests and confirm the red state**

Run `go test ./... -run 'TestCobra|TestRootDiscovery|TestCommandRouting' -count=1`.

Expected: compilation fails because `appDeps`, `newRootCommand`, and `execute` do not exist.

- [ ] **Step 3: Add Cobra and implement the root execution contract**

Run `go get github.com/spf13/cobra@v1.9.1`. Implement these core types in `cli.go`:

```go
type appDeps struct {
    In     io.Reader
    Out    io.Writer
    ErrOut io.Writer
    Home   func() string
}

type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func execute(args []string, deps appDeps) int
func newRootCommand(deps appDeps) *cobra.Command
func defaultAppDeps() appDeps
```

Set `SilenceErrors` and `SilenceUsage`. Make root and parent `RunE` handlers call `Help()` and return `nil`. Convert
Cobra argument and flag failures to `usageError`, print operational errors once to stderr, and preserve existing
`run`/`runWithStderr` wrappers only until their callers are migrated in this task.

- [ ] **Step 4: Implement factories for every existing public command**

Build local-flag commands for `configure`, `hooks install`, `status`, `selftest`, `ingest`, `flush`, and `version`.
Call the existing deterministic functions instead of moving business logic into command factories. Set
`DisableFlagParsing: true` only on ingest and pass its raw arguments to `parseIngestFlags`.

Add `completion bash|zsh|fish|powershell` subcommands using Cobra generators and ensure they write only to stdout.
Build the root from the existing command factories only. Task 9 adds lifecycle factories and registers them without
leaving forward references in this intermediate commit.

- [ ] **Step 5: Run command tests, then remove the handwritten router and help registry**

Run `go test ./... -run 'TestCobra|TestRun|TestRootDiscovery|TestCommandRouting' -count=1`.

Expected: PASS. Then delete `help.go`, remove `commandHelpEntries`, `routeHelp`, and the switch router from `main.go`,
and replace `main()` with `os.Exit(execute(os.Args[1:], defaultAppDeps()))`.

- [ ] **Step 6: Run the complete Go suite and commit**

Run `gofmt -w cli.go cli_commands.go cli_test.go main.go main_test.go`, then `go test ./... -race`.

Expected: PASS. Commit with `refactor(cli): migrate commands to cobra`.

### Task 2: Make explicit flush strict

**Files:**

- Modify: `flush.go`
- Modify: `flush_test.go`
- Modify: `cli_commands.go`
- Modify: `cli_test.go`

**Interfaces:**

- Produces: `flushExplicit(s *Outbox, resolve deliveryResolver) (int, error)`.
- Preserves: `ingest` ignores opportunistic flush errors and always returns `0` after valid hook parsing.

- [ ] **Step 1: Add failing strict-flush tests**

Cover empty outbox, missing endpoint, invalid CA, busy lock, unreadable event, invalid event, exporter failure, shutdown
failure, and removal failure. The empty case must assert that endpoint and CA resolvers were never called:

```go
sent, err := flushExplicit(outbox, deliveryResolver{
    Endpoint: func() (string, error) { t.Fatal("endpoint resolved for empty outbox"); return "", nil },
    TLS:      func() (*tls.Config, error) { t.Fatal("CA loaded for empty outbox"); return nil, nil },
})
if err != nil || sent != 0 { t.Fatalf("sent=%d err=%v", sent, err) }
```

- [ ] **Step 2: Run the focused tests and confirm the red state**

Run `go test ./... -run 'TestFlushExplicit|TestFlushCommand' -count=1`.

Expected: FAIL because explicit flush still validates configuration incorrectly or reports success on errors.

- [ ] **Step 3: Implement strict delivery and retained-event errors**

Lock and list the outbox before resolving delivery configuration. Return a sentinel success message for an empty
locked outbox. Aggregate unreadable or invalid event names while delivering independent valid events. Check exporter
shutdown and every `Outbox.Remove` error. Keep failed or unvalidated event files and return a bounded joined error.

- [ ] **Step 4: Wire Cobra output and verify ingest remains fail-open**

Print `nothing to flush` for the empty result. Map every other flush error to exit code `1`. Run:

```bash
go test ./... -run 'TestFlush|TestIngest' -count=1
go test ./... -race
```

Expected: PASS. Commit with `fix(cli): report explicit flush failures`.

### Task 3: Add selection, lifecycle results, and orchestration

**Files:**

- Create: `selection.go`
- Create: `selection_test.go`
- Create: `lifecycle.go`
- Create: `lifecycle_test.go`

**Interfaces:**

```go
type lifecycleAction string
const (
    actionInstall lifecycleAction = "install"
    actionUpdate lifecycleAction = "update"
    actionUninstall lifecycleAction = "uninstall"
)

type componentName string
const (
    componentAPM componentName = "apm"
    componentTelemetry componentName = "telemetry"
    componentGitHooks componentName = "git-hooks"
)

type lifecycleOptions struct {
    Action lifecycleAction
    Components []componentName
    Harnesses []hookTarget
    ForceGitHooks bool
    NonInteractive bool
    Purge bool
    RemoveCLI bool
    CLIOnly bool
}

type operationState string
const (
    operationOK      operationState = "OK"
    operationSkipped operationState = "SKIPPED"
    operationFailed  operationState = "FAILED"
)

type operationResult struct {
    Name   string
    State  operationState
    Detail string
    Err    error
}

type lifecycleSummary struct {
    Results []operationResult
    Err error
}

type componentOps struct {
    Preflight func(context.Context, lifecycleOptions) error
    Install func(context.Context, lifecycleOptions) operationResult
    Update func(context.Context, lifecycleOptions) operationResult
    Uninstall func(context.Context, lifecycleOptions) operationResult
}

type lifecycleDeps struct {
    ManagedCLI managedCLIService
    Components map[componentName]componentOps
}

func runLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) lifecycleSummary
```

- [ ] **Step 1: Add failing selection and orchestration tests**

Cover defaults, case normalization, duplicates, `all`, `--skip`, invalid names, empty selection, harness selection,
`--cli-only` conflicts, purge without telemetry, remove-CLI without telemetry, and full-set detection. Add fake
components that record preflight and execution order and independently fail.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestNormalizeLifecycle|TestRunLifecycle|TestCompleteSelection' -count=1`.

Expected: compilation fails because lifecycle types are undefined.

- [ ] **Step 3: Implement normalization and completion candidates**

Implement `normalizeSelection`, `normalizeHarnesses`, `normalizeLifecycleOptions`, and a comma-aware completion helper:

```go
func completeCSV(allowed []string, toComplete string) ([]string, cobra.ShellCompDirective)
```

Preserve the typed comma prefix, omit selected values, include `all` only at the first position, and return
`cobra.ShellCompDirectiveNoFileComp` plus `cobra.ShellCompDirectiveNoSpace` when another value can follow.

- [ ] **Step 4: Implement preflight, ordered execution, and summary formatting**

Run every selected preflight before mutation. Execute managed CLI, APM, telemetry, and Git hooks in that order for
install/update; execute components before CLI removal for uninstall. Continue independent components after failures.
Format a fixed-width summary and return `errors.Join` when any result is `FAILED`.

- [ ] **Step 5: Verify and commit**

Run `gofmt -w selection.go selection_test.go lifecycle.go lifecycle_test.go` and
`go test ./... -run 'TestNormalizeLifecycle|TestRunLifecycle|TestCompleteSelection' -count=1`.

Expected: PASS. Commit with `feat(lifecycle): add component orchestration`.

### Task 4: Implement managed CLI, PATH ownership, and removal

**Files:**

- Create: `managed_cli.go`
- Create: `managed_cli_test.go`
- Create: `managed_cli_posix.go`
- Create: `managed_cli_posix_test.go`
- Create: `managed_cli_windows.go`
- Create: `managed_cli_windows_test.go`

**Interfaces:**

```go
type pathReceipt struct {
    Version int `json:"version"`
    Kind string `json:"kind"`
    Profile string `json:"profile,omitempty"`
    Entry string `json:"entry"`
}

type managedCLIService struct {
    Install func(source string) operationResult
    Remove func() operationResult
    PreflightRemove func(lifecycleOptions) error
}
```

- [ ] **Step 1: Add failing managed-path and receipt tests**

Cover fixed POSIX and Windows paths, atomic copy, executable mode, exact marked profile block, Windows user PATH,
receipt atomicity, receipt-write rollback, existing PATH preservation, legacy POSIX block removal, repeated removal,
receipt removal, and unconditional preservation of `~/.local/bin`.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestManagedCLI|TestPathReceipt|TestManagedPath' -count=1`.

Expected: compilation fails because managed CLI services are undefined.

- [ ] **Step 3: Implement common managed-copy and receipt behavior**

Store `.ai-agent-telemetry-install.json` beside the managed executable. Write only PATH ownership fields. Use a
same-directory temporary file, sync, permission application, and rename. If PATH changes but receipt persistence
fails, remove the exact added block or entry and return failure.

- [ ] **Step 4: Implement POSIX and Windows PATH adapters**

POSIX writes and removes this exact block only:

```text
# Added by ai-agent-telemetry installer
export PATH="$HOME/.local/bin:$PATH"
```

Windows reads and writes the user-level PATH through `golang.org/x/sys/windows/registry` or a small injected adapter.
Never remove an entry without a matching receipt. Never remove the managed directory.

- [ ] **Step 5: Implement removal preflight and verify**

Allow direct removal on POSIX. On Windows, reject direct full uninstall or `--remove-cli` when the current executable
equals the managed path; include the canonical `install.ps1 uninstall ...` command before any mutation.

Run `go test ./... -run 'TestManagedCLI|TestPathReceipt|TestManagedPath' -count=1` and `go test ./... -race`.

Expected: PASS. Commit with `feat(lifecycle): manage cli and owned path entries`.

### Task 5: Add ownership-aware telemetry uninstall and purge

**Files:**

- Create: `hooks_remove.go`
- Create: `hooks_remove_test.go`
- Create: `component_telemetry.go`
- Create: `component_telemetry_test.go`
- Modify: `cli_commands.go`
- Modify: `cli_test.go`
- Modify: `hooks.go`
- Modify: `hooks_claude.go`
- Modify: `hooks_codex.go`
- Modify: `hooks_cursor.go`
- Modify: `codex_rule.go`
- Modify: corresponding `*_test.go` files

**Interfaces:**

- Produces: `uninstallHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult`.
- Produces: `newTelemetryComponent(deps telemetryDeps) componentOps`.
- Produces: `hooks uninstall`, which removes owned native hooks without deleting configuration, data, or the CLI.
- Uses direct inspection of native hook files on every invocation. It adds no hook tombstone or script lifecycle
  orchestration.

- [ ] **Step 1: Add failing ownership-aware hook-removal tests**

Cover unrelated-handler preservation, malformed JSON preservation, repeated removal, canonical and modified Codex
rules, symlinks, partial target failures, and `hooks uninstall --target` routing. Add lifecycle tests proving uninstall
always targets all harnesses and that an excluded install harness is not touched. The implementation must be
self-contained in this branch and must not depend on the superseded draft worktree or branch.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestRemove|TestUninstallHooks|TestTelemetryComponent' -count=1`.

Expected: compilation fails because removal merges and the component adapter are absent.

- [ ] **Step 3: Implement deterministic removal merges**

Port `removeGroupedHooks`, `removeCursorHook`, and target entry points. Remove only canonical handlers or explicit
`_apm_source` ownership markers. Delete a group or event only when removal made it empty and no unrelated extension
field remains. Preserve malformed files byte-for-byte and preserve modified Codex execution-policy rules with warning.

Expose the same removal service through `hooks uninstall`. Register hook-target CSV completion and keep the `hooks`
parent's discovery behavior.

- [ ] **Step 4: Implement telemetry preflight, install/update, uninstall, and purge**

Resolve endpoint/token from environment or existing config. Prompt only in interactive mode and retain answers until
execution. Install/update call `applyConfigure` and `installManagedHooks`; update re-canonicalizes only selected
harnesses. Uninstall removes all owned targets. Purge removes only package config and cache directories after successful
hook cleanup and never removes their parents.

- [ ] **Step 5: Verify and commit**

Run `gofmt -w hooks_remove.go hooks_remove_test.go component_telemetry.go component_telemetry_test.go cli_commands.go
cli_test.go` and `go test ./... -run 'TestRemove|TestUninstallHooks|TestTelemetryComponent' -count=1`.

Expected: PASS. Commit with `feat(lifecycle): add telemetry uninstall and purge`.

### Task 6: Implement the APM component in Go

**Files:**

- Create: `component_apm.go`
- Create: `component_apm_test.go`
- Modify: `legacy_apm.go`
- Modify: `legacy_apm_test.go`

**Interfaces:**

```go
type apmDeps struct {
    Home string
    LookPath func(string) (string, error)
    Download func(context.Context, string, string) error
    Run func(context.Context, string, ...string) (string, error)
}

func newAPMComponent(deps apmDeps) componentOps
```

- [ ] **Step 1: Add failing APM state and command tests**

Cover absent CLI installation, post-install discovery, install versus update command sequences, marketplace absent or
present, global dependency absent or present, mapping-shaped `dependencies.apm`, malformed manifest, not-installed
uninstall, repeated uninstall, bounded combined output, and independent result reporting.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestAPMComponent|TestGlobalAPMManifest' -count=1`.

Expected: compilation fails because `newAPMComponent` is undefined.

- [ ] **Step 3: Implement exact APM actions**

Use the official vendor installer only when `apm` is absent. Install ensures marketplace and package. Update runs
`apm self-update`, `apm marketplace update qubership-ai-packages`, and
`apm install --update qubership-global-essentials@qubership-ai-packages -g --target <csv>`. Both compile globally and
verify with `apm deps list -g`. Uninstall reads `~/.apm/apm.yml` first and runs
`apm uninstall -g qubership-global-essentials@qubership-ai-packages` only for a matching dependency.

- [ ] **Step 4: Verify and commit**

Run `gofmt -w component_apm.go component_apm_test.go legacy_apm.go legacy_apm_test.go` and
`go test ./... -run 'TestAPMComponent|TestGlobalAPMManifest|TestCleanupLegacy' -count=1`.

Expected: PASS. Commit with `feat(lifecycle): manage apm baseline in go`.

### Task 7: Implement Git/Java preflight and global Git hooks

**Files:**

- Create: `component_git_hooks.go`
- Create: `component_git_hooks_test.go`

**Interfaces:**

```go
type gitHooksDeps struct {
    Home string
    DataHome string
    LookPath func(string) (string, error)
    Run func(context.Context, string, ...string) (string, error)
    Confirm func(string) (bool, error)
    Warn io.Writer
}

func newGitHooksComponent(deps gitHooksDeps) componentOps
```

- [ ] **Step 1: Add failing prerequisite and ownership tests**

Cover missing Git, Java versions 20/21/22, one interactive retry, noninteractive failure, skipped-component behavior,
clone, origin mismatch, dirty worktree, fast-forward update, unrelated `core.hooksPath`, forced replacement, password
warning, owned uninstall, modified-clone preservation, and repeated uninstall.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestGitHooksComponent|TestJava21Preflight' -count=1`.

Expected: compilation fails because the component is undefined.

- [ ] **Step 3: Implement exact repository and configuration policy**

Use defaults `https://github.com/exadmin/pre-commit-global.git` and
`<data-base>/qubership/pre-commit-global`, retaining the existing environment overrides. Validate repository origin,
worktree, local changes, and `hooks-global` before setting `core.hooksPath`. Update uses `git pull --ff-only`.
Uninstall clears configuration only when it resolves to the owned hooks path and deletes the clone only after all
ownership and cleanliness checks pass.

- [ ] **Step 4: Verify and commit**

Run `gofmt -w component_git_hooks.go component_git_hooks_test.go` and
`go test ./... -run 'TestGitHooksComponent|TestJava21Preflight' -count=1`.

Expected: PASS. Commit with `feat(lifecycle): manage global git hooks in go`.

### Task 8: Replace self-update with verified new-version handoff

**Files:**

- Modify: `update.go`
- Modify: `update_test.go`
- Modify: `managed_cli_posix.go`
- Modify: `managed_cli_posix_test.go`
- Modify: `managed_cli_windows.go`
- Modify: `managed_cli_windows_test.go`

**Interfaces:**

```go
type releaseClient struct {
    Latest func(context.Context) (string, error)
    Download func(context.Context, string, string, string) error
    Checksums func(context.Context, string) (string, error)
}

type updateHandoff struct {
    Prepare func(context.Context, []string) (handoffResult, error)
}
```

- [ ] **Step 1: Replace advisory/self-update tests with handoff tests**

Delete `TestGatherUpdateCheck*` and `runSelfUpdate` command tests. Retain semver, asset, and checksum unit tests. Add
tests proving old-version component callbacks are never called, the child receives lifecycle arguments and standard
streams, current version runs directly, and child exit codes propagate.

- [ ] **Step 2: Add Windows rename and cleanup-helper tests**

Cover same-volume staging, collision-resistant `.ai-agent-telemetry.exe.update-old-<pid>-<nonce>`, refusal to
overwrite a collision, rollback after canonical copy failure, exact-path helper arguments, bounded retry, leftover-path
diagnostics, and preservation of every unrelated or stale-looking sibling. Assert that no durable update file appears.

- [ ] **Step 3: Run tests and confirm the red state**

Run `go test ./... -run 'TestUpdateHandoff|TestWindowsUpdateSwap|TestReleaseAsset' -count=1`.

Expected: FAIL because current code replaces the executable after running old lifecycle logic.

- [ ] **Step 4: Implement handoff and remove obsolete commands**

Download and verify N+1 before component preflight. POSIX starts the verified runner with inherited streams, waits,
and returns its status. Windows starts the verified temporary runner; N+1 performs preflight, renames N, installs
itself canonically, updates components, and reports status while N waits. Launch the exact-path cleanup helper only
after canonical replacement. Keep failures before child lifecycle side-effect free or roll back the canonical name.

- [ ] **Step 5: Verify and commit**

Run `gofmt -w update.go update_test.go managed_cli_posix.go managed_cli_posix_test.go managed_cli_windows.go
managed_cli_windows_test.go`, then `go test ./... -race` and
`GOOS=windows GOARCH=amd64 go test -c -o /tmp/telemetry.test.exe .`.

Expected: PASS. Commit with `feat(update): hand lifecycle to verified release`.

### Task 9: Expose install, update, uninstall, and completion through Cobra

**Files:**

- Modify: `cli_commands.go`
- Modify: `cli_test.go`
- Modify: `lifecycle.go`
- Modify: `lifecycle_test.go`

**Interfaces:**

- Produces public `install`, `update`, and `uninstall` commands from `newLifecycleCommand`.
- Produces flag completion for components, skips, harnesses, and hook targets.

- [ ] **Step 1: Add failing public-command and completion tests**

Exercise both option forms, default selections, summaries, error codes, `update --cli-only`, every invalid
combination, legacy error hints, root/parent help, and Cobra `__complete`. Assert concrete CSV candidates, prefix
retention, duplicate suppression, invalid-prefix behavior, and `ShellCompDirectiveNoFileComp`, not merely nonempty
generated scripts.

- [ ] **Step 2: Run tests and confirm the red state**

Run `go test ./... -run 'TestLifecycleCommand|TestCompletion|TestLegacyOption' -count=1`.

Expected: FAIL because lifecycle commands and value completion are not registered.

- [ ] **Step 3: Implement lifecycle factories and injected defaults**

Bind local flags to `lifecycleOptions`, normalize before side effects, and call the orchestrator. Make full uninstall
remove CLI automatically. Make partial uninstall preserve CLI unless `--remove-cli` is present. Reject
`--harnesses` on uninstall and preserve Cobra's standard unknown-option behavior.

- [ ] **Step 4: Implement completion and remove obsolete help tests**

Register `RegisterFlagCompletionFunc` for each string-list flag. Keep shell names as completion subcommands. Remove
byte-for-byte help tests and assertions for `update-check`, `self-update`, `--force-update`, `--force`,
`--skip-config`, and PowerShell named parameters; replace them with new commands or actionable breaking-change errors.

- [ ] **Step 5: Verify and commit**

Run `gofmt -w cli_commands.go cli_test.go lifecycle.go lifecycle_test.go` and `go test ./... -race`.

Expected: PASS. Commit with `feat(cli): expose lifecycle commands and completion`.

### Task 10: Replace both installers with thin bootstraps

**Files:**

- Replace: `scripts/install.sh`
- Replace: `scripts/install.ps1`
- Create: `scripts/install_test.sh`
- Create: `scripts/install.Tests.ps1`

**Interfaces:**

- No arguments forward `install`; an option prefix also forwards `install`.
- Explicit `install`, `update`, and `uninstall` pass through unchanged.
- POSIX connects stdin to `/dev/tty` when available; PowerShell preserves its console streams.

- [ ] **Step 1: Rewrite black-box tests around transport only**

Test OS/architecture asset choice, versioned/latest URLs, checksum mismatch, missing checksum, exact argument
forwarding, exit-code propagation, temp cleanup, signal cleanup, `/dev/tty` prompting, no-terminal behavior, and
PowerShell 5.1/7 syntax. Delete APM, telemetry, Git, Java, component-summary, and force-update cases now covered by Go
tests.

- [ ] **Step 2: Run bootstrap tests and confirm the red state**

Run `sh scripts/install_test.sh` and, where available, `pwsh -NoProfile -File scripts/install.Tests.ps1`.

Expected: FAIL because existing scripts still implement lifecycle behavior.

- [ ] **Step 3: Implement the canonical POSIX and PowerShell bootstraps**

Use private temporary directories, exact release asset names, mandatory SHA-256 verification, original argument arrays,
and cleanup traps/finally blocks. Do not parse component flags. Keep only first-token routing for the three lifecycle
commands and option-prefix defaulting.

- [ ] **Step 4: Verify and commit**

Run:

```bash
sh scripts/install_test.sh
shellcheck scripts/install.sh scripts/install_test.sh
pwsh -NoProfile -Command '[scriptblock]::Create((Get-Content -Raw scripts/install.ps1)) | Out-Null'
```

Expected: PASS. Commit with `refactor(installer): delegate lifecycle to go`.

### Task 11: Update CI, packaging, and release assets

**Files:**

- Modify: `.github/workflows/go-build.yaml`
- Modify: `.github/workflows/installer-tests.yaml`
- Create: `.github/workflows/bootstrap-tests.yaml`
- Modify: `.github/workflows/release.yaml`
- Modify: `Makefile`

**Interfaces:**

- Publish only the canonical `install.sh` and `install.ps1` bootstrap names.
- Include every binary and bootstrap asset exactly once in `SHA256SUMS`.

- [ ] **Step 1: Replace obsolete workflow assertions**

Remove clean/force/skip-config script expectations and duplicated component matrices. Add local-release integration
jobs for no-argument install, explicit update, uninstall/purge, Windows bootstrap CLI removal, POSIX `curl | sh` with a
pseudoterminal, and Windows direct-update console inheritance.

- [ ] **Step 2: Update release staging and checksum assertions**

Stamp the canonical scripts and keep all six platform binaries plus `install.sh` and `install.ps1` in the
expected-payload check.

- [ ] **Step 3: Update change filtering and local packaging**

Use pinned `dorny/paths-filter` jobs for Go and local lifecycle tests, then remove the custom change-filter scripts.
Keep general Markdown out of build filters; retain only reference files that Go tests compare with generated content.
Update `Makefile` staging to match release workflow behavior.

- [ ] **Step 4: Verify and commit**

Run `make build`, `make checksums`, PowerShell syntax checks, ShellCheck, actionlint, and `git diff --check`.

Expected: PASS with the expected asset set. Commit with `ci(installer): test go lifecycle delivery`.

### Task 12: Rewrite user and maintainer documentation

**Files:**

- Modify: `README.md`
- Modify: `docs/cli.md`
- Modify: `docs/release.md`
- Modify: `docs/agent-integration.md`
- Create: `docs/lifecycle-installer.md`
- Modify: `docs/adr/0002-bare-binary-on-path.md`
- Modify: `docs/adr/0005-cli-managed-global-hooks.md`
- Modify: `docs/superpowers/decisions/2026-06-23-on-path-binary-lifecycle.md`
- Modify: `docs/superpowers/decisions/2026-06-23-codex-sandbox-execpolicy-rule.md`
- Modify: `agent-packages/ai-agent-telemetry/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`

**Interfaces:**

- Canonical commands are `install`, `update`, `uninstall`, and `completion`.
- Removed commands and flags are documented as breaking changes, not supported aliases.

- [ ] **Step 1: Replace installation and update examples**

Use only canonical `install.sh`/`install.ps1` URLs in primary instructions. Document default install, component and
harness selection, `update`, `update --cli-only`, and noninteractive telemetry requirements.

- [ ] **Step 2: Document uninstall, purge, Windows behavior, and PATH ownership**

Explain full versus partial uninstall, `--remove-cli`, preserved data, purge scope, receipt-owned PATH removal,
unconditional preservation of `~/.local/bin`, direct Windows update handoff, bootstrap-required Windows CLI removal,
and rare exact stale-image manual cleanup diagnostics.

- [ ] **Step 3: Replace CLI and completion references**

Remove `update-check`, `self-update`, `--force-update`, `--force`, `--skip-config`, and PowerShell named-parameter
examples. Add strict flush semantics, empty-outbox no-op behavior, root/parent discovery help, and completion generation
for Bash, Zsh, Fish, and PowerShell.

- [ ] **Step 4: Update historical documents without rewriting history**

Add superseding notes to ADRs and decisions that describe old update or installer behavior. Keep their original
decision text intact below the note so the repository retains historical context.

- [ ] **Step 5: Verify and commit**

Run Markdown lint with the repository's 120-character policy, search for stale public commands, and run
`git diff --check`.

Expected: no active instructions reference removed behavior. Commit with `docs: document unified lifecycle cli`.

### Task 13: Run final cross-platform and regression gates

**Files:**

- Review every file changed by Tasks 1 through 12.

**Interfaces:**

- The branch is ready only when Go, bootstrap, packaging, documentation, and cross-build gates all pass.

- [ ] **Step 1: Run Go correctness gates**

Run:

```bash
gofmt -l *.go
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
```

Expected: no gofmt output and every command exits `0`.

- [ ] **Step 2: Run platform and script gates**

Run:

```bash
make build
make checksums
sh scripts/install_test.sh
shellcheck scripts/install.sh scripts/install_test.sh
pwsh -NoProfile -Command '[scriptblock]::Create((Get-Content -Raw scripts/install.ps1)) | Out-Null'
```

Expected: every command exits `0`. Run the PowerShell black-box suite on Windows PowerShell 5.1 and PowerShell 7 in CI.

- [ ] **Step 3: Audit removed tests and contracts**

Map each deleted installer test to its Go or bootstrap replacement in the PR description. Verify no runtime alias,
duplicate lifecycle function, wildcard stale cleanup, empty managed-directory deletion, or Windows async uninstall
success remains.

- [ ] **Step 4: Run final repository checks and commit any verification-only fixes**

Run `git diff --check`, inspect `git status --short`, and compare the diff with
`docs/superpowers/specs/2026-07-21-go-lifecycle-installer-design.md` section by section.

Expected: only intended files are changed and every specification requirement maps to a completed task. Commit any
necessary verification fix with a narrowly scoped Conventional Commit message.
