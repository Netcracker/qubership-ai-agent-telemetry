# Telemetry and developer-tool uninstall implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ownership-aware telemetry hook removal plus idempotent uninstall and purge modes to both Qubership
developer installers.

**Architecture:** The Go CLI remains the only native hook editor and records successful full removal in an XDG-style
tombstone receipt. The POSIX and PowerShell bootstraps compose that command with APM package removal, fixed-path
telemetry binary removal, optional telemetry data purge, and ownership-checked global Git hook removal.

**Tech Stack:** Go 1.26, POSIX `sh`, Windows PowerShell 5.1, PowerShell 7, JSON, Git CLI, APM CLI, ShellCheck.

## Global constraints

- Keep `ai-agent-telemetry hooks uninstall` under the existing `hooks` command; do not add a top-level uninstall.
- Preserve unrelated native handlers, JSON properties, hook files, modified Codex rules, shared PATH entries, the APM
  CLI, and the `qubership-ai-packages` marketplace registration.
- Write `<state-base>/ai-agent-telemetry/hooks-uninstalled` atomically with exact contents
  `version=1\nstate=uninstalled\n` after a successful full hook uninstall.
- Invalidate the receipt before legacy APM cleanup or native hook installation. Any invalidation error except a missing
  file is fatal and prevents hook changes.
- Keep the receipt during purge. Purge removes only telemetry config and cache package directories after hook cleanup.
- Treat a missing global APM manifest as `SKIPPED`; do not parse APM YAML in either platform script.
- Global uninstall always removes all telemetry targets. Reject explicit harness selection and install-only flags with
  exit code `2` before component changes.
- Continue independent components after a component failure. Return `1` when any component fails and `0` when all
  selected components are `OK` or `SKIPPED`.
- Do not require Java in uninstall mode. Git remains required when `git-hooks` is selected.
- Keep all new developer-facing text in American English and wrap Markdown body lines at 120 characters.

---

## File map

- Create `hook_receipt.go`: resolve, validate, write, and invalidate the hook-removal receipt.
- Create `hook_receipt_test.go`: receipt path, content, atomic lifecycle, and invalidation tests.
- Create `hooks_remove.go`: generic grouped-hook removal, Cursor removal, per-target orchestration, and Codex rule
  removal.
- Create `hooks_remove_test.go`: cross-harness removal, preservation, malformed-file, and warning tests.
- Modify `hooks.go`: fail-closed receipt invalidation before managed installation and action-aware command parsing.
- Modify `hooks_test.go`: install orchestration ordering and aggregate uninstall behavior.
- Modify `hooks_claude.go`, `hooks_codex.go`, and `hooks_cursor.go`: expose target-specific removal merge entry points.
- Modify `codex_rule.go`: remove an exact canonical rule and warn when a modified rule is preserved.
- Modify `main.go`: route `hooks install` and `hooks uninstall`, write the full-removal receipt, and print
  action-specific results.
- Modify `main_test.go`: public command, receipt, partial failure, help, and exit-code tests.
- Modify `help.go`: document both hook actions and route nested help for either action.
- Modify `paths_test.go`: state-base resolution coverage.
- Modify `global-scripts/qubership-dev-install.sh`: POSIX uninstall lifecycle and purge.
- Modify `global-scripts/tests/qubership-dev-install_test.sh`: POSIX black-box fixtures and uninstall cases.
- Modify `global-scripts/qubership-dev-install.ps1`: PowerShell uninstall lifecycle and purge.
- Modify `global-scripts/tests/qubership-dev-install.Tests.ps1`: PowerShell black-box fixtures and uninstall cases.
- Modify `global-scripts/README.md`: uninstall examples, purge warning, preserved state, and recovery guidance.
- Modify `docs/superpowers/specs/2026-07-20-uninstall-support-design.md`: mark the implemented design after all gates
  pass.

### Task 1: Add receipt state and fail-closed install invalidation

**Files:**

- Create: `hook_receipt.go`
- Create: `hook_receipt_test.go`
- Modify: `hooks.go:107-128`
- Modify: `hooks_test.go:16-65`
- Modify: `main.go:56-101`
- Modify: `main_test.go:292-357`
- Modify: `paths_test.go`

**Interfaces:**

- Produces: `hookReceiptPath(home string) string`, `validHookReceipt(home string) bool`,
  `writeHookReceipt(home string) error`, and `invalidateHookReceipt(home string) error`.
- Produces: `installManagedHooks(home string, targets []hookTarget, warnings io.Writer) ([]hookInstallResult, error)`.
- Preserves: no receipt access and no invalidation when `targets` is empty.

- [ ] **Step 1: Write failing receipt path and lifecycle tests**

Create `hook_receipt_test.go` with table tests for XDG state, home fallback, missing home, exact content, invalid
content, missing-file invalidation, and existing-file invalidation:

