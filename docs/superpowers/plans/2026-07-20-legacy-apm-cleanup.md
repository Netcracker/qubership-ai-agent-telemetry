# Automatic legacy APM cleanup implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every CLI-managed hook installation attempt best-effort removal of the exact legacy global APM
telemetry dependency.

**Architecture:** Add a focused Go compatibility module that parses the global APM manifest, invokes `apm uninstall`
when needed, and reports cleanup failures as warnings. Route `configure` and `hooks install` through one orchestration
function that always continues to native hook canonicalization.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, existing native hook adapters, table-driven Go tests, POSIX and PowerShell
installer black-box tests, and Markdown documentation.

## Global Constraints

- Cleanup is best effort and never changes a command exit code.
- Install and canonicalize requested hooks after every cleanup warning.
- Skip cleanup when no hook targets are requested.
- Match only the exact legacy package after case-insensitive comparison and optional revision removal.
- Parse only the top-level YAML `dependencies` sequence.
- Send cleanup warnings to `stderr`.
- Include at most 4 KiB of failed subprocess output in diagnostics and mark truncation.
- Suppress subprocess output after successful cleanup.
- Preserve configuration written by `configure` when cleanup or hook installation reports a later problem.
- Keep platform installers free of APM manifest parsing and direct legacy-package uninstall logic.

---

### Task 1: Rebuild the branch from the accepted base and detect the legacy dependency

**Files:**

- Create: `legacy_apm.go`
- Create: `legacy_apm_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**

- Produces: `legacyTelemetryAPMPackage string` with the exact retired global package path.
- Produces: `hasLegacyTelemetryAPMDependency([]byte) (bool, error)` for typed YAML detection.
- Produces: `normalizeAPMDependency(string) string` for whitespace and revision normalization.

- [ ] **Step 1: Remove the rejected script-based commits from the branch history**

Rebase the approved spec and this plan directly onto the PR base:

```bash
git rebase --onto origin/main d52eeff3c8de11026d172053e5d9a727a76f6193
git log --oneline origin/main..HEAD
git diff --name-status origin/main...HEAD
```

Expected: the log contains only the design and plan commits. The diff contains no changes to
`global-scripts/qubership-dev-install.sh`, `global-scripts/qubership-dev-install.ps1`, or their tests.

- [ ] **Step 2: Write failing manifest-detection tests**

Create `legacy_apm_test.go` with a table that covers plain, quoted, revision-pinned, commented, case-varied, near-match,
and unrelated-list values:

```go
func TestHasLegacyTelemetryAPMDependency(t *testing.T) {
    tests := []struct {
        name string
        yaml string
        want bool
    }{
        {name: "plain", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "\n", want: true},
        {name: "revision", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "#v1.0.0\n", want: true},
        {name: "single quoted", yaml: "dependencies:\n  - '" + legacyTelemetryAPMPackage + "#sha'\n", want: true},
        {name: "double quoted", yaml: "dependencies:\n  - \"" + legacyTelemetryAPMPackage + "#sha\"\n", want: true},
        {name: "comment", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "#sha # old hook\n", want: true},
        {name: "case insensitive", yaml: "dependencies:\n  - netcracker/Qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry\n", want: true},
        {name: "near match", yaml: "dependencies:\n  - " + legacyTelemetryAPMPackage + "-extra\n"},
        {name: "unrelated list", yaml: "examples:\n  - " + legacyTelemetryAPMPackage + "\n"},
        {name: "absent", yaml: "dependencies:\n  - another/package\n"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := hasLegacyTelemetryAPMDependency([]byte(tt.yaml))
            if err != nil {
                t.Fatal(err)
            }
            if got != tt.want {
                t.Fatalf("match = %v, want %v", got, tt.want)
            }
        })
    }
}

func TestHasLegacyTelemetryAPMDependencyRejectsMalformedYAML(t *testing.T) {
    if _, err := hasLegacyTelemetryAPMDependency([]byte("dependencies: [\n")); err == nil {
        t.Fatal("expected malformed YAML error")
    }
}
```

- [ ] **Step 3: Run the focused tests and confirm the red state**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestHasLegacyTelemetryAPMDependency' -count=1
```

Expected: build failure because `legacyTelemetryAPMPackage` and `hasLegacyTelemetryAPMDependency` do not exist.

- [ ] **Step 4: Implement typed manifest detection**

Add `gopkg.in/yaml.v3 v3.0.1` as a direct dependency. Implement the detection core in `legacy_apm.go`:

