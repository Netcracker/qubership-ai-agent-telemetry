# Backend maintenance simplification implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver backup, update, rollback, and retention tooling under the new identity, with a supervised manual path
for migrating the one verified legacy deployment.

**Architecture:** Keep the existing transaction-safe backup and update implementation, rename ordinary production
identity constants, and add one fixed legacy backup mode. The migration itself is a tested operator runbook; it has no
automatic cross-project transaction, cleanup state, or late rollback machinery.

**Tech Stack:** Bash 4+, Docker Engine, Docker Compose v2, GNU coreutils and tar, local HTTP fixtures, shell contracts.

## Global constraints

- Ordinary production identity is `ai-agent-telemetry-backend` under `/opt/ai-agent-telemetry-backend`.
- Backups use `/opt/ai-agent-telemetry-backups/pre-<target>-<UTC timestamp>`.
- Only `backup-backend.sh --legacy-source` may select the fixed `skills-telemetry-backend` source identity.
- Volume size measurement may use a read-only mount before downtime; volume archiving starts only after all services
  stop.
- A completed backup contains five checksummed `IMAGE=<service>|<immutable-reference>|<docker-image-id>` records.
- Manual migration never deletes legacy resources and never connects to the production server during testing.
- Update failure or health-gate failure automatically restores the previous new-identity release and exact image IDs.
- Retention removes eligible completed backups only with explicit authorization and always preserves the two newest.

---

### Task 1: Lock the simplified CLI and identity contracts with failing tests

**Files:**
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Modify: `telemetry-backend/tests/config-contract.sh`

**Interfaces:**
- Consumes: existing test-mode root and project overrides.
- Produces: failing assertions for new defaults, fixed legacy mode, and removed migration state.

- [ ] **Step 1: Add identity contract cases**

Add cases that source the backup command in test mode and assert:

```text
PROJECT_NAME_DEFAULT=ai-agent-telemetry-backend
BACKEND_ROOT_DEFAULT=/opt/ai-agent-telemetry-backend
BACKUP_ROOT_DEFAULT=/opt/ai-agent-telemetry-backups
LOCK_FILE_DEFAULT=/run/lock/ai-agent-telemetry-backend-maintenance.lock
```

Add CLI failures for `--legacy-source` combined with test-only or arbitrary production identity overrides. Add a static
allowlist assertion that legacy strings occur only in fixed legacy constants, their tests, historical specs, and the
manual migration runbook.

- [ ] **Step 2: Add backup-order and image-manifest cases**

Extend the command-event fixture to distinguish `VOLUME_MEASURE`, `COMPOSE_DOWN`, `VOLUME_ARCHIVE`, and
`TRANSACTION_CLEAR`. Require measurement before down, archive after down, five unique image records before transaction
clear, and `manifest.txt` in `SHA256SUMS`.

- [ ] **Step 3: Run the focused tests and confirm failure**

```bash
bash telemetry-backend/tests/maintenance-contract.sh cli
bash telemetry-backend/tests/maintenance-contract.sh backup
sh telemetry-backend/tests/config-contract.sh
```

Expected: failures name old production defaults, missing `--legacy-source`, or missing manifest image records.

### Task 2: Rename ordinary maintenance identity and add fixed legacy backup mode

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/scripts/update-backend.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Produces: `maintenance_init(identity_mode)` where `identity_mode` is `ordinary` or `legacy-source`.
- Produces: fixed legacy CLI flag consumed only by backup.

- [ ] **Step 1: Change ordinary defaults**

Set the four default constants to the exact new values from the global constraints. Keep test-mode overrides unchanged
and restricted to `/tmp`.

- [ ] **Step 2: Parse and validate `--legacy-source`**

Add the flag only to the backup command. Before `maintenance_init`, select fixed legacy root, project, active link, and
lock values. Reject the flag in update, reject repetitions, and reject combinations with unsupported identity inputs.
Keep the backup destination at `/opt/ai-agent-telemetry-backups`.

- [ ] **Step 3: Run CLI contracts**

```bash
bash telemetry-backend/tests/maintenance-contract.sh cli
```

