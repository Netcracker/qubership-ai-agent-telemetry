# Global hook installation implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Installation outcome:** One platform-installer run configures and maintains Claude Code, Codex, and Cursor telemetry
hooks, prompting for collector details only when they are missing.

**Architecture:** Add a Go hook-management layer with one adapter per harness and a shared atomic JSON file writer.
`configure` calls the layer by default, while a noninteractive `hooks install` command lets both installers refresh
hooks during upgrades. Status inspects the same canonical definitions so installation, repair, and diagnostics cannot
drift.

**Tech stack:** Go 1.26 standard library, POSIX shell, PowerShell, GitHub Actions, JSON harness configuration, Markdown.

## Global constraints

- Install `claude`, `codex`, and `cursor` hooks by default, even when a harness is not installed.
- Support Linux, macOS, and Windows with native tests.
- Preserve unrelated settings and unknown JSON fields.
- Leave malformed files unchanged, continue other targets, and return an aggregated failure.
- Keep `ai-agent-telemetry ingest --agent=<harness>` as the portable bare command.
- Do not edit Codex trust state; tell the user to inspect and approve the hook after restart.
- Keep the APM hook package only as a legacy compatibility surface.
- Use TDD and commit each independently testable task.
- Before creating the PR, dispatch a separate subagent to review the complete diff and fix confirmed findings.
- Keep the PR description concise, with `Why`, `What`, and `How to verify` sections.

---

## File structure

- `hooks.go`: target parsing, canonical commands, installation orchestration, and result types.
- `hookfile.go`: JSON decoding with number preservation, validation, permissions, and atomic replacement.
- `hooks_claude.go`: merge and inspect `~/.claude/settings.json`.
- `hooks_codex.go`: merge and inspect `~/.codex/hooks.json`.
- `hooks_cursor.go`: merge and inspect `~/.cursor/hooks.json`.
- `hooks_test.go`: target parsing, orchestration, partial failures, and end-to-end file fixtures.
- `hookfile_test.go`: malformed JSON, permissions, preservation, and replacement tests.
- `hooks_*_test.go`: focused native-shape and idempotence tests for each adapter.
- `main.go`, `main_test.go`: CLI routing, configure integration, usage failures, and status output.
- `commands.go`, `commands_test.go`: hook states in the status model and formatter.
- `scripts/install.sh`, `scripts/install.ps1`: configure clean installs and refresh hooks on updates.
- `.github/workflows/go-build.yaml`, `.github/workflows/installer-tests.yaml`: native platform and installer coverage.
- `README.md`, `docs/cli.md`, `docs/agent-integration.md`: primary installation and command documentation.
- `docs/adr/0005-cli-managed-global-hooks.md`: ownership, merge, migration, and trust decision.
- `agent-packages/ai-agent-telemetry/README.md`: legacy APM compatibility notice.
- `agent-packages/ai-agent-telemetry-configure/**`: global CLI-managed hook diagnosis and repair instructions.

---

### Task 1: Define hook targets and CLI parsing

**Files:**

- Create: `hooks.go`
- Create: `hooks_test.go`
- Modify: `main.go`
- Modify: `commands_test.go`
- Modify: `main_test.go`

**Interfaces:**

- Produces: `type hookTarget string` and constants `hookClaude`, `hookCodex`, and `hookCursor`.
- Produces: `func parseHookTargets(string) ([]hookTarget, error)`; empty and `all` return all targets, `none` returns an
  empty slice, and comma-separated values preserve canonical order without duplicates.
- Produces: `type configureOptions struct` and `func parseConfigureFlags([]string) (configureOptions, error)`.
- Produces: `func parseHooksCommand([]string) ([]hookTarget, error)` for `hooks install [--target=...]`.

- [ ] **Step 1: Add failing target parser tests**

Add table tests that cover `""`, `all`, `none`, subsets, duplicates, whitespace, unknown targets, and empty list
members:

