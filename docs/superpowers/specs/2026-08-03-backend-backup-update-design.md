# Backend backup and update scripts

## Goal

Publish the telemetry backend as a release asset and provide two server-side
scripts that create a portable backup and update an installed backend with
minimized, data-dependent downtime and automatic configuration rollback. The
scripts operate on the ordinary `ai-agent-telemetry-backend` identity. Moving
the verified legacy deployment to it is a separate, supervised procedure.

## Verified deployment

The production server was inspected read-only on August 3, 2026. Its active deployment has these properties:

- `/opt/skills-telemetry-backend/latest` points to `/opt/skills-telemetry-backend/eacf978`;
- `.env` is stored in each release directory with mode `600` and ownership `root:root`;
- the Compose project is `skills-telemetry-backend`;
- Caddy, Collector, Grafana, VictoriaLogs, and VictoriaMetrics are running;
- the named volumes are `caddy-config`, `caddy-data`, `grafana-data`, `vlogs-data`, and `vmetrics-data` under the
  Compose project;
- Docker, Docker Compose, Bash, `curl`, `flock`, `mktemp`, `readlink`, `sha256sum`, and GNU tar are available;
- the replacement machine may download Docker images from Docker Hub and GHCR.

The scripts must not connect to or modify the production server during development or testing. All implementation
verification uses an isolated local sandbox.

## Deployment identity transition

The inspected deployment uses the legacy project
`skills-telemetry-backend` and root `/opt/skills-telemetry-backend`. Ordinary
maintenance uses project `ai-agent-telemetry-backend`, root
`/opt/ai-agent-telemetry-backend`, backup root
`/opt/ai-agent-telemetry-backups`, and lock
`/run/lock/ai-agent-telemetry-backend-maintenance.lock`.

Identity migration is not part of `update-backend.sh`. An operator follows the
[manual runbook](../../../telemetry-backend/MIGRATE_LEGACY_BACKEND.md), remains
present, keeps at most one project running, and retains legacy resources for
explicit fallback and later cleanup. The ordinary update transaction begins
only after that one-time migration succeeds.

## Files and public commands

Create two executable Bash scripts:

- `telemetry-backend/scripts/backup-backend.sh`
- `telemetry-backend/scripts/update-backend.sh`

Both scripts require root, use `set -euo pipefail`, acquire the same maintenance lock, reject unresolved or unsafe
paths, and print each state transition without printing secret values. They verify GNU `sync` support before relying
on durable activation state.

Production defaults are:

- Compose project: `ai-agent-telemetry-backend`;
- backend root: `/opt/ai-agent-telemetry-backend`;
- backup root: `/opt/ai-agent-telemetry-backups`;
- active link: `/opt/ai-agent-telemetry-backend/latest`;
- GitHub repository: `Netcracker/qubership-ai-agent-telemetry`.

Tests may override the roots, Compose project name, maintenance lock path, repository API URL, download URL, and probe
timeouts through documented `TELEMETRY_TEST_*` variables only when `TELEMETRY_MAINTENANCE_TEST_MODE=1`. Test mode
rejects filesystem paths outside `/tmp` and project names that contain characters outside `[a-zA-Z0-9_-]`. Production
path and project variables are not accepted from the inherited environment, which avoids accidentally directing a
root process or Compose command to unrelated resources.

### Backup command

The public command is:

```bash
backup-backend.sh [--target-label <label>] [--leave-stopped] [--allow-large-backup]
```

`--target-label` changes only the backup directory label and defaults to `manual`. The script restricts it to ASCII
letters, digits, dots, underscores, and hyphens. `--leave-stopped` is intended for `update-backend.sh`; a standalone
backup restarts the original stack before it exits. `--allow-large-backup` authorizes offline backup when the measured
source and volume data exceeds the 1 GiB confirmation threshold.

### Update command

The public command is:

```bash
update-backend.sh [--ref <latest|tag|branch|sha>] [--allow-large-backup] [--prune-backups]
```

The default ref is `latest`. `--prune-backups` is the only noninteractive authorization to delete old backups. An
optional `GH_TOKEN` may authenticate GitHub downloads and API requests; the script never prints it. Update passes
`--allow-large-backup` to the backup command when the operator supplies it.

## Release asset

The release workflow creates `ai-agent-telemetry-backend.tar.gz` from an explicit allowlist:

- `.env.example`;
- `Caddyfile`;
- `README.md` and `native-otlp-onboarding.md`;
- `docker-compose.yml` and `otel-collector-config.yaml`;
- `grafana/Dockerfile`, dashboards, and provisioning files;
- `scripts/backup-backend.sh` and `scripts/update-backend.sh`.

