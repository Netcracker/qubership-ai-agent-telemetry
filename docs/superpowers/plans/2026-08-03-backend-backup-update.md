# Backend backup and update implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish the backend as a release asset and provide tested server-side backup and update commands with
portable backups, minimized downtime, strict health verification, durable rollback, and retention safeguards.

**Architecture:** `backup-backend.sh` owns shared maintenance primitives and the portable backup transaction;
`update-backend.sh` sources those primitives, resolves immutable GitHub content, stages pinned per-release images, and
drives activation. A Docker-backed contract test runs both scripts only under `/tmp`, uses an opaque-redirect HTTP
fixture, and injects process death at transaction boundaries. Release packaging
stays in a separate repository script and publishes only the two server
commands. The ordinary maintenance identity is
`ai-agent-telemetry-backend`. Moving the inspected legacy deployment to it is
a separate, supervised runbook procedure.

**Tech Stack:** Bash 4+, Docker Engine 24+, Docker Compose v2, GNU coreutils/tar/date, curl, Python 3 standard library,
ShellCheck, GitHub Actions, VictoriaLogs, VictoriaMetrics, Grafana, Caddy, and OpenTelemetry JSON/HTTP.

## Global constraints

- Keep exactly two public server commands: `backup-backend.sh` and `update-backend.sh`.
- Use `/opt/ai-agent-telemetry-backend`,
  `/opt/ai-agent-telemetry-backups`, and project `ai-agent-telemetry-backend`
  for ordinary production maintenance.
- Keep identity migration out of the updater. Require an operator to follow
  `telemetry-backend/MIGRATE_LEGACY_BACKEND.md`, run at most one project,
  retain legacy resources after success, and perform cleanup explicitly later.
- Allow path, lock, project, API, download, and timeout overrides only when
  `TELEMETRY_MAINTENANCE_TEST_MODE=1`; test filesystem paths must resolve under `/tmp`.
- Never connect tests to the production server.
- Never print `.env`, `GH_TOKEN`, `INGEST_TOKEN`, Grafana credentials, or Caddy credentials.
- Use `docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc`
  for backup and health helpers.
- Use `ghcr.io/jqlang/jq:1.7.1@sha256:096b83865ad59b5b02841f103f83f45c51318394331bf1995e187ea3be937432`
  for GitHub API JSON parsing.
- Use one explicit Compose wrapper with project name, project directory, environment file, base file, and optional
  `.maintenance-compose.yml` override.
- Store backups as compressed, root-only `pre-<target>-YYYYMMDD-HHMMSSZ` directories; never run Compose with `-v`.
- Pin registry services by repository digest and tag Grafana as
  `ai-agent-telemetry-backend-grafana:<release-id>` before downtime.
- Persist transaction phases with same-directory `mv -Tf` and `sync -f`; process-local traps are only a fast path.
- Preserve backup and update transaction recovery and exact-label helper
  cleanup after the identity transition.
- Roll back configuration and image identity, but never restore volume data automatically.
- Do not save Docker images in backups; restore pulls pinned registry images and rebuilds the release-specific Grafana
  image from the archived source.
- Offer deletion only after a successful health gate, retain the newest two backups, and default every prompt to No.
- Use signed Conventional Commits and do not push until PR feedback has been checked.

---

## File map

- Create `telemetry-backend/scripts/backup-backend.sh`: CLI validation, shared maintenance functions, image pinning,
  transaction persistence and recovery, portable backup, helper cleanup, and strict health gate.
- Create `telemetry-backend/scripts/update-backend.sh`: GitHub ref resolution, source staging, image preparation,
  activation, rollback orchestration, no-op validation, and retention.
- Create `scripts/package-backend-release.sh`: explicit backend allowlist, archive construction, standalone script
  staging, member validation, and modes validation.
- Create `scripts/package_backend_release_test.sh`: release packaging regression test.
- Create `telemetry-backend/tests/maintenance-contract.sh`: named CLI, backup, resolution, activation, recovery, and
  retention suites in an isolated Docker sandbox.
- Create `telemetry-backend/tests/fixtures/maintenance-http-fixture.py`: Releases and Commits API responses, release
  assets, source archives, checksum failures, and opaque redirects.
- Modify `.github/workflows/release.yaml`: stage backend assets, checksum them, verify the exact final list, and upload.
- Modify `.github/workflows/telemetry-backend-tests.yaml`: run syntax, ShellCheck, packaging, and maintenance contracts.
- Modify `telemetry-backend/tests/config-contract.sh`: enforce executable scripts and operator-documentation contract.
- Modify `telemetry-backend/README.md`: installation, backup, update, recovery, retention, and restore procedures.
- Create `telemetry-backend/MIGRATE_LEGACY_BACKEND.md`: supervised identity
  cutover, fallback, and later cleanup.