```go
func TestParseHookTargets(t *testing.T) {
    tests := []struct {
        name    string
        raw     string
        want    []hookTarget
        wantErr bool
    }{
        {name: "default", want: allHookTargets},
        {name: "all", raw: "all", want: allHookTargets},
        {name: "none", raw: "none", want: []hookTarget{}},
        {name: "subset", raw: "codex,claude", want: []hookTarget{hookClaude, hookCodex}},
        {name: "deduplicate", raw: "cursor,cursor", want: []hookTarget{hookCursor}},
        {name: "unknown", raw: "windsurf", wantErr: true},
        {name: "empty member", raw: "claude,,codex", wantErr: true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := parseHookTargets(tt.raw)
            if (err != nil) != tt.wantErr {
                t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Fatalf("targets = %v, want %v", got, tt.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run the parser test and confirm it fails**

Run: `go test ./... -run 'TestParseHookTargets|TestParseConfigureFlags|TestRunHooks' -count=1`

Expected: FAIL because the hook target types and new parser signatures do not exist.

- [ ] **Step 3: Implement target types and strict parsers**

Add the canonical target order and return errors that name the invalid value:

```go
type hookTarget string

const (
    hookClaude hookTarget = "claude"
    hookCodex  hookTarget = "codex"
    hookCursor hookTarget = "cursor"
)

var allHookTargets = []hookTarget{hookClaude, hookCodex, hookCursor}

type configureOptions struct {
    Endpoint  string
    CAPath    string
    RepoAllow string
    Hooks     []hookTarget
}

func parseHookTargets(raw string) ([]hookTarget, error) {
    if raw == "" || raw == "all" {
        return append([]hookTarget(nil), allHookTargets...), nil
    }
    if raw == "none" {
        return []hookTarget{}, nil
    }
    requested := map[hookTarget]bool{}
    for _, value := range strings.Split(raw, ",") {
        target := hookTarget(strings.TrimSpace(value))
        switch target {
        case hookClaude, hookCodex, hookCursor:
            requested[target] = true
        default:
            return nil, fmt.Errorf("unknown hook target %q", value)
        }
    }
    var targets []hookTarget
    for _, target := range allHookTargets {
        if requested[target] {
            targets = append(targets, target)
        }
    }
    return targets, nil
}
```

Change `parseConfigureFlags` to reject unknown flags and parse `--hooks=<targets>` or `--hooks <targets>`. Add nested
`hooks install` routing to `run`; reject missing actions and actions other than `install` with exit code 2.

- [ ] **Step 4: Run focused and full tests**

Run: `gofmt -w hooks.go hooks_test.go main.go main_test.go commands_test.go`

Run: `go test ./... -run 'TestParseHookTargets|TestParseConfigureFlags|TestRunHooks' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS with existing configure tests updated to use `configureOptions`.

- [ ] **Step 5: Commit target and CLI parsing**

```bash
git add hooks.go hooks_test.go main.go main_test.go commands_test.go
git commit -m "feat(cli): add global hook target options"
```

### Task 2: Add safe JSON file updates and the Claude adapter

**Files:**

- Create: `hookfile.go`
- Create: `hookfile_test.go`
- Create: `hooks_claude.go`
- Create: `hooks_claude_test.go`

**Interfaces:**

- Consumes: `hookTarget` and `hookClaude` from Task 1.
- Produces: `type hookMergeFunc func(map[string]any) (bool, error)`.
- Produces: `func updateHookFile(string, hookMergeFunc) (bool, error)`.
- Produces: `func mergeClaudeHook(map[string]any) (bool, error)` and `func inspectClaudeHook(map[string]any) bool`.

- [ ] **Step 1: Add failing safe-file tests**

Cover a missing file, preservation of a large `json.Number`, existing mode preservation on POSIX, malformed JSON, a
non-object root, and replacement of an existing file:

```go
func TestUpdateHookFileLeavesMalformedJSONUnchanged(t *testing.T) {
    path := filepath.Join(t.TempDir(), "settings.json")
    want := []byte("{not json\n")
    if err := os.WriteFile(path, want, 0o640); err != nil {
        t.Fatal(err)
    }
    if _, err := updateHookFile(path, mergeClaudeHook); err == nil {
        t.Fatal("want malformed JSON error")
    }
    got, err := os.ReadFile(path)
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Equal(got, want) {
        t.Fatalf("malformed file changed: %q", got)
    }
}
```

- [ ] **Step 2: Add failing Claude merge fixtures**

