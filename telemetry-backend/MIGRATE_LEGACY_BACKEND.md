# Migrate the legacy backend identity

Use this runbook once to move the deployed backend from the legacy Compose
identity to the ordinary identity. The deployments use these values:

- Legacy Compose project: `skills-telemetry-backend`.
- New Compose project: `ai-agent-telemetry-backend`.
- Legacy backend root: `/opt/skills-telemetry-backend`.
- New backend root: `/opt/ai-agent-telemetry-backend`.
- Backup root: `/opt/ai-agent-telemetry-backups` for both identities.
- Legacy lock: `/run/lock/skills-telemetry-backend-maintenance.lock`.
- New lock: `/run/lock/ai-agent-telemetry-backend-maintenance.lock`.

An operator must remain present for the entire migration and its stability
interval. At most one Compose project may run at any point. Do not schedule
this procedure as an unattended job.

Compose shutdown removes the legacy containers. The preserved resources are
the legacy source tree, volumes, backup, and image identities. They allow the
operator to recreate those containers for fallback. Remove those resources
only after the observation period ends.

## 1. Set the identities

Sign in as `root`, start a durable terminal session, and set these values.
Replace the release tag with the exact published tag to install.

```bash
LEGACY_PROJECT=skills-telemetry-backend
NEW_PROJECT=ai-agent-telemetry-backend
LEGACY_ROOT=/opt/skills-telemetry-backend
NEW_ROOT=/opt/ai-agent-telemetry-backend
BACKUP_ROOT=/opt/ai-agent-telemetry-backups
RELEASE_TAG=<release-tag>
RELEASE_DIR=/root/ai-agent-telemetry-backend-migration/$RELEASE_TAG
NEW_RELEASE=$NEW_ROOT/$RELEASE_TAG
export LEGACY_PROJECT NEW_PROJECT LEGACY_ROOT NEW_ROOT BACKUP_ROOT
export RELEASE_TAG RELEASE_DIR NEW_RELEASE
```

Verify the result:

```bash
printf '%s\n' "$LEGACY_PROJECT" "$NEW_PROJECT" "$LEGACY_ROOT"
printf '%s\n' "$NEW_ROOT" "$BACKUP_ROOT" "$RELEASE_TAG"
test "$LEGACY_PROJECT" = skills-telemetry-backend
test "$NEW_PROJECT" = ai-agent-telemetry-backend
test "$LEGACY_ROOT" = /opt/skills-telemetry-backend
test "$NEW_ROOT" = /opt/ai-agent-telemetry-backend
test "$BACKUP_ROOT" = /opt/ai-agent-telemetry-backups
test "$RELEASE_TAG" != '<release-tag>'
```

## 2. Download and verify the release

Create a private staging directory. Download the backend archive and checksum
list from the same release. Retain only the backend archive checksum.

```bash
install -d -m 700 "$RELEASE_DIR"
cd "$RELEASE_DIR"
RELEASE_URL=https://github.com/Netcracker/qubership-ai-agent-telemetry/releases
RELEASE_URL=$RELEASE_URL/download/$RELEASE_TAG
curl --fail --location --remote-name \
  "$RELEASE_URL/ai-agent-telemetry-backend.tar.gz"
curl --fail --location --remote-name \
  "$RELEASE_URL/backup-backend.sh"
curl --fail --location --remote-name \
  "$RELEASE_URL/SHA256SUMS"
grep -E '  (ai-agent-telemetry-backend.tar.gz|backup-backend.sh)$' SHA256SUMS \
  > SHA256SUMS.backend
mv -f SHA256SUMS.backend SHA256SUMS
sha256sum -c SHA256SUMS
```

Verify the result: the checksum command must print both of these lines:

```text
ai-agent-telemetry-backend.tar.gz: OK
backup-backend.sh: OK
```

Stop if either result is missing.

## 3. Inspect the legacy deployment

Require the five legacy services, record their containers and volumes, and
confirm that the new project is not running.

```bash
LEGACY_RELEASE=$(readlink -f "$LEGACY_ROOT/latest")
test -d "$LEGACY_RELEASE"
docker compose --project-name "$LEGACY_PROJECT" \
  --project-directory "$LEGACY_RELEASE" \
  --env-file "$LEGACY_RELEASE/.env" -f "$LEGACY_RELEASE/docker-compose.yml" ps
test "$(docker ps -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT" | wc -l)" -eq 5
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT")"
docker volume ls --filter "label=com.docker.compose.project=$LEGACY_PROJECT"
```

