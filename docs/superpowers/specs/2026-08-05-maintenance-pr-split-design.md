# Maintenance PR split

## Goal

Keep PR #26 focused on Codex native metrics, telemetry dashboards, and the backend changes required to validate those
metrics. Move the server maintenance tooling into a separate stacked PR without losing the reviewed backup, update,
rollback, or retention behavior.

The maintenance tooling uses the repository's Compose identity, `ai-agent-telemetry-backend`, for new installations.
It does not preserve the retired server-specific `skills-telemetry-backend` identity in ordinary defaults, generated
resources, image tags, or normal operator examples. The one-time legacy backup mode, its tests, and the manual migration
runbook may identify the retired deployment only as the migration source.

## Branch structure

The split produces two reviewable branches:

1. `feat/remote-codex-metrics` remains the branch for PR #26 and contains dashboard, query, self-scrape, and associated
   validation changes only.
2. `feat/backend-maintenance` starts from the cleaned `feat/remote-codex-metrics` head and contains the complete
   maintenance feature.

The maintenance PR initially targets `feat/remote-codex-metrics`. This stacked relationship avoids duplicating or
conflicting with backend changes that the maintenance implementation already depends on. After PR #26 merges, rebase
the maintenance-only commits onto the resulting `main` before retargeting the maintenance PR.

Before writing the implementation plan, create the signed local source tag
`backup/remote-codex-metrics-mixed-source-20260805` at
`de75b951e6829bc948dd81d4fa7ca53ff696d1bd`. This tag identifies the reviewed mixed implementation source.

After committing the implementation plan and immediately before rewriting PR #26, create the signed local recovery
tag `backup/remote-codex-metrics-pre-rewrite-20260805` at the final pre-rewrite `HEAD`. Verify that its peeled commit is
the checked-out `HEAD` and that the mixed source commit is its ancestor. Treat this second tag as the immutable recovery
ref for code, design, and plan artifacts. Verify both signed tags before each reconstruction step. Do not use the future
maintenance branch as either backup ref.

Reconstruct PR #26 from boundary commit `b15c3a8d6653b70afa12bf63cabd4a282cac2f75`, using the ownership ledger below.
Create `feat/backend-maintenance` only after the cleaned metrics head is final, then apply the maintenance-owned changes
from the recovery tag, using the mixed source tag for hunk provenance. The maintenance branch therefore has the
cleaned metrics head as its actual parent.

Force-push only the rewritten `feat/remote-codex-metrics` branch to the `im` remote with `--force-with-lease`. Push the
new maintenance branch only to `im`, then open its upstream PR from `IldarMinaev:feat/backend-maintenance` after both
branches pass their relevant checks.

When PR #26 merges, do not only retarget the maintenance PR. Record the old cleaned metrics head, fetch the resulting
`main`, and rebase only the maintenance commits with
`git rebase --onto origin/main <old-cleaned-metrics-head> feat/backend-maintenance`. Verify the rebased diff and tree,
force-push with `--force-with-lease` to `im`, then retarget the maintenance PR to `main`. This step is required when PR
#26 is squash-merged because the original metrics commits do not become ancestors of the squash commit.

## Split ownership ledger

The commit boundary and disposition are fixed:

- `da3e3004bcf9573cb9d4dca51753e28cf1c39678` through
  `2dab03e48d1ef8b3cbc58b0844df92fb24033647` belong to maintenance;
- `ffbbdcb2cbd2767cf8fe6fe7659bfcc56c35dab0` is the only mixed commit and follows the hunk ledger below;
- `e830b397efd80529450acec0c47add84a4f85a29` belongs to maintenance;
- `43182b83bc27be62ac1e68e0c0ecc945b0794a0f` belongs to metrics;
- `4305ae018d901440e8988bf7d688d77f70f25e79` through
  `de75b951e6829bc948dd81d4fa7ca53ff696d1bd` belong to maintenance;
- `6544b9dfd2a11481417491707e0c68f841fc2110` and its review-fix commits belong to maintenance documentation.

Within `ffbbdcb2cbd2767cf8fe6fe7659bfcc56c35dab0`, PR #26 owns these exact changes:

- `.github/workflows/telemetry-backend-tests.yaml`: backend configuration, metrics-query retry, and existing smoke-test
  execution only;