Test a clean object, an unrelated `Bash` matcher, an existing `Skill` matcher, a duplicate canonical handler, an APM
source marker, unknown fields, and a second identical merge. Assert this canonical handler:

```json
{
  "type": "command",
  "command": "ai-agent-telemetry ingest --agent=claude",
  "timeout": 30,
  "statusMessage": "Recording skill telemetry"
}
```

- [ ] **Step 3: Run focused tests and confirm they fail**

Run: `go test ./... -run 'TestUpdateHookFile|TestMergeClaudeHook' -count=1`

Expected: FAIL because the file layer and Claude adapter do not exist.

- [ ] **Step 4: Implement the file layer**

Decode with `json.Decoder.UseNumber`, require a single root object, marshal with two-space indentation and a trailing
newline, and replace through a same-directory temporary file:

```go
type hookMergeFunc func(map[string]any) (bool, error)

func updateHookFile(path string, merge hookMergeFunc) (bool, error) {
    root := map[string]any{}
    mode := os.FileMode(0o600)
    if data, err := os.ReadFile(path); err == nil {
        info, statErr := os.Stat(path)
        if statErr != nil {
            return false, statErr
        }
        mode = info.Mode().Perm()
        decoder := json.NewDecoder(bytes.NewReader(data))
        decoder.UseNumber()
        if err := decoder.Decode(&root); err != nil {
            return false, fmt.Errorf("parse %s: %w", path, err)
        }
        if err := requireJSONEOF(decoder); err != nil {
            return false, fmt.Errorf("parse %s: %w", path, err)
        }
    } else if !errors.Is(err, os.ErrNotExist) {
        return false, err
    }
    changed, err := merge(root)
    if err != nil || !changed {
        return changed, err
    }
    return true, writeJSONAtomically(path, root, mode)
}
```

`writeJSONAtomically` creates the parent with `0700`, writes and syncs a temporary file, applies the existing or new
mode, closes it, renames it over the destination, and removes the temporary file on every error path.

- [ ] **Step 5: Implement the Claude adapter**

Use small typed helpers to require arrays and objects at each occupied schema position. Reuse a `PreToolUse` group whose
matcher is exactly `Skill`; remove only recognized telemetry handlers; append one canonical handler; and preserve every
other value. Return a structural error such as `hooks.PreToolUse must be an array` rather than replacing an incompatible
field.

- [ ] **Step 6: Run focused tests twice, including native replacement behavior**

Run: `gofmt -w hookfile.go hookfile_test.go hooks_claude.go hooks_claude_test.go`

Run: `go test ./... -run 'TestUpdateHookFile|TestMergeClaudeHook' -count=1`

Expected: PASS.

Run: `go test ./... -run 'TestUpdateHookFile|TestMergeClaudeHook' -count=2`

Expected: PASS on repeated runs with no duplicate handler.

- [ ] **Step 7: Commit safe file handling and Claude support**

```bash
git add hookfile.go hookfile_test.go hooks_claude.go hooks_claude_test.go
git commit -m "feat(hooks): install the global Claude hook"
```

### Task 3: Add Codex and Cursor adapters and installation orchestration

**Files:**

