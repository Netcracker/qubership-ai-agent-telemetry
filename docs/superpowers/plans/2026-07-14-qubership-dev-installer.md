# Qubership Developer Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add separate POSIX and PowerShell workstation bootstrap scripts for the Qubership APM baseline, AI agent
telemetry, and global Git hooks.

**Architecture:** Two self-contained entry points expose the same registry-driven lifecycle and command contract. They
delegate telemetry installation to its published installer and manage APM and global Git hooks directly. A standalone
workflow runs black-box tests with local fixtures on Linux, macOS, and Windows.

**Tech Stack:** POSIX shell, PowerShell 5.1+, Git, and GitHub Actions.

## Global constraints

- Create `global-scripts/qubership-dev-install.sh` and `global-scripts/qubership-dev-install.ps1`.
- Do not modify the existing telemetry installers or `.github/workflows/installer-tests.yaml`.
- Select `apm`, `telemetry`, and `git-hooks` by default.
- Select `claude`, `codex`, and `cursor` by default.
- Support selection, exclusion, harness selection, forced Git-hook replacement, forced updates, non-interactive mode,
  and help.
- Check Git and Java only when `git-hooks` is selected.
- Continue after an individual component failure and return a nonzero final status.
- Keep all new installer CI in `.github/workflows/qubership-dev-installer-tests.yaml`.

---

### Task 1: Implement the POSIX developer installer

**Files:**
- Create: `global-scripts/qubership-dev-install.sh`
- Create: `global-scripts/tests/qubership-dev-install_test.sh`

**Interfaces:**
- Environment overrides: `QUBERSHIP_DEV_APM_INSTALL_URL`, `QUBERSHIP_DEV_TELEMETRY_INSTALL_URL`,
  `QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY`, and `QUBERSHIP_DEV_GIT_HOOKS_DIR`.
- Command handlers: `install_apm`, `install_telemetry`, and `install_git_hooks`.

- [ ] Write black-box tests for defaults, selection, exclusion, harnesses, help, validation, prerequisite retries,
  Git-hook conflicts, force update, independent failures, status summaries, and exit codes.
- [ ] Run `sh global-scripts/tests/qubership-dev-install_test.sh` and verify it fails because the installer is absent.
- [ ] Implement centralized component metadata, parsing, prerequisite checks, handlers, and summary output.
- [ ] Run `sh global-scripts/tests/qubership-dev-install_test.sh` and verify all cases pass.
- [ ] Commit with `feat(installer): add POSIX developer bootstrap`.

### Task 2: Implement the PowerShell developer installer

**Files:**
- Create: `global-scripts/qubership-dev-install.ps1`
- Create: `global-scripts/tests/qubership-dev-install.Tests.ps1`

**Interfaces:**
- Use the same environment overrides and lifecycle as Task 1.
- Default Git-hooks directory: `$env:LOCALAPPDATA\Qubership\pre-commit-global`.

- [ ] Write PowerShell black-box tests for the complete POSIX command contract with temporary command shims.
- [ ] Run `pwsh -NoProfile -File global-scripts/tests/qubership-dev-install.Tests.ps1` and verify RED.
- [ ] Implement an ordered component registry and platform-specific handlers.
- [ ] Run the PowerShell test file and verify GREEN.
- [ ] Commit with `feat(installer): add PowerShell developer bootstrap`.

### Task 3: Add a movable cross-platform workflow

**Files:**
- Create: `.github/workflows/qubership-dev-installer-tests.yaml`

**Interfaces:**
- Run `global-scripts/tests/qubership-dev-install_test.sh` on Ubuntu and macOS.
- Run `global-scripts/tests/qubership-dev-install.Tests.ps1` on Windows.

- [ ] Create a standalone workflow with its own `paths` filters and concurrency group.
- [ ] Run ShellCheck on Ubuntu and the black-box suites on all three platforms.
- [ ] Validate the workflow syntax and commit with `ci(installer): test developer bootstrap on each platform`.

### Task 4: Publish and document the new installers

**Files:**
- Modify: `.github/workflows/release.yaml`
- Modify: `Makefile`
- Modify: `README.md`
- Create: `global-scripts/README.md`

**Interfaces:**
- Release assets: `qubership-dev-install.sh` and `qubership-dev-install.ps1`.
- Existing telemetry release assets remain unchanged.

- [ ] Add failing expected-payload checks for the two new asset names.
- [ ] Stage the scripts unchanged and include them in `SHA256SUMS`.
- [ ] Document minimal installation, flags, component behavior, and follow-up warnings.
- [ ] Run `make clean build checksums` and verify both new assets.
- [ ] Commit with `docs(installer): publish developer bootstrap`.

### Task 5: Verify and review

**Files:**
- Review all files changed by Tasks 1 through 4.

- [ ] Run both installer suites, ShellCheck, build/checksum verification, workflow lint, and `git diff --check`.
- [ ] Confirm the existing telemetry installers and `.github/workflows/installer-tests.yaml` have no diff.
- [ ] Dispatch the user-required independent review subagent and resolve valid findings.
- [ ] Prepare a concise PR description with `Why`, `What`, and `How to verify`.