- Modify `docs/release.md`: exact asset list and backend archive/checksum rules.

---

### Task 1: Backend release packaging

**Files:**
- Create: `scripts/package_backend_release_test.sh`
- Create: `scripts/package-backend-release.sh`
- Create: `telemetry-backend/tests/fixtures/backend-release-members.txt`
- Test: `scripts/package_backend_release_test.sh`

**Interfaces:**
- Consumes: repository root as argument 1 and destination directory as argument 2.
- Produces: `ai-agent-telemetry-backend.tar.gz`, `backup-backend.sh`, and `update-backend.sh` with modes `0644`, `0755`,
  and `0755`, respectively.

- [ ] **Step 1: Write the failing package contract**

Create a fixture source tree with executable stub server commands, invoke the missing packager, and assert the exact
archive members and exclusions. The stubs isolate packaging behavior; Task 8 repeats the contract against the
implemented commands.

```bash
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
fixture_root=$work_dir/source
dist=$work_dir/dist
mkdir -p "$fixture_root"
cp -R "$repo_root/telemetry-backend" "$fixture_root/telemetry-backend"
mkdir -p "$fixture_root/telemetry-backend/scripts"
for script in backup-backend.sh update-backend.sh; do
  printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$fixture_root/telemetry-backend/scripts/$script"
  chmod 0755 "$fixture_root/telemetry-backend/scripts/$script"
done

"$repo_root/scripts/package-backend-release.sh" "$fixture_root" "$dist"
tar -tzf "$dist/ai-agent-telemetry-backend.tar.gz" | LC_ALL=C sort >"$dist/members"
diff -u "$repo_root/telemetry-backend/tests/fixtures/backend-release-members.txt" "$dist/members"
[ ! -e "$dist/.env" ]
[ -x "$dist/backup-backend.sh" ]
[ -x "$dist/update-backend.sh" ]
```

Store the sorted exact member list in `telemetry-backend/tests/fixtures/backend-release-members.txt`; include the six
backend root files, both public scripts, `grafana/Dockerfile`, all four dashboard JSON files, and the four provisioning
YAML files.

- [ ] **Step 2: Run the package test and verify RED**

Run: `bash scripts/package_backend_release_test.sh`

Expected: FAIL because `scripts/package-backend-release.sh` does not exist.

- [ ] **Step 3: Implement explicit allowlist packaging**

Use an embedded member list and fail before writing output when any member is absent:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_root=${1:?usage: package-backend-release.sh REPOSITORY_ROOT DIST_DIR}
dist_dir=${2:?usage: package-backend-release.sh REPOSITORY_ROOT DIST_DIR}
backend_dir=$repo_root/telemetry-backend
member_file=$(mktemp)
trap 'rm -f "$member_file"' EXIT HUP INT TERM

sed 's#/$##' >"$member_file" <<'EOF'
.env.example
Caddyfile
README.md
native-otlp-onboarding.md
docker-compose.yml
otel-collector-config.yaml
grafana/Dockerfile
grafana/dashboards/ai-agent-telemetry-adoption.json
grafana/dashboards/codex-native-metrics.json
grafana/dashboards/native-agent-metrics-overview.json
grafana/dashboards/telemetry-health.json
grafana/provisioning/alerting/empty.yaml
grafana/provisioning/dashboards/dashboards.yaml
grafana/provisioning/datasources/victorialogs.yaml
grafana/provisioning/plugins/empty.yaml
scripts/backup-backend.sh
scripts/update-backend.sh
EOF

while IFS= read -r member; do
  [[ -f "$backend_dir/$member" ]] || { printf 'Missing backend release member: %s\n' "$member" >&2; exit 1; }
done <"$member_file"
mkdir -p "$dist_dir"
tar -C "$backend_dir" -czf "$dist_dir/ai-agent-telemetry-backend.tar.gz" -T "$member_file"
install -m 0755 "$backend_dir/scripts/backup-backend.sh" "$dist_dir/backup-backend.sh"
install -m 0755 "$backend_dir/scripts/update-backend.sh" "$dist_dir/update-backend.sh"
```

After creation, compare `tar -tzf` output to the same normalized member file and fail if `.env`, Git metadata, tests,
fixtures, or local Grafana data appears.

- [ ] **Step 4: Run the package test and verify GREEN**

Run: `bash scripts/package_backend_release_test.sh`

Expected: `PASS: backend release packaging contract`.

- [ ] **Step 5: Commit the packaging unit**

```bash
git add scripts/package-backend-release.sh scripts/package_backend_release_test.sh \
  telemetry-backend/tests/fixtures/backend-release-members.txt