```go
package main

import (
    "os"
    "path/filepath"
    "testing"
)

func TestHookReceiptPathFrom(t *testing.T) {
    tests := []struct{ name, xdg, home, want string }{
        {"XDG wins", "/state", "/home/u", filepath.Join("/state", pkgName, hookReceiptName)},
        {"home fallback", "", "/home/u", filepath.Join("/home/u", ".local", "state", pkgName, hookReceiptName)},
        {"unavailable", "", "", ""},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := hookReceiptPathFrom(tt.xdg, tt.home); got != tt.want {
                t.Fatalf("got %q, want %q", got, tt.want)
            }
        })
    }
}

func TestHookReceiptLifecycle(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    t.Setenv("USERPROFILE", home)
    t.Setenv("XDG_STATE_HOME", "")
    if validHookReceipt(home) {
        t.Fatal("missing receipt reported valid")
    }
    if err := writeHookReceipt(home); err != nil {
        t.Fatal(err)
    }
    path := hookReceiptPath(home)
    data, err := os.ReadFile(path)
    if err != nil {
        t.Fatal(err)
    }
    if string(data) != hookReceiptContents || !validHookReceipt(home) {
        t.Fatalf("receipt = %q, valid = %v", data, validHookReceipt(home))
    }
    if err := invalidateHookReceipt(home); err != nil {
        t.Fatal(err)
    }
    if err := invalidateHookReceipt(home); err != nil {
        t.Fatalf("missing receipt invalidation = %v", err)
    }
}
```

- [ ] **Step 2: Run the receipt tests and confirm the red state**

Run:

```bash
go test ./... -run 'TestHookReceipt(PathFrom|Lifecycle)' -count=1
```

Expected: compilation fails because the receipt constants and functions are undefined.

- [ ] **Step 3: Implement the receipt helper**

Create `hook_receipt.go`:

```go
package main

import (
    "bytes"
    "errors"
    "os"
    "path/filepath"
)

const (
    hookReceiptName     = "hooks-uninstalled"
    hookReceiptContents = "version=1\nstate=uninstalled\n"
)

func stateBaseFrom(xdg, home string) string {
    if xdg != "" {
        return xdg
    }
    if home == "" {
        return ""
    }
    return filepath.Join(home, ".local", "state")
}

func hookReceiptPathFrom(xdg, home string) string {
    base := stateBaseFrom(xdg, home)
    if base == "" {
        return ""
    }
    return filepath.Join(base, pkgName, hookReceiptName)
}

func hookReceiptPath(home string) string {
    return hookReceiptPathFrom(os.Getenv("XDG_STATE_HOME"), home)
}

func validHookReceipt(home string) bool {
    path := hookReceiptPath(home)
    if path == "" {
        return false
    }
    data, err := os.ReadFile(path)
    return err == nil && bytes.Equal(data, []byte(hookReceiptContents))
}

func writeHookReceipt(home string) error {
    path := hookReceiptPath(home)
    if path == "" {
        return errUserHomeUnavailable
    }
    return writeFileAtomically(path, []byte(hookReceiptContents), 0o600)
}

func invalidateHookReceipt(home string) error {
    path := hookReceiptPath(home)
    if path == "" {
        return errUserHomeUnavailable
    }
    err := os.Remove(path)
    if errors.Is(err, os.ErrNotExist) {
        return nil
    }
    return err
}
```

Add pure state-base cases to `paths_test.go`. Run `gofmt -w hook_receipt.go hook_receipt_test.go paths_test.go`.

- [ ] **Step 4: Add failing orchestration tests for invalidation ordering**

Change `installManagedHooksWith` tests to inject an invalidator. Add a test that returns `errors.New("receipt locked")`
and asserts the call list remains `[]string{"invalidate"}`. Add a second test that expects
`[]string{"invalidate", "cleanup", "install"}` on success and a third that verifies an empty target set invokes only
the install function.

Use this test shape in `hooks_test.go`:

```go
results, err := installManagedHooksWith(
    "/home/test",
    []hookTarget{hookClaude},
    io.Discard,
    func(string) error { calls = append(calls, "invalidate"); return errors.New("receipt locked") },
    func(string, io.Writer) { calls = append(calls, "cleanup") },
    func(string, []hookTarget) []hookInstallResult { calls = append(calls, "install"); return nil },
)
if err == nil || !strings.Contains(err.Error(), "receipt locked") {
    t.Fatalf("error = %v", err)
}
if results != nil || !reflect.DeepEqual(calls, []string{"invalidate"}) {
    t.Fatalf("results = %#v, calls = %v", results, calls)
}
```

- [ ] **Step 5: Run the orchestration tests and confirm the red state**

Run:

```bash
go test ./... -run 'TestInstallManagedHooksWith' -count=1
```

Expected: compilation fails because `installManagedHooksWith` does not accept the invalidator or return an error.

- [ ] **Step 6: Implement fail-closed invalidation and update callers**

Change the managed install signatures and ordering in `hooks.go`:

```go
func installManagedHooks(
    home string,
    targets []hookTarget,
    warnings io.Writer,
) ([]hookInstallResult, error) {
    return installManagedHooksWith(
        home, targets, warnings, invalidateHookReceipt, cleanupLegacyTelemetryAPM, installHooks,
    )
}

func installManagedHooksWith(
    home string,
    targets []hookTarget,
    warnings io.Writer,
    invalidate func(string) error,
    cleanup func(string, io.Writer),
    install func(string, []hookTarget) []hookInstallResult,
) ([]hookInstallResult, error) {
    if len(targets) > 0 {
        if err := invalidate(home); err != nil {
            return nil, fmt.Errorf("invalidate hook removal receipt: %w", err)
        }
        cleanup(home, warnings)
    }
    return install(home, targets), nil
}
```