Verify the result: all five legacy services must run, the new project must
have no running containers, and the legacy volume list must have five entries.

## 4. Back up and stop the legacy deployment

Run the release's checksum-verified backup asset against the legacy identity.
The
`--leave-stopped` flag keeps the legacy project offline after the backup is
durable.

```bash
install -m 700 "$RELEASE_DIR/backup-backend.sh" \
  /usr/local/sbin/backup-backend.sh
/usr/local/sbin/backup-backend.sh --legacy-source \
  --target-label identity-migration --leave-stopped
BACKUP_DIR=$(find "$BACKUP_ROOT" -maxdepth 1 -type d \
  -name 'pre-identity-migration-*' ! -name '*.incomplete' \
  -printf '%T@ %p\n' | sort -n | tail -1 | cut -d' ' -f2-)
test -n "$BACKUP_DIR"
(cd "$BACKUP_DIR" && sha256sum -c SHA256SUMS)
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT")"
```

Verify the result: every checksum must report `OK`, the completed directory
must not end in `.incomplete`, and no legacy container may run. Stop if the
backup restarted the legacy project or left an unexplained transaction.

## 5. Install the new release

Extract the verified release into a new immutable directory. Install the
backed-up environment with its original ownership and mode. Activate the
relative release link.

```bash
test ! -e "$NEW_RELEASE"
install -d -m 700 "$NEW_RELEASE"
tar -xzf "$RELEASE_DIR/ai-agent-telemetry-backend.tar.gz" -C "$NEW_RELEASE"
install -o root -g root -m 600 "$BACKUP_DIR/backend.env" "$NEW_RELEASE/.env"
ln -s "$RELEASE_TAG" "$NEW_ROOT/.latest.migration"
mv -Tf "$NEW_ROOT/.latest.migration" "$NEW_ROOT/latest"
test "$(readlink "$NEW_ROOT/latest")" = "$RELEASE_TAG"
stat -c '%U:%G %a' "$NEW_RELEASE/.env"
```

Verify the result: `latest` must contain one relative release ID. `stat` must
print `root:root 600` for `.env`.

## 6. Create the five new labeled volumes

Create empty volumes under the new project identity. Keep the legacy volumes
unchanged.

```bash
for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
  actual="${NEW_PROJECT}_${logical}"
  test -z "$(docker volume ls -q --filter "name=^${actual}$")"
  docker volume create \
    --label "com.docker.compose.project=$NEW_PROJECT" \
    --label "com.docker.compose.volume=$logical" \
    "$actual" >/dev/null
done
```

Verify the result:

```bash
for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
  actual="${NEW_PROJECT}_${logical}"
  project_label=$(docker volume inspect \
    -f '{{index .Labels "com.docker.compose.project"}}' "$actual")
  logical_label=$(docker volume inspect \
    -f '{{index .Labels "com.docker.compose.volume"}}' "$actual")
  test "$project_label" = "$NEW_PROJECT"
  test "$logical_label" = "$logical"
done
test "$(docker volume ls -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT" | wc -l)" -eq 5
```

## 7. Restore and normalize-check every volume

Read each logical-to-archive mapping from the backup manifest. Extract it into
the matching new volume. The archive preserves file ownership and modes
because the helper runs as root.

```bash
HELPER_IMAGE=docker.io/library/alpine:3.20@sha256:
HELPER_IMAGE+=d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
while IFS='|' read -r logical legacy_actual archive; do
  actual="${NEW_PROJECT}_${logical}"
  docker run --rm \
    --mount "type=volume,src=$actual,dst=/actual" \
    --mount "type=bind,src=$BACKUP_DIR/volumes,dst=/backup,readonly" \
    "$HELPER_IMAGE" sh -eu -c \
    'cd /actual && tar -xzf "/backup/$1"' sh "$archive"
done < <(sed -n 's/^VOLUME=//p' "$BACKUP_DIR/manifest.txt")
```

Compare normalized metadata and file contents for every archive. The check
covers type, mode, numeric owner, numeric group, size, link target, and file
checksum without depending on the temporary extraction path.

