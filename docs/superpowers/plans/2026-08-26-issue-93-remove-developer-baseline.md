# Issue 93 developer baseline removal implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Limit the telemetry lifecycle to the managed CLI, telemetry configuration, native hooks, and an optional
configure skill delivered through an already-installed APM CLI.

**Architecture:** Replace the selectable component graph with a fixed telemetry lifecycle. Run strict legacy APM hook
migration before every native hook write, and model configure-skill delivery as a non-fatal optional operation. Retain
the repository-local compatibility package and leave unrelated artifacts from older installers untouched.

**Tech stack:** Go 1.27, Cobra, POSIX shell, PowerShell, APM package manifests, Markdown.

**Spec:** GitHub issue #93, `https://github.com/Netcracker/qubership-ai-agent-telemetry/issues/93`.

## Global constraints

- Start from `origin/main` commit `7d82ecce6ce34d5abb2ab000e51302e7d82fb4e9` or a descendant.
- Keep `--harnesses` for install and update. Remove `--components`, `--skip`, `--force-git-hooks`, `--cli-only`, and
  `--remove-cli` from the public lifecycle commands.
- Never install, update, or remove the APM CLI, `qubership-global-essentials`, CyberFerret, shared Git hooks, Java, or
  an unrelated `core.hooksPath`.
- Preserve endpoint, token, CA, repository and path policy, delivery settings, `machine-id`, and outbox during update
  and normal uninstall. Remove them only with `uninstall --purge` after safe native-hook cleanup.
- Match only the exact global legacy telemetry package identity. Accept its existing YAML forms and an optional
  `#revision`; never use prefix matching or inspect project-local manifests.
- Pin configure-skill delivery to the current CLI Git release tag with `#<version>`. Do not equate the package's own
  `apm.yml` version with the CLI version.
- APM absence skips configure-skill delivery. Configure-skill command failures produce a visible warning but do not
  fail an otherwise successful CLI and native-hook lifecycle.
- Keep `agent-packages/ai-agent-telemetry` and its parity tests for compatibility.
- Follow TDD for behavior changes. Write all committed prose and identifiers in American English.

---

### Task 1: Make legacy telemetry hook migration strict

**Files:**

- Modify: `legacy_apm.go`
- Modify: `legacy_apm_test.go`
- Modify: `hooks.go`
- Modify: `hooks_test.go`
- Modify: `main_test.go`
- Modify: `component_telemetry.go`
- Modify: `component_telemetry_test.go`

**Interfaces:**

- Produce `migrateLegacyTelemetryAPM(home string) error` and an injectable test variant.
- Change managed native-hook installation to return both adapter results and a migration error.
- Preserve `--hooks=none`: no requested hooks means no manifest inspection or APM command.

- [ ] **Step 1: Write failing migration tests**

Cover a missing manifest, an absent exact dependency, accepted `#revision` forms, an unreadable or malformed manifest,
missing `apm`, a failed `apm uninstall`, unrelated dependencies, and a successful exact uninstall.

- [ ] **Step 2: Prove RED**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'LegacyTelemetryAPM|ManagedHooks' -count=1
```

Expected: failure because cleanup is warning-only and native hook installation still runs.

- [ ] **Step 3: Implement the smallest strict boundary**

Return an actionable error that includes both commands:

```text
apm uninstall -g Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
ai-agent-telemetry update
```

Run the migration before any native hook file write. Keep the 4 KiB APM diagnostic limit and do not edit APM YAML.

- [ ] **Step 4: Prove GREEN and run direct-command coverage**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'LegacyTelemetryAPM|ManagedHooks|Configure' -count=1
```

- [ ] **Step 5: Commit**

```sh
git add legacy_apm.go legacy_apm_test.go hooks.go hooks_test.go main_test.go component_telemetry.go component_telemetry_test.go
git commit -m "fix(installer): block hooks on legacy migration failure"
```

### Task 2: Add optional configure-skill lifecycle delivery

**Files:**

- Create: `configure_skill.go`
- Create: `configure_skill_test.go`
- Modify: `lifecycle.go`
- Modify: `lifecycle_test.go`

**Interfaces:**

- Produce an optional lifecycle service with install, update, and uninstall operations.
- Use package identity
  `Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure`.
- Map Cline to APM target `agent-skills`; preserve the other harness target names and stable deduplication.
- Add a non-fatal `WARN` lifecycle result state for optional-operation failures.
- Keep the service self-contained so deleting the old APM baseline component cannot remove one of its helpers.