git commit -S -m "build(release): package telemetry backend assets"
```

---

### Task 2: Maintenance safety foundation and CLI contract

**Files:**
- Create: `telemetry-backend/scripts/backup-backend.sh`
- Create: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Produces shared Bash functions `maintenance_init`, `compose`, `write_transaction`, `read_transaction`,
  `clear_transaction`, `cleanup_transaction_helpers`, `pin_active_images`, `strict_health_gate`, `recover_transaction`,
  and `backup_main`.
- `update-backend.sh` later loads them with `TELEMETRY_SOURCE_ONLY=1 source "$script_dir/backup-backend.sh"`.
- Public backup syntax is
  `backup-backend.sh [--target-label LABEL] [--leave-stopped]`
  `[--allow-large-backup] [--legacy-source]`.
- The test harness defines `fail MESSAGE` and `run_fails COMMAND...`; `run_fails` succeeds only when its command exits
  nonzero.

- [ ] **Step 1: Write failing CLI and isolation tests**

Add a named `cli` suite that checks:

```bash
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
run_fails() { if "$@"; then fail "command unexpectedly passed: $*"; fi; }

run_fails env -u TELEMETRY_MAINTENANCE_TEST_MODE "$backup_script" --target-label test
run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=/opt/unsafe "$backup_script"
run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
  TELEMETRY_TEST_PROJECT_NAME='bad/project' "$backup_script"
run_fails "$backup_script" --target-label '../escape'
run_fails "$backup_script" --unknown-option
```

The suite also starts two processes against the same test lock and asserts that the second exits without touching the
active link.

- [ ] **Step 2: Run the CLI suite and verify RED**

Run: `bash telemetry-backend/tests/maintenance-contract.sh cli`

Expected: FAIL because `backup-backend.sh` does not exist.

- [ ] **Step 3: Implement guarded configuration and shared primitives**

Define exact production constants and allow overrides only in test mode:

```bash
readonly PROJECT_NAME_DEFAULT=ai-agent-telemetry-backend
readonly BACKEND_ROOT_DEFAULT=/opt/ai-agent-telemetry-backend
readonly BACKUP_ROOT_DEFAULT=/opt/ai-agent-telemetry-backups
readonly LOCK_FILE_DEFAULT=/run/lock/ai-agent-telemetry-backend-maintenance.lock
readonly LEGACY_PROJECT_NAME=skills-telemetry-backend
readonly LEGACY_BACKEND_ROOT=/opt/skills-telemetry-backend
readonly LEGACY_LOCK_FILE=/run/lock/skills-telemetry-backend-maintenance.lock
HELPER_IMAGE='docker.io/library/alpine:3.20@sha256:'
HELPER_IMAGE+='d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc'
readonly HELPER_IMAGE
readonly JSON_IMAGE='ghcr.io/jqlang/jq:1.7.1@sha256:096b83865ad59b5b02841f103f83f45c51318394331bf1995e187ea3be937432'
readonly TRANSACTION_LABEL='io.qubership.ai-agent-telemetry.maintenance.transaction'
readonly ROLE_LABEL='io.qubership.ai-agent-telemetry.maintenance.role'
readonly LARGE_BACKUP_BYTES=1073741824
```

Use `realpath -m` and a prefix check for every test path, validate project names against `^[a-zA-Z0-9_-]+$`, set
`umask 077`, require Bash/root in production, and acquire the shared lock on a dedicated file descriptor. Parse
transaction files with a `while IFS='=' read -r key value` case statement; never `source` or `eval` state or `.env`.

Implement `compose()` with a Bash array containing `--project-name`, `--project-directory`, `--env-file`, the base
file, and the optional generated override. Validate `docker compose config --quiet` before any mutating command.

- [ ] **Step 4: Run syntax, ShellCheck, and CLI tests**

```bash
bash -n telemetry-backend/scripts/backup-backend.sh
shellcheck telemetry-backend/scripts/backup-backend.sh
bash telemetry-backend/tests/maintenance-contract.sh cli
```

Expected: all three commands exit 0 and the suite prints `PASS: maintenance cli contract`.

- [ ] **Step 5: Commit the safety foundation**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "feat(backend): add maintenance safety foundation"
```

---

### Task 3: Portable offline-consistent backup

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- `backup_main` returns only after a completed backup is atomically renamed and the original stack is healthy, unless
  the validated internal `--leave-stopped` mode transfers an `activation-prepared` transaction to update.
- Completed backup members are `SHA256SUMS`, `RESTORE.md`, `backend.env`, `manifest.txt`,
  `telemetry-backend-source.tar.gz`, and one `volumes/<actual-volume>.tar.gz` per declared volume.
- Test helper `single_completed_backup ROOT` requires exactly one completed `pre-*` directory and prints it.
- Test helper `restore_backup_into_second_sandbox BACKUP ROOT` verifies checksums, extracts source and `.env`, creates
  the manifest's exact volumes, and untars every volume archive into them.
- Test helper `assert_volume_file PROJECT LOGICAL PATH` mounts the exact labeled volume read-only and requires `PATH`.

- [ ] **Step 1: Add failing backup and restore tests**