```bash
while IFS='|' read -r logical legacy_actual archive; do
  actual="${NEW_PROJECT}_${logical}"
  docker run --rm \
    --mount "type=volume,src=$actual,dst=/actual,readonly" \
    --mount "type=bind,src=$BACKUP_DIR/volumes,dst=/backup,readonly" \
    "$HELPER_IMAGE" sh -eu -c '
      mkdir /expected
      tar -xzf "/backup/$1" -C /expected
      find /expected -exec stat -c "%n|%F|%a|%u|%g|%s|%N" {} \; |
        sed "s#/expected##" | sort >/tmp/expected.meta
      find /actual -exec stat -c "%n|%F|%a|%u|%g|%s|%N" {} \; |
        sed "s#/actual##" | sort >/tmp/actual.meta
      find /expected -type f -exec sha256sum {} \; |
        sed "s#/expected##" | sort >/tmp/expected.sha
      find /actual -type f -exec sha256sum {} \; |
        sed "s#/actual##" | sort >/tmp/actual.sha
      cmp /tmp/expected.meta /tmp/actual.meta
      cmp /tmp/expected.sha /tmp/actual.sha
    ' sh "$archive"
done < <(sed -n 's/^VOLUME=//p' "$BACKUP_DIR/manifest.txt")
```

Verify the result: every `cmp` must exit successfully. Stop before startup if
metadata or content differs.

## 8. Start and verify the new deployment

Build the new Grafana image, start the new project, and run the shipped strict
health gate. Keep the legacy project stopped throughout this step.

```bash
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT")"
docker compose --project-name "$NEW_PROJECT" \
  --project-directory "$NEW_RELEASE" \
  --env-file "$NEW_RELEASE/.env" -f "$NEW_RELEASE/docker-compose.yml" config --quiet
docker compose --project-name "$NEW_PROJECT" \
  --project-directory "$NEW_RELEASE" \
  --env-file "$NEW_RELEASE/.env" -f "$NEW_RELEASE/docker-compose.yml" up -d --build
TELEMETRY_SOURCE_ONLY=1 source "$NEW_RELEASE/scripts/backup-backend.sh"
PROJECT_NAME=$NEW_PROJECT
BACKEND_ROOT=$NEW_ROOT
BACKUP_ROOT=$BACKUP_ROOT
LOCK_FILE=/run/lock/ai-agent-telemetry-backend-maintenance.lock
COMPOSE_FILE=$NEW_RELEASE/docker-compose.yml
ENV_FILE=$NEW_RELEASE/.env
GENERATED_OVERRIDE_FILE=$NEW_RELEASE/.maintenance-compose.yml
TRANSACTION_FILE=$NEW_ROOT/.maintenance-transaction
TEST_COMMAND_LOG=
strict_health_gate "$NEW_RELEASE"
sleep 60
strict_health_gate "$NEW_RELEASE"
```

Verify the result:

```bash
test "$(docker ps -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT" | wc -l)" -eq 5
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT")"
test "$(docker ps --format '{{.Label "com.docker.compose.project"}}' | \
  sed -n "/^${LEGACY_PROJECT}$/p;/^${NEW_PROJECT}$/p" | sort -u | wc -l)" -eq 1
```

At most one Compose project may run. Do not declare success until both health
gates pass and the second command still reports only the new project.

## 9. Fall back during the migration window

Use this procedure if the new health or stability gate fails. Stop the new
project first. Rebuild the legacy image override from the five `IMAGE=`
manifest records. Start without pulling or building, and verify the image IDs.

The manifest must contain one record for each service in this order:

```text
IMAGE=caddy|<reference>|<sha256-id>
IMAGE=collector|<reference>|<sha256-id>
IMAGE=grafana|<reference>|<sha256-id>
IMAGE=victorialogs|<reference>|<sha256-id>
IMAGE=victoriametrics|<reference>|<sha256-id>
```