- `telemetry-backend/.env.example`: `VM_SELF_SCRAPE_INTERVAL` only;
- `telemetry-backend/README.md`: `VM_SELF_SCRAPE_INTERVAL` configuration and operational guidance only;
- `telemetry-backend/docker-compose.yml`: configurable VictoriaMetrics self-scrape interval only;
- `telemetry-backend/tests/config-contract.sh`: self-scrape environment and rendering assertions only;
- `telemetry-backend/tests/metrics-query-contract.sh`: bounded query requests and operational-metric visibility only;
- `telemetry-backend/tests/metrics-query-retry-test.sh`: the complete added file;
- `telemetry-backend/tests/with-fixture-stack.sh`: the five-second fixture self-scrape setting only.

Maintenance owns every other hunk in that mixed commit, including release workflow paths and steps, release
documentation, package tests, updater changes, maintenance HTTP responses, and maintenance contract changes.

After reconstruction, verify that only the following ten paths differ in
`b15c3a8d6653b70afa12bf63cabd4a282cac2f75..<cleaned-metrics-head>`:

```text
.github/workflows/telemetry-backend-tests.yaml
telemetry-backend/.env.example
telemetry-backend/README.md
telemetry-backend/docker-compose.yml
telemetry-backend/grafana/dashboards/codex-native-metrics.json
telemetry-backend/tests/config-contract.sh
telemetry-backend/tests/dashboard-contract.sh
telemetry-backend/tests/metrics-query-contract.sh
telemetry-backend/tests/metrics-query-retry-test.sh
telemetry-backend/tests/with-fixture-stack.sh
```

The full PR #26 manifest is separate. Verify that only these 30 paths differ in
`2000143a0dae2aec549cf08a7a677a48f3245878...<cleaned-metrics-head>`:

```text
.github/workflows/telemetry-backend-tests.yaml
docs/adr/0007-native-otlp-metrics-privacy-and-capacity.md
docs/superpowers/plans/2026-07-29-remote-codex-metrics.md
docs/superpowers/plans/2026-07-30-dashboard-usability-redesign.md
docs/superpowers/plans/2026-07-30-multi-harness-native-metrics.md
docs/superpowers/plans/2026-07-31-dashboard-freshness-and-onboarding-docs.md
docs/superpowers/plans/2026-08-03-telemetry-health-dotted-labels.md
docs/superpowers/specs/2026-07-30-dashboard-usability-redesign.md
docs/superpowers/specs/2026-07-30-multi-harness-native-metrics-design.md
docs/superpowers/specs/2026-08-03-telemetry-health-dotted-labels-design.md
telemetry-backend/.env.example
telemetry-backend/Caddyfile
telemetry-backend/README.md
telemetry-backend/docker-compose.yml
telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json
telemetry-backend/grafana/dashboards/codex-native-metrics.json
telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
telemetry-backend/grafana/dashboards/telemetry-health.json
telemetry-backend/grafana/provisioning/datasources/victorialogs.yaml
telemetry-backend/native-otlp-onboarding.md
telemetry-backend/otel-collector-config.yaml
telemetry-backend/tests/config-contract.sh
telemetry-backend/tests/dashboard-contract.sh
telemetry-backend/tests/fixtures/otel-events.json
telemetry-backend/tests/fixtures/otel-metrics.json
telemetry-backend/tests/metrics-query-contract.sh
telemetry-backend/tests/metrics-query-retry-test.sh
telemetry-backend/tests/query-contract.sh
telemetry-backend/tests/smoke.sh
telemetry-backend/tests/with-fixture-stack.sh
```

Write a third sorted manifest for the maintenance diff from the cleaned metrics head. Record and compare both final
tree IDs before each push. Any path outside the applicable manifest stops publication until its ownership is resolved
explicitly.

Path manifests are necessary but not sufficient because several of the ten late paths contain both metrics and
maintenance hunks in the source history. Before rewriting either branch, create an independent detached validation
worktree at `b15c3a8d6653b70afa12bf63cabd4a282cac2f75`. In that worktree, author and review a metrics-only patch
from the hunk ledger without using the reconstruction commands or resulting branch. Apply that patch, apply the
complete `43182b83bc27be62ac1e68e0c0ecc945b0794a0f` dashboard change, and record:

- the SHA-256 checksum of the frozen metrics-only patch;
- the expected blob ID for every one of the ten late paths;
- the expected complete tree ID for the cleaned metrics head.