```go
const legacyTelemetryAPMPackage =
    "Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry"

type globalAPMManifest struct {
    Dependencies []string `yaml:"dependencies"`
}

func normalizeAPMDependency(value string) string {
    value = strings.TrimSpace(value)
    if revision := strings.IndexByte(value, '#'); revision >= 0 {
        value = value[:revision]
    }
    return strings.TrimSpace(value)
}

func hasLegacyTelemetryAPMDependency(data []byte) (bool, error) {
    var manifest globalAPMManifest
    if err := yaml.Unmarshal(data, &manifest); err != nil {
        return false, err
    }
    for _, dependency := range manifest.Dependencies {
        if strings.EqualFold(normalizeAPMDependency(dependency), legacyTelemetryAPMPackage) {
            return true, nil
        }
    }
    return false, nil
}
```

- [ ] **Step 5: Run focused and full Go tests**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestHasLegacyTelemetryAPMDependency' -count=1
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod go test ./... -count=1
```

Expected: both commands pass.

- [ ] **Step 6: Commit dependency detection**

```bash
git add legacy_apm.go legacy_apm_test.go go.mod go.sum
git commit -m "fix(hooks): detect legacy global APM dependency"
```

### Task 2: Attempt cleanup and emit bounded best-effort diagnostics

**Files:**

- Modify: `legacy_apm.go`
- Modify: `legacy_apm_test.go`

**Interfaces:**

- Consumes: `hasLegacyTelemetryAPMDependency([]byte) (bool, error)` from Task 1.
- Produces: `cleanupLegacyTelemetryAPMWith(home string, warnings io.Writer, lookPath func(string) (string, error),
  runCommand func(string, ...string) (string, error))`.
- Produces: `cleanupLegacyTelemetryAPM(home string, warnings io.Writer)` production wrapper.
- Produces: `limitAPMDiagnostic(string) (string, bool)` with a 4 KiB subprocess-text limit and truncation flag.

- [ ] **Step 1: Write failing no-op and invocation tests**

Add tests that create a real temporary `<home>/.apm/apm.yml` and use function values for process boundaries:

```go
func writeGlobalAPMManifest(t *testing.T, contents string) string {
    t.Helper()
    home := t.TempDir()
    dir := filepath.Join(home, ".apm")
    if err := os.MkdirAll(dir, 0o700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(dir, "apm.yml"), []byte(contents), 0o600); err != nil {
        t.Fatal(err)
    }
    return home
}

func TestCleanupLegacyTelemetryAPMWithUninstallsMatchingDependency(t *testing.T) {
    home := writeGlobalAPMManifest(t, "dependencies:\n  - "+legacyTelemetryAPMPackage+"#sha\n")
    var gotName string
    var gotArgs []string
    var warnings strings.Builder
    cleanupLegacyTelemetryAPMWith(
        home,
        &warnings,
        func(name string) (string, error) { return "/tools/apm", nil },
        func(name string, args ...string) (string, error) {
            gotName = name
            gotArgs = append([]string(nil), args...)
            return "removed\n", nil
        },
    )
    if gotName != "/tools/apm" || !reflect.DeepEqual(gotArgs, []string{"uninstall", "-g", legacyTelemetryAPMPackage}) {
        t.Fatalf("command = %q %v", gotName, gotArgs)
    }
    if warnings.Len() != 0 {
        t.Fatalf("warnings = %q, want none", warnings.String())
    }
}
```

Add separate tests proving that a missing manifest and an absent dependency do not call either function value.

- [ ] **Step 2: Run the invocation tests and confirm the red state**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestCleanupLegacyTelemetryAPMWith' -count=1
```

Expected: build failure because `cleanupLegacyTelemetryAPMWith` does not exist.

- [ ] **Step 3: Implement the best-effort cleanup flow**

Implement these production boundaries in `legacy_apm.go`:

```go
func cleanupLegacyTelemetryAPM(home string, warnings io.Writer) {
    cleanupLegacyTelemetryAPMWith(home, warnings, exec.LookPath, func(name string, args ...string) (string, error) {
        output, err := exec.Command(name, args...).CombinedOutput()
        return string(output), err
    })
}

func cleanupLegacyTelemetryAPMWith(
    home string,
    warnings io.Writer,
    lookPath func(string) (string, error),
    runCommand func(string, ...string) (string, error),
) {
    manifestPath := filepath.Join(home, ".apm", "apm.yml")
    data, err := os.ReadFile(manifestPath)
    if errors.Is(err, os.ErrNotExist) {
        return
    }
    if err != nil {
        warnLegacyAPMVerification(warnings, err)
        return
    }
    installed, err := hasLegacyTelemetryAPMDependency(data)
    if err != nil {
        warnLegacyAPMVerification(warnings, fmt.Errorf("parse %s: %w", manifestPath, err))
        return
    }
    if !installed {
        return
    }
    apm, err := lookPath("apm")
    if err != nil {
        fmt.Fprintln(warnings,
            "warning: legacy APM cleanup could not remove the telemetry dependency: apm was not found on PATH")
        return
    }
    output, err := runCommand(apm, "uninstall", "-g", legacyTelemetryAPMPackage)
    if err == nil {
        return
    }
    diagnostic, truncated := limitAPMDiagnostic(output)
    fmt.Fprintf(warnings, "warning: legacy APM cleanup failed: %s uninstall -g %s: %v\n",
        apm, legacyTelemetryAPMPackage, err)
    if diagnostic != "" {
        fmt.Fprintf(warnings, "apm output:\n%s\n", diagnostic)
    }
    if truncated {
        fmt.Fprintln(warnings, "[apm output truncated]")
    }
}
```

Keep `limitAPMDiagnostic` simple: trim surrounding whitespace, return at most the first `4 << 10` bytes, and report
whether more subprocess text existed. Do not add a streaming writer abstraction.

- [ ] **Step 4: Write failing diagnostic-contract tests**

Add distinct tests for unreadable/malformed manifests, missing `apm`, failed uninstall, 4 KiB truncation, and successful
output suppression. The success assertion must use a nonempty subprocess result:

```go
func TestCleanupLegacyTelemetryAPMWithSuppressesSuccessfulOutput(t *testing.T) {
    home := writeGlobalAPMManifest(t, "dependencies:\n  - "+legacyTelemetryAPMPackage+"\n")
    var warnings strings.Builder
    cleanupLegacyTelemetryAPMWith(
        home,
        &warnings,
        func(string) (string, error) { return "apm", nil },
        func(string, ...string) (string, error) { return "successful internal output\n", nil },
    )
    if warnings.Len() != 0 {
        t.Fatalf("warnings = %q, want successful output suppressed", warnings.String())
    }
}
```

- [ ] **Step 5: Run diagnostic tests, complete the helpers, and confirm green**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestCleanupLegacyTelemetryAPMWith|TestLimitAPMDiagnostic' -count=1
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod go test ./... -count=1
```

Expected: both commands pass. Failed subprocess diagnostics contain the command, error, bounded output, and truncation
marker. Successful subprocess output is absent.

- [ ] **Step 6: Commit cleanup diagnostics**

```bash
git add legacy_apm.go legacy_apm_test.go
git commit -m "fix(hooks): clean up legacy global APM package"
```

### Task 3: Run cleanup before every CLI-managed hook installation

**Files:**

- Modify: `hooks.go`
- Modify: `hooks_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**

- Consumes: `cleanupLegacyTelemetryAPM(home string, warnings io.Writer)` from Task 2.
- Produces: `installManagedHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult`.
- Produces: `installManagedHooksWith(home string, targets []hookTarget, warnings io.Writer,
  cleanup func(string, io.Writer), install func(string, []hookTarget) []hookInstallResult) []hookInstallResult`.
- Produces: `runWithStderr(args []string, stdout func(string), stderr io.Writer) int` for command diagnostics tests.

- [ ] **Step 1: Write failing orchestration tests**

Add tests that record call order, prove continuation after a cleanup warning, and prove cleanup suppression for no
targets:

```go
func TestInstallManagedHooksWithCleansBeforeInstalling(t *testing.T) {
    var calls []string
    var warnings strings.Builder
    results := installManagedHooksWith(
        "/home/test",
        []hookTarget{hookClaude},
        &warnings,
        func(_ string, warnings io.Writer) {
            calls = append(calls, "cleanup")
            fmt.Fprintln(warnings, "cleanup warning")
        },
        func(_ string, _ []hookTarget) []hookInstallResult {
            calls = append(calls, "install")
            return []hookInstallResult{{Target: hookClaude, Path: "/hook"}}
        },
    )
    if !reflect.DeepEqual(calls, []string{"cleanup", "install"}) {
        t.Fatalf("calls = %v", calls)
    }
    if err := hookInstallError(results); err != nil {
        t.Fatalf("hook result changed by cleanup warning: %v", err)
    }
}
```

The empty-target test must assert `cleanup` is not called and `install` is still called with the empty slice.