The archive has backend files at its root. It never contains `.env`, test fixtures, generated files, Git metadata, or
local Grafana data. The workflow verifies the archive member list before publishing it.

The two scripts are also staged as standalone release assets so an older installation can bootstrap the update. The
release asset list adds:

```text
ai-agent-telemetry-backend.tar.gz
backup-backend.sh
update-backend.sh
```

`SHA256SUMS` covers all three assets. The existing pinned `assets-action` uploads them with the CLI binaries and
installers; no new GitHub Action or permission is required.

## Source resolution and staging

`update-backend.sh` resolves and stages the new source before downtime.

For `latest`, it calls `GET /repos/{owner}/{repo}/releases/latest` and uses the response's `tag_name` as the immutable
release identity. For a ref matching `vMAJOR.MINOR.PATCH`, it calls
`GET /repos/{owner}/{repo}/releases/tags/{tag}` and requires the response's `tag_name` to match the requested tag. Both
paths select the backend archive and `SHA256SUMS` from the API response's asset list, require exactly one matching
checksum entry, and verify the downloaded archive with `sha256sum`.

Any other ref is treated as a branch name or commit SHA. The script percent-encodes the ref, calls
`GET /repos/{owner}/{repo}/commits/{ref}`, and requires a full 40-character commit SHA in the response. It then
downloads the repository tarball for that resolved SHA and extracts only the `telemetry-backend` subtree. The resolved
commit SHA is the source identity. The script records a local archive checksum because GitHub does not publish a
separate checksum for source archives.

Redirect destinations are transport details and never provide source identity. The updater accepts opaque release
asset redirects and codeload redirects that retain the requested ref because identity resolution has already completed
through the GitHub API. Authenticated API and download requests use `GH_TOKEN` without including it in URLs or logs.
The updater does not assume that the host has `jq`. Before API resolution, it pulls a digest-pinned JSON helper image
and uses it to validate and extract the required response fields from root-only temporary files.

Release tags use the tag as their target directory name. Branches and commit SHAs use the first 12 characters of the
resolved full commit SHA, so a moving branch produces a new immutable release directory.

Every Compose operation uses one wrapper that sets the production project explicitly and includes a generated
per-release override when present:

```bash
compose_files=(-f "$release_dir/docker-compose.yml")
if [[ -f "$release_dir/.maintenance-compose.yml" ]]; then
  compose_files+=(-f "$release_dir/.maintenance-compose.yml")
fi

docker compose \
  --project-name ai-agent-telemetry-backend \
  --project-directory "$release_dir" \
  --env-file "$release_dir/.env" \
  "${compose_files[@]}" \
  "$@"
```

The wrapper is the only path to `config`, `pull`, `build`, `down`, `up`, `ps`, and Compose metadata discovery. Direct
Docker discovery filters networks, volumes, and containers by the same exact
project label. The script validates that the active deployment belongs to
`ai-agent-telemetry-backend`. The explicit project name overrides the
archive's top-level Compose name and
preserves the production containers, network, and volumes.

Before pulling or building an image, both scripts bootstrap
`.maintenance-compose.yml` for an older active release that does not have one.
The bootstrap maps each registry-backed service to the immutable repository
digest of its running container. It tags the running Grafana image as
`ai-agent-telemetry-backend-grafana:<active-release-id>` and assigns that tag
to Grafana. Bootstrap stops
before changing any tag when a running container, service mapping, or registry digest is missing or ambiguous. The
script rerenders and validates the effective Compose configuration after writing the override.

Each target release gets its own `.maintenance-compose.yml`. The updater first pulls registry-backed images from the
base Compose configuration, resolves their local repository digests, and writes those digest references into the
override. It assigns Grafana the deployment-specific image tag
`ai-agent-telemetry-backend-grafana:<target-release-id>` before building it.
Later pulls, builds, activation, health
checks, backup restarts, and rollback all use the effective base-plus-override configuration. Pulling a mutable source
tag or building a target therefore cannot move an image reference used by the previous release.

Staging occurs under the backend root with a hidden temporary name. Before stopping the active stack, the script:

1. verifies the downloaded checksum and archive layout;
2. extracts the source without following archive paths outside the staging directory;
3. copies the active `.env` with mode `600` and ownership `root:root`;
4. writes a deployment manifest containing the requested ref, resolved tag or SHA, source URL, and archive checksum;
5. runs `docker compose config --quiet` from the staged release;
6. pulls each registry-backed service from the base configuration and resolves its immutable repository digest;
7. writes the per-release override, builds Grafana with its deployment-specific tag, and validates the effective
   configuration;