Add a `backup` suite that creates a unique Compose project and five named test volumes under `/tmp`, writes sentinel
files to every volume, and asserts:

```bash
backup_dir=$(single_completed_backup "$backup_root")
sha256sum -c "$backup_dir/SHA256SUMS"
[ "$(stat -c '%a' "$backup_dir")" = 700 ]
[ "$(stat -c '%a' "$backup_dir/backend.env")" = 600 ]
if tar -tzf "$backup_dir/telemetry-backend-source.tar.gz" | grep -Fx '.env'; then
  fail 'source archive contains .env'
fi
restore_backup_into_second_sandbox "$backup_dir" "$restore_root"
assert_volume_file "$restore_project" grafana-data /sentinel/grafana
assert_volume_file "$restore_project" vmetrics-data /sentinel/vmetrics
```

Record events from a test-only command logger and assert that source archive verification occurs before the first
`docker compose down`. Cover missing `.env`, ambiguous volume labels, checksum failure, helper failure, restart after
success, restart after failure, large-backup default No, and `--allow-large-backup`.

- [ ] **Step 2: Run the backup suite and verify RED**

Run: `bash telemetry-backend/tests/maintenance-contract.sh backup`

Expected: FAIL because `backup_main` does not create the required archive set.

- [ ] **Step 3: Implement image bootstrap and pre-downtime work**

Map the five running services to their containers through exact Compose labels. For each registry-backed service,
select the `RepoDigest` whose repository matches the configured image. Tag Grafana's running image as
`skills-telemetry-backend-grafana:<release-id>`. Write `.maintenance-compose.yml` atomically and rerender Compose.

The generated shape is exact and contains all five services:

```yaml
services:
  caddy:
    image: caddy@sha256:<resolved-caddy-digest>
  collector:
    image: otel/opentelemetry-collector-contrib@sha256:<resolved-collector-digest>
  grafana:
    image: skills-telemetry-backend-grafana:<release-id>
  victorialogs:
    image: victoriametrics/victoria-logs@sha256:<resolved-victorialogs-digest>
  victoriametrics:
    image: victoriametrics/victoria-metrics@sha256:<resolved-victoriametrics-digest>
```

Create the `.incomplete` backup, `backend.env`, source archive, static manifest, `RESTORE.md`, and their preliminary
checksums before stopping the stack. Measure source plus volume bytes, require free space greater than measured bytes
plus 10 percent, and apply the 1 GiB confirmation gate.

Generate the final path as `$backup_root/pre-$target_label-$(date -u +%Y%m%d-%H%M%SZ)`, create its `.incomplete`
sibling with mode `0700`, and reject collisions. `RESTORE.md` must contain the exact manifest-driven commands to
verify checksums, rebuild the release-specific Grafana image, pull pinned service digests, recreate volumes, restore
their archives, install `.env` with mode `0600`, replace `latest`, start, and run the strict health gate.

- [ ] **Step 4: Implement durable volume backup**

Write `backup-offline` before `compose down`. Resolve every actual volume through exact project and logical-volume
labels. Start each helper with both labels and an unambiguous name:

```bash
docker run --rm \
  --name "skills-telemetry-backup-${transaction_id}-${ordinal}" \
  --label "$TRANSACTION_LABEL=$transaction_id" \
  --label "$ROLE_LABEL=backup" \
  --env "BACKUP_FILE=$archive_name" \
  --mount "type=volume,src=$volume_name,dst=/source,readonly" \
  --mount "type=bind,src=$backup_work_dir/volumes,dst=/backup" \
  "$HELPER_IMAGE" sh -eu -c 'cd /source && tar -czf "/backup/$BACKUP_FILE" .'
```

Pass `BACKUP_FILE` with `--env`, verify each tarball, finalize manifest and `SHA256SUMS`, and rename `.incomplete` only
after `sha256sum -c` passes. Restart and health-check standalone backup, or transition the update-owned transaction to
`activation-prepared`.

- [ ] **Step 5: Run the backup suite and existing configuration contract**

```bash
bash telemetry-backend/tests/maintenance-contract.sh backup
sh telemetry-backend/tests/config-contract.sh
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit portable backup**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "feat(backend): add portable telemetry backups"
```

---

### Task 4: Immutable GitHub source resolution and staging

**Files:**
- Create: `telemetry-backend/scripts/update-backend.sh`
- Create: `telemetry-backend/tests/fixtures/maintenance-http-fixture.py`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Public syntax is
  `update-backend.sh [--ref REF] [--allow-large-backup] [--prune-backups]`; default ref is `latest`.
- `resolve_source REF` writes normalized fields `kind`, `requested_ref`, `resolved_identity`, `content_identity`,
  `download_url`, and `target_id` into root-only process state.
- `stage_release` produces a final release directory with `.env`, `.deployment-manifest`, normalized content checksums,
  and `.maintenance-compose.yml` before downtime.