Update `main.go` so `configure` reports `configure hooks: <error>` after `applyConfigure` and before native status
work. Make `hooks install` report `hooks: <error>`. Both return `1`. Keep `configure --hooks=none` on the
no-invalidation path.

- [ ] **Step 7: Add the configure partial-result regression test**

In `main_test.go`, create a nonempty directory at `hookReceiptPath(home)` so `os.Remove` fails. Run `configure` and
assert exit `1`, the config `env` exists, every native hook path is absent, and the legacy APM manifest is unchanged.

Run:

```bash
go test ./... -run 'Test(RunConfigureFailsClosedOnReceiptInvalidation|InstallManagedHooksWith|HookReceipt)' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit receipt state and invalidation**

```bash
git add hook_receipt.go hook_receipt_test.go hooks.go hooks_test.go main.go main_test.go paths_test.go
git commit -m "feat(hooks): invalidate uninstall receipt before install"
```

### Task 2: Implement ownership-aware native hook removal

**Files:**

- Create: `hooks_remove.go`
- Create: `hooks_remove_test.go`
- Modify: `hooks_claude.go`
- Modify: `hooks_codex.go`
- Modify: `hooks_cursor.go`
- Modify: `codex_rule.go`

**Interfaces:**

- Consumes: existing `validateGroupedHooks`, `validateCursorHooks`, ownership predicates, `updateHookFile`, and
  `writeFileAtomically`.
- Produces: `removeClaudeHook`, `removeCodexHook`, and `removeCursorHook` as `hookMergeFunc` values.
- Produces: `removeCodexRule(path string, warnings io.Writer) (bool, error)`.
- Produces: `uninstallHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult`.

- [ ] **Step 1: Write failing cross-harness removal tests**

Create `hooks_remove_test.go`. Build roots with canonical, legacy, `_apm_source`, and unrelated handlers. For each
harness, call its removal merge and assert:

```go
if changed, err := removeClaudeHook(root); err != nil || !changed {
    t.Fatalf("changed = %v, error = %v", changed, err)
}
if got := findCommands(root); !reflect.DeepEqual(got, []string{"user-hook"}) {
    t.Fatalf("commands = %v", got)
}
if root["theme"] != "dark" {
    t.Fatal("unrelated root property was removed")
}
```

Add separate tests that require these exact behaviors:

- a group that becomes empty and contains only `matcher`, `hooks`, and `_apm_source` is removed;
- a group with `groupExtension` remains with an empty `hooks` array;
- an empty managed event is removed, but unrelated events remain;
- Cursor keeps `version` and unrelated events;
- malformed or structurally invalid JSON returns an error without changing bytes;
- a second removal reports `Changed == false`;
- one malformed target does not prevent other targets from being removed.

- [ ] **Step 2: Run removal tests and confirm the red state**

Run:

```bash
go test ./... -run 'Test(Remove|UninstallHooks)' -count=1
```

Expected: compilation fails because removal merge functions are undefined.

- [ ] **Step 3: Implement grouped and Cursor removal merges**

Create `hooks_remove.go` with this generic grouped algorithm and a corresponding Cursor algorithm:

```go
package main

import (
    "fmt"
    "io"
    "os"
    "reflect"
)

func removeGroupedHooks(
    root map[string]any,
    specs []hookSpec,
    isOwned func(map[string]any) bool,
) (bool, error) {
    hooks, events, err := validateGroupedHooks(root, specs)
    if err != nil || hooks == nil {
        return false, err
    }
    changed := false
    for _, spec := range specs {
        groups := events[spec.event]
        keptGroups := make([]any, 0, len(groups))
        for _, value := range groups {
            group := cloneJSONObject(value.(map[string]any))
            handlers, hasHandlers := group["hooks"].([]any)
            keptHandlers := make([]any, 0, len(handlers))
            removed := group["_apm_source"] == hookAPMSource
            for _, handlerValue := range handlers {
                handler := handlerValue.(map[string]any)
                if isOwned(handler) {
                    removed = true
                    continue
                }
                keptHandlers = append(keptHandlers, handler)
            }
            if removed {
                delete(group, "_apm_source")
                if hasHandlers {
                    group["hooks"] = keptHandlers
                }
            }
            if removed && len(keptHandlers) == 0 && onlyHookGroupFields(group) {
                changed = true
                continue
            }
            keptGroups = append(keptGroups, group)
            changed = changed || removed
        }
        if len(keptGroups) == 0 {
            delete(hooks, spec.event)
        } else if !reflect.DeepEqual(groups, keptGroups) {
            hooks[spec.event] = keptGroups
        }
    }
    if changed && len(hooks) == 0 {
        delete(root, "hooks")
    }
    return changed, nil
}