- Create: `hooks_codex.go`
- Create: `hooks_codex_test.go`
- Create: `hooks_cursor.go`
- Create: `hooks_cursor_test.go`
- Modify: `hooks.go`
- Modify: `hooks_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**

- Consumes: `updateHookFile` and the target parser.
- Produces: `func mergeCodexHook(map[string]any) (bool, error)` and `func inspectCodexHook(map[string]any) bool`.
- Produces: `func mergeCursorHook(map[string]any) (bool, error)` and `func inspectCursorHook(map[string]any) bool`.
- Produces: `type hookInstallResult struct { Target hookTarget; Path string; Changed bool; Err error }`.
- Produces: `func installHooks(string, []hookTarget) []hookInstallResult` where the string is an explicit home
  directory.
- Produces: `func hookInstallError([]hookInstallResult) error` using `errors.Join`.

- [ ] **Step 1: Add failing Codex adapter tests**

Cover unrelated `Stop` handlers, a canonical hook with `_apm_source: "ai-agent-telemetry"`, duplicates, a known legacy
telemetry fixture, incompatible structures, and idempotence. The canonical entry is:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "ai-agent-telemetry ingest --agent=codex",
            "timeout": 30,
            "statusMessage": "Recording skill telemetry"
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 2: Add failing Cursor adapter tests**

Cover absent and existing `version`, preservation of unrelated events and commands, duplicate removal, malformed event
arrays, and idempotence. The canonical entry is:

```json
{
  "version": 1,
  "hooks": {
    "afterAgentResponse": [
      {
        "command": "ai-agent-telemetry ingest --agent=cursor"
      }
    ]
  }
}
```

- [ ] **Step 3: Add failing orchestration and CLI tests**

Create three files under a temporary home, make the Claude file malformed, and assert that Codex and Cursor are still
installed while the aggregate error names only Claude. Add `run([]string{"hooks", "install"}, ...)` tests for success,
unknown targets, and an unavailable home directory.

- [ ] **Step 4: Run focused tests and confirm they fail**

Run: `go test ./... -run 'TestMergeCodexHook|TestMergeCursorHook|TestInstallHooks|TestRunHooks' -count=1`

Expected: FAIL because both adapters and installation orchestration are missing.

- [ ] **Step 5: Implement Codex and Cursor adapters**

Follow the same structural validation and narrow ownership rules as Claude. For Codex, remove `_apm_source` only from
the telemetry-owned group that is being canonicalized. For Cursor, preserve a numeric `version`; add `json.Number("1")`
when it is absent and reject a present nonnumeric value.

- [ ] **Step 6: Implement orchestration and connect `hooks install`**

Map targets to paths and merge functions:

```go
func hookPath(home string, target hookTarget) string {
    switch target {
    case hookClaude:
        return filepath.Join(home, ".claude", "settings.json")
    case hookCodex:
        return filepath.Join(home, ".codex", "hooks.json")
    case hookCursor:
        return filepath.Join(home, ".cursor", "hooks.json")
    default:
        return ""
    }
}
```

Run every requested target, collect results in canonical order, print `installed` or `unchanged` per target, and return
one joined error after all targets complete.

- [ ] **Step 7: Run focused and full tests**

Run:

```bash
gofmt -w hooks.go hooks_test.go hooks_codex.go hooks_codex_test.go \
  hooks_cursor.go hooks_cursor_test.go main.go main_test.go
```

Run: `go test ./... -run 'TestMergeCodexHook|TestMergeCursorHook|TestInstallHooks|TestRunHooks' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit all-harness installation**

```bash
git add hooks.go hooks_test.go hooks_codex.go hooks_codex_test.go \
  hooks_cursor.go hooks_cursor_test.go main.go main_test.go
git commit -m "feat(hooks): install Codex and Cursor hooks"
```

### Task 4: Integrate hooks with configure and status

**Files:**

- Modify: `main.go`
- Modify: `main_test.go`
- Modify: `commands.go`
- Modify: `commands_test.go`
- Modify: `hooks.go`
- Modify: `hooks_test.go`

**Interfaces:**

- Consumes: `installHooks`, adapter inspection functions, and `configureOptions.Hooks`.
- Produces: `type hookState string` with `installed`, `missing`, and `invalid`.
- Produces: `type hookStatus struct { Target hookTarget; Path string; State hookState; Detail string }`.
- Produces: `func gatherHookStatus(string) []hookStatus`.
- Extends: `statusReport` with `Hooks []hookStatus`.

- [ ] **Step 1: Add failing configure integration tests**

Isolate `HOME`, `XDG_CONFIG_HOME`, and `XDG_CACHE_HOME`. Run configure with a test endpoint and EOF for the optional
token, then assert all three global files exist. Add `--hooks=none`, a two-target subset, and a malformed Claude file
that still lets the telemetry env file and other hooks be written.

- [ ] **Step 2: Add failing status tests**

Test all three states and normal versus verbose formatting:

```text
hooks:
  claude: installed
  codex: missing
  cursor: invalid
```

Verbose output must append each path and the Cursor parse detail. Normal output must not expose a raw parser error.

- [ ] **Step 3: Run focused tests and confirm they fail**