- [ ] **Step 1: Write failing service and lifecycle tests**

Test these cases: APM missing, release version `dev`, install from `#vX.Y.Z`, update from the new pinned source, Cline
mapping, command failure reported as `WARN`, exact global uninstall, absent dependency, and lifecycle continuation.

- [ ] **Step 2: Prove RED**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'ConfigureSkill|Lifecycle' -count=1
```

Expected: failure because no optional configure-skill lifecycle service exists.

- [ ] **Step 3: Implement the optional service**

Use the already-resolved `apm` executable only. Never download or update APM. For a release build, construct:

```text
Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure#<CLI release tag>
```

Install the exact pinned source globally for selected targets, then compile global primitives. Use the same targeted
install on update; do not use APM's graph-wide `--update` option. On uninstall, remove only the exact configure package
when present. Return `SKIPPED` when APM or a release tag is unavailable and `WARN` for command or manifest failures.

- [ ] **Step 4: Prove GREEN**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'ConfigureSkill|Lifecycle' -count=1
```

- [ ] **Step 5: Commit**

```sh
git add configure_skill.go configure_skill_test.go lifecycle.go lifecycle_test.go
git commit -m "feat(installer): manage the optional configure skill"
```

### Task 3: Reduce the public lifecycle to telemetry

**Files:**

- Modify: `lifecycle.go`
- Modify: `lifecycle_test.go`
- Modify: `selection.go`
- Modify: `selection_test.go`
- Modify: `cli_commands.go`
- Modify: `cli.go`
- Modify: `cli_test.go`
- Modify: `managed_cli.go`
- Modify: `managed_cli_test.go`
- Modify: `update_test.go`
- Modify: `scripts/install_test.sh`
- Modify: `scripts/install.Tests.ps1`
- Delete: `component_apm.go`
- Delete: `component_apm_test.go`
- Delete: `component_git_hooks.go`
- Delete: `component_git_hooks_test.go`

**Interfaces:**

- `install [--harnesses ...] [--non-interactive]`
- `update [--harnesses ...] [--non-interactive]`
- `uninstall [--purge]`
- Install and update run managed CLI, telemetry, then optional configure-skill delivery.
- Uninstall removes native hooks, best-effort removes the configure skill, then removes the managed CLI. A native-hook
  cleanup failure preserves the CLI and telemetry state.

- [ ] **Step 1: Rewrite lifecycle and CLI tests for the fixed contract**

Assert removed flags are rejected, lifecycle handoff forwards only retained flags, uninstall always targets telemetry
and the managed CLI, and unrelated APM and Git state is never called or changed.

- [ ] **Step 2: Prove RED**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'Lifecycle|ManagedCLI|Completion|Bootstrap' -count=1
```

Expected: failure because the old component selectors and partial uninstall remain.

- [ ] **Step 3: Simplify production code and delete baseline components**

Remove the public flags, component map, partial-uninstall branches, APM bootstrap, Git/Java preflight, global hook clone,
and `core.hooksPath` mutation. Retain harness normalization and generic CSV completion used by hook commands.

- [ ] **Step 4: Update bootstrap argument tests and prove GREEN**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'Lifecycle|ManagedCLI|Completion|Bootstrap' -count=1
sh scripts/install_test.sh
```

Run `scripts/install.Tests.ps1` on PowerShell when available; CI remains the Windows authority.

- [ ] **Step 5: Commit**

```sh
git add -A -- lifecycle.go lifecycle_test.go selection.go selection_test.go cli_commands.go cli.go cli_test.go managed_cli.go managed_cli_test.go update_test.go scripts/install_test.sh scripts/install.Tests.ps1 component_apm.go component_apm_test.go component_git_hooks.go component_git_hooks_test.go
git commit -m "refactor(installer): remove developer baseline components"
```

### Task 4: Teach the configure skill the update migration flow

**Files:**

- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/apm.yml`
- Modify: `hooks_package_test.go`

**Interfaces:**

- Configuration and verification must never trigger update implicitly.
- An explicit update request runs the single `ai-agent-telemetry update` command.
- On the exact legacy-migration failure, tell the user why the update stopped, require APM availability, run the exact
  legacy uninstall command, retry update, and verify native hooks with `status --verbose`.
- Never remove unrelated global packages or edit project-local manifests.

- [ ] **Step 1: Record the RED pressure test**

Use the controller's baseline report at `/private/tmp/issue-93-skill-red.md`. Confirm the current skill suggests
`--cli-only` or lacks the strict migration recovery flow.

- [ ] **Step 2: Write the minimum skill and package changes**

Remove every `--cli-only` instruction and obsolete component wording. Add one bounded migration-recovery branch under
the explicit update workflow. Bump the package version from `3.3.0` to `3.4.0` because the required workflow changes.

- [ ] **Step 3: Validate the package**

```sh
python3 /Users/denifilatov/.codex/skills/.system/skill-creator/scripts/quick_validate.py agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'HooksPackage|WorkflowContract' -count=1
```

- [ ] **Step 4: Run an independent GREEN pressure test**

Give a fresh sub-agent the same simulated update failure and the edited skill. Require it to stop, surface the exact
cleanup command, retry the single update command, and verify native hooks without touching unrelated APM state.

- [ ] **Step 5: Commit**

```sh
git add agent-packages/ai-agent-telemetry-configure hooks_package_test.go
git commit -m "docs(skill): handle legacy migration during update"
```

### Task 5: Update the ADR and maintained installation documentation

**Files:**

- Create: `docs/adr/0010-telemetry-installer-scope-and-lifecycle.md`
- Modify: `docs/adr/0005-cli-managed-global-hooks.md`
- Modify: `README.md`
- Modify: `docs/lifecycle-installer.md`
- Modify: `docs/cli.md`
- Modify: `docs/agent-integration.md`
- Modify: `docs/release.md`
- Modify: `docs/manual-uninstall.md`
- Modify: `agent-packages/ai-agent-telemetry/README.md`
- Modify only when matched by search: `telemetry-backend/README.md`, `telemetry-backend/native-otlp-onboarding.md`

**Interfaces:**

- Document only the telemetry-owned lifecycle and the optional configure skill.
- Include opt-in cleanup commands that invoke the pinned `v1.2.0` bootstrap with
  `uninstall --components apm,git-hooks`; normal install, update, and uninstall never run them.
- Keep the new ADR `Proposed`. Mark ADR 0005 superseded only after the new ADR is accepted.

- [ ] **Step 1: Add documentation contract tests before editing prose**

Extend existing workflow or README contract tests so maintained docs cannot reintroduce removed public flags or claim
that telemetry installs the developer baseline. Add bootstrap argument-forwarding coverage for the documented pinned
cleanup commands without running cleanup on the developer machine.

- [ ] **Step 2: Prove RED**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go test ./... -run 'WorkflowContract|Readme|ManagedCLI' -count=1
```

- [ ] **Step 3: Write the ADR and update maintained docs**

Use the approved local ADR draft as the source. Remove component-selection, APM installation, Git/Java prerequisites,
CyberFerret, partial uninstall, and `--cli-only` text. Keep compatibility-package and strict-migration guidance.

- [ ] **Step 4: Verify prose and stale references**

```sh
rg -n --glob '!docs/superpowers/**' -- '--components|--skip|--force-git-hooks|--cli-only|--remove-cli|qubership-global-essentials|CYBER_FERRET_PASSWORD' README.md docs agent-packages scripts
git diff --check
```

Every remaining match must be the compatibility history, the opt-in pinned `v1.2.0` cleanup command, or a test that
asserts rejection of an obsolete flag.

- [ ] **Step 5: Commit**

```sh
git add README.md docs agent-packages/ai-agent-telemetry/README.md workflow_contract_test.go managed_cli_test.go scripts
git commit -m "docs(installer): define the telemetry lifecycle boundary"
```

### Task 6: Run full verification and remove dead references

**Files:**

- Modify only files needed to fix failures or stale references found by verification.

- [ ] **Step 1: Run Go verification**

```sh
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp make test
GOCACHE=/private/tmp/qaat-issue93-go-cache TMPDIR=/private/tmp go vet ./...
```

- [ ] **Step 2: Run installer and bootstrap verification from CI workflows**

Run the local commands defined in `.github/workflows/installer-tests.yaml` and
`.github/workflows/bootstrap-tests.yaml`. Record unavailable Windows-only checks separately.

- [ ] **Step 3: Check scope and deletion completeness**

```sh
git diff --check origin/main...HEAD
git diff --stat origin/main...HEAD
rg -n -- '--components|--skip|--force-git-hooks|--cli-only|--remove-cli|componentAPM|componentGitHooks' . --glob '!docs/superpowers/**'
```

- [ ] **Step 4: Commit only if verification required a fix**

```sh
git add <verified-fix-files>
git commit -m "fix(installer): complete telemetry lifecycle migration"
```