- [ ] **Step 1: Implement the failing opaque-redirect fixture tests**

The Python fixture must serve these exact logical cases:

```text
GET /api/repos/Netcracker/qubership-ai-agent-telemetry/releases/latest
GET /api/repos/Netcracker/qubership-ai-agent-telemetry/releases/tags/v1.2.3
GET /api/repos/Netcracker/qubership-ai-agent-telemetry/commits/main
GET /api/repos/Netcracker/qubership-ai-agent-telemetry/commits/0123456789abcdef0123456789abcdef01234567
GET /api/repos/Netcracker/qubership-ai-agent-telemetry/commits/eacf978
GET /assets/backend -> 302 /opaque/release-asset-7f4c
GET /tarballs/0123456789abcdef0123456789abcdef01234567 -> 302 /opaque/source-archive-91aa
```

Responses expose `tag_name`, exact asset arrays, and full `.sha`; redirect targets deliberately contain neither tag
nor SHA. Add malformed JSON, duplicate asset, checksum mismatch, unresolved ref, and moving-branch responses.

- [ ] **Step 2: Run resolution tests and verify RED**

Run: `bash telemetry-backend/tests/maintenance-contract.sh resolution`

Expected: FAIL because `update-backend.sh` does not exist.

- [ ] **Step 3: Implement API resolution and safe extraction**

Pull the pinned JSON helper before API use. Send `Accept: application/vnd.github+json`,
`X-GitHub-Api-Version: 2022-11-28`, and optional `Authorization: Bearer $GH_TOKEN` headers. Parse files by mounting the
root-only response into the JSON helper. Resolve `latest` and SemVer through Releases API, and every other ref through
Commits API before downloading.

Use one request function that keeps credentials in headers:

```bash
github_get() {
  local url=$1 output=$2
  local -a headers=(-H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28')
  [[ -z ${GH_TOKEN:-} ]] || headers+=(-H "Authorization: Bearer $GH_TOKEN")
  curl --fail --silent --show-error --location "${headers[@]}" --output "$output" "$url"
}
```

Percent-encode a branch or SHA path component with the pinned JSON helper's `jq -nr --arg ref "$requested_ref"
'$ref|@uri'`; never concatenate an unchecked ref into an API path.

For releases, require one archive and one `SHA256SUMS` asset and verify exactly one checksum line. For source refs,
download by full SHA, extract only the single top-level `telemetry-backend` subtree, reject absolute paths, `..`, links
escaping the tree, duplicate paths, and unexpected root layouts. Use full SHA plus normalized extracted path checksums
as content identity; record the transport checksum only as provenance.

- [ ] **Step 4: Implement per-release image preparation and retry semantics**

Pull registry services from the base configuration, resolve local repository digests, generate the target override,
build Grafana with `skills-telemetry-backend-grafana:<target-id>`, and record effective references plus local IDs. An
active exact target enters marker-plus-health validation. Reuse an inactive target only when source identity and
normalized content identity match; otherwise fail without overwriting it.

The deployment manifest stores one stable key per value and one service record per line:

```text
format=1
requested_ref=main
resolved_identity=0123456789abcdef0123456789abcdef01234567
content_identity=<normalized-backend-sha256>
image.caddy=<repository-digest>|<local-image-id>
image.grafana=ai-agent-telemetry-backend-grafana:0123456789ab|<local-image-id>
```

- [ ] **Step 5: Run resolution tests and verify GREEN**

```bash
bash -n telemetry-backend/scripts/update-backend.sh
shellcheck telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh
bash telemetry-backend/tests/maintenance-contract.sh resolution
```

Expected: all commands exit 0.

- [ ] **Step 6: Commit immutable staging**

```bash
git add telemetry-backend/scripts/update-backend.sh \
  telemetry-backend/tests/maintenance-contract.sh \
  telemetry-backend/tests/fixtures/maintenance-http-fixture.py