- [ ] **Step 2: Run orchestration tests and confirm the red state**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestInstallManagedHooksWith' -count=1
```

Expected: build failure because the orchestration functions do not exist.

- [ ] **Step 3: Implement orchestration and route both commands through it**

Add this boundary to `hooks.go`:

```go
func installManagedHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult {
    return installManagedHooksWith(home, targets, warnings, cleanupLegacyTelemetryAPM, installHooks)
}

func installManagedHooksWith(
    home string,
    targets []hookTarget,
    warnings io.Writer,
    cleanup func(string, io.Writer),
    install func(string, []hookTarget) []hookInstallResult,
) []hookInstallResult {
    if len(targets) > 0 {
        cleanup(home, warnings)
    }
    return install(home, targets)
}
```

Keep `run` as the production wrapper and move its existing body into `runWithStderr`. Replace direct `os.Stderr` writes
inside that command router with the supplied writer. Call
`installManagedHooks(userHomeDir(), opts.Hooks, stderr)` from `configure` and
`installManagedHooks(home, targets, stderr)` from `hooks install`.

- [ ] **Step 4: Write failing command-contract tests**

Use malformed `<home>/.apm/apm.yml` files to exercise the real warning path without an external process:

```go
func TestRunHooksInstallContinuesAfterLegacyAPMCleanupWarning(t *testing.T) {
    home := writeGlobalAPMManifest(t, "dependencies: [\n")
    t.Setenv("HOME", home)
    t.Setenv("USERPROFILE", home)
    var out, stderr strings.Builder
    code := runWithStderr([]string{"hooks", "install", "--target=claude"},
        func(value string) { out.WriteString(value) }, &stderr)
    if code != 0 {
        t.Fatalf("exit code = %d, want 0; output = %q", code, out.String())
    }
    if !strings.Contains(stderr.String(), "could not verify or remove") {
        t.Fatalf("stderr = %q", stderr.String())
    }
    if _, err := os.Stat(hookPath(home, hookClaude)); err != nil {
        t.Fatalf("Claude hook not installed after cleanup warning: %v", err)
    }
}
```

Add a `configure` counterpart that asserts exit code `0`, the warning on `stderr`, the persisted env file, and all
requested hook files. Add a `configure --hooks=none` test with malformed YAML that asserts no cleanup warning.

- [ ] **Step 5: Run command tests and the full Go suite**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod \
  go test . -run 'TestRun(HooksInstall|Configure).*LegacyAPM' -count=1
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod go test ./... -count=1
```

Expected: cleanup warnings appear only on `stderr`; successful configuration and hook installation return exit code
`0`; `--hooks=none` emits no cleanup warning.

- [ ] **Step 6: Commit CLI orchestration**

```bash
git add hooks.go hooks_test.go main.go main_test.go
git commit -m "fix(hooks): migrate legacy APM registration"
```

### Task 4: Document the automatic best-effort cleanup

**Files:**

- Modify: `README.md`
- Modify: `docs/cli.md`
- Modify: `docs/adr/0005-cli-managed-global-hooks.md`
- Modify: `global-scripts/README.md`
- Verify: `agent-packages/ai-agent-telemetry/README.md`

**Interfaces:**

- Consumes: the warning and exit-code contract implemented in Tasks 2 and 3.
- Produces: user-facing guidance that distinguishes the global dependency cleanup from repository-local APM
  compatibility.

- [ ] **Step 1: Update the root installation and legacy-package guidance**

Replace wording that suggests a retained global dependency is harmless. State:

```text
Before it writes CLI-managed hooks, the CLI checks the global APM manifest for the legacy telemetry hook package and
asks APM to uninstall that exact dependency. Cleanup failures are warnings: the CLI still canonicalizes the requested
native hooks, and the command succeeds when configuration and hook installation succeed.
```

Keep the repository-local APM package documented as a compatibility surface. Clarify that automatic cleanup reads only
`~/.apm/apm.yml` and does not edit project manifests.

- [ ] **Step 2: Update CLI and ADR contracts**

Add the cleanup attempt, `stderr` warning, best-effort exit policy, and `configure` partial-result behavior to
`docs/cli.md`. Update ADR 0005 so the retained package remains available for repository-local consumers while the CLI
automatically attempts to remove its global dependency before native hook installation.

- [ ] **Step 3: Update developer-installer documentation**

In `global-scripts/README.md`, explain that the telemetry binary owns legacy cleanup. Do not condition the behavior on
selecting both the APM and telemetry components.

- [ ] **Step 4: Verify the retained package documentation remains accurate**