func onlyHookGroupFields(group map[string]any) bool {
    for key := range group {
        if key != "matcher" && key != "hooks" {
            return false
        }
    }
    return true
}
```

Implement `removeCursorHook` with `validateCursorHooks`: filter owned entries from `cursorHookEvents`, delete an event
only when filtering makes it empty, delete an empty `hooks` object, and never delete `version` or unrelated events:

```go
func removeCursorHook(root map[string]any) (bool, error) {
    hooks, events, _, err := validateCursorHooks(root)
    if err != nil || hooks == nil {
        return false, err
    }
    changed := false
    for _, event := range cursorHookEvents {
        entries := events[event]
        kept := make([]any, 0, len(entries))
        for _, value := range entries {
            entry := value.(map[string]any)
            if isOwnedCursorHook(entry) {
                changed = true
                continue
            }
            kept = append(kept, entry)
        }
        if len(kept) == 0 && len(entries) > 0 {
            delete(hooks, event)
        } else if !reflect.DeepEqual(entries, kept) {
            hooks[event] = kept
        }
    }
    if changed && len(hooks) == 0 {
        delete(root, "hooks")
    }
    return changed, nil
}
```

Add grouped wrappers in the harness files:

```go
func removeClaudeHook(root map[string]any) (bool, error) {
    return removeGroupedHooks(root, claudeHookSpecs, isOwnedClaudeHandler)
}
```

Use equivalent wrappers for Codex and Cursor. Run `gofmt` on all changed Go files.

- [ ] **Step 4: Add failing Codex rule ownership tests**

Test missing, exact canonical, and modified rules. Capture warnings with `strings.Builder`. The modified case must leave
the file byte-for-byte unchanged, report `changed == false`, return no error, and include `preserved modified Codex
execution policy` in the warning.

- [ ] **Step 5: Implement Codex rule removal and per-target orchestration**

Add to `codex_rule.go`:

```go
func removeCodexRule(path string, warnings io.Writer) (bool, error) {
    data, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) {
        return false, nil
    }
    if err != nil {
        return false, err
    }
    if !bytes.Equal(data, []byte(codexExecutionPolicy)) {
        _, _ = fmt.Fprintf(warnings, "warning: preserved modified Codex execution policy: %s\n", path)
        return false, nil
    }
    if err := os.Remove(path); err != nil {
        return false, err
    }
    return true, nil
}
```

Add `uninstallHooks` to `hooks_remove.go`. `updateHookFile` already treats a missing file as an unchanged empty root and
does not write when the removal merge returns false:

```go
func uninstallHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult {
    requested := make(map[hookTarget]bool, len(targets))
    for _, target := range targets {
        requested[target] = true
    }
    results := make([]hookInstallResult, 0, len(requested))
    for _, target := range allHookTargets {
        if !requested[target] {
            continue
        }
        path := hookPath(home, target)
        if path == "" {
            results = append(results, hookInstallResult{Target: target, Err: errUserHomeUnavailable})
            continue
        }
        merge := removeClaudeHook
        if target == hookCodex {
            merge = removeCodexHook
        } else if target == hookCursor {
            merge = removeCursorHook
        }
        changed, err := updateHookFile(path, merge)
        if target == hookCodex {
            ruleChanged, ruleErr := removeCodexRule(codexRulePath(home), warnings)
            changed = changed || ruleChanged
            err = errors.Join(err, ruleErr)
        }
        results = append(results, hookInstallResult{Target: target, Path: path, Changed: changed, Err: err})
    }
    return results
}
```

- [ ] **Step 6: Run the removal suite**

Run:

```bash
go test ./... -run 'Test(Remove|UninstallHooks)' -count=1
```

Expected: PASS, including malformed-file nonmutation and cross-target continuation.

- [ ] **Step 7: Commit native hook removal**

```bash
git add hooks_remove.go hooks_remove_test.go hooks_claude.go hooks_codex.go hooks_cursor.go codex_rule.go
git commit -m "feat(hooks): remove owned native hook entries"
```

### Task 3: Expose `hooks uninstall` and complete receipt semantics

**Files:**

- Modify: `hooks.go:385-443`
- Modify: `hooks_test.go`
- Modify: `main.go:86-122`
- Modify: `main_test.go:20-48,254-290,498-542`
- Modify: `help.go:27-52,129-160`

**Interfaces:**

- Consumes: `uninstallHooks`, `writeHookReceipt`, `installManagedHooks`, and `hookInstallError`.
- Produces: `hooksCommand{Action hooksAction, Targets []hookTarget}` from `parseHooksCommand`.
- Produces: exit `0` after successful removal and receipt persistence, exit `1` after target or receipt failure, and
  exit `2` after argument errors.

- [ ] **Step 1: Write failing parser, help, and command tests**

Add parser table cases for both actions, target subsets, repeated target flags, empty targets, `all`, `none`, missing
action, and an unknown action. Use these expected values:

```go
tests := []struct {
    args       []string
    wantAction hooksAction
    want       []hookTarget
    wantErr    bool
}{
    {[]string{"install"}, hooksInstall, allHookTargets, false},
    {[]string{"uninstall"}, hooksUninstall, allHookTargets, false},
    {[]string{"uninstall", "--target=claude,cursor"}, hooksUninstall, []hookTarget{hookClaude, hookCursor}, false},
    {[]string{"uninstall", "--target=all"}, "", nil, true},
    {[]string{"uninstall", "--target=none"}, "", nil, true},
}
```

Add `main_test.go` command tests that install all hooks, run `hooks uninstall`, verify all owned entries are absent,
verify the receipt exact content, and run uninstall a second time. Repeat the receipt assertion with the explicit
canonical target list in a different order to prove `fullHookTargetSet` is membership-based. Add a subset test that
verifies no receipt is created. Add a malformed-Claude test that verifies Codex and Cursor are removed, no receipt is
written, and exit is `1`. Add a receipt write failure test by creating a nonempty directory at the receipt path. Also
run `hooks install` with a nonempty directory at the receipt path and assert exit `1`, no native-file mutation, and no
legacy APM cleanup; this covers the public install-time invalidation failure, not only the internal helper.

Update help expectations to require both usage lines and nested `hooks uninstall --help` routing.

- [ ] **Step 2: Run public command tests and confirm the red state**

Run:

```bash
go test ./... -run 'Test(ParseHooksCommand|RunHooksUninstall|RunHelp)' -count=1
```

Expected: FAIL because `uninstall` is still rejected and help lists only install.

- [ ] **Step 3: Implement action-aware parsing**

Replace the parser return type in `hooks.go`:

```go
type hooksAction string