git commit -S -m "feat(backend): stage immutable backend updates"
```

---

### Task 5: Activation, strict health gate, and image-correct rollback

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/scripts/update-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- Transaction phases are `backup-offline`, `activation-prepared`, `activated`, and `committed`.
- `strict_health_gate RELEASE_DIR` returns 0 only after five running services, fresh OTLP log and gauge visibility,
  Grafana `/api/health`, and the 10-second stability interval.
- `rollback_transaction` restores previous symlink, configuration, image IDs, and health without restoring volumes.
- Test helper `assert_container_image_ids PROJECT MANIFEST` compares every running service container's `.Image` with
  the exact service-to-ID manifest.
- Test helper `assert_no_registry_event_after MARKER` reads the test command log and rejects pull or build entries after
  the named marker.

- [ ] **Step 1: Add failing activation and rollback tests**

The `activation` suite must save previous container image IDs, run a successful update, and assert the new relative
`latest` link, matching success marker, preserved volume sentinels, and visible unique OTLP probes. Then force a health
failure after target startup and assert:

```bash
[ "$(readlink "$backend_root/latest")" = "$previous_id" ]
assert_container_image_ids "$project" "$previous_image_manifest"
assert_no_registry_event_after compose_down
[ -f "$reported_backup/SHA256SUMS" ]
[ -f "$backend_root/$target_id/.deployment-manifest" ]
```

Before this test, retarget a mutable fixture image and build a different Grafana image under the target tag so the
image-ID assertion can detect the original defect.

- [ ] **Step 2: Run activation tests and verify RED**

Run: `bash telemetry-backend/tests/maintenance-contract.sh activation`

Expected: FAIL at symlink activation or strict health verification.

- [ ] **Step 3: Implement durable activation**

Persist the complete transaction and `sync -f` before same-directory `mv -Tf` replaces `latest`. Start with
`compose up -d --no-build --pull never`. Create unique OTLP log and gauge payloads without user, session, repository,
prompt, or machine fields. Label the internal health helper with transaction ID and role `health`; query VictoriaLogs,
VictoriaMetrics, and Grafana on the exact Compose network.

Capture Compose interpolation values into a mode-`0600` temporary file with `compose "$release_dir" config
--environment`, parse only the exact required keys, and delete the file on exit. Never source `.env`. Production Caddy
probes use normal TLS verification; test mode may add `--cacert "$TELEMETRY_TEST_CA_CERT"` only after validating that
the certificate path resolves under `/tmp`.

Use the exact activation primitive:

```bash
temporary_link=$backend_root/.latest.$transaction_id
ln -s -- "$target_id" "$temporary_link"
mv -Tf -- "$temporary_link" "$backend_root/latest"
sync -f "$backend_root"
write_transaction activated
compose "$target_dir" up -d --no-build --pull never
```

After health and stability pass, atomically write `.deployment-success`, record `committed`, and durably remove the
transaction. An active target is a no-op only after marker validation and a fresh health gate.

- [ ] **Step 4: Implement rollback and image verification**

Stop the target, restore the previous relative symlink with `mv -Tf`, start the previous pinned override with
`--no-build --pull never`, compare every running container `.Image` ID with the transaction manifest, and run the
strict health gate. Keep the transaction and backup when either image or health verification fails.

Rollback uses the same replacement function and never mutates volumes:

```bash
compose "$target_dir" down
replace_latest "$previous_id"
compose "$previous_dir" up -d --no-build --pull never
verify_running_image_ids "$previous_dir" "$transaction_file"
strict_health_gate "$previous_dir"
clear_transaction
```

- [ ] **Step 5: Run activation tests and verify GREEN**

```bash
bash telemetry-backend/tests/maintenance-contract.sh activation
sh telemetry-backend/tests/smoke.sh
```

Expected: successful update, forced rollback, and existing backend smoke all pass.

- [ ] **Step 6: Commit activation and rollback**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "feat(backend): add verified update rollback"
```

---

### Task 6: Crash recovery and orphan helper cleanup

**Files:**
- Modify: `telemetry-backend/scripts/backup-backend.sh`
- Modify: `telemetry-backend/scripts/update-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`

**Interfaces:**
- `recover_transaction` runs before either public command starts a new operation.
- `cleanup_transaction_helpers TRANSACTION_ID` selects only containers labeled with the exact validated transaction ID,
  removes them, and verifies the result is empty before any stack restart.
- Test-only `TELEMETRY_TEST_KILL_AT` accepts the exact phase names documented below and is rejected outside test mode.
- Test helpers `containers_for_transaction ID`, `single_container_for_transaction ID`, and `assert_container_absent ID`
  use exact Docker label filters; `run_recovery` reruns update with the current sandbox variables.

- [ ] **Step 1: Add forced-death recovery tests**

Run update as a child and send `SIGKILL` at `backup-helper-running`, `activation-prepared`, `symlink-replaced`,
`target-started`, `health-passed`, `success-marker-written`, and `committed-written`. After each death, rerun the public
command and assert either healthy previous rollback or healthy committed target completion according to the durable
phase.

For `backup-helper-running`, set a test-only helper delay, capture its transaction label, kill the shell, and assert:

```bash
helper_id=$(single_container_for_transaction "$transaction_id")
kill -KILL "$maintenance_pid"
wait "$maintenance_pid" || true
[ -n "$helper_id" ]
run_recovery
[ -z "$(containers_for_transaction "$transaction_id")" ]
assert_container_absent "$helper_id"
assert_previous_stack_healthy
```

Create an unrelated labeled container with another transaction ID and assert recovery leaves it untouched.

- [ ] **Step 2: Run recovery tests and verify RED**

Run: `bash telemetry-backend/tests/maintenance-contract.sh recovery`