8. pulls the backup and health helper image by an architecture-independent manifest-list digest;
9. verifies the target and previous release image references and local image IDs, then records them in the deployment
   manifest so activation and rollback require no registry access;
10. checks whether the exact resolved deployment is active or already staged;
11. measures the source and volume input sizes before downtime and requires free backup space of at least the measured
    uncompressed total plus 10 percent;
12. prints the measured offline-backup input size and applies the large-backup confirmation gate.

The validated staging directory is renamed to its final immutable release directory before backup. It is not made
active until backup succeeds. An existing active target with the same resolved identity and content identity is only
eligible for the success-marker and fresh-health no-op path. A release asset's content identity is its published
checksum. A commit archive's content identity is the full commit SHA plus a normalized manifest of the extracted
backend paths and file checksums. Its downloaded archive checksum is recorded for provenance but is not compared as a
stable identity because GitHub may change archive compression without changing repository content.

An existing inactive target is reusable only when its resolved identity and content identity match the newly resolved
source exactly. The updater refreshes only its `.env` from the active release and reruns all validation, image
resolution, build, and local-image checks. Any mismatch stops the update without changing the existing target
directory.

The 1 GiB large-backup gate is based on the uncompressed source and volume input size. Above that threshold, an
interactive run prints the measured size and asks once whether to continue; the default answer is No. A noninteractive
run stops before downtime unless `--allow-large-backup` is present. The threshold is a safety prompt, not a time
estimate: offline duration remains proportional to stored data, storage throughput, CPU speed, and compression ratio.

## Portable backup

Backups are stored at:

```text
/opt/ai-agent-telemetry-backups/pre-<target>-<UTC timestamp>
```

The timestamp format is `YYYYMMDD-HHMMSSZ`. The backup is created first as a mode-`700` sibling directory ending in
`.incomplete`. It is renamed to its final name only after every archive and checksum is verified.

The backup process sets `umask 077` before creating any file. Completed directories, manifests, environment copies,
and archives remain readable only by root even when the parent directory has broader permissions.

The script validates the active symlink, active `.env`, rendered Compose configuration, project name, running
services, declared named volumes, and Docker volume labels. It requires the exact declared-to-actual volume mapping and
refuses to back up an empty or ambiguous set.

Before stopping the stack, a standalone backup bootstraps and validates the active release's image override, then pulls
and inspects the digest-pinned backup and health helper image. An update verifies the override and helper image prepared
during staging. Both paths create and verify `telemetry-backend-source.tar.gz`, copy `.env` to `backend.env`, and write
the static manifest and restore instructions while the stack is running. The source archive includes deployment
metadata and `.maintenance-compose.yml`, but excludes `.env`, the activation transaction, temporary files, and local
data.

The stack is then stopped with `docker compose down` without `-v`. Only consistency-sensitive volume data is archived
during the offline interval. Each volume is mounted read-only into the helper image and stored as a gzip-compressed tar
archive. The script completes `manifest.txt` with the volume archive results, generates the final `SHA256SUMS`, and
verifies every archive before restarting or activating a release. Secrets are stored once as `backend.env` with mode
`600`; the backup directory and all archives are readable only by root.

Every backup or health helper container started while a transaction exists carries the exact transaction ID and helper
role in dedicated Docker labels. Normal completion verifies that no container with that transaction label remains.
Recovery lists containers by the exact recorded transaction label, force-stops and removes only those containers, and
verifies their absence before restarting the previous release or resuming committed verification. It never selects
containers by image name, partial name, or Compose project alone.

Immediately before `docker compose down`, backup creates the durable maintenance transaction described below with the
`backup-offline` phase. A standalone backup records no target release. A backup invoked by update records the staged
target and transfers transaction ownership back to update after the completed backup is durable. This state lets
either script restart the previous release after a process death or host reboot during volume archiving.

Each completed backup contains:

```text
SHA256SUMS
RESTORE.md
backend.env
manifest.txt
telemetry-backend-source.tar.gz
volumes/<compose-volume>.tar.gz
```

`manifest.txt` records the backup format version, UTC time, source release path and manifest, Compose project name,
logical-to-actual volume mapping, and Docker Compose version. `SHA256SUMS` covers the environment copy, source archive,
volume archives, manifest, and restore instructions.