const (
    hooksInstall   hooksAction = "install"
    hooksUninstall hooksAction = "uninstall"
)

type hooksCommand struct {
    Action  hooksAction
    Targets []hookTarget
}

func parseHooksCommand(args []string) (hooksCommand, error) {
    if len(args) == 0 {
        return hooksCommand{}, fmt.Errorf("missing hooks action")
    }
    action := hooksAction(args[0])
    if action != hooksInstall && action != hooksUninstall {
        return hooksCommand{}, fmt.Errorf("unknown hooks action %q", args[0])
    }
    rawTargets := ""
    targetSet := false
    for _, arg := range args[1:] {
        if !strings.HasPrefix(arg, "--target=") {
            return hooksCommand{}, fmt.Errorf("unknown hooks %s flag %q", action, arg)
        }
        if targetSet {
            return hooksCommand{}, fmt.Errorf("hook target flag may be specified only once")
        }
        rawTargets = strings.TrimPrefix(arg, "--target=")
        if rawTargets == "" {
            return hooksCommand{}, fmt.Errorf("hook target value must not be empty")
        }
        targetSet = true
    }
    if targetSet && (rawTargets == "all" || rawTargets == "none") {
        return hooksCommand{}, fmt.Errorf(
            "hook target %q is not valid here; omit --target to process all hooks", rawTargets,
        )
    }
    targets, err := parseHookTargets(rawTargets)
    return hooksCommand{Action: action, Targets: targets}, err
}
```

- [ ] **Step 4: Route install and uninstall actions**

In `main.go`, parse once and switch on `command.Action`. Keep existing install output. For uninstall:

```go
results := uninstallHooks(home, command.Targets, stderr)
for _, result := range results {
    state := "unchanged"
    if result.Err != nil {
        state = "failed"
    } else if result.Changed {
        state = "removed"
    }
    stdout(fmt.Sprintf("%s: %s: %s\n", result.Target, state, result.Path))
}
if err := hookInstallError(results); err != nil {
    stdout("hooks: " + err.Error() + "\n")
    return 1
}
if fullHookTargetSet(command.Targets) {
    if err := writeHookReceipt(home); err != nil {
        _, _ = fmt.Fprintln(stderr, "hooks: write uninstall receipt:", err)
        return 1
    }
}
return 0
```

Implement `fullHookTargetSet` by requiring the same length and canonical target membership as `allHookTargets`.
Uninstall must not call `cleanupLegacyTelemetryAPM`.

- [ ] **Step 5: Update help and nested action routing**

Set the hooks summary to `Install, repair, or remove global harness hooks without changing collector settings.` Add
both usage lines and describe `--target` as operating on a subset. In `routeHelp`, recognize `install` and `uninstall`
when routing a third-position help token.

- [ ] **Step 6: Run the complete Go suite**

Run:

```bash
gofmt -w hooks.go hooks_test.go main.go main_test.go help.go
go test ./... -count=1
go vet ./...
go build ./...
```

Expected: every command exits `0` and the Go test output ends with `ok ai-agent-telemetry`.

- [ ] **Step 7: Commit the public CLI**

```bash
git add hooks.go hooks_test.go main.go main_test.go help.go
git commit -m "feat(cli): add hooks uninstall command"
```

### Task 4: Add POSIX developer uninstall and purge

**Files:**

- Modify: `global-scripts/qubership-dev-install.sh`
- Modify: `global-scripts/tests/qubership-dev-install_test.sh`

**Interfaces:**

- Consumes: `ai-agent-telemetry hooks uninstall` and the version 1 receipt contract.
- Produces: `--uninstall`, `--purge`, component filtering, install-only option rejection, and uninstall summaries.
- Preserves: APM CLI, marketplace registration, unrelated `core.hooksPath`, modified Git clones, shared PATH, config,
  and cache unless purge is explicit.

- [ ] **Step 1: Extend POSIX fixtures and write failing option tests**

Add `XDG_STATE_HOME`, `XDG_CACHE_HOME`, receipt, config, cache, native hook, and managed telemetry binary paths to
`setup_component_fixture`. Extend fake Git with `git config --global --unset-all core.hooksPath`. Extend fake telemetry
with `QDI_FAIL_TELEMETRY_HOOKS=1` support.

Add tests for:

```sh
assert_exit_with 2 '--purge requires --uninstall' --purge
assert_exit_with 2 '--harnesses is not valid with --uninstall' --uninstall --harnesses claude
assert_exit_with 2 '--force-update is not valid with --uninstall' --uninstall --force-update
assert_exit_with 2 '--force-git-hooks is not valid with --uninstall' --uninstall --force-git-hooks
assert_exit_with 2 '--non-interactive is not valid with --uninstall' --uninstall --non-interactive
```

Run `sh global-scripts/tests/qubership-dev-install_test.sh`; expect failure because the options are unknown.

- [ ] **Step 2: Implement uninstall mode parsing and lifecycle dispatch**

Track explicit flags with `HARNESSES_SET`, `FORCE_GIT_HOOKS_SET`, `FORCE_UPDATE_SET`, and `NON_INTERACTIVE_SET`.
Introduce `MODE=install` and `PURGE=0`. Validate all uninstall-only combinations before prerequisite checks.

Split component dispatch:

```sh
run_component() {
  _component=$1
  _prefix=$(registry_value "$_component" 4)
  if [ "$MODE" = uninstall ]; then
    printf '\n[%s] UNINSTALLING\n' "$_component"
    "${_prefix}_uninstall"
    _code=$?
  else
    run_install_component "$_component" "$_prefix"
    return
  fi
  record_component_code "$_component" "$_code"
}
```

Use `Uninstall the selected Qubership developer tools.` and `Uninstall summary` in mode-specific output. Run Java
prerequisite checks only when `MODE=install`.

- [ ] **Step 3: Write failing APM and telemetry uninstall tests**

Cover missing manifest `SKIPPED`, existing manifest command invocation, absent package success, APM failure with later
telemetry continuation, hook cleanup before binary deletion, hook failure preserving the binary, external telemetry
command preservation, valid-receipt repeat uninstall, unsafe no-binary/no-receipt failure, and no-hook-file receipt
creation.

For purge, create files under config and cache package directories, then assert both directories are gone while the
receipt and marketplace marker remain.

- [ ] **Step 4: Implement APM and telemetry uninstall handlers**

Add the POSIX helpers and handlers:

```sh
telemetry_receipt_path() {
  printf '%s/ai-agent-telemetry/hooks-uninstalled' "${XDG_STATE_HOME:-$HOME/.local/state}"
}