Expected: FAIL because an interrupted helper or transaction remains.

- [ ] **Step 3: Implement idempotent phase recovery**

On `backup-offline`, remove exact-transaction helpers, require `latest` to identify previous, restart pinned previous,
verify image IDs and health, preserve `.incomplete`, and clear state. On `activation-prepared` or `activated`, perform
the same helper cleanup and rollback. On `committed`, clean helpers, validate marker and target health, then clear; if
validation fails, rollback. Reject unexpected fields, phases, symlinks, paths, IDs, or multiple helper matches.

Dispatch only validated phases:

```bash
case $transaction_phase in
  backup-offline) recover_offline_backup ;;
  activation-prepared|activated) rollback_transaction ;;
  committed) recover_committed_transaction ;;
  *) fail "Unsupported maintenance transaction phase: $transaction_phase" ;;
esac
```

Normal backup and health completion must call the same helper-absence assertion before phase transition or transaction
removal.

- [ ] **Step 4: Run recovery tests and verify GREEN**

Run: `bash telemetry-backend/tests/maintenance-contract.sh recovery`

Expected: every forced-death case passes, no transaction-scoped helper remains, and the unrelated helper still runs.

- [ ] **Step 5: Commit crash recovery**

```bash
git add telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  telemetry-backend/tests/maintenance-contract.sh
git commit -S -m "feat(backend): recover interrupted maintenance"
```

---

### Task 7: Backup retention and operator documentation

**Files:**
- Modify: `telemetry-backend/scripts/update-backend.sh`
- Modify: `telemetry-backend/tests/maintenance-contract.sh`
- Modify: `telemetry-backend/README.md`
- Modify: `telemetry-backend/tests/config-contract.sh`
- Test: `telemetry-backend/tests/maintenance-contract.sh`
- Test: `telemetry-backend/tests/config-contract.sh`

**Interfaces:**
- `prune_backups` considers only completed names ending in `YYYYMMDD-HHMMSSZ`, protects the newest two, and deletes
  candidates older than 14 days only after interactive Yes or `--prune-backups`.

- [ ] **Step 1: Add failing retention and documentation tests**

Create backups at 30, 20, 10, and 1 day old plus malformed and `.incomplete` entries. Assert interactive default No,
noninteractive report-only behavior, explicit prune, newest-two protection, and preservation of malformed entries.
Extend `config-contract.sh` to require `/opt/ai-agent-telemetry-backups`,
`--ref`, `--allow-large-backup`, `--prune-backups`, `SIGKILL`, `RESTORE.md`,
image digests, the manual identity migration link, and the no-volume-restore
rollback warning.

- [ ] **Step 2: Run retention and documentation tests and verify RED**

```bash
bash telemetry-backend/tests/maintenance-contract.sh retention
sh telemetry-backend/tests/config-contract.sh
```

Expected: both fail because retention and operator sections are missing.

- [ ] **Step 3: Implement retention**

Parse the final UTC suffix with GNU `date -u`, sort by parsed timestamp, ignore malformed paths and `.incomplete`, and
remove only candidates outside the newest two whose timestamp is earlier than 14 days ago. Print the exact candidate
list. Read one interactive answer with `[yY]` as the only affirmative value; EOF and empty input mean No.

Compute the fixed cutoff once and validate every suffix before conversion:

```bash
cutoff_epoch=$(date -u -d '14 days ago' +%s)
if [[ $timestamp =~ ^[0-9]{8}-[0-9]{6}Z$ ]]; then
  backup_epoch=$(date -u -d "${timestamp:0:8} ${timestamp:9:2}:${timestamp:11:2}:${timestamp:13:2} UTC" +%s)
fi
```

- [ ] **Step 4: Write backend operator documentation**

Add sections for release installation layout, standalone backup, latest/tag/branch/SHA update, required GitHub token
behavior, data-dependent downtime, large-backup confirmation, pinned images, health probes, automatic rollback,
interrupted transaction recovery, helper cleanup, retention, and cross-machine restore. State that operators rerun
update after a reboot during maintenance and that volume restore is always
manual. Link the supervised migration from the legacy project and keep
identity migration out of the update command.

- [ ] **Step 5: Run retention and documentation tests and verify GREEN**

```bash
bash telemetry-backend/tests/maintenance-contract.sh retention
sh telemetry-backend/tests/config-contract.sh
```

Expected: both commands exit 0.

- [ ] **Step 6: Commit retention and docs**

```bash
git add telemetry-backend/scripts/update-backend.sh telemetry-backend/tests/maintenance-contract.sh \
  telemetry-backend/README.md telemetry-backend/tests/config-contract.sh
git commit -S -m "feat(backend): add safe backup retention"
```

---

### Task 8: Release workflow, CI, and release guide