```bash
docker compose --project-name "$NEW_PROJECT" \
  --project-directory "$NEW_RELEASE" \
  --env-file "$NEW_RELEASE/.env" -f "$NEW_RELEASE/docker-compose.yml" down
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT")"
{
  printf 'services:\n'
  sed -n 's/^IMAGE=//p' "$BACKUP_DIR/manifest.txt" |
    while IFS='|' read -r service reference expected_id; do
      printf '  %s:\n    image: %s\n' "$service" "$reference"
    done
} > "$LEGACY_RELEASE/.maintenance-compose.yml"
docker compose --project-name "$LEGACY_PROJECT" \
  --project-directory "$LEGACY_RELEASE" \
  --env-file "$LEGACY_RELEASE/.env" -f "$LEGACY_RELEASE/docker-compose.yml" \
  -f "$LEGACY_RELEASE/.maintenance-compose.yml" up -d --no-build --pull never
while IFS='|' read -r service reference expected_id; do
  container=$(docker ps -q \
    --filter "label=com.docker.compose.project=$LEGACY_PROJECT" \
    --filter "label=com.docker.compose.service=$service")
  test -n "$container"
  test "$(docker inspect --format {{.Image}} "$container")" = "$expected_id"
done < <(sed -n 's/^IMAGE=//p' "$BACKUP_DIR/manifest.txt")
PROJECT_NAME=$LEGACY_PROJECT
BACKEND_ROOT=$LEGACY_ROOT
LOCK_FILE=/run/lock/skills-telemetry-backend-maintenance.lock
COMPOSE_FILE=$LEGACY_RELEASE/docker-compose.yml
ENV_FILE=$LEGACY_RELEASE/.env
GENERATED_OVERRIDE_FILE=$LEGACY_RELEASE/.maintenance-compose.yml
TRANSACTION_FILE=$LEGACY_ROOT/.maintenance-transaction
strict_health_gate "$LEGACY_RELEASE"
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT")"
```

Verify the result: all five actual image IDs must match the backup manifest.
The legacy health gate must pass, and no new-project container may run.

## 10. Inspect an interrupted migration

After a disconnected terminal, host reboot, or interrupted command, inspect
state before taking action. Do not infer ownership from resource names alone.

```bash
docker ps -a --filter "label=com.docker.compose.project=$LEGACY_PROJECT"
docker ps -a --filter "label=com.docker.compose.project=$NEW_PROJECT"
docker volume ls --filter "label=com.docker.compose.project=$LEGACY_PROJECT"
docker volume ls --filter "label=com.docker.compose.project=$NEW_PROJECT"
test ! -e "$LEGACY_ROOT/.maintenance-transaction" || \
  sed -n '1,120p' "$LEGACY_ROOT/.maintenance-transaction"
test ! -e "$NEW_ROOT/.maintenance-transaction" || \
  sed -n '1,120p' "$NEW_ROOT/.maintenance-transaction"
test ! -L "$NEW_ROOT/latest" || readlink "$NEW_ROOT/latest"
```

Verify the result: choose the fallback procedure or the next incomplete step.
If both projects run, stop the project started last before any other action.
If neither runs, start only the project whose checks passed.

## 11. Take a fresh backup before a late fallback

After the new deployment accepts traffic, returning to the unchanged legacy
volumes discards newer logical state. Create and verify a fresh new-project
backup before that destructive late fallback.

```bash
backup-backend.sh --target-label pre-late-fallback
LATE_BACKUP=$(find "$BACKUP_ROOT" -maxdepth 1 -type d \
  -name 'pre-pre-late-fallback-*' ! -name '*.incomplete' \
  -printf '%T@ %p\n' | sort -n | tail -1 | cut -d' ' -f2-)
test -n "$LATE_BACKUP"
(cd "$LATE_BACKUP" && sha256sum -c SHA256SUMS)
```

Verify the result: the new project must be healthy after the backup, and every
checksum must report `OK`. Then use step 9. Keep the fresh backup so data
written after the original cutover can be recovered explicitly.

## 12. Remove the legacy deployment later

Keep the legacy source tree, volumes, original migration backup, and fresh
late-fallback backup through the agreed observation period. The manifest's
image identities allow the removed legacy containers to be recreated. After
approval, confirm that only the new project runs and print the exact legacy
resources before removal.

```bash
test "$(docker ps -q \
  --filter "label=com.docker.compose.project=$NEW_PROJECT" | wc -l)" -eq 5
test -z "$(docker ps -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT")"
mapfile -t legacy_volumes < <(docker volume ls -q \
  --filter "label=com.docker.compose.project=$LEGACY_PROJECT")
printf 'legacy volume: %s\n' "${legacy_volumes[@]}"
printf 'legacy root: %s\n' "$LEGACY_ROOT"
```

Remove the legacy deployment later only when the printed list matches the
approved cleanup scope:

```bash
docker compose --project-name "$LEGACY_PROJECT" \
  --project-directory "$LEGACY_RELEASE" \
  --env-file "$LEGACY_RELEASE/.env" \
  -f "$LEGACY_RELEASE/docker-compose.yml" down --remove-orphans
test "${#legacy_volumes[@]}" -eq 5
docker volume rm "${legacy_volumes[@]}"
rm -rf -- "$LEGACY_ROOT"
```

Verify the result: no container or volume may carry the legacy project label.
Backup deletion is separate; this cleanup does not remove either backup.