telemetry_receipt_valid() {
  _path=$(telemetry_receipt_path)
  [ -f "$_path" ] || return 1
  _value=$(cat "$_path") || return 1
  [ "$_value" = "$(printf 'version=1\nstate=uninstalled')" ]
}

write_telemetry_receipt() {
  _path=$(telemetry_receipt_path)
  _dir=$(dirname "$_path")
  mkdir -p "$_dir" || return 1
  _tmp="$_path.tmp.$$"
  (umask 077 && printf 'version=1\nstate=uninstalled\n' > "$_tmp") || return 1
  mv -f "$_tmp" "$_path"
}

apm_uninstall() {
  [ -f "$HOME/.apm/apm.yml" ] || return 10
  command -v apm >/dev/null 2>&1 || {
    printf '%s: apm: cannot remove the global package because apm is not on PATH.\n' "$PROGRAM" >&2
    return 1
  }
  apm uninstall -g qubership-global-essentials@qubership-ai-packages
}
```

Use the receipt helpers in this ordered handler:

```sh
telemetry_hooks_may_exist() {
  [ -e "$HOME/.claude/settings.json" ] ||
    [ -e "$HOME/.codex/hooks.json" ] ||
    [ -e "$HOME/.cursor/hooks.json" ] ||
    [ -e "$HOME/.codex/rules/ai-agent-telemetry.rules" ]
}