Construct the actual cleaned branch separately from the signed source history. Do not use the frozen validation patch
as the reconstruction mechanism. Before any push, require its complete tree ID and all ten blob IDs to match the
independent expected values exactly. The 30-path manifest then verifies full PR scope, while the independent tree and
blob comparison verifies hunk ownership. Preserve the patch checksum, expected IDs, and actual IDs in the split
execution record on the maintenance branch.

## Maintenance scope

The maintenance PR retains the complete reviewed behavior:

- portable, compressed, self-contained backups;
- release, branch, and commit update resolution through immutable GitHub identities;
- pre-downtime download, image preparation, source archiving, and capacity checks;
- durable activation transactions and interrupted-operation recovery;
- health-gated activation and automatic configuration rollback;
- per-release immutable image references for reliable rollback;
- backup retention for completed backups older than 14 days while always preserving the two newest backups;
- one-time manual migration instructions for the verified legacy deployment;
- release assets, checksum generation, documentation, and local sandbox contract tests.

The implementation continues to avoid production SSH access. Tests use local temporary directories, mock GitHub HTTP
responses with opaque redirects, disposable Docker resources, and the existing local test harness.

## Deployment identity and paths

All production defaults use one identity:

- Compose project: `ai-agent-telemetry-backend`;
- backend root: `/opt/ai-agent-telemetry-backend`;
- backup root: `/opt/ai-agent-telemetry-backups`;
- active link: `/opt/ai-agent-telemetry-backend/latest`;
- transaction file: `/opt/ai-agent-telemetry-backend/.maintenance-transaction`;
- lock file: `/run/lock/ai-agent-telemetry-backend-maintenance.lock`;
- deployment-specific Grafana image prefix: `ai-agent-telemetry-backend-grafana`.

Every ordinary Compose command passes `--project-name ai-agent-telemetry-backend`, including configuration validation,
image preparation, start, stop, status, backup discovery, recovery, and rollback. Docker resource discovery requires
the same exact Compose project label. The explicit legacy backup mode instead passes its fixed legacy project name to
the same wrapper and requires the matching legacy labels.

Ordinary backup and update commands do not auto-detect or operate on `skills-telemetry-backend`. Supporting two active
identities in the updater would make volume ownership and rollback ambiguous. The backup command alone provides an
explicit `--legacy-source` mode for the one-time migration. This mode selects only these fixed source values:

- Compose project: `skills-telemetry-backend`;
- backend root: `/opt/skills-telemetry-backend`;
- active link: `/opt/skills-telemetry-backend/latest`;
- maintenance lock: `/run/lock/skills-telemetry-backend-maintenance.lock`.

The flag cannot be combined with arbitrary production path or project overrides. It changes the source identity but
keeps the destination backup root at `/opt/ai-agent-telemetry-backups`. The command verifies the exact legacy Compose
labels and volume mapping, creates the same portable backup format, and supports `--leave-stopped` for the supervised
manual cutover. If backup fails, its existing durable backup transaction restarts the legacy stack before returning
failure. After a successful legacy backup, `--leave-stopped` durably completes and clears the backup transaction before
returning with the stack stopped; later manual migration steps have no automatic transaction state. Before downtime,
the helper may mount each volume read-only only to run the bounded size measurement used by the capacity and
large-backup gates. It must not create an archive, copy files, hash file contents, or otherwise read volume payload
before the script has stopped and verified every declared legacy service.

Before clearing the successful backup transaction, the command writes exactly one image record for each of the five
Compose services to `manifest.txt`:

```text
IMAGE=<service>|<immutable-reference>|<docker-image-id>
```

The service names must match the rendered Compose model, references must match the effective pinned configuration, and
IDs must match the running legacy containers captured before downtime. The completed `SHA256SUMS` covers
`manifest.txt`, so these fallback identities remain available after the transaction is removed. Missing, duplicate,
mutable, or mismatched image records prevent backup completion.

## Manual identity migration

The repository ships a runbook, not an automated migration command. An operator performs the procedure during an
explicit maintenance window and remains available until either the new health gate passes or the legacy stack is
restored. The runbook uses only fixed legacy and new identities; it does not introduce generic root-level overrides.