Expected: new defaults and fixed legacy mode pass; unsafe combinations fail before Docker access.

- [ ] **Step 4: Commit**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  telemetry-backend/tests/maintenance-contract.sh telemetry-backend/tests/config-contract.sh
git commit -S -m "refactor(backend): adopt telemetry maintenance identity"
```

### Task 3: Persist fallback image identities in portable backups

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Produces: five `IMAGE=<service>|<reference>|<id>` manifest records.
- Consumes: existing effective Compose override and running-container identity capture.

- [ ] **Step 1: Add failing manifest tests**

Require exactly the services `caddy`, `collector`, `grafana`, `victorialogs`, and `victoriametrics`. Reject duplicate
services, mutable registry references, missing local image IDs, and a manifest checksum mismatch.

- [ ] **Step 2: Write image records before static checksums**

Capture identities while the original containers still exist. Validate five unique service mappings, append records in
sorted service order to `manifest.txt`, then generate `SHA256SUMS`. Keep the same records in the transaction while a
backup or update is active; clear the successful `--leave-stopped` transaction only after the completed backup rename
and directory synchronization.

- [ ] **Step 3: Verify fallback identity**

Extend the fallback fixture to start the legacy stack from manifest references and compare every container `.Image`
value with its recorded ID. Run:

```bash
bash telemetry-backend/tests/maintenance-contract.sh backup
bash telemetry-backend/tests/maintenance-contract.sh recovery
```

Expected: success with five exact IDs; injected mismatch fails closed.

- [ ] **Step 4: Commit**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "feat(backend): persist backup image identities"
```

### Task 4: Preserve preflight capacity checks and offline archives

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Consumes: `measure_volume_bytes(volume, transaction_id, ordinal)`.
- Produces: explicit measurement and archive events with enforced ordering.

- [ ] **Step 1: Add ordering failures**

Instrument helper invocations so a size-only `du -sb` emits `VOLUME_MEASURE=<volume>`, while tar emits
`VOLUME_ARCHIVE=<volume>`. Assert all measurements precede `COMPOSE_DOWN` and all archives follow the stopped-state
verification event.

- [ ] **Step 2: Restrict the online helper path**

Keep the read-only mount and `du -sb` capacity measurement. Ensure the preflight function cannot select tar, checksum,
copy, or restore commands. Keep the existing `uncompressed total + 10 percent` free-space requirement and large-backup
authorization gate.

- [ ] **Step 3: Run backup contracts**

```bash
bash telemetry-backend/tests/maintenance-contract.sh backup
```

Expected: insufficient space fails before `COMPOSE_DOWN`; archive failures occur only after the stopped event and
restart the original stack unless `--leave-stopped` completed successfully.

- [ ] **Step 4: Commit**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "test(backend): enforce offline volume archives"
```

### Task 5: Replace automated migration design with a manual runbook

**Files:**
- Create: `telemetry-backend/MIGRATE_LEGACY_BACKEND.md`
- Modify: `telemetry-backend/README.md`
- Modify: `docs/superpowers/specs/2026-08-03-backend-backup-update-design.md`
- Modify: `docs/superpowers/plans/2026-08-03-backend-backup-update.md`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Consumes: completed legacy backup format and new release asset.
- Produces: supervised commands for restore, health verification, fallback, and later cleanup.

- [ ] **Step 1: Write static runbook contract tests**

Require exact legacy and new projects, fixed paths, release and checksum verification, legacy backup with
`--leave-stopped`, new volume label checks, normalized restore verification, new health gate, exact fallback image-ID
checks, interruption inspection, fresh new backup before destructive late fallback, and explicit cleanup commands.
Require the runbook to state that an operator remains present and that at most one project may run.

- [ ] **Step 2: Write the runbook**

Use numbered commands with a verifiable result after each step. Keep legacy resources after success. Do not include an
automated migration command, dual lock, migration marker, tombstone, cleanup state, or automatic legacy deletion.

- [ ] **Step 3: Reconcile historical maintenance docs**

Update the original maintenance spec and plan to use the new ordinary identity and link to the manual runbook. Remove
requirements for automated identity migration while preserving backup/update transaction recovery and helper cleanup.

- [ ] **Step 4: Run documentation and contract checks**

```bash
bash telemetry-backend/tests/maintenance-contract.sh cli
bash telemetry-backend/tests/maintenance-contract.sh backup
git diff --check
```

Expected: runbook contract and legacy-string allowlist pass.

- [ ] **Step 5: Commit**

```bash
git add telemetry-backend/MIGRATE_LEGACY_BACKEND.md telemetry-backend/README.md \
  docs/superpowers/specs/2026-08-03-backend-backup-update-design.md \
  docs/superpowers/plans/2026-08-03-backend-backup-update.md \
  telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "docs(backend): add manual identity migration runbook"