```bash
rg -n 'legacy|global|apm.yml|CLI-managed' README.md docs/cli.md docs/adr/0005-cli-managed-global-hooks.md \
  global-scripts/README.md agent-packages/ai-agent-telemetry/README.md
git diff --check
```

Expected: global cleanup and repository-local compatibility are distinct; no document claims cleanup is guaranteed;
Markdown has no whitespace errors.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md docs/cli.md docs/adr/0005-cli-managed-global-hooks.md global-scripts/README.md
git commit -m "docs(hooks): explain legacy APM cleanup"
```

### Task 5: Verify and update PR #20

**Files:**

- Verify: all files changed from `origin/main`.
- Update externally: PR #20 description and inline review thread.

**Interfaces:**

- Consumes: all implementation and documentation from Tasks 1 through 4.
- Produces: a clean PR containing no rejected platform-specific implementation and a review reply in the original
  inline thread.

- [ ] **Step 1: Run focused static checks**

```bash
gofmt -w legacy_apm.go legacy_apm_test.go hooks.go hooks_test.go main.go main_test.go
git diff --check
go vet ./...
```

Expected: no output from `gofmt` diff checks or `go vet`.

- [ ] **Step 2: Run all automated tests**

```bash
env GOCACHE=/tmp/legacy-apm-go-cache GOMODCACHE=/tmp/legacy-apm-go-mod go test ./... -race -count=1
sh global-scripts/tests/qubership-dev-install_test.sh
pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1
shellcheck global-scripts/qubership-dev-install.sh global-scripts/tests/qubership-dev-install_test.sh
```

Expected: every command exits `0` with no test, race, or lint failures.

- [ ] **Step 3: Audit the final diff and history**

```bash
git status --short
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
rg -n 'remove_legacy_telemetry|Remove-LegacyTelemetry|LegacyTelemetryApmPackage' global-scripts
```

Expected: the worktree is clean; history contains the design, plan, and focused implementation commits; no rejected
script parser or script uninstall function remains; the diff matches the spec.

- [ ] **Step 4: Request code review and address findings**

Run `superpowers:requesting-code-review` against `origin/main...HEAD`. Re-run the focused tests for every accepted change,
then repeat Steps 1 through 3.

- [ ] **Step 5: Rewrite the remote PR branch safely**

```bash
git fetch origin fix/remove-legacy-apm-telemetry-package
git push --force-with-lease=refs/heads/fix/remove-legacy-apm-telemetry-package:$(git rev-parse origin/fix/remove-legacy-apm-telemetry-package) \
  origin HEAD:fix/remove-legacy-apm-telemetry-package
```

Expected: GitHub PR #20 now shows the rebuilt commits based directly on `main`.

- [ ] **Step 6: Update the PR description and inline review thread**

Update the PR body:

```bash
gh pr edit 20 --repo Netcracker/qubership-ai-agent-telemetry --body-file - <<'EOF'
## Why

The retired global telemetry APM dependency can restore obsolete hook registrations during a later `apm compile -g`.
Cleanup must apply to every CLI-managed hook installation without duplicating YAML parsing across platform installers.

## What

- Detect the exact legacy dependency in the global APM manifest from the Go CLI.
- Attempt best-effort `apm uninstall` before canonicalizing native hooks.
- Report bounded cleanup diagnostics on stderr without changing a successful command exit code.
- Apply the same behavior to configure, hooks install, and standalone installer paths.

## How to verify

- `go test ./... -race -count=1`
- `sh global-scripts/tests/qubership-dev-install_test.sh`
- `pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1`
- `shellcheck global-scripts/qubership-dev-install.sh global-scripts/tests/qubership-dev-install_test.sh`
EOF
```

Reply to review comment `3610365765` through its inline thread:

```bash
gh api --method POST \
  repos/Netcracker/qubership-ai-agent-telemetry/pulls/20/comments/3610365765/replies \
  -F body=@- <<'EOF'
Implemented in the cross-platform Go CLI. Both configure and hooks install now attempt best-effort removal of the exact
legacy global APM dependency before canonicalizing native hooks. Platform installers no longer own YAML parsing or
uninstall behavior, and standalone installation paths receive the same cleanup.
EOF
```

- [ ] **Step 7: Confirm the remote result**

```bash
gh pr view 20 --repo Netcracker/qubership-ai-agent-telemetry \
  --json headRefOid,title,body,files,commits,reviews,url
gh pr checks 20 --repo Netcracker/qubership-ai-agent-telemetry
```

Expected: the remote head equals local `HEAD`, the PR description describes the Go-owned best-effort cleanup, and all
available checks pass.