telemetry_uninstall() {
  _managed_bin=$HOME/.local/bin/ai-agent-telemetry
  _telemetry_bin=
  if [ -x "$_managed_bin" ]; then
    _telemetry_bin=$_managed_bin
  elif command -v ai-agent-telemetry >/dev/null 2>&1; then
    _telemetry_bin=ai-agent-telemetry
  fi

  if [ -n "$_telemetry_bin" ]; then
    "$_telemetry_bin" hooks uninstall || return 1
  elif telemetry_receipt_valid; then
    :
  elif telemetry_hooks_may_exist; then
    printf '%s: telemetry: native hook files exist, but no telemetry CLI or valid removal receipt is available.\n' \
      "$PROGRAM" >&2
    return 1
  else
    write_telemetry_receipt || return 1
  fi

  rm -f "$_managed_bin" || return 1
  if [ "$PURGE" -eq 1 ]; then
    _config_dir=${XDG_CONFIG_HOME:-$HOME/.config}/ai-agent-telemetry
    _cache_dir=${XDG_CACHE_HOME:-$HOME/.cache}/ai-agent-telemetry
    rm -rf "$_config_dir" "$_cache_dir" || return 1
  fi
}
```

The handler preserves the receipt and never deletes a telemetry executable found only through `PATH`.

- [ ] **Step 5: Write failing Git hook uninstall tests**

Test exact managed `core.hooksPath` deactivation, unrelated value preservation, missing clone success, clean expected
origin deletion, wrong-origin preservation, dirty clone preservation, and deactivation before a clone-validation
failure. Run the POSIX suite and confirm these cases fail before implementation.

- [ ] **Step 6: Implement ownership-checked Git hook uninstall**

Initialize `GIT_HOOKS_DIR` and `GIT_HOOKS_REPOSITORY` through one helper shared by install and uninstall. Implement the
ownership checks explicitly:

```sh
git_hooks_uninstall() {
  init_git_hooks
  _desired_hooks_path=$(git_hooks_desired_path) || return 1
  _current_hooks_path=$(git config --global --get core.hooksPath 2>/dev/null || :)
  if [ -n "$_current_hooks_path" ] && [ -d "$_current_hooks_path" ]; then
    _current_hooks_path=$(CDPATH='' cd -- "$_current_hooks_path" && pwd -P) || return 1
  fi
  if [ "$_current_hooks_path" = "$_desired_hooks_path" ]; then
    git config --global --unset-all core.hooksPath || return 1
  fi

  [ -e "$GIT_HOOKS_DIR" ] || return 0
  if ! git -C "$GIT_HOOKS_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf '%s: git-hooks: preserving %s because it is not a Git worktree.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  fi
  _origin=$(git -C "$GIT_HOOKS_DIR" remote get-url origin 2>/dev/null) || return 1
  if [ "$_origin" != "$GIT_HOOKS_REPOSITORY" ]; then
    printf '%s: git-hooks: preserving %s because its origin is %s.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" "$_origin" >&2
    return 1
  fi
  _status=$(git -C "$GIT_HOOKS_DIR" status --porcelain) || return 1
  if [ -n "$_status" ]; then
    printf '%s: git-hooks: preserving modified worktree %s.\n' "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  fi
  rm -rf "$GIT_HOOKS_DIR"
}
```

`init_git_hooks` must resolve either the fixed installer default or the explicit test override. Never use a glob or an
unresolved broad path for deletion.

- [ ] **Step 7: Run POSIX verification**

Run:

```bash
sh global-scripts/tests/qubership-dev-install_test.sh
shellcheck global-scripts/qubership-dev-install.sh global-scripts/tests/qubership-dev-install_test.sh
sh -n global-scripts/qubership-dev-install.sh
sh -n global-scripts/tests/qubership-dev-install_test.sh
```

Expected: the suite prints `PASS: POSIX developer installer tests`; ShellCheck and syntax checks produce no errors.

- [ ] **Step 8: Commit POSIX uninstall**

```bash
git add global-scripts/qubership-dev-install.sh global-scripts/tests/qubership-dev-install_test.sh
git commit -m "feat(installer): add POSIX uninstall mode"
```

### Task 5: Add PowerShell developer uninstall and purge

**Files:**

- Modify: `global-scripts/qubership-dev-install.ps1`
- Modify: `global-scripts/tests/qubership-dev-install.Tests.ps1`

**Interfaces:**

- Consumes: the same Go command and receipt contract as Task 4.
- Produces: `-Uninstall` and `-Purge` with behavior equivalent to POSIX on Windows PowerShell 5.1 and PowerShell 7.
- Preserves: external telemetry commands and deletes only `~/.local/bin/ai-agent-telemetry.exe`.

- [ ] **Step 1: Extend PowerShell fixtures and write failing option tests**

Set `XDG_STATE_HOME` and `XDG_CACHE_HOME` in `Setup-ComponentFixture`. Add fake Git handling for
`config --global --unset-all core.hooksPath`. Make the fake telemetry command honor
`QDI_FAIL_TELEMETRY_HOOKS`. Add option cases equivalent to POSIX:

```powershell
@{ Arguments = @('-Purge'); Message = '-Purge requires -Uninstall' }
@{ Arguments = @('-Uninstall', '-Harnesses', 'claude'); Message = '-Harnesses is not valid with -Uninstall' }
@{ Arguments = @('-Uninstall', '-ForceUpdate'); Message = '-ForceUpdate is not valid with -Uninstall' }
@{ Arguments = @('-Uninstall', '-ForceGitHooks'); Message = '-ForceGitHooks is not valid with -Uninstall' }
@{ Arguments = @('-Uninstall', '-NonInteractive'); Message = '-NonInteractive is not valid with -Uninstall' }
```

Run `pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1`; expect failure.

- [ ] **Step 2: Implement PowerShell mode parsing and lifecycle dispatch**

Add `[switch]$Uninstall` and `[switch]$Purge`. Use `$PSBoundParameters.ContainsKey(...)` to distinguish explicit
harness and install-only options from defaults. Validate before prerequisites or component execution. Split
`Invoke-Component` into install and uninstall branches while preserving independent result aggregation.

- [ ] **Step 3: Write failing APM, telemetry, purge, and repeat tests**

Mirror every Task 4 case. Use an external `.ps1` telemetry command for invocation and create a dummy managed
`~/.local/bin/ai-agent-telemetry.exe` to verify fixed-path deletion. Assert the external script remains. Verify normal
uninstall preserves config/cache and purge deletes only their package directories while retaining the receipt.

- [ ] **Step 4: Implement PowerShell APM and telemetry handlers**

Add exact receipt helpers:

```powershell
function Get-TelemetryReceiptPath {
  $stateRoot = if ($env:XDG_STATE_HOME) {
    $env:XDG_STATE_HOME
  } else {
    Join-Path $env:USERPROFILE '.local/state'
  }
  return Join-Path $stateRoot 'ai-agent-telemetry/hooks-uninstalled'
}

function Test-TelemetryReceipt {
  $path = Get-TelemetryReceiptPath
  if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return $false }
  return [System.IO.File]::ReadAllText($path) -eq "version=1`nstate=uninstalled`n"
}