`RESTORE.md` is self-contained. It explains how to install Docker and Compose on another machine, verify checksums,
extract the source into an immutable release directory, restore `.env` permissions, create and populate the original
Compose volumes, create the `latest` symlink, pull or build images, start the stack, and run the documented health
checks. Docker images are not included because the recovery machine has registry access.

If any backup step fails, the directory keeps the `.incomplete` suffix. A standalone backup attempts to restart the
original stack before returning failure. Update mode leaves it stopped only after a completed backup; a failed backup
also attempts to restore the original running stack and prevents activation of the new release. A successful standalone
backup removes its transaction only after the original release restarts with the recorded image IDs and passes the
strict health gate.

## Activation, health gate, and rollback

The root-only transaction file is
`/opt/ai-agent-telemetry-backend/.maintenance-transaction`. It contains a
format version, transaction ID, operation, phase, previous release, optional
target release, backup path, and expected image
references and IDs for both releases. Both scripts write each state through a mode-`600` temporary file, replace the
transaction with `mv -Tf`, and call GNU `sync -f` on the backend filesystem before crossing the associated state
boundary. Every transaction removal is followed by the same filesystem synchronization.

After update's backup is complete, the backup command records the durable `activation-prepared` phase instead of
restarting the previous stack. Update then creates a temporary relative symlink in the backend root and atomically
replaces the active link with GNU `mv -Tf -- "$temporary_link" "$latest_link"`. Keeping both links in the same
directory guarantees that rename occurs within one filesystem. After synchronizing the link replacement, update
records the `activated` phase and starts the new release with `docker compose up -d --no-build --pull never`, using
only the image references from the target's per-release override.

The health gate uses values already present in `.env`. It never needs the plaintext dashboard Basic Auth password,
which is not stored by the deployment. The gate:

1. waits until all five declared services are running;
2. submits an OTLP log through the public Caddy endpoint with `INGEST_TOKEN` and a unique probe ID;
3. submits an OTLP gauge through Caddy using a fixed metric identity and a unique numeric value, avoiding a new time
   series per update;
4. starts the pre-pulled, digest-pinned helper image on the discovered Compose network and confirms the fresh log in
   VictoriaLogs, the exact gauge value in VictoriaMetrics, and Grafana's `/api/health` response;
5. confirms that every service remains running for a final stability interval.

The default startup and probe deadline is 120 seconds, followed by a 10-second stability interval. Tests may shorten
both values. Each failure names the component and observed state. Probe payloads contain no user, repository, prompt,
session, or machine data.

After the stability interval, the updater writes a durable `.deployment-success` marker in the target release. The
marker contains the resolved identity, content identity, effective image references and IDs, transaction ID, and UTC
verification time. The updater writes it through a same-directory temporary file, replaces it with `mv -Tf`, and
synchronizes the backend filesystem. It then records the durable `committed` transaction phase and removes the
transaction file. A target is an already-active no-op only when its marker matches the target manifest and effective
image configuration and a fresh strict health gate passes. A missing or mismatched marker is never a successful no-op.

An error or signal after symlink activation enters rollback. The script stops the new release, restores the previous
symlink with the same-directory `mv -Tf` primitive, and runs `docker compose up -d --no-build --pull never` from the
previous release's pinned override. Rollback verifies that each restored container uses the image ID recorded in the
transaction, then runs the strict health gate. It removes the transaction only after the previous release passes. The
script reports the failed target, restored release, and backup path. If the previous release does not start or its
image identity differs, the script reports both failures and leaves the transaction and backup untouched.

At startup, both scripts recover an existing transaction before starting a new operation. A `backup-offline` phase
requires `latest` to identify the recorded previous release; recovery starts that release with its pinned images and
runs the strict health gate. It preserves any `.incomplete` backup for diagnosis and clears the transaction only after
recovery succeeds. An `activation-prepared` or `activated` transaction triggers the same idempotent rollback path,
regardless of whether `latest` points to the previous or target release. A `committed` transaction reruns the target
health gate and validates its success marker; success clears the transaction, while failure rolls back.

An unexpected operation, phase, path, symlink target, or image identity stops recovery without guessing. This durable
state is authoritative after `SIGKILL`, shell death, or host reboot; traps only provide faster recovery for catchable
errors and signals.

Rollback does not restore volume data automatically. The old and new releases use the same Compose project and named
volumes, so configuration rollback preserves data written after backup. An operator uses the backup's `RESTORE.md`
only for explicit disaster recovery or incompatible data changes.