**Files:**
- Modify: `.github/workflows/release.yaml`
- Modify: `.github/workflows/telemetry-backend-tests.yaml`
- Modify: `docs/release.md`
- Modify: `scripts/package_backend_release_test.sh`
- Test: `scripts/package_backend_release_test.sh`

**Interfaces:**
- Release payload adds exactly `ai-agent-telemetry-backend.tar.gz`, `backup-backend.sh`, and `update-backend.sh`.
- `SHA256SUMS` covers all release assets except itself exactly once.

- [ ] **Step 1: Extend the package test with workflow contract assertions**

Require the release workflow's expected asset list and upload paths to contain all three backend assets exactly once.
Generate a synthetic complete `dist`, run the same checksum command, and compare sorted checksum names with all files
except `SHA256SUMS`.

- [ ] **Step 2: Run package/workflow tests and verify RED**

Run: `bash scripts/package_backend_release_test.sh`

Expected: FAIL because `.github/workflows/release.yaml` does not stage or upload backend assets.

- [ ] **Step 3: Stage and checksum backend assets in the release job**

After installer staging, invoke:

```bash
bash scripts/package-backend-release.sh "$GITHUB_WORKSPACE" "$GITHUB_WORKSPACE/dist"
```

Keep the pinned `assets-action`. Extend `sha256sum ai-agent-telemetry-* install.sh install.ps1` output, the exact
expected-assets heredoc, checksum comparison, and `item-path` so the three backend assets are uploaded exactly once.

- [ ] **Step 4: Add backend CI gates**

Extend path filters for `scripts/package-backend-release.sh`, `scripts/package_backend_release_test.sh`,
`.github/workflows/release.yaml`, and `docs/release.md`. Add steps for:

```bash
bash -n telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  scripts/package-backend-release.sh scripts/package_backend_release_test.sh
shellcheck telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  scripts/package-backend-release.sh scripts/package_backend_release_test.sh
bash scripts/package_backend_release_test.sh
bash telemetry-backend/tests/maintenance-contract.sh
sh telemetry-backend/tests/config-contract.sh
```

Retain the existing full smoke step. Increase the job timeout only if a fresh local full run exceeds 20 minutes; use
the observed duration plus five minutes rather than an arbitrary value.

- [ ] **Step 5: Update the release guide**

List the exact final assets, state that the backend archive has files at its root, and document checksum coverage and
the standalone updater bootstrap commands. Keep every GitHub paragraph and list item on one source line as required by
repository guidance.

- [ ] **Step 6: Run workflow/package/config verification**

```bash
bash scripts/package_backend_release_test.sh
sh telemetry-backend/tests/config-contract.sh
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 7: Commit release integration**

```bash
git add .github/workflows/release.yaml .github/workflows/telemetry-backend-tests.yaml docs/release.md \
  scripts/package_backend_release_test.sh
git commit -S -m "build(release): publish telemetry backend"
```

---

### Task 9: Full verification and PR readiness

**Files:**
- Verify only; modify a file only to fix a failure caused by Tasks 1–8.

**Interfaces:**
- Produces a clean, signed branch ready for review and push after GitHub comments are rechecked.

- [ ] **Step 1: Run static checks**

```bash
bash -n telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  scripts/package-backend-release.sh scripts/package_backend_release_test.sh
shellcheck telemetry-backend/scripts/backup-backend.sh telemetry-backend/scripts/update-backend.sh \
  scripts/package-backend-release.sh scripts/package_backend_release_test.sh \
  telemetry-backend/tests/maintenance-contract.sh
git diff --check
```

Expected: all commands exit 0 with no diagnostics.

- [ ] **Step 2: Run release and maintenance contracts**

```bash
bash scripts/package_backend_release_test.sh
bash telemetry-backend/tests/maintenance-contract.sh
```

Expected: packaging plus CLI, backup, resolution, activation, recovery, and retention suites all pass.

- [ ] **Step 3: Run existing backend contracts and full fixture smoke**

```bash
sh telemetry-backend/tests/config-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
```

Expected: configuration, dashboard, and end-to-end backend tests pass.

- [ ] **Step 4: Run repository Go checks**

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
```

Expected: `gofmt -l` prints nothing and the remaining commands exit 0.

- [ ] **Step 5: Inspect changes and GitHub review state**

```bash
git status --short --branch
git log --show-signature --oneline im/feat/remote-codex-metrics..HEAD
gh pr view 26 --repo Netcracker/qubership-ai-agent-telemetry --comments
gh pr checks 26 --repo Netcracker/qubership-ai-agent-telemetry
```

Confirm that only requested files changed, every commit has a good signature, no unresolved actionable comment remains,
and the branch is not pushed until this check is complete.

- [ ] **Step 6: Request final code review**

Use `superpowers:requesting-code-review` against the complete diff. Resolve only verified findings, rerun the affected
test plus the full verification set, and present the resulting commits for approval before pushing.