Run: `go test ./... -run 'TestRunConfigureInstallsHooks|TestGatherHookStatus|TestFormatStatus.*Hooks' -count=1`

Expected: FAIL because configure does not install hooks and status has no hook model.

- [ ] **Step 4: Install selected hooks from configure**

After `applyConfigure` succeeds, call `installHooks(userHomeDir(), options.Hooks)`. Gather and print the existing status
report even when one hook target fails, then print the aggregate hook error to stderr and return exit code 1. Do not
prompt for endpoint or token in the `hooks install` route.

- [ ] **Step 5: Implement read-only hook status**

Read every native file without creating directories. Map absence or absence of the canonical command to `missing`, a
decode or schema failure to `invalid`, and exactly one canonical entry to `installed`. Append the status block in
canonical target order. Keep trust reminders out of repeated status output. Print the following reminder from a
successful `configure` or `hooks install` operation that changed the Codex file, and document it in the README:

```text
restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted
```

- [ ] **Step 6: Run focused and full tests**

Run: `gofmt -w main.go main_test.go commands.go commands_test.go hooks.go hooks_test.go`

Run: `go test ./... -run 'TestRunConfigureInstallsHooks|TestGatherHookStatus|TestFormatStatus.*Hooks' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit configure and status integration**

```bash
git add main.go main_test.go commands.go commands_test.go hooks.go hooks_test.go
git commit -m "feat(configure): manage and report global hooks"
```

### Task 5: Refresh hooks from both platform installers

**Files:**

- Modify: `scripts/install.sh`
- Modify: `scripts/install.ps1`
- Modify: `.github/workflows/installer-tests.yaml`

**Interfaces:**

- Consumes: `ai-agent-telemetry configure` and `ai-agent-telemetry hooks install`.
- Preserves: `--skip-config` and `-SkipConfig` as a skip for all user configuration writes.

- [ ] **Step 1: Extend the POSIX installer fixture to fail under the old behavior**

Make both fake binaries log their complete arguments. Add an endpoint to the test home before the force update, run the
installer without `--skip-config`, and assert this exact call sequence:

```text
configure
hooks install
```

Add a separate skip-config run whose log remains unchanged.

- [ ] **Step 2: Extend the PowerShell installer fixture to fail under the old behavior**

Run the test with an executable test binary on a native Windows runner. Assert a clean install invokes `configure`, an
existing endpoint invokes `hooks install`, and `-SkipConfig` invokes neither.

- [ ] **Step 3: Run installer tests and confirm the update case fails**

Run: `actionlint .github/workflows/installer-tests.yaml`

Expected: PASS if `actionlint` is installed; otherwise record that CI performs this validation.

Run the local POSIX fixture commands from the workflow.

Expected: FAIL because the current installer does nothing when an endpoint already exists.

- [ ] **Step 4: Split configure-or-refresh behavior in both installers**

Use the same decision in shell and PowerShell:

```sh
configure_or_refresh_hooks() {
  env_file="$(config_dir)/env"
  endpoint="$(env_value AI_AGENT_TELEMETRY_ENDPOINT "$env_file")"
  if [ -z "$endpoint" ]; then
    "$BIN" configure
  else
    "$BIN" hooks install
  fi
}
```

```powershell
function Configure-OrRefreshHooks([string]$Bin) {
  $values = Read-EnvFile (Join-Path (Config-Dir) 'env')
  if ([string]::IsNullOrWhiteSpace($values['AI_AGENT_TELEMETRY_ENDPOINT'])) {
    & $Bin configure
  } else {
    & $Bin hooks install
  }
  if ($LASTEXITCODE -ne 0) { throw "hook configuration failed with exit code $LASTEXITCODE" }
}
```

- [ ] **Step 5: Run shell checks and installer fixtures**

Run: `sh -n scripts/install.sh`

Run:

```powershell
pwsh -NoProfile -Command '$errors = $null; $tokens = $null;
  [System.Management.Automation.Language.Parser]::ParseFile(
    "scripts/install.ps1", [ref]$tokens, [ref]$errors) | Out-Null;
  if ($errors) { $errors; exit 1 }'