Before downtime, the operator downloads and verifies the target release and helper images, validates both Compose
configurations, checks free space, and prepares `/opt/ai-agent-telemetry-backend/<release-id>` with the legacy
environment. The operator then runs the backup command in legacy mode with `--leave-stopped`. That command writes and
verifies source, environment, manifests, restore instructions, and every legacy volume archive before it returns with
the legacy stack stopped.

The operator follows the completed backup's manifest to create the new project's named volumes, verify their exact
Compose labels, and restore each matching archive with numeric ownership, permissions, timestamps, and file types
preserved. The runbook verifies normalized content manifests before it creates the new `latest` symlink, starts only
`ai-agent-telemetry-backend` with pinned images, and runs the same strict health probes as the updater.

If any restore, activation, or health step fails, the operator stops the new project and starts the legacy project with
the immutable references recorded in the backup. The runbook then compares every restored container image ID with the
five checksummed manifest records and verifies legacy health before ending the failed attempt. This is an
operator-supervised recovery procedure, not an automatic crash-recovery state machine. After shell loss or host reboot,
the operator first inspects both exact Compose projects, keeps at most one running, and then resumes the documented
restore or fallback step.

After successful cutover, the legacy directories and volumes remain a pre-migration snapshot. They do not receive new
telemetry. Starting them later loses all data accepted after cutover, so the runbook labels late fallback as destructive
and requires a fresh backup of the new deployment before an operator chooses it. Cleanup is manual and never performed
by backup, update, rollback, or retention. The runbook lists exact legacy resources and removal commands, but the
operator runs them only after the desired observation period.

Once manual migration succeeds, normal backup, update, automatic rollback, and retention use only the new project,
paths, lock, transaction, and volumes. They do not store migration success, cleanup, tombstone, or resource-manifest
state and do not inspect the retained legacy deployment.

## Portability contract

Each completed backup remains sufficient to recreate the deployment on another Linux machine that has registry
access. It contains the backend source, root-only environment file, all declared named volumes, checksums, a manifest,
and restore instructions. Docker images are intentionally excluded.

The restore documentation states the complete host requirements: Linux, Docker Engine, Docker Compose v2, Bash, and
GNU coreutils and tar. It uses the new project name and paths in every normal restore command. The migration runbook
documents the legacy source identity only where needed to verify, migrate, roll back, or remove retained resources.

## Verification

Verify the cleaned PR #26 with its backend configuration, dashboard, query, retry, and Docker smoke suites. Confirm
that its diff contains no maintenance scripts, maintenance specifications or plans, release packaging, maintenance
workflow changes, or maintenance documentation.

Verify the maintenance branch with all backup, update, rollback, recovery, retention, release-package, and Docker
sandbox tests. Assert the exact new constants in ordinary executable defaults, generated Compose resources, manifests,
backup paths, transaction paths, restore commands, and normal fixtures. Allow the legacy project and path strings only
in historical design records, the backup command's fixed `--legacy-source` constants, its tests, and the manual
migration runbook. String splitting or construction must not bypass this allowlist.

The legacy backup test starts a complete deployment with the legacy project name and populated named volumes. It
verifies the portable backup, exact source labels, read-only size measurement before downtime, offline-only volume
archiving, helper cleanup, five checksummed image records, and the `--leave-stopped` result. Injected backup failures
must restart the legacy project with its original image IDs and must not create or modify new-project resources.

A separate local sandbox exercise follows the manual runbook against the completed legacy backup. It verifies new
volume creation, exact Compose labels, normalized content equality, numeric ownership and mode preservation, symlink
activation, and strict health probes. Failure exercises stop the new project and restore the healthy legacy project.
The sandbox compares all five fallback container image IDs with the completed backup manifest, records every command,
and confirms that at most one project runs after each documented success or failure path. It does not connect to the
production server.

Static documentation tests require explicit interruption recovery, destructive late-fallback warnings, a fresh new
backup before late fallback, and exact manual cleanup commands. No executable maintenance file may contain migration
success, cleanup, tombstone, dual-lock, or automatic legacy-resource deletion state.

Run the forced-process-death case and verify that recovery removes only helper containers carrying the interrupted
transaction ID before restarting the previous stack. Verify rollback by comparing restored container image IDs, not
only service state. Verify the restore instructions in a second local sandbox with Linux and GNU tool prerequisites
documented.

Before publishing either rewritten branch, inspect the latest PR #26 review threads again, confirm the repository's
contribution requirements, rebase on the agreed base where safe, and run the checks relevant to each resulting diff.