## Backup retention

Retention runs only after a successful update and health gate. The script parses and sorts the UTC timestamp suffixes
of completed `pre-*` backup directory names, always protects the two newest, and selects older backups whose parsed
timestamp is more than 14 days old. It ignores malformed names, `.incomplete` directories, and unrelated files.

With an interactive terminal, update prints every candidate and asks once whether to delete them; the default answer
is No. Without a terminal, it reports candidates without deleting them. `--prune-backups` authorizes candidate
deletion in either mode. The script never deletes a protected backup or the backup created by the running update.

## Concurrency and failure boundaries

Backup and update share one `flock` under `/run/lock`. Update invokes backup with an internal inherited-lock marker so
the child does not deadlock by acquiring the same lock. A standalone backup always acquires it.

Temporary downloads and unfinalized staging directories are removed on normal exit. Completed backups and finalized
release directories are never removed automatically. A failed target therefore remains available for the exact-match
reuse path on retry. The durable transaction, rather than process-local trap state, defines whether recovery must
restart an interrupted backup, roll back activation, or finish a committed update. Traps invoke that recovery path for
catchable errors and signals. Failures during resolution, download, staging, or static backup preparation occur before
transaction creation and cannot stop the active stack or change `latest`.

## Documentation

Update `telemetry-backend/README.md` with installation paths, backup and update commands, supported refs, the
data-dependent downtime model, the large-backup confirmation gate, health probes, rollback behavior, retention, and
disaster recovery steps. Document per-release image pinning,
interrupted-update recovery, and the requirement to rerun update after a host
reboot during activation. Link the supervised legacy identity migration
runbook; the updater does not automate identity migration. Update
`docs/release.md` with the exact release asset list and checksum contract.

The documentation uses `/opt/ai-agent-telemetry-backend` and
`/opt/ai-agent-telemetry-backups`. It names the legacy root only in the manual
migration procedure and does not retain `/root/skills-telemetry-backups` as a
default.

## Verification

Add a shell contract test that creates an isolated deployment and backup root under `/tmp`, a unique Compose project,
test volumes, and a local HTTP fixture for release and source-archive downloads. Tests never connect to the production
server.

The contract covers:

- refusal to run as a non-root production invocation and safe acceptance of test-mode roots;
- release asset member validation and absence of `.env`;
- release checksum success and mismatch failure;
- latest release, explicit release tag, branch, full SHA, and short SHA resolution through API responses;
- opaque release-asset and source-archive redirects that contain no usable source identity;
- source archives with different archive checksums but identical resolved commit and extracted backend content;
- compressed source, environment, and every declared volume in a completed backup;
- source archive creation and verification before the first `docker compose down` event;
- checksum verification and portable restoration into a second sandbox;
- explicit `ai-agent-telemetry-backend` project selection;
- older active-release image pinning before any target pull or build;
- per-release registry digests and distinct Grafana image tags across two releases;
- original-stack restart after standalone backup success and failure;
- no symlink change after download, validation, or backup failure;
- exact-match reuse of an inactive target after backup or health-gate failure and refusal on any content mismatch;
- large-backup default-No and noninteractive authorization behavior before downtime;
- same-directory, no-dereference atomic activation after a successful backup;
- completion of helper-image pull, service-image pull, and Grafana build before the stack stops;
- no image pull or build attempt during activation, health checks, or rollback;
- rollback restoration of every previous container image ID after mutable-tag pulls and a target Grafana build;
- strict health-gate success;
- automatic symlink and configuration rollback after a forced health failure;
- restart of the previous release after forced process death during offline volume backup;
- removal of only the interrupted transaction's labeled helper containers before recovery restarts the stack;
- absence of labeled helper containers after forced process death and after normal completion;
- recovery after forced process death before symlink replacement, after replacement, during startup, during health
  verification, after success-marker creation, and after the committed phase;
- refusal to treat an active deployment as a no-op without a matching success marker and a fresh passing health gate;
- stable Compose project and shared volume names across releases;
- interactive default-No retention behavior;
- explicit noninteractive pruning of backups older than 14 days while preserving the newest two;
- maintenance-lock exclusion and cleanup of temporary files.

Extend the release workflow contract to assert the exact backend archive members, executable script assets, complete
`SHA256SUMS`, and exact final asset list. Run ShellCheck, `sh -n` or `bash -n` as appropriate, the maintenance contract,
the existing configuration and dashboard contracts, the complete backend fixture smoke test, and the repository Go
test suite before publishing.