```

Expected: both commands exit 0.

Run the updated local installer fixture steps.

Expected: clean, update, force update, and skip-config cases pass.

- [ ] **Step 6: Commit installer migration behavior**

```bash
git add scripts/install.sh scripts/install.ps1 .github/workflows/installer-tests.yaml
git commit -m "feat(installer): refresh global hooks on update"
```

### Task 6: Replace the APM-first documentation and update the setup skill

**Files:**

- Modify: `README.md`
- Modify: `docs/cli.md`
- Modify: `docs/agent-integration.md`
- Create: `docs/adr/0005-cli-managed-global-hooks.md`
- Modify: `agent-packages/ai-agent-telemetry/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/instructions/ai-agent-telemetry-configure.instructions.md`
- Modify:
  `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/references/codex-sandbox.md`
- Create: `hooks_package_test.go`

**Interfaces:**

- Consumes: final CLI names, output, file locations, and trust behavior from Tasks 1 through 5.
- Produces: an APM package parity test that reads the three package JSON files and verifies their command strings.

- [ ] **Step 1: Add a failing legacy-package parity test**

Read each file under `agent-packages/ai-agent-telemetry/.apm/hooks/`, decode it, and compare the command with
`canonicalHookCommand(target)`. Fail with the target and path when a package command drifts.

- [ ] **Step 2: Rewrite the root TL;DR and installation flow**

Keep the TL;DR complete and short:

```text
1. Run the installer for the operating system.
2. Complete `configure` if prompted.
3. Run `ai-agent-telemetry status` and `ai-agent-telemetry selftest`.
4. Fully restart Claude Code, Codex, or Cursor.
5. Inspect and trust the telemetry hook if the harness prompts.
```

Remove APM installation from the required path. Explain `--hooks=all|none|<list>`, the noninteractive repair command,
the distinction between status and selftest, and the exact Codex command the user approves.

- [ ] **Step 3: Update reference docs and add the ADR**

Document native global paths, merge preservation, installer migration, malformed-file behavior, legacy package scope,
and why the CLI does not modify Codex trust state. Leave historical design and decision records unchanged; the new ADR
supersedes their APM-first installation assumptions.

- [ ] **Step 4: Update the optional setup skill**

Replace project-level `apm install` repair steps with:

```sh
ai-agent-telemetry hooks install
ai-agent-telemetry status --verbose
```

Keep the Codex sandbox rule because the global hook still needs permission to read machine configuration and use the
network. Remove instructions that clear trust hashes automatically; tell the user to review and approve a changed
command in Codex.

- [ ] **Step 5: Mark the APM hook package as legacy**

State that existing APM consumers may keep using the package, but new global installations use the CLI. Do not remove
the package or change its manifest in this PR.

- [ ] **Step 6: Run documentation and parity checks**

Run: `gofmt -w hooks_package_test.go && go test ./... -run TestLegacyHookPackageParity -count=1`

Expected: PASS.

Run:

```bash
rg -n 'Install APM|apm install .*ai-agent-telemetry --target|hooks \(every repository' \
  README.md docs/cli.md docs/agent-integration.md agent-packages/ai-agent-telemetry/README.md
```

Expected: no APM-first installation instructions; only the explicitly labeled legacy section may mention the old
command.

Run: `git diff --check`

Expected: no whitespace errors.

- [ ] **Step 7: Commit documentation and compatibility updates**

```bash
git add README.md docs/cli.md docs/agent-integration.md docs/adr/0005-cli-managed-global-hooks.md \
  agent-packages/ai-agent-telemetry agent-packages/ai-agent-telemetry-configure hooks_package_test.go