function Write-TelemetryReceipt {
  $path = Get-TelemetryReceiptPath
  $dir = Split-Path -Parent $path
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  $temp = "$path.tmp-$PID-$([guid]::NewGuid().ToString('N'))"
  [System.IO.File]::WriteAllText($temp, "version=1`nstate=uninstalled`n")
  Move-Item -Force -LiteralPath $temp -Destination $path
}
```

`Uninstall-Apm` returns the skip sentinel when `~/.apm/apm.yml` is absent, otherwise resolves APM and invokes the exact
global uninstall command. `Uninstall-Telemetry` follows the Task 4 ordering and removes only the fixed `.exe`.

- [ ] **Step 5: Write failing Git hook ownership tests**

Mirror the POSIX exact-path, unrelated-path, clean-clone, missing-clone, wrong-origin, dirty-clone, and partial-result
cases. Ensure no Java invocation appears in the log during `-Uninstall -Components git-hooks`.

- [ ] **Step 6: Implement PowerShell Git hook removal**

Factor managed path initialization out of `Install-GitHooks`. In `Uninstall-GitHooks`, unset `core.hooksPath` only on
the resolved managed match. Validate worktree, origin, and porcelain status before
`Remove-Item -Recurse -Force -LiteralPath $script:GitHooksDir`. Preserve unsafe directories and throw an actionable
error naming the path.

- [ ] **Step 7: Run PowerShell verification**

Run:

```bash
powershell.exe -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1
pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1
```

If `powershell.exe` is unavailable on the development host, run the `pwsh` command locally and require both commands
from the Windows CI job before merge. Expected output for each available runtime:
`PASS: PowerShell developer installer tests`.

- [ ] **Step 8: Commit PowerShell uninstall**

```bash
git add global-scripts/qubership-dev-install.ps1 global-scripts/tests/qubership-dev-install.Tests.ps1
git commit -m "feat(installer): add PowerShell uninstall mode"
```

### Task 6: Document the lifecycle and run the release-quality gate

**Files:**

- Modify: `global-scripts/README.md`
- Modify: `docs/superpowers/specs/2026-07-20-uninstall-support-design.md`

**Interfaces:**

- Consumes: the final CLI and platform contracts from Tasks 1-5.
- Produces: user-facing uninstall and purge instructions plus an implemented design status.

- [ ] **Step 1: Add uninstall documentation**

Add an `## Uninstall` section after component selection with these commands and rules:

````markdown
## Uninstall

Remove every managed component while preserving telemetry settings and buffered events:

```sh
qubership-dev-install.sh --uninstall
```

```powershell
./qubership-dev-install.ps1 -Uninstall
```

Add `--purge` or `-Purge` to also delete the telemetry configuration and cache. Purge deletes the collector token,
private CA, machine identity, repository policy, delivery settings, diagnostics, and unsent events.

The uninstall keeps the APM CLI, the `qubership-ai-packages` marketplace registration, shared PATH entries, and the
nonsensitive telemetry hook-removal receipt. It preserves unrelated harness hooks and an unrelated global Git hooks
path. A modified managed Git hooks clone is preserved and reported as a failure for manual review.
````

Update the options table with uninstall and purge rows. State that harness and install-only options are rejected in
uninstall mode. Add the manual marketplace removal command only as an optional action after the user verifies it is
unused.

- [ ] **Step 2: Mark the design implemented**

Change `Status: proposed design.` to `Status: implemented design.` only after Tasks 1-5 pass their platform suites.

- [ ] **Step 3: Run the full local verification gate**

Run:

```bash
test -z "$(gofmt -l *.go)"
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
sh global-scripts/tests/qubership-dev-install_test.sh
shellcheck global-scripts/qubership-dev-install.sh global-scripts/tests/qubership-dev-install_test.sh
sh -n global-scripts/qubership-dev-install.sh
sh -n global-scripts/tests/qubership-dev-install_test.sh
pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1
git diff --check origin/main...HEAD
```

Expected: all commands exit `0`; POSIX and PowerShell scripts print their PASS lines; Git reports no whitespace errors.
On Windows CI, also require the Windows PowerShell 5.1 command from Task 5 and the existing workflow matrix.

- [ ] **Step 4: Review the final diff against the spec**

Confirm all of these from the diff and tests:

- install invalidation is fail-closed and precedes legacy cleanup;
- full hook uninstall writes a receipt, subset uninstall does not, and purge preserves it;
- native JSON and modified Codex rules preserve unrelated user state;
- repeat uninstall and purge are successful;
- APM CLI, marketplace registration, PATH, external telemetry commands, and unsafe Git clones remain;
- no Java check runs during uninstall;
- platform component failures aggregate without stopping independent components;
- no release workflow change or new release asset is present.

- [ ] **Step 5: Commit documentation and implemented status**

```bash
git add global-scripts/README.md docs/superpowers/specs/2026-07-20-uninstall-support-design.md
git commit -m "docs: explain developer tool uninstall"
```

- [ ] **Step 6: Request final code review before PR publication**

Use `superpowers:requesting-code-review` against `origin/main...HEAD`. Resolve confirmed findings, rerun the full gate,
then use `superpowers:finishing-a-development-branch` to prepare the PR handoff.