```

### Task 6: Exercise the manual migration locally

**Files:**
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Modify if needed: `telemetry-backend/tests/fixtures/maintenance-http-fixture.py`

**Interfaces:**
- Consumes: legacy backup and manual runbook sequence.
- Produces: recorded success and failure traces for two isolated Compose projects.

- [ ] **Step 1: Build the legacy sandbox**

Create unique `/tmp` roots, legacy and new project names, populated volumes with numeric ownership and nondefault modes,
and pinned fixture images. Start only the legacy project.

- [ ] **Step 2: Execute the success path**

Run legacy backup with `--leave-stopped`, create new labeled volumes, restore archives, verify normalized content, start
the new project, and run strict log, metric, Grafana, and stability probes. Assert only the new project runs.

- [ ] **Step 3: Execute the failure fallback path**

Inject a new health failure, stop the new project, start legacy from manifest references, compare all five image IDs,
and pass legacy health. Assert only legacy runs and both data sets remain available.

- [ ] **Step 4: Run all maintenance suites**

```bash
for suite in activation activation-real backup cli recovery resolution retention; do
  bash telemetry-backend/tests/maintenance-contract.sh "$suite"
done
```

Expected: every suite passes without production network or filesystem access.

- [ ] **Step 5: Commit**

```bash
git add telemetry-backend/tests/maintenance-contract.sh \
  telemetry-backend/tests/fixtures/maintenance-http-fixture.py
git commit -S -m "test(backend): exercise manual identity migration"
```

### Task 7: Verify release, Docker behavior, and PR readiness

**Files:**
- Modify if required: `.github/workflows/telemetry-backend-tests.yaml`
- Modify if required: `.github/workflows/release.yaml`
- Modify if required: `docs/release.md`

**Interfaces:**
- Consumes: completed maintenance implementation.
- Produces: publishable stacked maintenance PR.

- [ ] **Step 1: Run static and package verification**

```bash
bash -n telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh
shellcheck telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh
bash scripts/package_backend_release_test.sh
sh telemetry-backend/tests/config-contract.sh
```

- [ ] **Step 2: Run Docker smoke and migration sandbox**

Run the backend smoke suite on unused local ports, then rerun the success and fallback migration sandbox. Expected: all
services healthy, exact image IDs, no orphan helpers, and no legacy/new project overlap.

- [ ] **Step 3: Inspect contribution rules and current review feedback**

Read repository contribution instructions, search existing maintenance issues and PRs, inspect all current PR #26
threads, and verify no unresolved finding affects the maintenance diff.

- [ ] **Step 4: Rebase and verify the stacked diff**

Rebase on the final cleaned `feat/remote-codex-metrics` head if it moved. Verify signed commits, the maintenance path
manifest, `git diff --check`, and a clean worktree.

- [ ] **Step 5: Push and open the stacked PR**

```bash
git push -u im feat/backend-maintenance
gh pr create --repo Netcracker/qubership-ai-agent-telemetry \
  --base feat/remote-codex-metrics \
  --head IldarMinaev:feat/backend-maintenance \
  --title "feat(backend): add transactional maintenance tooling"
```

Use a PR body with Why, What, and How to verify sections. State that identity migration is a supervised manual runbook
and that the PR is stacked on PR #26.