git commit -m "docs: document machine-wide hook setup"
```

### Task 7: Add native macOS and Windows CI coverage

**Files:**

- Modify: `.github/workflows/go-build.yaml`
- Modify: `.github/workflows/installer-tests.yaml`
- Modify: `scripts/has-relevant-changes.sh` only if hook files are incorrectly excluded by the existing filter.

**Interfaces:**

- Consumes: native Go tests and installer fixtures from earlier tasks.
- Produces: required Linux coverage through the existing job plus native macOS and Windows hook test jobs.

- [ ] **Step 1: Add platform jobs**

Extend the workflow with a matrix for the two platforms not covered by the existing Ubuntu build:

```yaml
  hook-platform-tests:
    name: "Global hooks (${{ matrix.os }})"
    strategy:
      fail-fast: false
      matrix:
        os: [macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    timeout-minutes: 15
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - name: Test global hook files
        run: go test ./... -run 'Hook|Configure' -count=1
```

Retain pinned action SHAs and least-privilege permissions. Ensure Windows executes an existing-file replacement test and
uses a temporary `USERPROFILE`; ensure macOS executes the POSIX permission test.

- [ ] **Step 2: Validate workflow syntax and local test selection**

Run: `actionlint .github/workflows/go-build.yaml .github/workflows/installer-tests.yaml`

Expected: PASS when `actionlint` is available; otherwise rely on Super-Linter and GitHub workflow parsing.

Run: `go test ./... -run 'Hook|Configure' -count=1`

Expected: PASS on the local Linux environment.

- [ ] **Step 3: Commit CI coverage**

```bash
git add .github/workflows/go-build.yaml .github/workflows/installer-tests.yaml scripts/has-relevant-changes.sh
git commit -m "ci: test global hooks on macOS and Windows"
```

### Task 8: Verify, review with a subagent, and prepare the PR

**Files:**

- Modify: any file with a confirmed review finding.
- Do not create a repository file for the PR description unless the repository already requires one.

**Interfaces:**

- Consumes: the complete branch diff and all verification commands.
- Produces: a reviewed branch and a concise PR description.

- [ ] **Step 1: Run the complete local verification suite**

Run:

```bash
gofmt -w *.go
go vet ./...
go test ./... -count=1
sh -n scripts/install.sh
git diff --check
make check
```

Expected: every command exits 0. If `make check` already includes one of the preceding commands, keep the explicit
commands as evidence for the PR.

- [ ] **Step 2: Exercise the CLI against an isolated home**

Build a temporary binary, set `HOME`, `XDG_CONFIG_HOME`, and `XDG_CACHE_HOME` to temporary directories, and run:

```bash
go build -o /tmp/ai-agent-telemetry-hook-test .
HOME=/tmp/ai-agent-telemetry-hook-home \
XDG_CONFIG_HOME=/tmp/ai-agent-telemetry-hook-home/.config \
XDG_CACHE_HOME=/tmp/ai-agent-telemetry-hook-home/.cache \
/tmp/ai-agent-telemetry-hook-test hooks install
```

Expected: all three hook files exist, contain one canonical command each, and a second run reports unchanged files.

- [ ] **Step 3: Dispatch the required independent subagent review**

Give a fresh review subagent the design spec, implementation plan, `git diff main...HEAD`, and these explicit review
areas: JSON preservation, legacy-entry ownership, partial failures, Windows replacement semantics, installer migration,
status accuracy, trust documentation, and missing tests. Require evidence-backed findings with file and line references.

- [ ] **Step 4: Resolve review findings**

Verify every finding against the code and tests. Fix confirmed defects using a failing regression test first. Record
rejected findings with the technical reason in the working notes, not in the repository.

- [ ] **Step 5: Rerun affected and full verification**

Run the focused regression test for each fix, then repeat Step 1. Expected: every command exits 0.

- [ ] **Step 6: Commit review fixes if needed**

```bash
git add -u
git commit -m "fix(hooks): address independent review findings"
```

Skip this commit when the review has no confirmed findings.

- [ ] **Step 7: Check branch and commit history**

Run: `git status --short --branch`

Expected: clean feature branch.

Run: `git log --oneline --decorate main..HEAD`

Expected: the design commit and focused implementation commits appear in dependency order.

- [ ] **Step 8: Push and create the PR**

Use a concise description:

```markdown
## Why

Global telemetry setup required separate machine and project-level steps, so installing and configuring the CLI did not
leave every supported harness ready to use.

## What

- install and repair Claude Code, Codex, and Cursor hooks from the CLI
- refresh hooks during installer upgrades while preserving existing harness settings
- document restart and trust steps, and keep the APM package as a legacy option

## How to verify

- `go vet ./...`
- `go test ./... -count=1`
- `make check`
- native Linux, macOS, Windows, and installer CI jobs
```

Confirm the PR base is `main`, inspect the rendered description, and report the PR link plus the final verification
results to the user.
