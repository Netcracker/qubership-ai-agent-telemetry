#!/usr/bin/env bash
# shellcheck disable=SC2030,SC2031,SC2153 # Tests isolate sourced scripts and inspect globals assigned by them.

set -euo pipefail

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
backup_script=$backend_dir/scripts/backup-backend.sh
update_script=$backend_dir/scripts/update-backend.sh

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

fixture_image_id() {
  printf 'sha256:%s\n' "$(printf '%s' "$1-$2" | sha256sum | cut -d' ' -f1)"
}
export -f fixture_image_id

run_fails() {
  if "$@"; then
    fail "command unexpectedly passed: $*"
  fi
}

assert_fails_with() {
  local expected=$1 output
  shift

  if output=$("$@" 2>&1); then
    fail "command unexpectedly passed: $*"
  fi
  [[ $output == *"$expected"* ]] ||
    fail "command failed without the expected message '$expected': $output"
}

single_completed_backup() {
  local root=$1 candidate
  local -a backup_dirs

  while IFS= read -r candidate; do
    [[ $candidate == *.incomplete ]] || backup_dirs+=("$candidate")
  done < <(compgen -G "$root/pre-*" || true)
  [ "${#backup_dirs[@]}" -eq 1 ] || fail "expected one completed backup in $root"
  [ -d "${backup_dirs[0]}" ] || fail 'backup result is not a directory'
  [[ ${backup_dirs[0]} != *.incomplete ]] || fail 'backup was left incomplete'
  printf '%s\n' "${backup_dirs[0]}"
}

assert_volume_file() {
  local project=$1 logical=$2 path=$3 volume
  local -a volumes

  mapfile -t volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project" \
    --filter "label=com.docker.compose.volume=$logical")
  [ "${#volumes[@]}" -eq 1 ] || fail "expected one restored volume for $logical"
  volume=${volumes[0]}
  docker run --rm --mount "type=volume,src=$volume,dst=/source,readonly" \
    docker.io/library/alpine:3.20 test -f "/source$path" ||
    fail "volume $volume does not contain $path"
}

assert_services_running() {
  local project=$1 root=$2

  docker compose --project-name "$project" --project-directory "$root" --env-file "$root/.env" \
    -f "$root/docker-compose.yml" ps --status running --services |
    sort | cmp - <(printf '%s\n' caddy collector grafana victorialogs victoriametrics | sort) ||
    fail 'backup did not restart every service'
}

assert_services_stopped() {
  local project=$1
  local -a containers

  mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$project")
  [ "${#containers[@]}" -eq 0 ] || fail 'backup intentionally left backend services running'
}

assert_container_image_ids() {
  local project=$1 manifest=$2 service expected_id container
  local -a containers

  while IFS='=' read -r service expected_id; do
    [ -n "$service" ] && [ -n "$expected_id" ] || fail 'image manifest contains an empty service or ID'
    mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$project" \
      --filter "label=com.docker.compose.service=$service")
    [ "${#containers[@]}" -eq 1 ] || fail "expected one running container for $service"
    container=${containers[0]}
    [ "$(docker inspect --format '{{.Image}}' "$container")" = "$expected_id" ] ||
      fail "container image ID differs from the manifest for $service"
  done <"$manifest"
}

assert_no_registry_event_after() {
  local marker=$1 seen=0 line

  while IFS= read -r line; do
    if [ "$line" = "$marker" ]; then
      seen=1
      continue
    fi
    if [ "$seen" -eq 1 ] && { [[ $line == *' pull '* ]] || [[ $line == *' build '* ]]; }; then
      fail "registry event followed $marker: $line"
    fi
  done <"$TELEMETRY_TEST_COMMAND_LOG"
  [ "$seen" -eq 1 ] || fail "command log lacks marker: $marker"
}

assert_task_resources_absent() {
  local project=$1 transaction_id output
  shift

  output=$(docker ps -aq --filter "label=com.docker.compose.project=$project") ||
    fail "cannot audit task containers for project $project"
  [ -z "$output" ] || fail "task cleanup retained containers for project $project"
  output=$(docker volume ls -q --filter "label=com.docker.compose.project=$project") ||
    fail "cannot audit task volumes for project $project"
  [ -z "$output" ] || fail "task cleanup retained volumes for project $project"
  output=$(docker network ls -q --filter "label=com.docker.compose.project=$project") ||
    fail "cannot audit task networks for project $project"
  [ -z "$output" ] || fail "task cleanup retained networks for project $project"
  for transaction_id in "$@"; do
    [ -n "$transaction_id" ] || continue
    output=$(docker ps -aq \
      --filter "label=io.qubership.ai-agent-telemetry.maintenance.transaction=$transaction_id") ||
      fail "cannot audit task helpers for transaction $transaction_id"
    [ -z "$output" ] || fail "task cleanup retained helpers for transaction $transaction_id"
  done
}

cleanup_backup_sandbox() {
  local root=$1 project=$2 restore_project=$3
  local -a sandbox_volumes restore_volumes

  docker compose --project-name "$project" --project-directory "$root/backend/previous" \
    -f "$root/backend/previous/docker-compose.yml" \
    down --remove-orphans >/dev/null 2>&1 || true
  mapfile -t sandbox_volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#sandbox_volumes[@]}" -eq 0 ] || docker volume rm "${sandbox_volumes[@]}" >/dev/null 2>&1 || true
  mapfile -t restore_volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$restore_project")
  [ "${#restore_volumes[@]}" -eq 0 ] || docker volume rm "${restore_volumes[@]}" >/dev/null 2>&1 || true
  rm -rf "$root"
}

assert_backup_helper_names() {
  local command_log=$1 ordinal measure_name archive_name transaction_prefix
  local -a measure_names archive_names

  mapfile -t measure_names < <(sed -n 's/^VOLUME_MEASURE_HELPER=//p' "$command_log")
  mapfile -t archive_names < <(sed -n 's/^VOLUME_ARCHIVE_HELPER=//p' "$command_log")
  [ "${#measure_names[@]}" -eq 5 ] || fail 'backup did not log exactly five volume measurement helper names'
  [ "${#archive_names[@]}" -eq 5 ] || fail 'backup did not log exactly five volume archive helper names'
  for ordinal in 0 1 2 3 4; do
    measure_name=${measure_names[ordinal]}
    archive_name=${archive_names[ordinal]}
    [[ $measure_name =~ ^ai-agent-telemetry-backup-backup-[0-9]{14}-[0-9]+-[0-9]+-measure-$ordinal$ ]] ||
      fail "volume measurement helper has an unexpected name: $measure_name"
    transaction_prefix=${measure_name%-measure-"$ordinal"}
    [ "$archive_name" = "$transaction_prefix-$ordinal" ] ||
      fail "volume archive helper has an unexpected name: $archive_name"
  done
}

restore_backup_into_second_sandbox() {
  local backup_dir=$1 root=$2 mismatch=${3:-0} project source_project docker_dir restore_log real_docker output

  (cd "$backup_dir" && sha256sum -c SHA256SUMS >/dev/null) || fail 'backup checksums did not verify before restore'
  source_project=$(sed -n 's/^PROJECT_NAME=//p' "$backup_dir/manifest.txt")
  [ -n "$source_project" ] || fail 'backup manifest omitted project name'
  project=$source_project
  docker_dir=$root/bin
  restore_log=$root/restore-docker.log
  real_docker=$(command -v docker)
  mkdir -p "$docker_dir" "$root/docker-config"
  cat >"$docker_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$RESTORE_DOCKER_LOG"
if [ "${RESTORE_IMAGE_MISMATCH:-0}" = 1 ] && [ "$1" = inspect ]; then
  case " $* " in
    *' --format {{.Image}} '*) printf 'sha256:%064d\n' 0; exit 0 ;;
  esac
fi
exec "$RESTORE_REAL_DOCKER" "$@"
EOF
  chmod 700 "$docker_dir/docker"
  if ! output=$(PATH="$docker_dir:$PATH" DOCKER_CONFIG="$root/docker-config" \
    RESTORE_DOCKER_LOG="$restore_log" RESTORE_REAL_DOCKER="$real_docker" \
    RESTORE_IMAGE_MISMATCH="$mismatch" TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$root" \
    TELEMETRY_TEST_BACKUP_ROOT="$(dirname -- "$backup_dir")" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 TELEMETRY_TEST_HEALTH_ATTEMPTS=5 \
    TELEMETRY_TEST_STABILITY_SECONDS=0 "$backup_dir/restore-backend.sh" "$backup_dir" "$root/release" 2>&1); then
    [ "$mismatch" -eq 1 ] && [[ $output == *'image identity differs from the prepared image'* ]] ||
      fail "restore helper failed unexpectedly: $output"
    printf '%s\n' "$project"
    return 0
  fi
  [ "$mismatch" -eq 0 ] || fail 'restore helper accepted an injected container image mismatch'
  grep -F ' pull --ignore-buildable' "$restore_log" >/dev/null ||
    fail 'restore helper did not skip the buildable Grafana image during registry pulls'
  grep -F ' build --pull grafana' "$restore_log" >/dev/null ||
    fail 'restore helper did not build the pinned per-release Grafana image'
  if grep -Eq ' compose .* pull[[:space:]]*$' "$restore_log"; then
    fail 'restore helper performed an unrestricted Compose pull'
  fi
  [ -L "$root/latest" ] && [ "$(readlink -- "$root/latest")" = release ] ||
    fail 'restore helper did not activate the restored immutable release'
  printf '%s\n' "$project"
}

cleanup_restore_project() {
  local root=$1 project=$2
  local -a containers volumes

  mapfile -t containers < <(docker ps -aq --filter "label=com.docker.compose.project=$project")
  [ "${#containers[@]}" -eq 0 ] || docker rm -f "${containers[@]}" >/dev/null
  mapfile -t volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#volumes[@]}" -eq 0 ] || docker volume rm "${volumes[@]}" >/dev/null
  rm -rf "$root"
}

cleanup_restore_portability_sandbox() {
  local sandbox=$1 project=$2
  local -a containers volumes networks

  mapfile -t containers < <(docker ps -aq --filter "label=com.docker.compose.project=$project")
  [ "${#containers[@]}" -eq 0 ] || docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
  mapfile -t volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#volumes[@]}" -eq 0 ] || docker volume rm "${volumes[@]}" >/dev/null 2>&1 || true
  mapfile -t networks < <(docker network ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#networks[@]}" -eq 0 ] || docker network rm "${networks[@]}" >/dev/null 2>&1 || true
  rm -rf "$sandbox"
}

run_restore_portability_suite() {
  local sandbox backup_dir source_dir release_dir project registry_reference registry_id grafana_reference
  local source_grafana_id logical volume_source container running_id rebuilt_grafana_id service
  local -a containers

  sandbox=$(mktemp -d /tmp/telemetry-restore-portability.XXXXXX)
  project="telemetry_restore_portability_$RANDOM$RANDOM"
  trap 'cleanup_restore_portability_sandbox "${sandbox:-}" "${project:-}"' EXIT HUP INT TERM
  backup_dir=$sandbox/backup
  source_dir=$sandbox/source
  release_dir=$sandbox/backend/restored
  mkdir -p "$backup_dir/volumes" "$source_dir" "$sandbox/volume-source"
  cp "$backend_dir/.env.example" "$backup_dir/backend.env"
  cat >"$source_dir/docker-compose.yml" <<EOF
services:
  caddy: {image: alpine:3.20, command: ["sh", "-c", "while :; do sleep 3600; done"]}
  collector: {image: alpine:3.20, command: ["sh", "-c", "while :; do sleep 3600; done"]}
  grafana:
    build: {context: ., dockerfile: Dockerfile}
    command: ["sh", "-c", "while :; do sleep 3600; done"]
  victorialogs: {image: alpine:3.20, command: ["sh", "-c", "while :; do sleep 3600; done"]}
  victoriametrics: {image: alpine:3.20, command: ["sh", "-c", "while :; do sleep 3600; done"]}
EOF
  printf '%s\n' 'FROM alpine:3.20' >"$source_dir/Dockerfile"
  printf '%s\n' 'services: {}' >"$source_dir/.maintenance-compose.yml"
  tar -C "$source_dir" -czf "$backup_dir/telemetry-backend-source.tar.gz" .
  if tar -tzf "$backup_dir/telemetry-backend-source.tar.gz" | grep -Eq '(^|/)scripts/backup-backend[.]sh$'; then
    fail 'portability source archive unexpectedly contains the maintenance library'
  fi

  docker pull docker.io/library/alpine:3.20 >/dev/null
  registry_reference=$(docker image inspect --format '{{index .RepoDigests 0}}' docker.io/library/alpine:3.20)
  registry_id=$(docker image inspect --format '{{.Id}}' "$registry_reference")
  grafana_reference=${project}-grafana:restored
  source_grafana_id=sha256:0000000000000000000000000000000000000000000000000000000000000000
  {
    printf 'PROJECT_NAME=%s\n' "$project"
    printf 'IMAGE=caddy|%s|%s\n' "$registry_reference" "$registry_id"
    printf 'IMAGE=collector|%s|%s\n' "$registry_reference" "$registry_id"
    printf 'IMAGE=grafana|%s|%s\n' "$grafana_reference" "$source_grafana_id"
    printf 'IMAGE=victorialogs|%s|%s\n' "$registry_reference" "$registry_id"
    printf 'IMAGE=victoriametrics|%s|%s\n' "$registry_reference" "$registry_id"
    for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
      printf 'VOLUME=%s|%s_%s|%s_%s.tar.gz\n' "$logical" "$project" "$logical" "$project" "$logical"
    done
  } >"$backup_dir/manifest.txt"
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    volume_source=$sandbox/volume-source/$logical
    mkdir -p "$volume_source"
    printf '%s' "$logical" >"$volume_source/sentinel"
    tar -C "$volume_source" -czf "$backup_dir/volumes/${project}_${logical}.tar.gz" .
  done
  (
    # shellcheck disable=SC1090,SC1091 # Exercise the generated helper from the production maintenance library.
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    write_restore_helper "$backup_dir"
    write_restore_instructions "$backup_dir"
    bundle_maintenance_library "$backup_dir"
  )
  (
    cd "$backup_dir"
    sha256sum backend.env backup-backend.sh telemetry-backend-source.tar.gz manifest.txt RESTORE.md restore-backend.sh \
      volumes/*.tar.gz >SHA256SUMS
    sha256sum -c SHA256SUMS >/dev/null
  )
  [ "$(stat -c '%a' "$backup_dir/backup-backend.sh")" = 700 ] ||
    fail 'portability backup maintenance library is not executable mode 700'

  TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$sandbox" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 TELEMETRY_TEST_HEALTH_ATTEMPTS=5 TELEMETRY_TEST_STABILITY_SECONDS=0 \
    "$backup_dir/restore-backend.sh" "$backup_dir" "$release_dir" >/dev/null
  for service in caddy collector victorialogs victoriametrics; do
    mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$project" \
      --filter "label=com.docker.compose.service=$service")
    [ "${#containers[@]}" -eq 1 ] || fail "portability restore did not start one $service container"
    running_id=$(docker inspect --format '{{.Image}}' "${containers[0]}")
    [ "$running_id" = "$registry_id" ] || fail "portability restore changed registry image identity: $service"
  done
  mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$project" \
    --filter 'label=com.docker.compose.service=grafana')
  [ "${#containers[@]}" -eq 1 ] || fail 'portability restore did not start one Grafana container'
  running_id=$(docker inspect --format '{{.Image}}' "${containers[0]}")
  rebuilt_grafana_id=$(docker image inspect --format '{{.Id}}' "$grafana_reference")
  [ "$running_id" = "$rebuilt_grafana_id" ] && [ "$running_id" != "$source_grafana_id" ] ||
    fail 'portability restore did not run and verify the rebuilt Grafana image'

  cleanup_restore_portability_sandbox "$sandbox" "$project"
  trap - EXIT HUP INT TERM
  printf '%s\n' 'PASS: maintenance cross-host restore portability contract'
}

run_backup_suite() {
  local sandbox backup_root restore_root mismatch_root functional_root project restore_project backup_dir event_log
  local unsafe_event_log output functional_backup functional_container functional_grafana_id rebuilt_grafana_id
  local down_lie_event_log docker_wrapper_dir real_docker
  local restore_instructions logical service image_record image_reference image_id extra manifest_checksum_count
  local invalid_backup invalid_release invalid_case original_image_record previous_caddy_id bundled_checksum_count
  local grafana_reference source_grafana_id fake_grafana_id
  local -a source_volumes manifest_images manifest_services

  export TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 TELEMETRY_TEST_STABILITY_SECONDS=0
  sandbox=$(mktemp -d /tmp/telemetry-backup.XXXXXX)
  trap 'cleanup_backup_sandbox "${sandbox:-}" "${project:-}" "${restore_project:-}"' EXIT HUP INT TERM
  backup_root=$sandbox/backups
  restore_root=$sandbox/restore
  project="telemetry_backup_$RANDOM$RANDOM"
  event_log=$sandbox/events.log
  export BUILDX_NO_DEFAULT_ATTESTATIONS=1 DOCKER_CONFIG=$sandbox/docker-config
  mkdir -p "$sandbox/backend/previous" "$backup_root" "$DOCKER_CONFIG"
  cp "$backend_dir/.env.example" "$sandbox/backend/previous/.env"
  cat >"$sandbox/backend/previous/docker-compose.yml" <<EOF
services:
  caddy:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [caddy-config:/caddy-config, caddy-data:/caddy-data]
  collector:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
  grafana:
    image: ${project}-grafana:previous
    build:
      context: .
      dockerfile: Dockerfile
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [grafana-data:/grafana-data]
  victorialogs:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vlogs-data:/vlogs-data]
  victoriametrics:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vmetrics-data:/vmetrics-data]
volumes:
  caddy-config: {}
  caddy-data: {}
  grafana-data: {}
  vlogs-data: {}
  vmetrics-data: {}
EOF
  printf '%s\n' 'FROM alpine:3.20' >"$sandbox/backend/previous/Dockerfile"
  install -D -m 755 "$backup_script" "$sandbox/backend/previous/scripts/backup-backend.sh"
  ln -s previous "$sandbox/backend/latest"
  docker compose --project-name "$project" --project-directory "$sandbox/backend/previous" \
    --env-file "$sandbox/backend/previous/.env" -f "$sandbox/backend/previous/docker-compose.yml" up -d --build
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    docker run --rm --mount "type=volume,src=${project}_${logical},dst=/data" docker.io/library/alpine:3.20 \
      sh -eu -c "mkdir -p /data/sentinel && printf '%s' '$logical' > /data/sentinel/$logical"
  done
  rm -f "$sandbox/backend/previous/scripts/backup-backend.sh"

  TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$event_log" \
    "$backup_script" --target-label contract
  assert_backup_helper_names "$event_log"
  backup_dir=$(single_completed_backup "$backup_root")
  [ "$(sed -n 's/^SOURCE_RELEASE=//p' "$backup_dir/manifest.txt")" = "$sandbox/backend/previous" ] ||
    fail 'backup manifest did not identify the active release as its source'
  cmp -s "$sandbox/backend/previous/.env" "$backup_dir/backend.env" ||
    fail 'backup environment did not come from the active release'
  tar -xOf "$backup_dir/telemetry-backend-source.tar.gz" ./docker-compose.yml |
    cmp - "$sandbox/backend/previous/docker-compose.yml" ||
    fail 'backup source archive did not come from the active release'
  mapfile -t manifest_images < <(sed -n 's/^IMAGE=//p' "$backup_dir/manifest.txt")
  [ "${#manifest_images[@]}" -eq 5 ] || fail 'backup manifest must contain exactly five image records'
  manifest_services=()
  for image_record in "${manifest_images[@]}"; do
    IFS='|' read -r service image_reference image_id extra <<<"$image_record"
    [ -n "$service" ] && [ -n "$image_reference" ] && [ -n "$image_id" ] && [ -z "${extra:-}" ] ||
      fail "backup manifest contains an invalid image record: $image_record"
    [[ $image_id =~ ^sha256:[0-9a-f]{64}$ ]] ||
      fail "backup manifest contains a noncanonical image ID for $service"
    if [ "$service" = grafana ]; then
      [ "$image_reference" = "${project}-grafana:previous" ] ||
        fail 'backup manifest contains an unexpected Grafana image reference'
      grafana_reference=$image_reference
      source_grafana_id=$image_id
    else
      [[ $image_reference =~ @sha256:[0-9a-f]{64}$ ]] ||
        fail "backup manifest contains a mutable registry reference for $service"
    fi
    manifest_services+=("$service")
    assert_event_before "IMAGE_MANIFEST=$service" TRANSACTION_CLEAR "$event_log"
  done
  [ "$(printf '%s\n' "${manifest_services[@]}")" = \
    $'caddy\ncollector\ngrafana\nvictorialogs\nvictoriametrics' ] ||
    fail 'backup manifest image records must be sorted by service'
  [ "$(printf '%s\n' "${manifest_services[@]}" | sort -u | tr '\n' ' ')" = \
    'caddy collector grafana victorialogs victoriametrics ' ] ||
    fail 'backup manifest image records must identify the five unique backend services'
  [ "$(docker image inspect --format '{{.Id}}' "$grafana_reference")" = "$source_grafana_id" ] ||
    fail 'backup manifest did not preserve the exact source-host Grafana image ID'
  manifest_checksum_count=$(sed -n '/[[:space:]]manifest[.]txt$/p' "$backup_dir/SHA256SUMS" | wc -l)
  [ "$manifest_checksum_count" -eq 1 ] || fail 'SHA256SUMS must contain manifest.txt exactly once'
  bundled_checksum_count=$(sed -n '/[[:space:]]backup-backend[.]sh$/p' "$backup_dir/SHA256SUMS" | wc -l)
  [ "$bundled_checksum_count" -eq 1 ] || fail 'SHA256SUMS must cover the bundled maintenance library exactly once'
  [ -f "$backup_dir/backup-backend.sh" ] && [ ! -L "$backup_dir/backup-backend.sh" ] &&
    [ "$(stat -c '%a' "$backup_dir/backup-backend.sh")" = 700 ] ||
    fail 'backup does not contain a safe executable maintenance library'
  cmp -s -- "$backup_script" "$backup_dir/backup-backend.sh" ||
    fail 'backup maintenance library differs from the executing library'
  if tar -tzf "$backup_dir/telemetry-backend-source.tar.gz" | grep -Eq '(^|/)scripts/backup-backend[.]sh$'; then
    fail 'legacy source fixture unexpectedly contains the maintenance library'
  fi
  original_image_record=${manifest_images[0]}
  for invalid_case in duplicate mutable missing-id; do
    invalid_backup=$sandbox/invalid-$invalid_case
    invalid_release=$sandbox/restore-invalid-$invalid_case/release
    cp -a "$backup_dir" "$invalid_backup"
    case "$invalid_case" in
      duplicate)
        printf 'IMAGE=%s\n' "$original_image_record" >>"$invalid_backup/manifest.txt"
        ;;
      mutable)
        sed -i "/^IMAGE=caddy|/c\\IMAGE=caddy|registry.example/caddy:latest|${original_image_record##*|}" \
          "$invalid_backup/manifest.txt"
        ;;
      missing-id)
        sed -i "/^IMAGE=caddy|/c\\IMAGE=${original_image_record%|*}|" \
          "$invalid_backup/manifest.txt"
        ;;
    esac
    (
      cd "$invalid_backup"
      sha256sum backend.env backup-backend.sh telemetry-backend-source.tar.gz manifest.txt RESTORE.md restore-backend.sh \
        volumes/*.tar.gz >SHA256SUMS
    )
    assert_fails_with 'manifest image records are invalid' \
      "$invalid_backup/restore-backend.sh" "$invalid_backup" "$invalid_release"
  done
  invalid_backup=$sandbox/invalid-checksum
  cp -a "$backup_dir" "$invalid_backup"
  printf 'IMAGE=%s\n' "$original_image_record" >>"$invalid_backup/manifest.txt"
  assert_fails_with 'FAILED' "$invalid_backup/restore-backend.sh" "$invalid_backup" \
    "$sandbox/restore-invalid-checksum/release"
  unsafe_event_log=$sandbox/unsafe-events.log
  for unsafe_link in missing absolute multi-component; do
    rm -f "$sandbox/backend/latest"
    case "$unsafe_link" in
      missing) ;;
      absolute) ln -s "$sandbox/backend/previous" "$sandbox/backend/latest" ;;
      multi-component) ln -s ./previous "$sandbox/backend/latest" ;;
    esac
    : >"$unsafe_event_log"
    run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$unsafe_event_log" \
      "$backup_script" --target-label "unsafe-$unsafe_link"
    grep -Fx COMPOSE_DOWN "$unsafe_event_log" >/dev/null && fail "$unsafe_link latest link caused downtime"
    [ ! -f "$sandbox/backend/.maintenance-transaction" ] || fail "$unsafe_link latest link created a transaction"
  done
  rm -f "$sandbox/backend/latest"
  ln -s previous "$sandbox/backend/latest"
  (cd "$backup_dir" && sha256sum -c SHA256SUMS >/dev/null) || fail 'completed backup checksum verification failed'
  [ "$(stat -c '%a' "$backup_dir")" = 700 ] || fail 'completed backup directory is not mode 700'
  [ "$(stat -c '%a' "$backup_dir/backend.env")" = 600 ] || fail 'backup environment is not mode 600'
  [ "$(stat -c '%a' "$backup_dir/backup-backend.sh")" = 700 ] || fail 'backup maintenance library is not mode 700'
  [ "$(stat -c '%a' "$backup_dir/restore-backend.sh")" = 700 ] || fail 'restore helper is not mode 700'
  if tar -tzf "$backup_dir/telemetry-backend-source.tar.gz" | grep -Fx '.env'; then
    fail 'source archive contains .env'
  fi
  # shellcheck disable=SC2016 # These are literal generated-script contracts.
  grep -F './restore-backend.sh "$BACKUP_DIR" "$RELEASE_DIR"' "$backup_dir/RESTORE.md" >/dev/null ||
    fail 'RESTORE.md does not invoke the checked executable restore helper'
  restore_instructions=$backup_dir/restore-backend.sh
  # shellcheck disable=SC2016 # These are literal generated-script contracts.
  for restore_contract in \
    'restore_compose pull --ignore-buildable' \
    'restore_compose build --pull grafana' \
    '--project-directory "$RELEASE_DIR"' \
    'docker volume inspect "$actual"' \
    'mv -Tf -- "$temporary_link" "$BACKEND_ROOT/latest"' \
    'TELEMETRY_SOURCE_ONLY=1 source "$BACKUP_DIR/backup-backend.sh"' \
    'timeout --foreground --signal=TERM 120' \
    'strict_health_gate "$RELEASE_DIR"' \
    'cleanup_transaction_helpers "$health_transaction_id"'; do
    grep -F -- "$restore_contract" "$restore_instructions" >/dev/null ||
      fail "restore helper is missing clean-host contract: $restore_contract"
  done
  if grep -Eq 'restore_compose pull[[:space:]]*$' "$restore_instructions"; then
    fail 'restore helper performs an unrestricted Compose pull'
  fi
  for dashboard_uid in ai-agent-health ai-agent-telemetry-adoption native-agent-metrics-overview codex-native-metrics; do
    grep -F "$dashboard_uid" "$backup_script" >/dev/null ||
      fail "strict health helper does not verify dashboard: $dashboard_uid"
  done
  [ "$(grep -n '^VERIFY_SOURCE_ARCHIVE$' "$event_log" | cut -d: -f1)" -lt \
    "$(grep -n '^COMPOSE_DOWN$' "$event_log" | head -n 1 | cut -d: -f1)" ] ||
    fail 'source archive verification did not precede compose down'
  [ "$(grep -n '^PRELIMINARY_CHECKSUMS$' "$event_log" | cut -d: -f1)" -lt \
    "$(grep -n '^COMPOSE_DOWN$' "$event_log" | head -n 1 | cut -d: -f1)" ] ||
    fail 'preliminary checksums did not precede compose down'

  : >"$unsafe_event_log"
  assert_fails_with '--leave-stopped is available only for an updater handoff' env \
    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$unsafe_event_log" \
    "$backup_script" --target-label unsafe-leave-stopped --leave-stopped
  [ ! -s "$unsafe_event_log" ] || fail 'unsafe standalone --leave-stopped reached Docker-backed backup work'

  down_lie_event_log=$sandbox/down-lie-events.log
  docker_wrapper_dir=$sandbox/down-lie-bin
  real_docker=$(command -v docker)
  mkdir -p "$docker_wrapper_dir"
  cat >"$docker_wrapper_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = compose ]; then
  prefix=()
  for argument in "$@"; do
    if [ "$argument" = down ]; then
      "$TELEMETRY_REAL_DOCKER" "${prefix[@]}" stop collector grafana victorialogs victoriametrics >/dev/null
      "$TELEMETRY_REAL_DOCKER" "${prefix[@]}" rm -f collector grafana victorialogs victoriametrics >/dev/null
      exit 0
    fi
    prefix+=("$argument")
  done
fi
exec "$TELEMETRY_REAL_DOCKER" "$@"
EOF
  chmod 700 "$docker_wrapper_dir/docker"
  run_fails env PATH="$docker_wrapper_dir:$PATH" TELEMETRY_REAL_DOCKER="$real_docker" \
    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$down_lie_event_log" \
    "$backup_script" --target-label incomplete-compose-down
  if grep -F 'VOLUME_ARCHIVE=' "$down_lie_event_log" >/dev/null; then
    fail 'backup archived a volume after Compose left a declared service container behind'
  fi
  assert_services_running "$project" "$sandbox/backend/previous"
  [ ! -f "$sandbox/backend/.maintenance-transaction" ] ||
    fail 'incomplete Compose shutdown retained its durable transaction after recovery'

  (
    # shellcheck disable=SC1090,SC2030,SC2031
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend"
    export TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_FORCE_UNHEALTHY=1
    maintenance_init
    run_fails strict_health_gate "$sandbox/backend/previous"
    unset TELEMETRY_TEST_FORCE_UNHEALTHY
    strict_health_gate "$sandbox/backend/previous"
  ) || fail 'health gate did not reject and then recover an unhealthy service'

  (
    # shellcheck disable=SC1090,SC2030,SC2031
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend"
    export TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$event_log"
    export TELEMETRY_TEST_LARGE_BACKUP_BYTES=1
    mkdir -p "$sandbox/backend/previous-release" "$sandbox/backend/next-release"
    cp "$sandbox/backend/previous/docker-compose.yml" "$sandbox/backend/previous/.env" \
      "$sandbox/backend/previous-release/"
    cp "$sandbox/backend/previous/docker-compose.yml" "$sandbox/backend/previous/.env" \
      "$sandbox/backend/next-release/"
    maintenance_init
    entries=('FORMAT_VERSION=1' 'TRANSACTION_ID=updater-transaction' 'OPERATION=update' \
      'PHASE=backup-offline' 'TARGET_RELEASE=next-release' 'PREVIOUS_RELEASE=previous-release' 'BACKUP_PATH=')
    for service in caddy collector grafana victorialogs victoriametrics; do
      container=$(docker ps -q --filter "label=com.docker.compose.project=$project" \
        --filter "label=com.docker.compose.service=$service")
      image_id=$(docker inspect --format '{{.Image}}' "$container")
      [ "$service" != caddy ] || previous_caddy_id=$image_id
      entries+=("PREVIOUS_IMAGE_${service^^}=$image_id")
      entries+=("TARGET_IMAGE_${service^^}=$(fixture_image_id next-release "$service")")
    done
    write_transaction "${entries[@]}"
    exec {handoff_fd}>"$LOCK_FILE"
    flock -n "$handoff_fd"
    export TELEMETRY_UPDATE_LOCK_HELD=1 TELEMETRY_UPDATE_LOCK_FD=$handoff_fd
    export TELEMETRY_UPDATE_TRANSACTION_ID=updater-transaction
    export TELEMETRY_UPDATE_PREVIOUS_RELEASE=previous-release
    backup_main --target-label handoff --leave-stopped --allow-large-backup
    [ "$(read_transaction TRANSACTION_ID)" = updater-transaction ] || exit 1
    [ "$(read_transaction PHASE)" = activation-prepared ] || exit 1
    [ "$(read_transaction PREVIOUS_IMAGE_CADDY)" = "$previous_caddy_id" ] || exit 1
    exec {handoff_fd}>&-
  ) || fail 'validated update handoff did not preserve its transaction'
  docker compose --project-name "$project" --project-directory "$sandbox/backend/previous" \
    --env-file "$sandbox/backend/previous/.env" -f "$sandbox/backend/previous/docker-compose.yml" \
    up -d --no-build --pull never
  (
    # shellcheck disable=SC1090,SC2030,SC2031
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend"
    export TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock"
    maintenance_init
    clear_transaction
    write_transaction 'FORMAT_VERSION=1' 'TRANSACTION_ID=bad-transaction' 'OPERATION=update' \
      'PHASE=invalid' 'TARGET_RELEASE=next-release' 'PREVIOUS_RELEASE=previous-release'
    run_fails backup_main --target-label invalid-handoff --leave-stopped
    [ "$(read_transaction TRANSACTION_ID)" = bad-transaction ]
    clear_transaction
  ) || fail 'invalid update handoff was accepted or changed its transaction'

  rm -f "$sandbox/backend/previous/.env"
  if output=$(TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    "$backup_script" --target-label missing-env 2>&1); then
    fail 'backup accepted a missing environment file'
  fi
  [[ $output == *'environment file is missing'* ]] || fail 'missing environment file failed for the wrong reason'
  cp "$backend_dir/.env.example" "$sandbox/backend/previous/.env"

  docker volume create --label "com.docker.compose.project=$project" \
    --label 'com.docker.compose.volume=grafana-data' "${project}_grafana-data-duplicate" >/dev/null
  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    "$backup_script" --target-label ambiguous
  docker volume rm "${project}_grafana-data-duplicate" >/dev/null

  cp "$backup_dir/SHA256SUMS" "$sandbox/SHA256SUMS.original"
  printf 'bad  backend.env\n' >"$backup_dir/SHA256SUMS"
  # shellcheck disable=SC2016 # The child shell expands its positional parameter.
  run_fails bash -c 'cd "$1" && sha256sum -c SHA256SUMS' _ "$backup_dir"
  cp "$sandbox/SHA256SUMS.original" "$backup_dir/SHA256SUMS"

  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_FAIL_BACKUP_HELPER=1 "$backup_script" --target-label helper-failure
  assert_services_running "$project" "$sandbox/backend/previous"
  compgen -G "$backup_root/pre-helper-failure-*.incomplete" >/dev/null ||
    fail 'failed helper did not preserve an incomplete backup'

  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_CORRUPT_BACKUP_CHECKSUM=1 "$backup_script" --target-label checksum-failure
  assert_services_running "$project" "$sandbox/backend/previous"
  compgen -G "$backup_root/pre-checksum-failure-*.incomplete" >/dev/null ||
    fail 'failed checksum verification did not preserve an incomplete backup'

  for failure_control in TELEMETRY_TEST_FAIL_HELPER_CLEANUP TELEMETRY_TEST_FAIL_HELPER_ASSERTION; do
    if env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" "$failure_control=1" \
      "$backup_script" --target-label "${failure_control,,}"; then
      fail "$failure_control did not fail backup_main"
    fi
    [ "$(sed -n 's/^PHASE=//p' "$sandbox/backend/.maintenance-transaction")" = backup-offline ] ||
      fail "$failure_control advanced the transaction to activation-prepared"
    # shellcheck disable=SC2016 # The child shell receives the script path as $0.
    env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" bash -c \
      'TELEMETRY_SOURCE_ONLY=1 source "$0"; maintenance_init; acquire_maintenance_lock; recover_transaction' \
      "$backup_script"
  done

  if output=$(TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_LARGE_BACKUP_BYTES=1 "$backup_script" --target-label too-large 2>&1); then
    fail 'large backup proceeded without confirmation'
  fi
  [[ $output == *'--allow-large-backup'* ]] || fail 'large backup failed for the wrong reason'
  assert_services_running "$project" "$sandbox/backend/previous"
  docker compose --project-name "$project" --project-directory "$sandbox/backend/previous" \
    --env-file "$sandbox/backend/previous/.env" -f "$sandbox/backend/previous/docker-compose.yml" \
    down --remove-orphans
  mapfile -t source_volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#source_volumes[@]}" -eq 5 ] || fail 'source project did not expose five exact volumes for restore'
  docker volume rm "${source_volumes[@]}" >/dev/null
  mismatch_root=$sandbox/restore-mismatch
  restore_project=$(restore_backup_into_second_sandbox "$backup_dir" "$mismatch_root" 1)
  cleanup_restore_project "$mismatch_root" "$restore_project"
  printf '%s\n' 'TRACE: fallback image mismatch rejected'

  functional_root=$sandbox/restore-functional-grafana
  functional_backup=$sandbox/functional-grafana-backup
  fake_grafana_id=sha256:0000000000000000000000000000000000000000000000000000000000000000
  cp -a "$backup_dir" "$functional_backup"
  sed -i "/^IMAGE=grafana|/c\\IMAGE=grafana|$grafana_reference|$fake_grafana_id" "$functional_backup/manifest.txt"
  (
    cd "$functional_backup"
    sha256sum backend.env backup-backend.sh telemetry-backend-source.tar.gz manifest.txt RESTORE.md restore-backend.sh \
      volumes/*.tar.gz >SHA256SUMS
  )
  restore_project=$(restore_backup_into_second_sandbox "$functional_backup" "$functional_root")
  functional_container=$(docker ps -q --filter "label=com.docker.compose.project=$restore_project" \
    --filter 'label=com.docker.compose.service=grafana')
  [ -n "$functional_container" ] || fail 'functional Grafana restore did not start one container'
  functional_grafana_id=$(docker inspect --format '{{.Image}}' "$functional_container")
  rebuilt_grafana_id=$(docker image inspect --format '{{.Id}}' "$grafana_reference")
  [ "$functional_grafana_id" = "$rebuilt_grafana_id" ] && [ "$functional_grafana_id" != "$fake_grafana_id" ] ||
    fail 'cross-host restore did not run the Grafana image rebuilt from archived context'
  cleanup_restore_project "$functional_root" "$restore_project"
  printf '%s\n' 'TRACE: fallback accepted a functional Grafana rebuild with a different source-host image ID'

  restore_project=$(restore_backup_into_second_sandbox "$backup_dir" "$restore_root")
  [ "$restore_project" = "$project" ] || fail 'restore changed the manifest project identity'
  for image_record in "${manifest_images[@]}"; do
    IFS='|' read -r service image_reference image_id extra <<<"$image_record"
    [ "$service" = grafana ] && continue
    printf '%s=%s\n' "$service" "$image_id"
  done >"$sandbox/restored-images"
  assert_container_image_ids "$restore_project" "$sandbox/restored-images"
  functional_container=$(docker ps -q --filter "label=com.docker.compose.project=$restore_project" \
    --filter 'label=com.docker.compose.service=grafana')
  functional_grafana_id=$(docker inspect --format '{{.Image}}' "$functional_container")
  rebuilt_grafana_id=$(docker image inspect --format '{{.Id}}' "$grafana_reference")
  [ "$functional_grafana_id" = "$rebuilt_grafana_id" ] ||
    fail 'cross-host restore did not verify the rebuilt Grafana image ID'
  printf '%s\n' 'TRACE: fallback restored four exact registry image IDs and rebuilt Grafana from archived context'
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    assert_volume_file "$restore_project" "$logical" "/sentinel/$logical"
  done
  docker compose --project-name "$project" --project-directory "$sandbox/backend/previous" \
    --env-file "$sandbox/backend/previous/.env" -f "$sandbox/backend/previous/docker-compose.yml" \
    up -d --no-build --pull never
  assert_services_running "$project" "$sandbox/backend/previous"
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    assert_event_before "VOLUME_MEASURE=${project}_${logical}" COMPOSE_DOWN "$event_log"
    assert_event_before COMPOSE_STOPPED "VOLUME_ARCHIVE=${project}_${logical}" "$event_log"
    assert_event_before "VOLUME_ARCHIVE=${project}_${logical}" TRANSACTION_CLEAR "$event_log"
  done
  cleanup_backup_sandbox "$sandbox" "$project" "$restore_project"
  trap - EXIT HUP INT TERM
  printf 'PASS: maintenance backup contract\n'
}

run_cli_suite() {
  local function_name sandbox active_before first_pid first_status lock_output missing_backup_root pin_output production_log
  local fixture_registry_digest
  local resolution_sandbox resolution_root resolution_release

  [ -x "$backup_script" ] || fail 'backup-backend.sh does not exist or is not executable'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    [ "$PROJECT_NAME_DEFAULT" = ai-agent-telemetry-backend ]
    [ "$BACKEND_ROOT_DEFAULT" = /opt/ai-agent-telemetry-backend ]
    [ "$BACKUP_ROOT_DEFAULT" = /opt/ai-agent-telemetry-backups ]
    [ "$LOCK_FILE_DEFAULT" = /run/lock/ai-agent-telemetry-backend-maintenance.lock ]
  ) || fail 'ordinary maintenance identity defaults do not use ai-agent-telemetry-backend'

  resolution_sandbox=$(mktemp -d /tmp/telemetry-active-resolution.XXXXXX)
  trap 'rm -rf "${resolution_sandbox:-}" "${sandbox:-}"' EXIT HUP INT TERM
  resolution_root=$resolution_sandbox/backend
  resolution_release=$resolution_root/release-current
  mkdir -p "$resolution_release"
  : >"$resolution_release/docker-compose.yml"
  : >"$resolution_release/.env"
  ln -s release-current "$resolution_root/latest"
  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    [ "$(resolve_active_backend_dir "$resolution_root")" = "$resolution_release" ] || exit 1
    rm -f "$resolution_root/latest"
    ln -s release-current/ "$resolution_root/latest"
    [ "$(resolve_active_backend_dir "$resolution_root")" = "$resolution_release" ] || exit 1
    for unsafe_target in "$resolution_release" ./release-current ../release-current nested/release-current release-current//; do
      rm -f "$resolution_root/latest"
      ln -s "$unsafe_target" "$resolution_root/latest"
      run_fails resolve_active_backend_dir "$resolution_root"
    done
    mkdir -p "$resolution_root/release-target"
    ln -s release-target "$resolution_root/release-link"
    rm -f "$resolution_root/latest"
    ln -s release-link "$resolution_root/latest"
    run_fails resolve_active_backend_dir "$resolution_root"
  ) || fail 'active backend resolution accepted an unsafe latest link or missed its immutable release'

  (
    # The test resolves the sibling script from its own location.
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    for function_name in maintenance_init resolve_active_backend_dir compose write_transaction read_transaction \
      clear_transaction \
      cleanup_transaction_helpers pin_active_images metric_sample_is_fresh strict_health_gate recover_transaction \
      backup_main; do
      declare -F "$function_name" >/dev/null || exit 1
    done
  ) || fail 'backup-backend.sh must expose the shared maintenance functions when sourced'

  sandbox=$(mktemp -d /tmp/telemetry-maintenance.XXXXXX)
  trap 'rm -rf "${resolution_sandbox:-}" "${sandbox:-}"' EXIT HUP INT TERM
  mkdir -p "$sandbox/backend/active-release" "$sandbox/backups"
  : >"$sandbox/backend/active-release/docker-compose.yml"
  printf '%s\n' 'ACTIVE_RELEASE_ENV=1' >"$sandbox/backend/active-release/.env"
  ln -s active-release "$sandbox/backend/latest"

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    maintenance_init || exit 1
    [ "$BACKEND_ROOT" = "$sandbox/backend" ] || exit 1
    [ "$CURRENT_BACKEND_DIR" = "$sandbox/backend/active-release" ] || exit 1
    [ "$COMPOSE_FILE" = "$sandbox/backend/active-release/docker-compose.yml" ] || exit 1
    [ "$ENV_FILE" = "$sandbox/backend/active-release/.env" ] || exit 1
    [ "$TRANSACTION_FILE" = "$sandbox/backend/.maintenance-transaction" ] || exit 1
  ) || fail 'ordinary maintenance did not resolve configuration from the active release'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    export TELEMETRY_TEST_BACKUP_ROOT=$sandbox/backups TELEMETRY_TEST_PROJECT_NAME=recovery_probe_test
    maintenance_init
    entries=('FORMAT_VERSION=1' 'TRANSACTION_ID=recovery-probe' 'OPERATION=backup' \
      'PHASE=backup-offline' 'PREVIOUS_RELEASE=active-release' \
      "BACKUP_PATH=$sandbox/backups/pre-recovery-probe")
    for service in caddy collector grafana victorialogs victoriametrics; do
      entries+=("PREVIOUS_IMAGE_${service^^}=$(fixture_image_id active-release "$service")")
    done
    write_transaction "${entries[@]}"
    rm "$BACKEND_ROOT/latest"
    recovery_probe_output=$(maintenance_init 2>&1) || exit 1
    [ -z "$recovery_probe_output" ] || exit 1
    [ "$CURRENT_BACKEND_DIR" = "$BACKEND_ROOT/active-release" ] || exit 1
    clear_transaction
    ln -s active-release "$BACKEND_ROOT/latest"
  ) || fail 'successful transaction recovery reported a missing active release'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    BACKEND_ROOT=$sandbox/backend
    missing_release=$(basename "$BACKEND_ROOT")
    if missing_release_output=$(transaction_release_dir "$missing_release" 2>&1); then
      exit 1
    fi
    [[ $missing_release_output == *"transaction release directory is missing: $BACKEND_ROOT/$missing_release"* ]]
  ) || fail 'transaction release resolution accepted the backend root as a release directory'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    export TELEMETRY_TEST_COMMAND_LOG=$sandbox/measure-events.log
    maintenance_init
    # shellcheck disable=SC2329 # measure_volume_bytes reaches Docker through this boundary double.
    docker() {
      printf '%s\n' "$*" >"$sandbox/measure-command"
      printf '%s\n' 123
    }
    [ "$(measure_volume_bytes fixture-volume measure-contract 0)" = 123 ] || exit 1
    measure_command=$(<"$sandbox/measure-command")
    [[ $measure_command == *'--name ai-agent-telemetry-backup-measure-contract-measure-0'* ]] || exit 1
    [[ $measure_command == *'type=volume,src=fixture-volume,dst=/source,readonly'* ]] || exit 1
    [[ $measure_command == *'du -sb /source | cut -f1'* ]] || exit 1
    grep -Fx 'VOLUME_MEASURE=fixture-volume' "$TELEMETRY_TEST_COMMAND_LOG" >/dev/null
  ) || fail 'volume measurement helper is not read-only and size-only'

  (
    # The test resolves the sibling script from its own location.
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    missing_backup_root=$sandbox/missing-backups
    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend \
      TELEMETRY_TEST_BACKUP_ROOT=$missing_backup_root maintenance_init
    [ ! -e "$missing_backup_root" ] || exit 1
    write_transaction 'PHASE=prepared'
    [ "$(read_transaction PHASE)" = prepared ] || exit 1
    write_transaction 'PHASE=activated'
    [ "$(read_transaction PHASE)" = activated ] || exit 1
    [ ! -e "$missing_backup_root" ] || exit 1
    clear_transaction
    [ ! -e "$TRANSACTION_FILE" ] || exit 1
  ) || fail 'transaction persistence must use the transaction file directory'

  (
    # The test resolves the sibling script from its own location.
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend maintenance_init
    if pin_output=$(pin_active_images "$sandbox/backend" 2>&1); then
      exit 1
    fi
    [ -n "$pin_output" ]
  ) || fail 'image pinning must reject an invalid Compose configuration'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    maintenance_init
    release_dir=$sandbox/backend/ordered-release
    mkdir -p "$release_dir"
    : >"$release_dir/docker-compose.yml"
    : >"$release_dir/.env"
    # shellcheck disable=SC2329 # pin_active_images reaches Compose and Docker through this test double.
    docker() {
      local argument service=

      if [ "$1" = compose ]; then
        case " $* " in
          *' config --services '*) printf '%s\n' caddy collector grafana victorialogs victoriametrics ;;
          *' config --images '*)
            printf '%s\n' registry.example/grafana:1 registry.example/caddy:1 \
              registry.example/victorialogs:1 registry.example/victoriametrics:1 \
              registry.example/collector:1
            ;;
          *' config --format json '*) printf '%s\n' '{"services":{}}' ;;
          *' config --quiet '*) return 0 ;;
        esac
        return 0
      fi
      if [ "$1" = run ]; then
        printf '%s\n' \
          $'caddy\tregistry.example/caddy:1' \
          $'collector\tregistry.example/collector:1' \
          $'grafana\tregistry.example/grafana:1' \
          $'victorialogs\tregistry.example/victorialogs:1' \
          $'victoriametrics\tregistry.example/victoriametrics:1'
        return 0
      fi
      if [ "$1" = ps ]; then
        for argument in "$@"; do
          case "$argument" in label=com.docker.compose.service=*) service=${argument##*=} ;; esac
        done
        printf 'container-%s\n' "$service"
        return 0
      fi
      if [ "$1" = inspect ]; then
        service=${!#}
        fixture_image_id ordered-release "${service#container-}"
        return 0
      fi
      if [ "$1" = image ] && [ "$2" = inspect ]; then
        printf '%s\n' \
          registry.example/caddy@sha256:caddy \
          registry.example/collector@sha256:collector \
          registry.example/victorialogs@sha256:victorialogs \
          registry.example/victoriametrics@sha256:victoriametrics
        return 0
      fi
      if [ "$1" = tag ]; then return 0; fi
      return 1
    }
    pin_active_images "$release_dir"
    for service in caddy collector victorialogs victoriametrics; do
      grep -F "    image: registry.example/$service@sha256:$service" \
        "$release_dir/.maintenance-compose.yml" >/dev/null || exit 1
    done
    grep -F '    image: ai-agent-telemetry-backend-grafana:ordered-release' \
      "$release_dir/.maintenance-compose.yml" >/dev/null || exit 1
  ) || fail 'image pinning associated Compose images by output position instead of service name'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    metric_sample_is_fresh \
      $'METRIC_VALUE_SAMPLE=1700000019.75|987654321\nMETRIC_RAW_TIMESTAMP_SAMPLE=1700000019.80|1700000010.25' \
      987654321 1700000000 1700000020 || exit 1
    run_fails metric_sample_is_fresh \
      $'METRIC_VALUE_SAMPLE=1700000019.75|987654321\nMETRIC_RAW_TIMESTAMP_SAMPLE=1700000019.80|1699999999.99' \
      987654321 1700000000 1700000020
    run_fails metric_sample_is_fresh \
      $'METRIC_VALUE_SAMPLE=1700000019.75|987654320\nMETRIC_RAW_TIMESTAMP_SAMPLE=1700000019.80|1700000010.25' \
      987654321 1700000000 1700000020
  ) || fail 'fixed-series validation did not separate exact value from the raw sample timestamp'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    export TELEMETRY_TEST_COMMAND_LOG=$sandbox/helper-events.log
    maintenance_init
    # shellcheck disable=SC2329 # The sourced maintenance functions invoke this test double.
    docker() { [ "$1" != ps ]; }
    run_fails cleanup_transaction_helpers query-failure
    [ ! -s "$TELEMETRY_TEST_COMMAND_LOG" ]
    printf '%s\n' 0 >"$sandbox/query-count"
    # shellcheck disable=SC2329 # The sourced maintenance functions invoke this test double.
    docker() {
      if [ "$1" = ps ]; then
        query_count=$(<"$sandbox/query-count")
        query_count=$((query_count + 1))
        printf '%s\n' "$query_count" >"$sandbox/query-count"
        case "$query_count" in 1) printf '%s\n' helper-id ;; 2) : ;; *) return 1 ;; esac
      elif [ "$1" = rm ]; then
        return 0
      fi
    }
    run_fails cleanup_transaction_helpers verify-failure
    ! grep -F HELPERS_ABSENT "$TELEMETRY_TEST_COMMAND_LOG" >/dev/null
  ) || fail 'helper cleanup accepted a Docker query or verification failure'

  for recovery_failure in query removal requery; do
    (
      # shellcheck disable=SC1090,SC1091
      TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
      export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
      export TELEMETRY_TEST_BACKUP_ROOT=$sandbox/backups TELEMETRY_TEST_PROJECT_NAME=recovery_status_test
      export TELEMETRY_TEST_LOCK_FILE=$sandbox/recovery.lock
      export TELEMETRY_TEST_COMMAND_LOG=$sandbox/public-recovery-events.log
      maintenance_init
      # shellcheck disable=SC2153 # maintenance_init initializes this sourced-library global.
      mkdir -p "$BACKEND_ROOT/previous"
      : >"$BACKEND_ROOT/previous/docker-compose.yml"
      : >"$BACKEND_ROOT/previous/.env"
      ln -sfn previous "$BACKEND_ROOT/latest"
      entries=('FORMAT_VERSION=1' "TRANSACTION_ID=recover-$recovery_failure" 'OPERATION=backup' \
        'PHASE=backup-offline' 'PREVIOUS_RELEASE=previous' \
        "BACKUP_PATH=$sandbox/backups/pre-recovery-status")
      for service in caddy collector grafana victorialogs victoriametrics; do
        entries+=("PREVIOUS_IMAGE_${service^^}=$(fixture_image_id previous "$service")")
      done
      write_transaction "${entries[@]}"
      : >"$TELEMETRY_TEST_COMMAND_LOG"
      recovery_query_counter=$sandbox/recovery-query-count-$recovery_failure
      printf '%s\n' 0 >"$recovery_query_counter"
      # shellcheck disable=SC2329 # backup_main reaches Docker only through the sourced production functions.
      docker() {
        local argument service=

        if [ "$1" = compose ]; then
          case " $* " in
            *' config --services '*) printf '%s\n' caddy collector grafana victorialogs victoriametrics ;;
          esac
          return 0
        fi
        if [ "$1" = ps ]; then
          case " $* " in
            *io.qubership.ai-agent-telemetry.maintenance.role*)
              recovery_query_count=$(<"$recovery_query_counter")
              recovery_query_count=$((recovery_query_count + 1))
              printf '%s\n' "$recovery_query_count" >"$recovery_query_counter"
              case "$recovery_failure:$recovery_query_count" in
                query:1|requery:3) return 1 ;;
                removal:1|requery:1) printf '%s\n' helper-id ;;
              esac
              return 0
              ;;
          esac
          for argument in "$@"; do
            case "$argument" in label=com.docker.compose.service=*) service=${argument##*=} ;; esac
          done
          printf 'previous-%s\n' "$service"
          return 0
        fi
        if [ "$1" = rm ]; then
          [ "$recovery_failure" != removal ]
          return
        fi
        if [ "$1" = inspect ]; then
          service=${!#}
          service=${service#previous-}
          case " $* " in
            *'{{.Image}}'*) fixture_image_id previous "$service" ;;
            *) printf '%s\n' 'running none' ;;
          esac
          return 0
        fi
        return 0
      }
      run_fails backup_main --target-label after-recovery
      [ -f "$TRANSACTION_FILE" ] || exit 1
      if grep -Fx COMPOSE_UP "$TELEMETRY_TEST_COMMAND_LOG" >/dev/null; then exit 1; fi
      if grep -F HELPERS_ABSENT "$TELEMETRY_TEST_COMMAND_LOG" >/dev/null; then exit 1; fi
    ) || fail "backup_main recovery ignored a helper $recovery_failure failure"
  done

  for preoffline_failure in pin-inspect pin-sync capture-inspect capture-missing-id capacity archive-grep-error \
    archive-read-error archive-env-match; do
    (
      # shellcheck disable=SC1090,SC1091
      TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
      preoffline_root=$sandbox/preoffline-$preoffline_failure
      export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$preoffline_root/backend
      export TELEMETRY_TEST_BACKUP_ROOT=$preoffline_root/backups
      export TELEMETRY_TEST_PROJECT_NAME=preoffline_status_test
      export TELEMETRY_TEST_LOCK_FILE=$preoffline_root/maintenance.lock
      export TELEMETRY_TEST_COMMAND_LOG=$preoffline_root/events.log
      mkdir -p "$TELEMETRY_TEST_BACKEND_ROOT/previous" "$TELEMETRY_TEST_BACKUP_ROOT"
      : >"$TELEMETRY_TEST_BACKEND_ROOT/docker-compose.yml"
      : >"$TELEMETRY_TEST_BACKEND_ROOT/.env"
      : >"$TELEMETRY_TEST_BACKEND_ROOT/previous/docker-compose.yml"
      : >"$TELEMETRY_TEST_BACKEND_ROOT/previous/.env"
      : >"$TELEMETRY_TEST_COMMAND_LOG"
      ln -s previous "$TELEMETRY_TEST_BACKEND_ROOT/latest"
      printf '%s\n' 0 >"$preoffline_root/inspect-count"
      printf '%s\n' 0 >"$preoffline_root/json-count"
      printf '%s\n' 0 >"$preoffline_root/sync-count"
      fixture_registry_digest=sha256:0000000000000000000000000000000000000000000000000000000000000000
      # shellcheck disable=SC2329 # pin_active_images and write_transaction invoke this failure double.
      sync() {
        local sync_count

        sync_count=$(<"$preoffline_root/sync-count")
        sync_count=$((sync_count + 1))
        printf '%s\n' "$sync_count" >"$preoffline_root/sync-count"
        [ "$preoffline_failure" != pin-sync ] || [ "$sync_count" -ne 2 ]
      }
      if [[ $preoffline_failure != archive-* ]]; then
        # shellcheck disable=SC2329 # backup_main invokes this bounded filesystem double.
        archive_source() { : >"$1/telemetry-backend-source.tar.gz"; }
      fi
      if [ "$preoffline_failure" = archive-grep-error ] || [ "$preoffline_failure" = archive-read-error ]; then
        # shellcheck disable=SC2329 # The real archive_source invokes this injected verification failure.
        grep() {
          if [ "$preoffline_failure" = archive-read-error ]; then
            rm -f -- "${!#}" || return 2
            command grep "$@"
          else
            return 2
          fi
        }
      fi
      if [ "$preoffline_failure" = archive-env-match ]; then
        # shellcheck disable=SC2329 # The real archive_source invokes this injected member listing.
        tar() {
          if [ "$1" = -tzf ]; then
            printf '%s\n' './.env'
          else
            command tar "$@"
          fi
        }
      fi
      # shellcheck disable=SC2329 # backup_main invokes this bounded filesystem double.
      write_restore_instructions() { : >"$1/RESTORE.md"; }
      # shellcheck disable=SC2329 # backup_main invokes this bounded filesystem double.
      write_static_checksums() { : >"$1/SHA256SUMS"; }
      # shellcheck disable=SC2329 # backup_main invokes this bounded Docker discovery double.
      resolve_volume_name() { printf '%s\n' fixture-volume; }
      # shellcheck disable=SC2329 # backup_main invokes this bounded Docker discovery double.
      measure_volume_bytes() { printf '%s\n' 0; }
      # shellcheck disable=SC2329 # backup_main invokes this bounded capacity double.
      check_backup_capacity() { [ "$preoffline_failure" != capacity ]; }
      # shellcheck disable=SC2329 # backup_main invokes this bounded health double.
      strict_health_gate() { return 0; }
      # shellcheck disable=SC2329 # backup_main reaches Docker through the sourced production functions.
      docker() {
        local argument inspect_count json_count service=

        if [ "$1" = compose ]; then
          case " $* " in
            *' config --services '*) printf '%s\n' caddy collector grafana victorialogs victoriametrics ;;
            *' config --images '*)
              printf '%s\n' registry.example/caddy:1 registry.example/collector:1 registry.example/grafana:1 \
                registry.example/victorialogs:1 registry.example/victoriametrics:1
              ;;
            *' config --format json '*) printf '%s\n' '{"services":{}}' ;;
            *' config --volumes '*) printf '%s\n' data ;;
            *' version --short '*) printf '%s\n' 2.0.0 ;;
            *' down '*) return 1 ;;
          esac
          return 0
        fi
        if [ "$1" = ps ]; then
          case " $* " in *io.qubership.ai-agent-telemetry.maintenance.role*) return 0 ;; esac
          for argument in "$@"; do
            case "$argument" in label=com.docker.compose.service=*) service=${argument##*=} ;; esac
          done
          printf 'container-%s\n' "$service"
          return 0
        fi
        if [ "$1" = inspect ]; then
          inspect_count=$(<"$preoffline_root/inspect-count")
          inspect_count=$((inspect_count + 1))
          printf '%s\n' "$inspect_count" >"$preoffline_root/inspect-count"
          if { [ "$preoffline_failure" = pin-inspect ] && [ "$inspect_count" -eq 1 ]; } ||
            { [ "$preoffline_failure" = capture-inspect ] && [ "$inspect_count" -eq 6 ]; }; then
            return 1
          fi
          if [ "$preoffline_failure" = capture-missing-id ] && [ "$inspect_count" -eq 6 ]; then
            return 0
          fi
          service=${!#}
          service=${service#container-}
          fixture_image_id previous "$service"
          return 0
        fi
        if [ "$1" = image ] && [ "$2" = inspect ]; then
          case " $* " in
            *RepoDigests*)
              printf '%s\n' "registry.example/caddy@$fixture_registry_digest" \
                "registry.example/collector@$fixture_registry_digest" \
                "registry.example/victorialogs@$fixture_registry_digest" \
                "registry.example/victoriametrics@$fixture_registry_digest"
              ;;
          esac
          return 0
        fi
        if [ "$1" = tag ] || [ "$1" = pull ] || [ "$1" = rm ]; then
          return 0
        fi
        if [ "$1" = run ] && [ "${5:-}" = -er ]; then
          json_count=$(<"$preoffline_root/json-count")
          json_count=$((json_count + 1))
          printf '%s\n' "$json_count" >"$preoffline_root/json-count"
          if [ "$json_count" -eq 1 ]; then
            printf '%s\n' \
              $'caddy\tregistry.example/caddy:1' \
              $'collector\tregistry.example/collector:1' \
              $'grafana\tregistry.example/grafana:1' \
              $'victorialogs\tregistry.example/victorialogs:1' \
              $'victoriametrics\tregistry.example/victoriametrics:1'
          else
            printf '%s\n' \
              "caddy"$'\t'"registry.example/caddy@$fixture_registry_digest" \
              "collector"$'\t'"registry.example/collector@$fixture_registry_digest" \
              $'grafana\tpreoffline_status_test-grafana:previous' \
              "victorialogs"$'\t'"registry.example/victorialogs@$fixture_registry_digest" \
              "victoriametrics"$'\t'"registry.example/victoriametrics@$fixture_registry_digest"
          fi
          return 0
        fi
        if [ "$1" = run ]; then
          return 1
        fi
        return 0
      }
      run_fails backup_main --target-label "$preoffline_failure"
      if command grep -Fx COMPOSE_DOWN "$TELEMETRY_TEST_COMMAND_LOG" >/dev/null; then exit 1; fi
      [ ! -f "$TELEMETRY_TEST_BACKEND_ROOT/.maintenance-transaction" ] || exit 1
    ) || fail "$preoffline_failure failure allowed an offline transition or durable transaction"
  done

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    maintenance_init
    # shellcheck disable=SC2153 # maintenance_init initializes this sourced-library global.
    mkdir -p "$BACKEND_ROOT/previous"
    entries=('FORMAT_VERSION=1' 'TRANSACTION_ID=parser-test' 'OPERATION=backup' 'PHASE=backup-offline' \
      'PREVIOUS_RELEASE=previous' "BACKUP_PATH=$(dirname "$BACKEND_ROOT")/backups/pre-parser")
    for service in caddy collector grafana victorialogs victoriametrics; do
      entries+=("PREVIOUS_IMAGE_${service^^}=$(fixture_image_id previous "$service")")
    done
    write_transaction "${entries[@]}"
    printf 'UNKNOWN=tail' >>"$TRANSACTION_FILE"
    run_fails validate_transaction_state
    write_transaction "${entries[@]}"
    printf 'UNKNOWN=tail\0' >>"$TRANSACTION_FILE"
    run_fails validate_transaction_state
    write_transaction "${entries[@]}"
    printf 'CORRUPT=interior\001byte\n' >>"$TRANSACTION_FILE"
    run_fails validate_transaction_state
    entries[6]='PREVIOUS_IMAGE_CADDY=sha256:abc'
    write_transaction "${entries[@]}"
    run_fails validate_transaction_state
  ) || fail 'transaction parser accepted binary corruption, an unterminated record, or a noncanonical image ID'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    export TELEMETRY_TEST_LOCK_FILE=$sandbox/handoff.lock
    maintenance_init
    write_transaction 'FORMAT_VERSION=1' 'TRANSACTION_ID=handoff-transaction' 'OPERATION=update' \
      'PHASE=backup-offline' 'PREVIOUS_RELEASE=previous-release' 'TARGET_RELEASE=target-release'
    exec {handoff_fd}>"$LOCK_FILE"
    export TELEMETRY_UPDATE_LOCK_HELD=1 TELEMETRY_UPDATE_LOCK_FD=$handoff_fd
    export TELEMETRY_UPDATE_TRANSACTION_ID=handoff-transaction
    export TELEMETRY_UPDATE_PREVIOUS_RELEASE=previous-release
    declare -F validate_inherited_maintenance_lock >/dev/null || exit 1
    flock -n "$handoff_fd"
    validate_inherited_maintenance_lock || exit 1
    TELEMETRY_UPDATE_TRANSACTION_ID=wrong-transaction run_fails validate_inherited_maintenance_lock
    TELEMETRY_UPDATE_PREVIOUS_RELEASE=wrong-release run_fails validate_inherited_maintenance_lock
    flock -u "$handoff_fd"
    # shellcheck disable=SC2016 # The child shell receives the ready path as its first argument.
    flock "$LOCK_FILE" sh -c 'touch "$1"; sleep 2' _ "$sandbox/handoff.ready" &
    holder_pid=$!
    while [ ! -f "$sandbox/handoff.ready" ]; do sleep 0.02; done
    run_fails validate_inherited_maintenance_lock
    wait "$holder_pid"
    exec {handoff_fd}>&-
    clear_transaction
  ) || fail 'inherited update lock validation did not prove ownership and transaction identity'

  (
    # shellcheck disable=SC1090,SC1091
    TELEMETRY_SOURCE_ONLY=1 source "$backup_script"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=$sandbox/backend
    # shellcheck disable=SC2329 # backup_main invokes this injected failure.
    validate_inherited_maintenance_lock() { return 1; }
    if TELEMETRY_UPDATE_LOCK_HELD=1 backup_main --leave-stopped; then
      exit 1
    fi
    [ "$(read_transaction PHASE 2>/dev/null || true)" != activation-prepared ]
  ) || fail 'backup_main ignored an inherited-lock validation failure under a tested caller'

  run_fails env -u TELEMETRY_MAINTENANCE_TEST_MODE "$backup_script" --target-label test
  run_fails env -u TELEMETRY_MAINTENANCE_TEST_MODE TELEMETRY_TEST_KILL_AT=activation-prepared \
    "$backup_script" --target-label test
  production_log=$sandbox/production-command.log
  run_fails env -u TELEMETRY_MAINTENANCE_TEST_MODE TELEMETRY_TEST_COMMAND_LOG="$production_log" \
    "$backup_script" --target-label test
  [ ! -e "$production_log" ] || fail 'production maintenance honored a test command-log override'
  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT=/opt/unsafe "$backup_script"
  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_KILL_AT=unsupported-phase "$backup_script"
  run_fails env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_PROJECT_NAME='bad/project' "$backup_script"
  run_fails "$backup_script" --target-label '../escape'
  run_fails "$backup_script" --unknown-option

  active_before=$(readlink "$sandbox/backend/latest")
  env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
    TELEMETRY_TEST_HOLD_LOCK_SECONDS=2 "$backup_script" --target-label first >"$sandbox/first.out" 2>&1 &
  first_pid=$!
  while flock -n "$sandbox/maintenance.lock" true 2>/dev/null; do
    kill -0 "$first_pid" 2>/dev/null || fail 'first maintenance process did not acquire its lock'
    sleep 0.05
  done
  if lock_output=$(env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" "$backup_script" --target-label second 2>&1); then
    fail 'second maintenance process unexpectedly acquired the active lock'
  fi
  [ "$lock_output" = 'ERROR: another maintenance operation is already running' ] ||
    fail "second maintenance process failed for the wrong reason: $lock_output"
  [ "$(readlink "$sandbox/backend/latest")" = "$active_before" ] ||
    fail 'a blocked maintenance process changed the active release link'
  kill -TERM "$first_pid"
  if wait "$first_pid"; then
    fail 'signaled maintenance process exited successfully'
  else
    first_status=$?
  fi
  [ "$first_status" -eq 143 ] || fail "signaled maintenance process exited with status $first_status"
  if [[ $(<"$sandbox/first.out") == *'portable volume backup is not implemented yet'* ]]; then
    fail 'signaled maintenance process continued after releasing its lock'
  fi
  flock -n "$sandbox/maintenance.lock" true || fail 'signaled maintenance process did not release its lock'

  printf 'PASS: maintenance cli contract\n'
}

write_activation_docker() {
  local path=$1

  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$TELEMETRY_TEST_COMMAND_LOG"
if [ "$1" = compose ]; then
  release_dir=
  previous=
  for argument in "$@"; do
    [ "$previous" != --project-directory ] || release_dir=$argument
    previous=$argument
  done
  case " $* " in
    *' config --quiet '*) exit 0 ;;
    *' config --services '*) printf '%s\n' caddy collector grafana victorialogs victoriametrics; exit 0 ;;
    *' config --environment '*)
      printf '%s\n' 'SITE_ADDRESS=localhost' 'HTTPS_PORT=8443' 'INGEST_TOKEN=activation-token' \
        'DASHBOARD_AUTH_USER=activation-viewer'
      exit 0
      ;;
    *' down '*) printf '%s\n' COMPOSE_DOWN >>"$TELEMETRY_TEST_COMMAND_LOG"; exit 0 ;;
    *' up -d --no-build --pull never '*) basename "$release_dir" >"$TELEMETRY_TEST_STATE"; exit 0 ;;
  esac
  exit 0
fi
if [ "$1" = ps ]; then
  service=
  for argument in "$@"; do
    case "$argument" in
      label=io.qubership.ai-agent-telemetry.maintenance.role=*) exit 0 ;;
      label=com.docker.compose.service=*) service=${argument##*=} ;;
    esac
  done
  printf '%s-%s\n' "$(<"$TELEMETRY_TEST_STATE")" "$service"
  exit 0
fi
if [ "$1" = inspect ]; then
  container=${!#}
  service=${container##*-}
  release=${container%-$service}
  case " $* " in
    *'{{.Image}}'*) fixture_image_id "$release" "$service" ;;
    *) printf '%s\n' 'running none' ;;
  esac
  exit 0
fi
if [ "$1" = network ] && [ "$2" = ls ]; then
  printf '%s\n' activation_backend
  exit 0
fi
if [ "$1" = run ]; then
  case " $* " in
    *'io.qubership.ai-agent-telemetry.maintenance.role=health'*)
      if [ "${TELEMETRY_TEST_HELPER_UNAVAILABLE:-}" = 1 ]; then
        case " $* " in *' --pull never '*) exit 1 ;; *) exit 0 ;; esac
      fi
      if [ "${TELEMETRY_TEST_REJECT_ANONYMOUS_GRAFANA:-}" = 1 ]; then
        case " $* " in *X-WEBAUTH-USER*) ;; *) exit 1 ;; esac
      fi
      evaluation_timestamp=$(date +%s)
      raw_sample_timestamp=$evaluation_timestamp
      sample_value=${!#}
      [ "${TELEMETRY_TEST_STALE_RAW_METRIC:-}" != 1 ] || raw_sample_timestamp=1
      [ "${TELEMETRY_TEST_WRONG_METRIC_VALUE:-}" != 1 ] || sample_value=0
      case " $* " in
        *'query=telemetry_maintenance_health'*)
          printf 'METRIC_VALUE_SAMPLE=%s|%s\n' "$evaluation_timestamp" "$sample_value"
          ;;
      esac
      case " $* " in
        *'query=timestamp%28telemetry_maintenance_health%29'*)
          printf 'METRIC_RAW_TIMESTAMP_SAMPLE=%s|%s\n' "$evaluation_timestamp" "$raw_sample_timestamp"
          ;;
      esac
      exit 0
      ;;
  esac
fi
printf 'unexpected docker command: %s\n' "$*" >&2
exit 1
EOF
  chmod 700 "$path"
}

run_activation_suite() {
  local sandbox backend_root backup_path project command_log state docker_dir service output

  sandbox=$(mktemp -d /tmp/telemetry-activation.XXXXXX)
  trap 'rm -rf "${sandbox:-}"' EXIT HUP INT TERM
  backend_root=$sandbox/backend
  backup_path=$sandbox/backups/pre-fixture
  project="telemetry_activation_$RANDOM$RANDOM"
  command_log=$sandbox/commands.log
  state=$sandbox/state
  docker_dir=$sandbox/bin
  mkdir -p "$backend_root/previous" "$backend_root/target" "$backup_path" "$docker_dir"
  : >"$command_log"
  printf '%s\n' target >"$state"
  for release in previous target; do
    : >"$backend_root/$release/docker-compose.yml"
    cat >"$backend_root/$release/.env" <<'EOF'
SITE_ADDRESS=localhost
HTTPS_PORT=8443
INGEST_TOKEN=activation-token
DASHBOARD_AUTH_USER=activation-viewer
EOF
    : >"$backend_root/$release/.maintenance-compose.yml"
    if [ "$release" = target ]; then
      {
        printf '%s\n' 'format=1' 'resolved_identity=target-resolved' 'content_identity=target-content'
        for service in caddy collector grafana victorialogs victoriametrics; do
          printf 'image.%s=fixture/%s:target|%s\n' "$service" "$service" "$(fixture_image_id target "$service")"
        done
      } >"$backend_root/$release/.deployment-manifest"
    else
      : >"$backend_root/$release/.deployment-manifest"
    fi
  done
  ln -s target "$backend_root/latest"
  write_activation_docker "$docker_dir/docker"
  cat >"$docker_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'curl %s\n' "$*" >>"$TELEMETRY_TEST_COMMAND_LOG"
case " $* " in *' --config - '*) input=$(cat) ;; *) input= ;; esac
case "$*" in
  *user.email*|*user.account*|*session.id*|*repository*|*prompt*|*machine*) exit 1 ;;
esac
: "$input"
printf '%s\n' '{}'
EOF
  chmod 700 "$docker_dir/curl"

  (
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
    export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem"
    : >"$TELEMETRY_TEST_CA_CERT"
    for service in caddy collector grafana victorialogs victoriametrics; do
      printf '%s=%s\n' "$service" "$(fixture_image_id previous "$service")"
    done >"$sandbox/previous-images"
    export sandbox backend_root backup_path
    bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      maintenance_init
      replace_latest previous
      [ "$(readlink "$backend_root/latest")" = previous ] || exit 1
      write_transaction "FORMAT_VERSION=1" "TRANSACTION_ID=activation-contract" "OPERATION=update" \
        "PHASE=activated" "PREVIOUS_RELEASE=previous" "TARGET_RELEASE=target" \
        "BACKUP_PATH=$backup_path"
      for service in caddy collector grafana victorialogs victoriametrics; do
        printf "PREVIOUS_IMAGE_%s=%s\\n" "${service^^}" "$(fixture_image_id previous "$service")" >>"$TRANSACTION_FILE"
      done
      : >"$backup_path/SHA256SUMS"
      rollback_transaction
      [ "$(readlink "$backend_root/latest")" = previous ] || exit 1
      [ ! -f "$TRANSACTION_FILE" ] || exit 1
    ' "$update_script"
    assert_container_image_ids "$project" "$sandbox/previous-images"
    assert_no_registry_event_after COMPOSE_DOWN
  ) || fail 'rollback did not restore the relative link and exact previous image IDs'

  (
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
    export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem" sandbox backend_root backup_path
    printf '%s\n' previous >"$state"
    bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      maintenance_init
      replace_latest previous
      write_transaction "FORMAT_VERSION=1" "TRANSACTION_ID=activation-success" "OPERATION=update" \
        "PHASE=activation-prepared" "PREVIOUS_RELEASE=previous" "TARGET_RELEASE=target" \
        "BACKUP_PATH=$backup_path" \
        "TARGET_IMAGE_CADDY=$(fixture_image_id target caddy)" \
        "TARGET_IMAGE_COLLECTOR=$(fixture_image_id target collector)" \
        "TARGET_IMAGE_GRAFANA=$(fixture_image_id target grafana)" \
        "TARGET_IMAGE_VICTORIALOGS=$(fixture_image_id target victorialogs)" \
        "TARGET_IMAGE_VICTORIAMETRICS=$(fixture_image_id target victoriametrics)"
      activate_transaction
      [ "$(readlink "$backend_root/latest")" = target ] || exit 1
      [ -f "$backend_root/target/.deployment-success" ] || exit 1
      [ ! -f "$TRANSACTION_FILE" ] || exit 1
    ' "$update_script"
  ) || fail 'activation did not commit the target after its strict health gate'
  cp "$backend_root/target/.deployment-success" "$sandbox/deployment-success.valid"

  committed_failures=0
  for committed_case in raw-link transaction-id target-image; do
    if ! (
      export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
      export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
      export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
      export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
      export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem" sandbox backend_root backup_path committed_case
      printf '%s\n' target >"$state"
      cp "$sandbox/deployment-success.valid" "$backend_root/target/.deployment-success"
      rm -f "$backend_root/latest"
      ln -s target "$backend_root/latest"
      bash -c '
        TELEMETRY_SOURCE_ONLY=1 source "$0"
        maintenance_init
        transaction_id=activation-success
        [ "$committed_case" != transaction-id ] || transaction_id=committed-mismatch
        entries=("FORMAT_VERSION=1" "TRANSACTION_ID=$transaction_id" "OPERATION=update" \
          "PHASE=committed" "PREVIOUS_RELEASE=previous" "TARGET_RELEASE=target" \
          "BACKUP_PATH=$backup_path")
        for service in caddy collector grafana victorialogs victoriametrics; do
          entries+=("PREVIOUS_IMAGE_${service^^}=$(fixture_image_id previous "$service")")
          target_id=$(fixture_image_id target "$service")
          [ "$committed_case" != target-image ] || target_id=$(fixture_image_id wrong "$service")
          entries+=("TARGET_IMAGE_${service^^}=$target_id")
        done
        write_transaction "${entries[@]}"
        if [ "$committed_case" = raw-link ]; then
          rm -f "$backend_root/latest"
          ln -s "$backend_root/target" "$backend_root/latest"
          exec bash -c '\''
            TELEMETRY_SOURCE_ONLY=1 source "$0"
            maintenance_init
            recover_update_transaction
            [ "$(readlink "$backend_root/latest")" = previous ]
          '\'' "$0"
        fi
        recover_update_transaction
        [ "$(readlink "$backend_root/latest")" = previous ]
      ' "$update_script"
    ); then
      printf 'RED: committed recovery accepted invalid %s state\n' "$committed_case" >&2
      committed_failures=$((committed_failures + 1))
    fi
  done

  if ! (
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
    export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem" sandbox backend_root backup_path
    printf '%s\n' target >"$state"
    rm -f "$backend_root/latest"
    ln -s target "$backend_root/latest"
    manifest_checksum=$(sha256sum "$backend_root/target/.deployment-manifest" | cut -d' ' -f1)
    printf 'manifest_sha256=%s\n' "$manifest_checksum" >"$backend_root/target/.deployment-success"
    chmod 644 "$backend_root/target/.deployment-success"
    bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      maintenance_init
      entries=("FORMAT_VERSION=1" "TRANSACTION_ID=activation-success" "OPERATION=update" \
        "PHASE=committed" "PREVIOUS_RELEASE=previous" "TARGET_RELEASE=target" \
        "BACKUP_PATH=$backup_path")
      for service in caddy collector grafana victorialogs victoriametrics; do
        entries+=("PREVIOUS_IMAGE_${service^^}=$(fixture_image_id previous "$service")")
        entries+=("TARGET_IMAGE_${service^^}=$(fixture_image_id target "$service")")
      done
      write_transaction "${entries[@]}"
      recover_transaction
      [ "$(readlink "$backend_root/latest")" = previous ]
    ' "$backup_script"
  ); then
    printf '%s\n' 'RED: backup committed recovery accepted a checksum-only marker' >&2
    committed_failures=$((committed_failures + 1))
  fi
  [ "$committed_failures" -eq 0 ] || fail "$committed_failures committed recovery contracts failed"

  cp "$sandbox/deployment-success.valid" "$backend_root/target/.deployment-success"
  grep -F '/v1/logs' "$command_log" >/dev/null || fail 'strict health gate did not submit an OTLP log'
  grep -F '/v1/metrics' "$command_log" >/dev/null || fail 'strict health gate did not submit an OTLP gauge'
  grep -F 'maintenance.role=health' "$command_log" >/dev/null ||
    fail 'strict health gate did not label its internal helper'

  probe_contract_failures=0
  if grep -F 'activation-token' "$command_log" >/dev/null; then
    printf '%s\n' 'RED: OTLP bearer token is exposed in process argv' >&2
    probe_contract_failures=$((probe_contract_failures + 1))
  fi
  if ! grep -F -- '--pull never' "$command_log" >/dev/null; then
    printf '%s\n' 'RED: strict-health helper permits an implicit pull' >&2
    probe_contract_failures=$((probe_contract_failures + 1))
  fi
  if ! grep -F 'X-WEBAUTH-USER' "$command_log" >/dev/null; then
    printf '%s\n' 'RED: Grafana health request lacks an auth-proxy identity' >&2
    probe_contract_failures=$((probe_contract_failures + 1))
  fi
  for grafana_path in \
    /api/health \
    /api/datasources/uid/victoriametrics \
    /api/dashboards/uid/ai-agent-health \
    /api/dashboards/uid/ai-agent-telemetry-adoption \
    /api/dashboards/uid/native-agent-metrics-overview \
    /api/dashboards/uid/codex-native-metrics; do
    if ! grep -F "$grafana_path" "$command_log" >/dev/null; then
      printf 'RED: strict health gate did not query Grafana path %s\n' "$grafana_path" >&2
      probe_contract_failures=$((probe_contract_failures + 1))
    fi
  done
  if ! (
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
    export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem" TELEMETRY_TEST_REJECT_ANONYMOUS_GRAFANA=1
    bash -c 'TELEMETRY_SOURCE_ONLY=1 source "$0"; maintenance_init; strict_health_gate "$1"' \
      "$backup_script" "$backend_root/target"
  ); then
    printf '%s\n' 'RED: authenticated Grafana health cannot pass when anonymous access fails' >&2
    probe_contract_failures=$((probe_contract_failures + 1))
  fi
  if (
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
    export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem" TELEMETRY_TEST_HELPER_UNAVAILABLE=1
    bash -c 'TELEMETRY_SOURCE_ONLY=1 source "$0"; maintenance_init; strict_health_gate "$1"' \
      "$backup_script" "$backend_root/target"
  ); then
    printf '%s\n' 'RED: unavailable helper was implicitly pulled during health verification' >&2
    probe_contract_failures=$((probe_contract_failures + 1))
  fi
  for metric_case in TELEMETRY_TEST_STALE_RAW_METRIC TELEMETRY_TEST_WRONG_METRIC_VALUE; do
    if (
      export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
      export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
      export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
      export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_STABILITY_SECONDS=0
      export TELEMETRY_TEST_CA_CERT="$sandbox/test-ca.pem"
      export "$metric_case=1"
      bash -c 'TELEMETRY_SOURCE_ONLY=1 source "$0"; maintenance_init; strict_health_gate "$1"' \
        "$backup_script" "$backend_root/target"
    ); then
      printf 'RED: fixed-series health probe accepted %s\n' "$metric_case" >&2
      probe_contract_failures=$((probe_contract_failures + 1))
    fi
  done
  [ "$probe_contract_failures" -eq 0 ] || fail "$probe_contract_failures strict-health probe contracts failed"

  if output=$(
    export PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1
    export TELEMETRY_TEST_BACKEND_ROOT="$backend_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_COMMAND_LOG="$command_log" TELEMETRY_TEST_STATE="$state"
    export TELEMETRY_TEST_HEALTH_ATTEMPTS=1 TELEMETRY_TEST_FORCE_UNHEALTHY=1
    export sandbox backend_root backup_path
    bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      maintenance_init
      ln -sfn target "$backend_root/latest"
      write_transaction "FORMAT_VERSION=1" "TRANSACTION_ID=activation-unhealthy" "OPERATION=update" \
        "PHASE=activated" "PREVIOUS_RELEASE=previous" "TARGET_RELEASE=target" \
        "BACKUP_PATH=$backup_path" \
        "PREVIOUS_IMAGE_CADDY=$(fixture_image_id previous caddy)" \
        "PREVIOUS_IMAGE_COLLECTOR=$(fixture_image_id previous collector)" \
        "PREVIOUS_IMAGE_GRAFANA=$(fixture_image_id previous grafana)" \
        "PREVIOUS_IMAGE_VICTORIALOGS=$(fixture_image_id previous victorialogs)" \
        "PREVIOUS_IMAGE_VICTORIAMETRICS=$(fixture_image_id previous victoriametrics)"
      rollback_transaction
    ' "$update_script" 2>&1
  ); then
    fail 'rollback accepted an unhealthy restored release'
  fi
  [ -f "$backend_root/.maintenance-transaction" ] || fail 'unhealthy rollback removed its transaction'
  [ -f "$backup_path/SHA256SUMS" ] || fail 'unhealthy rollback removed its backup'
  [[ $output == *'healthy'* ]] || fail 'unhealthy rollback failed without a health diagnostic'

  rm -rf "$sandbox"
  trap - EXIT HUP INT TERM
  printf '%s\n' 'PASS: maintenance activation contract'
}

write_resolution_docker() {
  local path=$1

  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "$1" = pull ]; then
  case "${2:-}" in
    ghcr.io/jqlang/jq:1.7.1@sha256:096b83865ad59b5b02841f103f83f45c51318394331bf1995e187ea3be937432|\
      caddy:2|otel/opentelemetry-collector-contrib:0.119.0|victoriametrics/victoria-logs:v1.16.0-victorialogs|\
      victoriametrics/victoria-metrics:v1.148.0|example/alertmanager:1.0|\
      "$TELEMETRY_TEST_CURRENT_CADDY_REFERENCE"|"$TELEMETRY_TEST_CURRENT_COLLECTOR_REFERENCE"|\
      "$TELEMETRY_TEST_CURRENT_VLOGS_REFERENCE"|"$TELEMETRY_TEST_CURRENT_VMETRICS_REFERENCE")
      exit 0
      ;;
    *)
      printf 'unknown pull reference: %s\n' "${2:-}" >&2
      exit 1
      ;;
  esac
fi
if [ "$1" = tag ] || [ "$1" = build ]; then
  exit 0
fi
if [ "$1" = image ] && [ "$2" = inspect ]; then
  image=${!#}
  case "$image" in
    "$TELEMETRY_TEST_CURRENT_CADDY_REFERENCE") document="[{\"Id\":\"sha256:caddy-local\",\"RepoDigests\":[\"$TELEMETRY_TEST_CURRENT_CADDY_DIGEST_REFERENCE\"]}]" ;;
    "$TELEMETRY_TEST_CURRENT_COLLECTOR_REFERENCE") document="[{\"Id\":\"sha256:collector-local\",\"RepoDigests\":[\"$TELEMETRY_TEST_CURRENT_COLLECTOR_DIGEST_REFERENCE\"]}]" ;;
    "$TELEMETRY_TEST_CURRENT_VLOGS_REFERENCE") document="[{\"Id\":\"sha256:vlogs-local\",\"RepoDigests\":[\"$TELEMETRY_TEST_CURRENT_VLOGS_DIGEST_REFERENCE\"]}]" ;;
    "$TELEMETRY_TEST_CURRENT_VMETRICS_REFERENCE") document="[{\"Id\":\"sha256:vmetrics-local\",\"RepoDigests\":[\"$TELEMETRY_TEST_CURRENT_VMETRICS_DIGEST_REFERENCE\"]}]" ;;
    caddy:2) document='[{"Id":"sha256:caddy-local","RepoDigests":["caddy@sha256:caddyfixture"]}]' ;;
    otel/opentelemetry-collector-contrib:0.119.0) document='[{"Id":"sha256:collector-local","RepoDigests":["otel/opentelemetry-collector-contrib@sha256:collectorfixture"]}]' ;;
    victoriametrics/victoria-logs:v1.16.0-victorialogs) document='[{"Id":"sha256:vlogs-local","RepoDigests":["victoriametrics/victoria-logs@sha256:vlogsfixture"]}]' ;;
    victoriametrics/victoria-metrics:v1.148.0) document='[{"Id":"sha256:vmetrics-local","RepoDigests":["victoriametrics/victoria-metrics@sha256:vmetricsfixture"]}]' ;;
    example/alertmanager:1.0) document='[{"Id":"sha256:alertmanager-local","RepoDigests":["example/alertmanager@sha256:alertmanagerfixture"]}]' ;;
    "$TELEMETRY_TEST_CURRENT_CADDY_DIGEST_REFERENCE") document='[{"Id":"sha256:caddy-local","RepoDigests":[]}]' ;;
    "$TELEMETRY_TEST_CURRENT_COLLECTOR_DIGEST_REFERENCE") document='[{"Id":"sha256:collector-local","RepoDigests":[]}]' ;;
    "$TELEMETRY_TEST_CURRENT_VLOGS_DIGEST_REFERENCE") document='[{"Id":"sha256:vlogs-local","RepoDigests":[]}]' ;;
    "$TELEMETRY_TEST_CURRENT_VMETRICS_DIGEST_REFERENCE") document='[{"Id":"sha256:vmetrics-local","RepoDigests":[]}]' ;;
    caddy@sha256:caddyfixture) document='[{"Id":"sha256:caddy-local","RepoDigests":[]}]' ;;
    otel/opentelemetry-collector-contrib@sha256:collectorfixture) document='[{"Id":"sha256:collector-local","RepoDigests":[]}]' ;;
    victoriametrics/victoria-logs@sha256:vlogsfixture) document='[{"Id":"sha256:vlogs-local","RepoDigests":[]}]' ;;
    victoriametrics/victoria-metrics@sha256:vmetricsfixture) document='[{"Id":"sha256:vmetrics-local","RepoDigests":[]}]' ;;
    example/alertmanager@sha256:alertmanagerfixture) document='[{"Id":"sha256:alertmanager-local","RepoDigests":[]}]' ;;
      ai-agent-telemetry-backend-grafana:0123456789ab|ai-agent-telemetry-backend-grafana:111111111111|\
      ai-agent-telemetry-backend-grafana:222222222222|ai-agent-telemetry-backend-grafana:bbbbbbbbbbbb|\
      ai-agent-telemetry-backend-grafana:cccccccccccc|ai-agent-telemetry-backend-grafana:dddddddddddd|\
      ai-agent-telemetry-backend-grafana:release-e3cad1a6f905017fd828|\
      ai-agent-telemetry-backend-grafana:release-777818d28a303a6bffed)
      document='[{"Id":"sha256:grafana-local","RepoDigests":[]}]'
      ;;
    *)
      printf 'unknown inspect reference: %s\n' "$image" >&2
      exit 1
      ;;
  esac
  if [ "${TELEMETRY_TEST_IMAGE_TOCTOU:-}" = 1 ] && [[ $image == caddy@sha256:* ]]; then
    document='[{"Id":"sha256:caddy-changed","RepoDigests":[]}]'
  fi
  case " $* " in
    *' --format '*) printf '%s\n' "${document#*\"Id\":\"}" | cut -d'"' -f1 ;;
    *) printf '%s\n' "$document" ;;
  esac
  exit 0
fi
if [ "$1" = run ]; then
  response= query= ref=
  for ((index = 1; index <= $#; index++)); do
    argument=${!index}
    case "$argument" in
      type=bind,src=*,dst=/input,readonly)
        response=${argument#type=bind,src=}
        response=${response%%,dst=/input,readonly}
        ;;
      .tag_name|.sha|.service|.build|'.image // ""'|'.assets[] | [.name, .browser_download_url] | @tsv'|*to_entries*|*RepoDigests*|'.[0].Id')
        query=$argument
        ;;
      --arg)
        next=$((index + 1))
        following=$((index + 2))
        [ "${!next}" = ref ] && ref=${!following}
        ;;
    esac
  done
  python3 - "$response/response.json" "$query" "$ref" <<'PY'
import json
import sys
import urllib.parse

with open(sys.argv[1], encoding="utf-8") as response:
    document = json.load(response)
query = sys.argv[2]
if not query:
    print(urllib.parse.quote(sys.argv[3], safe=""))
elif query in {".tag_name", ".sha"}:
    print(document[query[1:]])
elif "assets[]" in query:
    for asset in document["assets"]:
        print(f"{asset['name']}\t{asset['browser_download_url']}")
elif "to_entries[]" in query:
    for name, service in document["services"].items():
        print(json.dumps({"service": name, "image": service.get("image"), "build": "build" in service}))
elif "RepoDigests[]" in query:
    for digest in document[0]["RepoDigests"]:
        print(digest)
elif ".[0].Id" in query:
    print(document[0]["Id"])
elif query == ".service":
    print(document["service"])
elif ".image" in query:
    print(document.get("image") or "")
elif query == ".build":
    print(str(document["build"]).lower())
else:
    raise ValueError(f"unsupported jq query: {query}")
PY
  exit 0
fi
if [ "$1" = ps ]; then
  service=
  for argument in "$@"; do
    case "$argument" in label=com.docker.compose.service=*) service=${argument##*=} ;; esac
  done
  printf 'container-%s\n' "$service"
  exit 0
fi
if [ "$1" = inspect ]; then
  container=${!#}
  service=${container#container-}
  case " $* " in
    *'{{.Image}}'*)
      case "$service" in
        caddy) printf '%s\n' sha256:caddy-local ;;
        collector) printf '%s\n' sha256:collector-local ;;
        grafana) printf '%s\n' sha256:grafana-local ;;
        victorialogs) printf '%s\n' sha256:vlogs-local ;;
        victoriametrics) printf '%s\n' sha256:vmetrics-local ;;
        *) exit 1 ;;
      esac
      ;;
    *) printf '%s\n' 'running none' ;;
  esac
  exit 0
fi
if [ "$1" = compose ]; then
  previous=
  compose_files=()
  for argument in "$@"; do
    if [ "$previous" = -f ] && [ ! -f "$argument" ]; then
      printf 'missing Compose file: %s\n' "$argument" >&2
      exit 1
    fi
    [ "$previous" != -f ] || compose_files+=("$argument")
    previous=$argument
  done
  case " $* " in
    *' config --services '*)
      printf '%s\n' caddy collector grafana victorialogs victoriametrics
      [ "${TELEMETRY_TEST_RESOLUTION_ACTIVE:-}" = 1 ] || printf '%s\n' alertmanager
      ;;
    *' config --format json '*)
      python3 - "${compose_files[@]}" <<'PY'
import json
import re
import sys

services = {}
for compose_file in sys.argv[1:]:
    current = None
    for line in open(compose_file, encoding="utf-8"):
        service = re.match(r"^  ([A-Za-z0-9][A-Za-z0-9_-]*):\s*$", line)
        if service:
            current = service.group(1)
            services.setdefault(current, {})
            continue
        if current is None:
            continue
        image = re.match(r"^    image: ([^\s]+)\s*$", line)
        if image:
            services[current]["image"] = image.group(1)
        if re.match(r"^    build:\s*$", line):
            services[current]["build"] = {"context": "./grafana"}
print(json.dumps({"services": services}))
PY
      ;;
  esac
  exit 0
fi
printf 'unexpected docker command: %s\n' "$*" >&2
exit 1
EOF
  chmod 700 "$path"
}

run_resolution_suite() {
  local sandbox fixture_pid fixture_port fixture_log output docker_dir release_target requests implicit_identity explicit_identity
  local release_dist rendered_compose
  local caddy_reference collector_reference vlogs_reference vmetrics_reference

  [ -x "$update_script" ] || fail 'update-backend.sh does not exist or is not executable'
  rendered_compose=$(docker compose --env-file "$backend_dir/.env.example" \
    -f "$backend_dir/docker-compose.yml" config --format json)
  caddy_reference=$(jq -r '.services.caddy.image' <<<"$rendered_compose")
  collector_reference=$(jq -r '.services.collector.image' <<<"$rendered_compose")
  vlogs_reference=$(jq -r '.services.victorialogs.image' <<<"$rendered_compose")
  vmetrics_reference=$(jq -r '.services.victoriametrics.image' <<<"$rendered_compose")
  for reference in "$caddy_reference" "$collector_reference" "$vlogs_reference" "$vmetrics_reference"; do
    [[ $reference =~ @sha256:[0-9a-f]{64}$ ]] || fail "current backend image is not digest-pinned: $reference"
  done
  export TELEMETRY_TEST_CURRENT_CADDY_REFERENCE=$caddy_reference
  export TELEMETRY_TEST_CURRENT_COLLECTOR_REFERENCE=$collector_reference
  export TELEMETRY_TEST_CURRENT_VLOGS_REFERENCE=$vlogs_reference
  export TELEMETRY_TEST_CURRENT_VMETRICS_REFERENCE=$vmetrics_reference
  export TELEMETRY_TEST_CURRENT_CADDY_DIGEST_REFERENCE="caddy@${caddy_reference##*@}"
  export TELEMETRY_TEST_CURRENT_COLLECTOR_DIGEST_REFERENCE="otel/opentelemetry-collector-contrib@${collector_reference##*@}"
  export TELEMETRY_TEST_CURRENT_VLOGS_DIGEST_REFERENCE="victoriametrics/victoria-logs@${vlogs_reference##*@}"
  export TELEMETRY_TEST_CURRENT_VMETRICS_DIGEST_REFERENCE="victoriametrics/victoria-metrics@${vmetrics_reference##*@}"
  export TELEMETRY_TEST_STAGE_ONLY=1
  sandbox=$(mktemp -d /tmp/telemetry-resolution.XXXXXX)
  trap 'kill "${fixture_pid:-}" >/dev/null 2>&1 || true; rm -rf "${sandbox:-}"' EXIT HUP INT TERM
  release_dist=$sandbox/release-dist
  bash "$backend_dir/../scripts/package-backend-release.sh" "$backend_dir/.." "$release_dist"
  fixture_log=$sandbox/fixture.log
  python3 "$backend_dir/tests/fixtures/maintenance-http-fixture.py" --port 0 \
    --release-archive "$release_dist/ai-agent-telemetry-backend.tar.gz" >"$fixture_log" 2>&1 &
  fixture_pid=$!
  for _ in $(seq 1 50); do
    [ -s "$fixture_log" ] && break
    sleep 0.05
  done
  fixture_port=$(head -n 1 "$fixture_log")
  [[ $fixture_port =~ ^[0-9]+$ ]] || fail 'HTTP fixture did not report a port'
  mkdir -p "$sandbox/backend" "$sandbox/tmp"
  docker_dir=$sandbox/bin
  mkdir -p "$docker_dir"
  write_resolution_docker "$docker_dir/docker"
  if PATH="$docker_dir:$PATH" docker image inspect otel/unapproved-collector:0.119.0 >/dev/null 2>&1; then
    fail 'resolution Docker stub accepted an unknown repository path'
  fi
  if PATH="$docker_dir:$PATH" docker image inspect caddy:unapproved >/dev/null 2>&1; then
    fail 'resolution Docker stub accepted an unknown product tag'
  fi
  if PATH="$docker_dir:$PATH" docker image inspect caddy@sha256:unknown-digest >/dev/null 2>&1; then
    fail 'resolution Docker stub accepted an unknown repository digest'
  fi
  if grep -F 'read_bytes(' "$update_script" >/dev/null; then
    fail 'normalized content hashing reads a retained source file into memory'
  fi
  mkdir "$sandbox/backend/current"
  cp "$backend_dir/docker-compose.yml" "$sandbox/backend/current/docker-compose.yml"
  cp "$backend_dir/.env.example" "$sandbox/backend/current/.env"
  ln -s current "$sandbox/backend/latest"
  run_update() {
    env TMPDIR="$sandbox/tmp" PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 \
      TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
      TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" "$@"
  }
  if output=$(TELEMETRY_TEST_FAIL_TEMP_AT=2 run_update --ref main 2>&1); then
    fail 'injected second temporary-directory allocation was accepted'
  fi
  compgen -G "$sandbox/tmp/*" >/dev/null && fail 'failed temporary allocation left an unregistered directory'
  if output=$(PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
    TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" --ref main 2>&1); then
    :
  else
    fail "resolution failed: $output"
  fi
  [ -d "$sandbox/backend/0123456789ab" ] || fail 'source resolution did not stage the full commit target'
  [ -f "$sandbox/backend/0123456789ab/.deployment-manifest" ] || fail 'staged release lacks a manifest'
  grep -Fx 'resolved_identity=0123456789abcdef0123456789abcdef01234567' \
    "$sandbox/backend/0123456789ab/.deployment-manifest" >/dev/null || fail 'manifest lost the resolved SHA'
  grep -F 'image.caddy=' "$sandbox/backend/0123456789ab/.deployment-manifest" >/dev/null ||
    fail 'manifest lacks the pinned Caddy image'
  grep -F 'image.alertmanager=example/alertmanager@sha256:alertmanagerfixture|sha256:alertmanager-local' \
    "$sandbox/backend/0123456789ab/.deployment-manifest" >/dev/null ||
    fail 'manifest lacks the pinned additional registry image'
  grep -Fx 'image.grafana=ai-agent-telemetry-backend-grafana:0123456789ab|sha256:grafana-local' \
    "$sandbox/backend/0123456789ab/.deployment-manifest" >/dev/null ||
    fail 'manifest lacks the deployment-specific Grafana image'
  grep -F 'alertmanager:' "$sandbox/backend/0123456789ab/.maintenance-compose.yml" >/dev/null ||
    fail 'override lacks the additional registry service'
  if output=$(PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 \
    TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
    TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" \
    --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    :
  else
    fail "equivalent immutable source did not reuse the staged target: $output"
  fi
  cp "$sandbox/backend/0123456789ab/.maintenance-compose.yml" "$sandbox/override.original"
  sed 's#example/alertmanager@sha256:alertmanagerfixture#example/alertmanager:mutable#' \
    "$sandbox/override.original" >"$sandbox/backend/0123456789ab/.maintenance-compose.yml"
  if output=$(TELEMETRY_TEST_RESOLUTION_ACTIVE=1 TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 \
    TELEMETRY_TEST_STABILITY_SECONDS=0 run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    fail 'changed effective Compose image was reused'
  fi
  mv "$sandbox/override.original" "$sandbox/backend/0123456789ab/.maintenance-compose.yml"
  if output=$(PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 \
    TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
    TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" --ref v1.2.3 2>&1); then
    :
  else
    fail "release resolution failed: $output"
  fi
  release_target=release-$(printf '%s' v1.2.3 | sha256sum | cut -c1-20)
  grep -Fx 'resolved_identity=v1.2.3' "$sandbox/backend/$release_target/.deployment-manifest" >/dev/null ||
    fail 'release manifest lost its immutable tag identity'
  if output=$(run_update --ref latest 2>&1); then
    :
  else
    fail "latest release resolution failed: $output"
  fi
  if output=$(run_update --ref v1.2.3+build.7 2>&1); then
    :
  else
    fail "SemVer build-metadata release resolution failed: $output"
  fi
  for rejected_release in v1.3.0 v1.3.1 v1.3.2 v1.3.3 v1.3.4 v1.3.5; do
    if output=$(run_update --ref "$rejected_release" 2>&1); then
      fail "hostile root-layout release archive was accepted: $rejected_release"
    fi
  done
  if output=$(run_update --ref 'feature/with space' 2>&1); then
    :
  else
    fail "URI-encoded branch resolution failed: $output"
  fi
  requests=$(curl --fail --silent "http://127.0.0.1:$fixture_port/requests")
  [[ $requests == *'commits/feature%2Fwith%20space'* ]] || fail 'branch path was not URI-encoded'
  if output=$(run_update --ref 01.2.3 2>&1); then
    fail 'invalid SemVer-looking branch was routed to Releases API'
  fi
  [[ $output == *404* ]] || fail 'invalid SemVer-looking branch failed for the wrong reason'
  if output=$(run_update --ref moving 2>&1); then
    :
  else
    fail "first moving branch resolution failed: $output"
  fi
  if output=$(TELEMETRY_TEST_IMAGE_TOCTOU=1 run_update --ref moving 2>&1); then
    fail 'changed repository digest mapping was accepted'
  fi
  [ ! -e "$sandbox/backend/222222222222" ] || fail 'TOCTOU failure published a release'
  compgen -G "$sandbox/tmp/*" >/dev/null && fail 'failed staging left download or helper artifacts'
  compgen -G "$sandbox/backend/.*.staging.*" >/dev/null && fail 'failed staging left a copied environment file'
  if output=$(run_update --ref moving 2>&1); then
    :
  else
    fail "second moving branch resolution failed: $output"
  fi
  grep -Fx 'resolved_identity=1111111111111111111111111111111111111111' \
    "$sandbox/backend/111111111111/.deployment-manifest" >/dev/null || fail 'moving branch changed an existing target identity'
  for rejected_ref in traversal link canonical unexpected special reserved active; do
    if output=$(run_update --ref "$rejected_ref" 2>&1); then
      fail "hostile archive was accepted: $rejected_ref"
    fi
  done
  if output=$(run_update --ref malformed 2>&1); then
    fail 'malformed API response was accepted'
  fi
  compgen -G "$sandbox/tmp/*" >/dev/null && fail 'failed API parsing left temporary response artifacts'
  if output=$(run_update --ref implicit 2>&1); then
    :
  else
    fail "implicit-directory archive failed: $output"
  fi
  if output=$(run_update --ref explicit 2>&1); then
    :
  else
    fail "explicit-directory archive failed: $output"
  fi
  implicit_identity=$(sed -n 's/^content_identity=//p' "$sandbox/backend/bbbbbbbbbbbb/.deployment-manifest")
  explicit_identity=$(sed -n 's/^content_identity=//p' "$sandbox/backend/cccccccccccc/.deployment-manifest")
  [ "$implicit_identity" = "$explicit_identity" ] || fail 'equivalent archive directory headers changed content identity'
  if output=$(run_update --ref large 2>&1); then
    :
  else
    fail "large-source archive failed streaming staging: $output"
  fi
  ln -s missing "$sandbox/backend/999999999999"
  if output=$(run_update --ref collision 2>&1); then
    fail 'existing target symlink was replaced'
  fi
  [ -L "$sandbox/backend/999999999999" ] || fail 'existing target symlink was clobbered'
  ln -s 0123456789ab "$sandbox/backend/.latest.test"
  mv -Tf "$sandbox/backend/.latest.test" "$sandbox/backend/latest"
  if output=$(run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    fail 'active target without marker was accepted'
  fi
  printf 'format=1\ntransaction_id=fixture-active\nmanifest_sha256=%s\n' \
    "$(sha256sum "$sandbox/backend/0123456789ab/.deployment-manifest" | cut -d' ' -f1)" \
    >"$sandbox/backend/0123456789ab/.deployment-success"
  sed -n '/^resolved_identity=/p; /^content_identity=/p' \
    "$sandbox/backend/0123456789ab/.deployment-manifest" >>"$sandbox/backend/0123456789ab/.deployment-success"
  for service in caddy collector grafana victorialogs victoriametrics; do
    sed -n "/^image\.$service=/p" "$sandbox/backend/0123456789ab/.deployment-manifest" \
      >>"$sandbox/backend/0123456789ab/.deployment-success"
  done
  chmod 600 "$sandbox/backend/0123456789ab/.deployment-success"
  if output=$(TELEMETRY_TEST_RESOLUTION_ACTIVE=1 TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 \
    TELEMETRY_TEST_STABILITY_SECONDS=0 run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    :
  else
    fail "active target marker and health validation failed: $output"
  fi
  rm "$sandbox/backend/latest"
  ln -s "$sandbox/backend/0123456789ab" "$sandbox/backend/latest"
  if output=$(TELEMETRY_TEST_RESOLUTION_ACTIVE=1 TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 \
    TELEMETRY_TEST_STABILITY_SECONDS=0 run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    fail 'active target accepted an absolute latest link'
  fi
  rm "$sandbox/backend/latest"
  ln -s ./0123456789ab "$sandbox/backend/latest"
  if output=$(TELEMETRY_TEST_RESOLUTION_ACTIVE=1 TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 \
    TELEMETRY_TEST_STABILITY_SECONDS=0 run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    fail 'active target accepted a multi-component latest link'
  fi
  rm "$sandbox/backend/latest"
  ln -s current "$sandbox/backend/latest"
  printf '%s\n' corrupt >"$sandbox/backend/0123456789ab/.normalized-content-checksums"
  if output=$(run_update --ref 0123456789abcdef0123456789abcdef01234567 2>&1); then
    fail 'damaged inactive staging target was reused'
  fi
  for rejected_ref in v1.2.4 v1.2.5 v1.2.6; do
    if output=$(PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 \
      TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
      TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" --ref "$rejected_ref" 2>&1); then
      fail "invalid release response was accepted: $rejected_ref"
    fi
  done
  if output=$(env PATH="$docker_dir:$PATH" TELEMETRY_MAINTENANCE_TEST_MODE=1 \
    TELEMETRY_TEST_BACKEND_ROOT="$sandbox/backend" TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
    TELEMETRY_GITHUB_API_URL="http://127.0.0.1:$fixture_port/api" "$update_script" --ref eacf978 2>&1); then
    fail 'unresolved commit ref was accepted'
  fi
  [[ $output == *'404'* ]] || fail 'unresolved commit ref failed for the wrong reason'
  kill "$fixture_pid" >/dev/null 2>&1 || true
  wait "$fixture_pid" 2>/dev/null || true
  trap - EXIT HUP INT TERM
  rm -rf "$sandbox"
  printf '%s\n' 'PASS: maintenance resolution contract'
}

cleanup_real_activation_sandbox() {
  local sandbox=${1:-} project=${2:-} original_alpine_id=${3:-} previous_release=${4:-} target_release=${5:-}
  local -a containers volumes networks transaction_ids

  [ -n "$project" ] || return 0
  if [ -n "$sandbox" ] && [ -f "$sandbox/commands.log" ]; then
    mapfile -t transaction_ids < <(sed -n 's/^HELPERS_ABSENT=//p' "$sandbox/commands.log" | sort -u)
  fi
  mapfile -t containers < <(docker ps -aq --filter "label=com.docker.compose.project=$project")
  [ "${#containers[@]}" -eq 0 ] || docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
  mapfile -t volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#volumes[@]}" -eq 0 ] || docker volume rm "${volumes[@]}" >/dev/null 2>&1 || true
  mapfile -t networks < <(docker network ls -q --filter "label=com.docker.compose.project=$project")
  [ "${#networks[@]}" -eq 0 ] || docker network rm "${networks[@]}" >/dev/null 2>&1 || true
  assert_task_resources_absent "$project" "${transaction_ids[@]}"
  [ -z "$original_alpine_id" ] || docker tag "$original_alpine_id" alpine:3.20 >/dev/null 2>&1 || true
  docker image rm "task5-mutable:$project" "$project-grafana:$previous_release" \
    "$project-grafana:$target_release" "alpine:task5-original-$project" >/dev/null 2>&1 || true
  [ -z "$sandbox" ] || rm -rf "$sandbox"
}

run_activation_real_suite() {
  local sandbox backend_root source_root backup_root docker_dir project command_log previous_ids output
  local original_alpine_id mutable_container mutable_id target_grafana_id backup_dir real_docker
  local previous_release target_release
  local alpine_digest='alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc'
  local json_image='ghcr.io/jqlang/jq:1.7.1@sha256:096b83865ad59b5b02841f103f83f45c51318394331bf1995e187ea3be937432'

  docker info >/dev/null 2>&1 || fail 'real-Docker activation contract requires a running Docker daemon'
  docker pull "$alpine_digest" >/dev/null ||
    fail 'real-Docker activation setup could not pull the pinned Alpine helper image'
  docker pull "$json_image" >/dev/null ||
    fail 'real-Docker activation setup could not pull the pinned JSON helper image'
  docker image inspect "$alpine_digest" >/dev/null 2>&1 ||
    fail 'real-Docker activation setup requires the pinned Alpine helper image'
  docker image inspect "$json_image" >/dev/null 2>&1 ||
    fail 'real-Docker activation setup requires the pinned JSON helper image'
  real_docker=$(command -v docker)
  original_alpine_id=$(docker image inspect --format '{{.Id}}' "$alpine_digest")
  docker tag "$original_alpine_id" alpine:3.20
  sandbox=$(mktemp -d /tmp/telemetry-activation-real.XXXXXX)
  project="telemetry_activation_real_$RANDOM$RANDOM"
  previous_release="previous-$RANDOM$RANDOM"
  target_release="target-$RANDOM$RANDOM"
  trap 'cleanup_real_activation_sandbox "${sandbox:-}" "${project:-}" "${original_alpine_id:-}" \
    "${previous_release:-}" "${target_release:-}"' EXIT HUP INT TERM
  docker tag "$original_alpine_id" "alpine:task5-original-$project"
  backend_root=$sandbox/backend
  source_root=$sandbox/target-source
  backup_root=$sandbox/backups
  docker_dir=$sandbox/bin
  command_log=$sandbox/commands.log
  previous_ids=$sandbox/previous-images
  mkdir -p "$backend_root/$previous_release" "$source_root/grafana" "$backup_root" "$docker_dir" \
    "$sandbox/docker-config"
  : >"$command_log"
  : >"$backend_root/$previous_release/.env"
  cat >"$backend_root/$previous_release/docker-compose.yml" <<'EOF'
services:
  caddy:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [caddy-config:/caddy-config, caddy-data:/caddy-data]
  collector:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
  grafana:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [grafana-data:/grafana-data]
  victorialogs:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vlogs-data:/vlogs-data]
  victoriametrics:
    image: alpine:3.20
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vmetrics-data:/vmetrics-data]
volumes:
  caddy-config: {}
  caddy-data: {}
  grafana-data: {}
  vlogs-data: {}
  vmetrics-data: {}
EOF
  ln -s "$previous_release" "$backend_root/latest"
  docker compose --project-name "$project" --project-directory "$backend_root/$previous_release" \
    --env-file "$backend_root/$previous_release/.env" -f "$backend_root/$previous_release/docker-compose.yml" \
    up -d --pull never
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    docker run --rm --mount "type=volume,src=${project}_${logical},dst=/data" "$alpine_digest" \
      sh -eu -c "mkdir -p /data/sentinel && printf '%s' '$logical' > /data/sentinel/$logical"
  done
  for service in caddy collector grafana victorialogs victoriametrics; do
    container=$(docker ps -q --filter "label=com.docker.compose.project=$project" \
      --filter "label=com.docker.compose.service=$service")
    printf '%s=%s\n' "$service" "$(docker inspect --format '{{.Image}}' "$container")"
  done >"$previous_ids"

  mutable_container=$(docker create "$alpine_digest" true)
  docker commit --change "LABEL task5.mutable=$project" "$mutable_container" "task5-mutable:$project" >/dev/null
  docker rm "$mutable_container" >/dev/null
  docker tag "task5-mutable:$project" alpine:3.20
  mutable_id=$(docker image inspect --format '{{.Id}}' alpine:3.20)
  [ "$mutable_id" != "$original_alpine_id" ] || fail 'real-Docker fixture did not retarget its mutable image tag'

  cat >"$source_root/docker-compose.yml" <<EOF
services:
  caddy:
    image: $alpine_digest
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [caddy-config:/caddy-config, caddy-data:/caddy-data]
  collector:
    image: $alpine_digest
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
  grafana:
    build:
      context: ./grafana
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [grafana-data:/grafana-data]
  victorialogs:
    image: $alpine_digest
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vlogs-data:/vlogs-data]
  victoriametrics:
    image: $alpine_digest
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vmetrics-data:/vmetrics-data]
volumes:
  caddy-config: {}
  caddy-data: {}
  grafana-data: {}
  vlogs-data: {}
  vmetrics-data: {}
EOF
  cat >"$source_root/grafana/Dockerfile" <<EOF
FROM $alpine_digest
LABEL task5.grafana=target-real
EOF
  printf '%s\n' fixture-content >"$sandbox/target.checksums"
  cat >"$docker_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >>"$TELEMETRY_TEST_COMMAND_LOG"
if [ "$1" = pull ]; then
  "$REAL_DOCKER" image inspect "$2" >/dev/null
  exit 0
fi
exec "$REAL_DOCKER" "$@"
EOF
  chmod 700 "$docker_dir/docker"

  if output=$(
    export PATH="$docker_dir:$PATH" REAL_DOCKER="$real_docker" TELEMETRY_TEST_COMMAND_LOG="$command_log"
    export DOCKER_CONFIG="$sandbox/docker-config"
    export TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$backend_root"
    export TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project"
    export TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_SKIP_REMOTE_PROBES=1
    export TELEMETRY_TEST_STABILITY_SECONDS=0 TELEMETRY_TEST_HEALTH_ATTEMPTS=1
    export TELEMETRY_TEST_FORCE_TARGET_UNHEALTHY="$target_release" TELEMETRY_TEST_REAL_SOURCE="$source_root"
    export TELEMETRY_TEST_REAL_CHECKSUMS="$sandbox/target.checksums" TELEMETRY_TEST_REAL_TARGET="$target_release"
    bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      resolve_source() {
        kind=commit
        requested_ref=$1
        resolved_identity=1111111111111111111111111111111111111111
        content_identity=real-docker-content
        transport_checksum=real-docker-transport
        download_url=local-fixture
        target_id=$TELEMETRY_TEST_REAL_TARGET
        RESOLVED_BACKEND_DIR=$TELEMETRY_TEST_REAL_SOURCE
        RESOLVED_CHECKSUMS_FILE=$TELEMETRY_TEST_REAL_CHECKSUMS
      }
      json_query() {
        python3 - "$1/response.json" "$2" <<"PY"
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    document = json.load(source)
query = sys.argv[2]
if "to_entries[]" in query:
    for name, service in document["services"].items():
        print(json.dumps({"service": name, "image": service.get("image"), "build": "build" in service}))
elif query == ".service":
    print(document["service"])
elif ".image" in query:
    print(document.get("image") or "")
elif query == ".build":
    print(str(document["build"]).lower())
elif "RepoDigests[]" in query:
    for digest in document[0]["RepoDigests"]:
        print(digest)
elif ".[0].Id" in query:
    print(document[0]["Id"])
else:
    raise SystemExit(f"unsupported query: {query}")
PY
      }
      update_main --ref local-fixture
    ' "$update_script" 2>&1
  ); then
    fail 'forced real-Docker target health failure unexpectedly committed'
  fi
  [[ $output == *"restored $previous_release"* ]] || fail "real-Docker rollback failed: $output"
  [ "$(readlink "$backend_root/latest")" = "$previous_release" ] ||
    fail 'real-Docker rollback did not restore latest'
  [ ! -f "$backend_root/.maintenance-transaction" ] || fail 'healthy real-Docker rollback retained its transaction'
  assert_container_image_ids "$project" "$previous_ids"
  for logical in caddy-config caddy-data grafana-data vlogs-data vmetrics-data; do
    assert_volume_file "$project" "$logical" "/sentinel/$logical"
  done
  backup_dir=$(single_completed_backup "$backup_root")
  [ -f "$backup_dir/SHA256SUMS" ] || fail 'real-Docker rollback did not retain its verified backup'
  [ -f "$backend_root/$target_release/.deployment-manifest" ] ||
    fail 'real-Docker rollback removed the target manifest'
  target_grafana_id=$(sed -n 's/^image\.grafana=.*|//p' "$backend_root/$target_release/.deployment-manifest")
  [ -n "$target_grafana_id" ] && [ "$target_grafana_id" != "$original_alpine_id" ] ||
    fail 'real-Docker target Grafana image was not rebuilt to a distinct ID'
  export TELEMETRY_TEST_COMMAND_LOG="$command_log"
  assert_no_registry_event_after COMPOSE_DOWN
  cleanup_real_activation_sandbox "$sandbox" "$project" "$original_alpine_id" "$previous_release" "$target_release"
  trap - EXIT HUP INT TERM
  printf '%s\n' 'PASS: maintenance real-Docker activation contract'
}

containers_for_transaction() {
  local transaction_id=$1 role
  local -a containers=()

  for role in backup health; do
    mapfile -t role_containers < <(docker ps -aq \
      --filter "label=io.qubership.ai-agent-telemetry.maintenance.transaction=$transaction_id" \
      --filter "label=io.qubership.ai-agent-telemetry.maintenance.role=$role")
    containers+=("${role_containers[@]}")
  done
  [ "${#containers[@]}" -eq 0 ] || printf '%s\n' "${containers[@]}" | sort -u
}

single_container_for_transaction() {
  local transaction_id=$1
  local -a containers

  mapfile -t containers < <(containers_for_transaction "$transaction_id")
  [ "${#containers[@]}" -eq 1 ] || fail "expected one helper for transaction $transaction_id"
  printf '%s\n' "${containers[0]}"
}

assert_container_absent() {
  local container=$1

  if docker inspect "$container" >/dev/null 2>&1; then
    fail "container still exists after transaction recovery: $container"
  fi
}

wait_for_kill_checkpoint() {
  local phase=$1 maintenance_pid=$2 event_log=$3 attempt

  for ((attempt = 1; attempt <= 200; attempt++)); do
    grep -Fx "KILL_AT=$phase" "$event_log" >/dev/null 2>&1 && return 0
    if ! kill -0 "$maintenance_pid" >/dev/null 2>&1; then
      [ -z "${checkpoint_child_output:-}" ] || sed -n '1,240p' "$checkpoint_child_output" >&2
      fail "maintenance process exited before the $phase checkpoint"
    fi
    sleep 0.05
  done
  fail "maintenance process did not reach the $phase checkpoint"
}

assert_event_before() {
  local first=$1 second=$2 event_log=$3 first_line second_line

  first_line=$(grep -n -m1 -Fx "$first" "$event_log" | cut -d: -f1 || true)
  second_line=$(grep -n -m1 -Fx "$second" "$event_log" | cut -d: -f1 || true)
  [ -n "$first_line" ] && [ -n "$second_line" ] && [ "$first_line" -lt "$second_line" ] ||
    fail "$first did not precede $second"
}

cleanup_recovery_sandbox() {
  local sandbox=${1:-} project=${2:-} transaction_id=${3:-}
  local unrelated_container=${4:-} same_transaction_other_role=${5:-} role
  local -a containers volumes networks

  [ -z "${maintenance_pid:-}" ] || kill -KILL "$maintenance_pid" >/dev/null 2>&1 || true
  [ -z "$unrelated_container" ] || docker rm -f "$unrelated_container" >/dev/null 2>&1 || true
  [ -z "$same_transaction_other_role" ] ||
    docker rm -f "$same_transaction_other_role" >/dev/null 2>&1 || true
  if [ -n "$transaction_id" ]; then
    for role in backup health; do
      mapfile -t containers < <(docker ps -aq \
        --filter "label=io.qubership.ai-agent-telemetry.maintenance.transaction=$transaction_id" \
        --filter "label=io.qubership.ai-agent-telemetry.maintenance.role=$role")
      [ "${#containers[@]}" -eq 0 ] || docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
    done
  fi
  if [ -n "$project" ]; then
    mapfile -t containers < <(docker ps -aq --filter "label=com.docker.compose.project=$project")
    [ "${#containers[@]}" -eq 0 ] || docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
    mapfile -t volumes < <(docker volume ls -q --filter "label=com.docker.compose.project=$project")
    [ "${#volumes[@]}" -eq 0 ] || docker volume rm "${volumes[@]}" >/dev/null 2>&1 || true
    mapfile -t networks < <(docker network ls -q --filter "label=com.docker.compose.project=$project")
    [ "${#networks[@]}" -eq 0 ] || docker network rm "${networks[@]}" >/dev/null 2>&1 || true
    assert_task_resources_absent "$project" "$transaction_id"
  fi
  [ -z "$sandbox" ] || rm -rf "$sandbox"
}

run_recovery_suite() {
  local sandbox backend_root backup_root project event_log previous_release=previous target_release=target
  local alpine_digest alpine_id service_image=alpine:3.20 transaction_id helper_id duplicate_helper
  local unrelated_container
  local same_transaction_other_role
  local maintenance_pid phase expected_active service
  local -a transaction_entries task_transaction_ids

  docker info >/dev/null 2>&1 || fail 'recovery contract requires a running local Docker daemon'
  alpine_digest='docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc'
  docker pull "$alpine_digest" >/dev/null ||
    fail 'recovery contract could not pull the pinned Alpine helper image'
  docker image inspect "$alpine_digest" >/dev/null 2>&1 ||
    fail 'recovery contract requires the pinned Alpine helper image'
  alpine_id=$(docker image inspect --format '{{.Id}}' "$alpine_digest")
  docker tag "$alpine_id" "$service_image"
  sandbox=$(mktemp -d /tmp/telemetry-recovery.XXXXXX)
  backend_root=$sandbox/backend
  backup_root=$sandbox/backups
  project="telemetry_recovery_$RANDOM$RANDOM"
  event_log=$sandbox/events.log
  transaction_id=
  unrelated_container=
  same_transaction_other_role=
  maintenance_pid=
  trap 'cleanup_recovery_sandbox "${sandbox:-}" "${project:-}" "${transaction_id:-}" \
    "${unrelated_container:-}" "${same_transaction_other_role:-}"' EXIT HUP INT TERM
  mkdir -p "$backend_root/$previous_release" "$backend_root/$target_release" "$backup_root/pre-fixture"
  : >"$event_log"
  for release in "$previous_release" "$target_release"; do
    cat >"$backend_root/$release/docker-compose.yml" <<EOF
services:
  caddy:
    image: $service_image
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [caddy-config:/caddy-config, caddy-data:/caddy-data]
  collector:
    image: $service_image
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
  grafana:
    image: $service_image
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [grafana-data:/grafana-data]
  victorialogs:
    image: $service_image
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vlogs-data:/vlogs-data]
  victoriametrics:
    image: $service_image
    command: ["sh", "-c", "while :; do sleep 3600; done"]
    stop_grace_period: 1s
    volumes: [vmetrics-data:/vmetrics-data]
volumes:
  caddy-config: {}
  caddy-data: {}
  grafana-data: {}
  vlogs-data: {}
  vmetrics-data: {}
EOF
    : >"$backend_root/$release/.env"
    {
      printf 'services:\n'
      for service in caddy collector grafana victorialogs victoriametrics; do
        printf '  %s:\n    image: %s\n' "$service" "$service_image"
      done
    } >"$backend_root/$release/.maintenance-compose.yml"
  done
  {
    printf '%s\n' 'format=1' 'kind=commit' 'requested_ref=recovery-fixture'
    printf '%s\n' 'resolved_identity=1111111111111111111111111111111111111111'
    printf '%s\n' 'content_identity=recovery-content' 'transport_checksum=recovery-transport'
    printf '%s\n' 'download_url=local-recovery-fixture'
    for service in caddy collector grafana victorialogs victoriametrics; do
      printf 'image.%s=%s|%s\n' "$service" "$service_image" "$alpine_id"
    done
  } >"$backend_root/$target_release/.deployment-manifest"
  chmod 600 "$backend_root/$target_release/.deployment-manifest"
  ln -s "$previous_release" "$backend_root/latest"
  docker compose --project-name "$project" --project-directory "$backend_root/$previous_release" \
    --env-file "$backend_root/$previous_release/.env" -f "$backend_root/$previous_release/docker-compose.yml" \
    -f "$backend_root/$previous_release/.maintenance-compose.yml" up -d --no-build --pull never

  export TELEMETRY_TEST_SKIP_REMOTE_PROBES=1 TELEMETRY_TEST_STABILITY_SECONDS=0 TELEMETRY_TEST_HEALTH_ATTEMPTS=1
  # shellcheck disable=SC2016 # The child shell uses the exported target to run the public update path.
  env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$backend_root" \
    TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
    TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$event_log" \
    TELEMETRY_TEST_KILL_AT=backup-helper-running TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS=120 \
    TELEMETRY_TEST_RECOVERY_TARGET="$target_release" bash -c '
      TELEMETRY_SOURCE_ONLY=1 source "$0"
      resolve_source() { target_id=$TELEMETRY_TEST_RECOVERY_TARGET; }
      stage_release() { :; }
      update_main --ref recovery-fixture
    ' "$update_script" >"$sandbox/backup-crash.out" 2>&1 &
  maintenance_pid=$!
  checkpoint_child_output=$sandbox/backup-crash.out
  wait_for_kill_checkpoint backup-helper-running "$maintenance_pid" "$event_log"
  transaction_id=$(sed -n 's/^TRANSACTION_ID=//p' "$backend_root/.maintenance-transaction")
  task_transaction_ids+=("$transaction_id")
  helper_id=$(single_container_for_transaction "$transaction_id")
  unrelated_container=$(docker run -d --label \
    'io.qubership.ai-agent-telemetry.maintenance.transaction=unrelated-recovery' --label \
    'io.qubership.ai-agent-telemetry.maintenance.role=backup' "$alpine_digest" sleep 3600)
  same_transaction_other_role=$(docker run -d --label \
    "io.qubership.ai-agent-telemetry.maintenance.transaction=$transaction_id" --label \
    'io.qubership.ai-agent-telemetry.maintenance.role=unrelated' "$alpine_digest" sleep 3600)
  kill -KILL "$maintenance_pid"
  wait "$maintenance_pid" 2>/dev/null || true
  maintenance_pid=
  [ -n "$helper_id" ] || fail 'forced backup death did not leave a transaction helper'
  : >"$event_log"
  run_recovery() {
    # shellcheck disable=SC2016 # The child shell reads the exported recovery target.
    env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$backend_root" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$event_log" \
      TELEMETRY_TEST_STAGE_ONLY=1 TELEMETRY_TEST_RECOVERY_TARGET="$target_release" \
      bash -c '
        TELEMETRY_SOURCE_ONLY=1 source "$0"
        resolve_source() { target_id=$TELEMETRY_TEST_RECOVERY_TARGET; }
        stage_release() { :; }
        update_main --ref recovery-fixture
      ' "$update_script"
  }
  duplicate_helper=$(docker run -d --label \
    "io.qubership.ai-agent-telemetry.maintenance.transaction=$transaction_id" --label \
    'io.qubership.ai-agent-telemetry.maintenance.role=health' "$alpine_digest" sleep 3600)
  run_fails run_recovery
  if grep -Fx COMPOSE_UP "$event_log" >/dev/null; then
    fail 'ambiguous helper recovery restarted the previous stack'
  fi
  docker rm -f "$duplicate_helper" >/dev/null
  : >"$event_log"
  run_recovery
  [ -z "$(containers_for_transaction "$transaction_id")" ] ||
    fail 'recovery retained an allowed-role transaction helper'
  assert_container_absent "$helper_id"
  docker inspect "$unrelated_container" >/dev/null || fail 'recovery removed an unrelated transaction helper'
  docker inspect "$same_transaction_other_role" >/dev/null || fail 'recovery removed a same-transaction unrelated role'
  assert_event_before "HELPERS_ABSENT=$transaction_id" COMPOSE_UP "$event_log"
  assert_services_running "$project" "$backend_root/$previous_release"
  compgen -G "$backup_root/pre-target-*.incomplete" >/dev/null ||
    fail 'recovery removed the interrupted backup'
  [ ! -f "$backend_root/.maintenance-transaction" ] || fail 'healthy backup recovery retained its transaction'
  docker rm -f "$unrelated_container" "$same_transaction_other_role" >/dev/null
  unrelated_container=
  same_transaction_other_role=

  for phase in activation-prepared symlink-replaced target-started health-passed success-marker-written committed-written; do
    docker compose --project-name "$project" --project-directory "$backend_root/$target_release" \
      --env-file "$backend_root/$target_release/.env" -f "$backend_root/$target_release/docker-compose.yml" \
      -f "$backend_root/$target_release/.maintenance-compose.yml" down >/dev/null 2>&1 || true
    rm -f "$backend_root/latest" "$backend_root/$target_release/.deployment-success"
    ln -s "$previous_release" "$backend_root/latest"
    docker compose --project-name "$project" --project-directory "$backend_root/$previous_release" \
      --env-file "$backend_root/$previous_release/.env" -f "$backend_root/$previous_release/docker-compose.yml" \
      -f "$backend_root/$previous_release/.maintenance-compose.yml" up -d --no-build --pull never >/dev/null
    transaction_id="recovery-$phase"
    task_transaction_ids+=("$transaction_id")
    transaction_entries=("FORMAT_VERSION=1" "TRANSACTION_ID=$transaction_id" 'OPERATION=update' \
      'PHASE=activation-prepared' "PREVIOUS_RELEASE=$previous_release" "TARGET_RELEASE=$target_release" \
      "BACKUP_PATH=$backup_root/pre-fixture")
    for service in caddy collector grafana victorialogs victoriametrics; do
      transaction_entries+=("PREVIOUS_IMAGE_${service^^}=$alpine_id" "TARGET_IMAGE_${service^^}=$alpine_id")
    done
    # shellcheck disable=SC2016 # The child shell receives transaction entries as positional parameters.
    env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$backend_root" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" \
      bash -c '
        TELEMETRY_SOURCE_ONLY=1 source "$0"
        maintenance_init
        write_transaction "$@"
      ' "$backup_script" "${transaction_entries[@]}"
    : >"$event_log"
    # shellcheck disable=SC2016 # The child shell sources the script path passed as $0.
    env TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$backend_root" \
      TELEMETRY_TEST_BACKUP_ROOT="$backup_root" TELEMETRY_TEST_PROJECT_NAME="$project" \
      TELEMETRY_TEST_LOCK_FILE="$sandbox/maintenance.lock" TELEMETRY_TEST_COMMAND_LOG="$event_log" \
      TELEMETRY_TEST_KILL_AT="$phase" \
      bash -c '
        TELEMETRY_SOURCE_ONLY=1 source "$0"
        maintenance_init
        acquire_maintenance_lock
        activate_transaction
      ' "$update_script" >"$sandbox/$phase.out" 2>&1 &
    maintenance_pid=$!
    checkpoint_child_output=$sandbox/$phase.out
    wait_for_kill_checkpoint "$phase" "$maintenance_pid" "$event_log"
    kill -KILL "$maintenance_pid"
    wait "$maintenance_pid" 2>/dev/null || true
    maintenance_pid=
    : >"$event_log"
    run_recovery
    case "$phase" in
      success-marker-written|committed-written) expected_active=$target_release ;;
      *) expected_active=$previous_release ;;
    esac
    [ "$(readlink "$backend_root/latest")" = "$expected_active" ] ||
      fail "$phase recovery selected the wrong active release"
    assert_services_running "$project" "$backend_root/$expected_active"
    [ ! -f "$backend_root/.maintenance-transaction" ] || fail "$phase recovery retained its transaction"
    [ -z "$(containers_for_transaction "$transaction_id")" ] ||
      fail "$phase recovery retained a transaction helper"
  done

  cleanup_recovery_sandbox "$sandbox" "$project" "$transaction_id" '' ''
  assert_task_resources_absent "$project" "${task_transaction_ids[@]}"
  trap - EXIT HUP INT TERM
  printf '%s\n' 'PASS: maintenance recovery contract'
}

run_retention_suite() {
  local sandbox fixture backup_root current_backup old_backup control_backup newer_backup newest_backup
  local incomplete_backup malformed_backup symlink_backup regular_backup output old_timestamp
  local newest_race first_race second_race third_race fourth_race race_hook
  local root_race root_candidate root_hook root_replacement root_original
  local replacement_race replacement_candidate replacement_saved replacement_hook
  local quarantine_race quarantine_candidate quarantine_saved quarantine_hook
  local nested_race nested_candidate nested_hook nested_outside nested_marker
  local cursor_race cursor_candidate cursor_hook cursor_state cursor_quarantine cursor_tombstone
  local final_race final_candidate final_hook final_marker
  local leaf_race leaf_candidate leaf_hook leaf_saved leaf_tombstone
  local final_operation_race final_operation_candidate final_operation_hook final_operation_saved final_operation_tombstone
  local hardlink_race hardlink_candidate hardlink_outside
  local partial_stop partial_candidate partial_state
  local payload_stop payload_candidate payload_state
  local tombstoned_stop tombstoned_candidate tombstoned_state tombstoned_tombstone
  local completed_stop completed_candidate completed_state completed_tombstone
  local malicious_original malicious_quarantine malicious_claim malicious_state_name
  local claimed_stop claimed_candidate claimed_state deleting_stop deleting_candidate deleting_state
  local corrupt_state corrupt_old unknown_claim
  local state_phase state_boundary unique_temp unique_quarantine unique_state_temp unique_claim
  local ledger_cleanup ledger_entry

  python3 - "$update_script" <<'PY'
import os
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
start = source.index("import calendar\n", source.index("prune_backups()"))
end = source.index("\nPY\n", start)
namespace = {"__name__": "retention_contract"}
original_argv = sys.argv
try:
    sys.argv = ["retention-helper", "", "", "0", "", "", "", "", "", ""]
    exec(compile(source[start:end], sys.argv[0], "exec"), namespace)
finally:
    sys.argv = original_argv

events = []


def record_lseek(directory_fd, offset, whence):
    events.append(("lseek", directory_fd, offset, whence))


def require_rewind(directory_fd):
    expected = ("lseek", directory_fd, 0, os.SEEK_SET)
    if not events or events[-1] != expected:
        raise RuntimeError("directory enumeration was not preceded by a rewind")
    events.append(("listdir", directory_fd))
    return []


namespace["os"].lseek = record_lseek
namespace["os"].listdir = require_rewind

directory_fd = 17
namespace["validate_claimed_tree"](directory_fd, 1)
namespace["remove_claimed_tree"](directory_fd, 1, "", "", "", (), "", [False], {}, set())
namespace["audit_claimed_tree"](directory_fd, 1, {}, set())
namespace["remove_empty_directories"](directory_fd, 1)

expected_events = [
    ("lseek", directory_fd, 0, os.SEEK_SET),
    ("listdir", directory_fd),
    ("lseek", directory_fd, 0, os.SEEK_SET),
    ("listdir", directory_fd),
    ("lseek", directory_fd, 0, os.SEEK_SET),
    ("listdir", directory_fd),
    ("lseek", directory_fd, 0, os.SEEK_SET),
    ("listdir", directory_fd),
]
if events != expected_events:
    raise SystemExit(f"unexpected directory enumeration sequence: {events!r}")
PY

  prepare_retention_fixture() {
    local fixture_root=$1

    mkdir -p "$fixture_root/backend/release" "$fixture_root/backups"
    : >"$fixture_root/backend/release/docker-compose.yml"
    : >"$fixture_root/backend/release/.env"
    ln -s release "$fixture_root/backend/latest"
  }

  run_prune() {
    local fixture_root=$1
    shift

    TELEMETRY_MAINTENANCE_TEST_MODE=1 TELEMETRY_TEST_BACKEND_ROOT="$fixture_root/backend" \
      TELEMETRY_TEST_BACKUP_ROOT="$fixture_root/backups" \
      TELEMETRY_TEST_LOCK_FILE="$fixture_root/maintenance.lock" \
      bash -c 'TELEMETRY_SOURCE_ONLY=1 source "$0"; maintenance_init; prune_backups "$@"' "$update_script" "$@"
  }

  assert_state_write_recovery() {
    local boundary=$1 phase=$2 fixture_root candidate temp_state ledger output
    local stop_at="state-$boundary-$phase"

    fixture_root=$sandbox/state-write-$boundary-$phase
    prepare_retention_fixture "$fixture_root"
    candidate=$fixture_root/backups/pre-state-write-$boundary-$phase-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
    mkdir -p "$candidate" \
      "$fixture_root/backups/pre-state-write-$boundary-$phase-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
      "$fixture_root/backups/pre-state-write-$boundary-$phase-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
    printf '%s' state-write-payload >"$candidate/payload"

    if [ "$phase" = restored ]; then
      output=$(TELEMETRY_TEST_RETENTION_STOP_AT=claimed run_prune "$fixture_root" 1 '' 2>&1 || true)
      output=$(TELEMETRY_TEST_RETENTION_STOP_AT="$stop_at" run_prune "$fixture_root" 1 "$candidate" 2>&1 || true)
      [ -d "$candidate" ] || fail "$stop_at interruption did not retain the restored namespace outcome"
    else
      output=$(TELEMETRY_TEST_RETENTION_STOP_AT="$stop_at" run_prune "$fixture_root" 1 '' 2>&1 || true)
    fi
    : "$output"
    temp_state=$(compgen -G "$fixture_root/backups/.retention-state-tmp-*" || true)
    [ -n "$temp_state" ] || fail "$stop_at interruption did not retain its file-synced temporary state"

    run_prune "$fixture_root" 1 "$candidate"
    [ -z "$(compgen -G "$fixture_root/backups/.retention-state-*" || true)" ] ||
      fail "$stop_at recovery retained canonical or temporary state"
    ledger=$(compgen -G "$fixture_root/backups/.maintenance-retention-ledger-*" || true)
    [ -z "$ledger" ] || fail "$stop_at recovery retained an interrupted state ledger"
    if [ "$phase" = restored ]; then
      [ -d "$candidate" ] && [ -s "$candidate/payload" ] ||
        fail "$stop_at recovery did not finish the restored claim"
    else
      [ -z "$(compgen -G "$fixture_root/backups/.retention-*" || true)" ] ||
        fail "$stop_at recovery retained structural deletion residue"
    fi
  }

  sandbox=$(mktemp -d /tmp/telemetry-retention.XXXXXX)
  trap 'rm -rf "${sandbox:-}"' EXIT HUP INT TERM
  fixture=$sandbox/default
  prepare_retention_fixture "$fixture"
  backup_root=$fixture/backups
  current_backup=$backup_root/pre-current-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  old_backup=$backup_root/pre-delete-me-$(date -u -d '30 days ago' +%Y%m%d-%H%M%SZ)
  control_backup=$backup_root/$'pre-control\n\033-'
  control_backup+=$(date -u -d '29 days ago' +%Y%m%d-%H%M%SZ)
  newer_backup=$backup_root/pre-keep-newer-$(date -u -d '20 days ago' +%Y%m%d-%H%M%SZ)
  newest_backup=$backup_root/pre-keep-newest-$(date -u -d '19 days ago' +%Y%m%d-%H%M%SZ)
  old_timestamp=$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  incomplete_backup=$backup_root/pre-interrupted-$old_timestamp.incomplete
  malformed_backup=$backup_root/pre-malformed-backup
  symlink_backup=$backup_root/pre-symlink-$old_timestamp
  regular_backup=$backup_root/pre-regular-$old_timestamp
  mkdir -p "$current_backup" "$old_backup" "$control_backup" "$newer_backup" "$newest_backup" "$incomplete_backup" \
    "$malformed_backup"
  ln -s "$old_backup" "$symlink_backup"
  : >"$regular_backup"

  output=$(run_prune "$fixture" 0 "$current_backup" </dev/null)
  [[ $output == *'Candidate backups:'* ]] || fail 'retention did not report candidates before defaulting to no'
  [[ $output == *"$old_backup"* ]] || fail 'retention did not report the eligible backup'
  [[ $output == *"$current_backup"* ]] && fail 'retention reported the current transaction backup'
  [[ $output != *\\n* ]] && [[ $output != *\\x1b* ]] ||
    fail 'retention treated an impossible generated backup name as eligible'
  [ -d "$old_backup" ] && [ -d "$current_backup" ] || fail 'default retention deleted a backup'

  output=$(printf 'yes\n' | run_prune "$fixture" 0 "$current_backup")
  [[ $output == *'Backups were not deleted.'* ]] || fail 'noninteractive retention accepted confirmation input'
  [ -d "$old_backup" ] || fail 'noninteractive retention deleted a backup'

  run_prune "$fixture" 1 "$current_backup"
  [ ! -e "$old_backup" ] || fail 'explicit retention did not delete an old candidate'
  [ -d "$control_backup" ] || fail 'retention deleted an impossible generated backup name'
  [ -d "$current_backup" ] && [ -d "$newer_backup" ] && [ -d "$newest_backup" ] ||
    fail 'retention did not protect the current and newest two completed backups'
  [ -d "$incomplete_backup" ] && [ -d "$malformed_backup" ] ||
    fail 'retention deleted an incomplete or malformed entry'
  [ -L "$symlink_backup" ] && [ -f "$regular_backup" ] ||
    fail 'retention deleted a symlink or non-directory entry'
  [ -z "$(compgen -G "$backup_root/.retention-*" || true)" ] ||
    fail 'retention left a claimed backup or tombstone after explicit deletion'
  [ -z "$(compgen -G "$backup_root/.maintenance-retention-ledger-*" || true)" ] ||
    fail 'retention left ledger residue after explicit deletion'

  ledger_cleanup=$sandbox/ledger-cleanup
  prepare_retention_fixture "$ledger_cleanup"
  ledger_entry=$ledger_cleanup/backups/.maintenance-retention-ledger-cleared-123-0123456789abcdef0123456789abcdef
  : >"$ledger_entry"
  chmod 600 "$ledger_entry"
  run_prune "$ledger_cleanup" 1 ''
  [ ! -e "$ledger_entry" ] || fail 'retention did not remove a durable ledger residue during recovery'

  newest_race=$sandbox/newest-race
  prepare_retention_fixture "$newest_race"
  first_race=$newest_race/backups/pre-first-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  second_race=$newest_race/backups/pre-second-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)
  third_race=$newest_race/backups/pre-third-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)
  fourth_race=$newest_race/backups/pre-fourth-$(date -u -d '32 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$first_race" "$second_race" "$third_race" "$fourth_race"
  race_hook=$newest_race/newest-race-hook.sh
  cat >"$race_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
rm -rf -- "$RETENTION_NEWER_ONE" "$RETENTION_NEWER_TWO"
EOF
  chmod 700 "$race_hook"
  output=$(RETENTION_NEWER_ONE="$third_race" RETENTION_NEWER_TWO="$fourth_race" \
    TELEMETRY_TEST_RETENTION_HOOK_POSTCLAIM="$race_hook" run_prune "$newest_race" 1 '' 2>&1)
  [ -d "$first_race" ] && [ -d "$second_race" ] ||
    fail 'retention deleted a candidate that became one of the newest two'

  root_race=$sandbox/root-race
  prepare_retention_fixture "$root_race"
  root_candidate=$root_race/backups/pre-root-candidate-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$root_candidate" \
    "$root_race/backups/pre-root-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$root_race/backups/pre-root-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  root_original=$root_race/backups-original
  root_replacement=$root_race/replacement
  root_hook=$root_race/root-race-hook.sh
  cat >"$root_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ -L "$RETENTION_ROOT" ] && exit 0
mv -- "$RETENTION_ROOT" "$RETENTION_ROOT_ORIGINAL"
mkdir -p "$RETENTION_ROOT_REPLACEMENT/$RETENTION_ROOT_CANDIDATE"
ln -s -- "$RETENTION_ROOT_REPLACEMENT" "$RETENTION_ROOT"
EOF
  chmod 700 "$root_hook"
  output=$(RETENTION_ROOT="$root_race/backups" RETENTION_ROOT_ORIGINAL="$root_original" \
    RETENTION_ROOT_REPLACEMENT="$root_replacement" RETENTION_ROOT_CANDIDATE="$(basename -- "$root_candidate")" \
    TELEMETRY_TEST_RETENTION_HOOK_PRECLAIM="$root_hook" run_prune "$root_race" 1 '' 2>&1)
  [ -d "$root_original/$(basename -- "$root_candidate")" ] ||
    fail 'retention deleted from the original root after a root replacement'
  [ -d "$root_replacement/$(basename -- "$root_candidate")" ] ||
    fail 'retention followed a replacement backup-root symlink'

  replacement_race=$sandbox/candidate-race
  prepare_retention_fixture "$replacement_race"
  replacement_candidate=$replacement_race/backups/pre-replacement-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$replacement_candidate" \
    "$replacement_race/backups/pre-replacement-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$replacement_race/backups/pre-replacement-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  replacement_hook=$replacement_race/candidate-race-hook.sh
  cat >"$replacement_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mv -- "$RETENTION_REPLACEMENT_CANDIDATE" "$RETENTION_REPLACEMENT_SAVED"
mkdir -- "$RETENTION_REPLACEMENT_CANDIDATE"
EOF
  chmod 700 "$replacement_hook"
  replacement_saved=$replacement_race/original-candidate
  output=$(RETENTION_REPLACEMENT_CANDIDATE="$replacement_candidate" RETENTION_REPLACEMENT_SAVED="$replacement_saved" \
    TELEMETRY_TEST_RETENTION_HOOK_PRECLAIM="$replacement_hook" run_prune "$replacement_race" 1 '' 2>&1)
  [ -d "$replacement_saved" ] || fail 'retention lost the candidate replaced after final validation'

  quarantine_race=$sandbox/quarantine-race
  prepare_retention_fixture "$quarantine_race"
  quarantine_candidate=$quarantine_race/backups/pre-quarantine-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$quarantine_candidate" \
    "$quarantine_race/backups/pre-quarantine-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$quarantine_race/backups/pre-quarantine-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  quarantine_saved=$quarantine_race/original-quarantine
  quarantine_hook=$quarantine_race/quarantine-race-hook.sh
  cat >"$quarantine_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mv -- "$RETENTION_QUARANTINE_PATH" "$RETENTION_QUARANTINE_SAVED"
mkdir -- "$RETENTION_QUARANTINE_PATH"
EOF
  chmod 700 "$quarantine_hook"
  output=$(RETENTION_QUARANTINE_SAVED="$quarantine_saved" \
    TELEMETRY_TEST_RETENTION_HOOK_POSTCLAIM="$quarantine_hook" run_prune "$quarantine_race" 1 '' 2>&1)
  [ -d "$quarantine_saved" ] || fail 'retention lost the claimed candidate after quarantine replacement'

  nested_race=$sandbox/nested-race
  prepare_retention_fixture "$nested_race"
  nested_candidate=$nested_race/backups/pre-nested-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$nested_candidate" \
    "$nested_race/backups/pre-nested-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$nested_race/backups/pre-nested-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  nested_outside=$nested_race/outside
  nested_marker=$nested_race/nested-hook-ran
  mkdir -p "$nested_outside"
  : >"$nested_outside/keep"
  nested_hook=$nested_race/nested-race-hook.sh
  cat >"$nested_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$RETENTION_QUARANTINE_PATH/nested"
ln -s -- "$RETENTION_NESTED_OUTSIDE" "$RETENTION_QUARANTINE_PATH/nested/outside"
: >"$RETENTION_NESTED_MARKER"
EOF
  chmod 700 "$nested_hook"
  output=$(RETENTION_NESTED_OUTSIDE="$nested_outside" RETENTION_NESTED_MARKER="$nested_marker" \
    TELEMETRY_TEST_RETENTION_HOOK_POSTCLAIM="$nested_hook" run_prune "$nested_race" 1 '' 2>&1)
  [ -f "$nested_marker" ] || fail 'retention did not run the post-claim hook'
  [ -f "$nested_outside/keep" ] || fail 'retention followed a nested symlink while deleting a claimed backup'

  cursor_race=$sandbox/cursor-race
  prepare_retention_fixture "$cursor_race"
  cursor_candidate=$cursor_race/backups/pre-cursor-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$cursor_candidate/a" "$cursor_candidate/b" \
    "$cursor_race/backups/pre-cursor-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$cursor_race/backups/pre-cursor-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' cursor-payload >"$cursor_candidate/b/payload"
  cursor_hook=$cursor_race/cursor-race-hook.sh
  cat >"$cursor_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$RETENTION_ENTRY_KIND" = directory ] || exit 0
[ "${RETENTION_ENTRY_PATH##*/}" = b ] || exit 0
mv -- "$RETENTION_ENTRY_PATH/payload" "${RETENTION_ENTRY_PATH%/b}/a/payload"
EOF
  chmod 700 "$cursor_hook"
  output=$(TELEMETRY_TEST_RETENTION_HOOK_BEFORE_ENTRY_REMOVE="$cursor_hook" \
    run_prune "$cursor_race" 1 '' 2>&1)
  cursor_state=$(compgen -G "$cursor_race/backups/.retention-state-*" || true)
  cursor_quarantine=$(compgen -G "$cursor_race/backups/.retention-[1-9]*-*" || true)
  cursor_tombstone=$(compgen -G "$cursor_race/backups/.retention-tombstone-*" || true)
  [ -n "$cursor_state" ] && [ -n "$cursor_quarantine" ] && [ -s "$cursor_quarantine/a/payload" ] ||
    fail 'retention completed after an accepted payload inode moved behind the traversal cursor'
  [ -z "$cursor_tombstone" ] || fail 'retention tombstoned a claim whose accepted payload was not reclaimed'
  run_prune "$cursor_race" 1 ''
  [ -z "$(compgen -G "$cursor_race/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after reconciling a moved payload inode'
  [ -z "$(compgen -G "$cursor_race/backups/.maintenance-retention-ledger-*" || true)" ] ||
    fail 'retention retained ledger residue after reconciling a moved payload inode'

  leaf_race=$sandbox/leaf-race
  prepare_retention_fixture "$leaf_race"
  leaf_candidate=$leaf_race/backups/pre-leaf-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$leaf_candidate" \
    "$leaf_race/backups/pre-leaf-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$leaf_race/backups/pre-leaf-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' original-payload >"$leaf_candidate/payload"
  leaf_saved=$leaf_race/original-payload
  leaf_hook=$leaf_race/leaf-race-hook.sh
  cat >"$leaf_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$RETENTION_ENTRY_KIND" = regular ] || exit 0
mv -- "$RETENTION_ENTRY_PATH" "$RETENTION_LEAF_SAVED"
printf '%s' replacement-payload >"$RETENTION_ENTRY_PATH"
EOF
  chmod 700 "$leaf_hook"
  output=$(RETENTION_LEAF_SAVED="$leaf_saved" TELEMETRY_TEST_RETENTION_HOOK_BEFORE_ENTRY_REMOVE="$leaf_hook" \
    run_prune "$leaf_race" 1 '' 2>&1)
  [ -f "$leaf_saved" ] && [ ! -s "$leaf_saved" ] ||
    fail 'retention did not reclaim the exact opened leaf inode after its name was replaced'
  leaf_tombstone=$(compgen -G "$leaf_race/backups/.retention-tombstone-*" || true)
  [ -n "$leaf_tombstone" ] && [ "$(<"$leaf_tombstone/payload")" = replacement-payload ] ||
    fail 'retention deleted or changed a replacement leaf after validation'

  final_race=$sandbox/final-race
  prepare_retention_fixture "$final_race"
  final_candidate=$final_race/backups/pre-final-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$final_candidate" \
    "$final_race/backups/pre-final-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$final_race/backups/pre-final-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  final_marker=$final_race/final-hook-ran
  final_hook=$final_race/final-race-hook.sh
  cat >"$final_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mv -- "$RETENTION_QUARANTINE_PATH" "$RETENTION_CANDIDATE_PATH"
mkdir -- "$RETENTION_QUARANTINE_PATH"
: >"$RETENTION_FINAL_MARKER"
EOF
  chmod 700 "$final_hook"
  output=$(RETENTION_FINAL_MARKER="$final_marker" \
    TELEMETRY_TEST_RETENTION_HOOK_BEFORE_DELETE="$final_hook" run_prune "$final_race" 1 '' 2>&1)
  [ -f "$final_marker" ] || fail 'retention did not reach the final deletion interposition point'
  [ -d "$final_race/backups/$(basename -- "$final_candidate")" ] ||
    fail 'retention deleted data after the final claim validation'

  final_operation_race=$sandbox/final-operation-race
  prepare_retention_fixture "$final_operation_race"
  final_operation_candidate=$final_operation_race/backups/pre-final-operation-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$final_operation_candidate" \
    "$final_operation_race/backups/pre-final-operation-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$final_operation_race/backups/pre-final-operation-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' claimed-payload >"$final_operation_candidate/payload"
  final_operation_saved=$final_operation_race/opened-claim
  final_operation_hook=$final_operation_race/final-operation-hook.sh
  cat >"$final_operation_hook" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mv -- "$RETENTION_QUARANTINE_PATH" "$RETENTION_FINAL_OPERATION_SAVED"
mkdir -- "$RETENTION_QUARANTINE_PATH"
printf '%s' replacement-data >"$RETENTION_QUARANTINE_PATH/keep"
EOF
  chmod 700 "$final_operation_hook"
  output=$(RETENTION_FINAL_OPERATION_SAVED="$final_operation_saved" \
    TELEMETRY_TEST_RETENTION_HOOK_BEFORE_FINAL_REMOVE="$final_operation_hook" \
    run_prune "$final_operation_race" 1 '' 2>&1)
  [ -d "$final_operation_saved" ] && [ ! -s "$final_operation_saved/payload" ] ||
    fail 'retention did not reclaim the exact opened claim after its final name was replaced'
  final_operation_tombstone=$(compgen -G "$final_operation_race/backups/.retention-tombstone-*" || true)
  [ -n "$final_operation_tombstone" ] && [ "$(<"$final_operation_tombstone/keep")" = replacement-data ] ||
    fail 'retention deleted a replacement directory after final validation'

  hardlink_race=$sandbox/hardlink-race
  prepare_retention_fixture "$hardlink_race"
  hardlink_candidate=$hardlink_race/backups/pre-hardlink-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$hardlink_candidate" \
    "$hardlink_race/backups/pre-hardlink-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$hardlink_race/backups/pre-hardlink-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' shared-payload >"$hardlink_candidate/payload"
  hardlink_outside=$hardlink_race/outside-link
  ln "$hardlink_candidate/payload" "$hardlink_outside"
  output=$(run_prune "$hardlink_race" 1 '' 2>&1)
  [[ $output == *'hard-linked regular file'* ]] || fail 'retention did not reject a hard-linked regular file'
  [ "$(<"$hardlink_outside")" = shared-payload ] || fail 'retention truncated data through an outside hard link'

  claimed_stop=$sandbox/claimed-stop
  prepare_retention_fixture "$claimed_stop"
  claimed_candidate=$claimed_stop/backups/pre-claimed-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$claimed_candidate" \
    "$claimed_stop/backups/pre-claimed-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$claimed_stop/backups/pre-claimed-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=claimed run_prune "$claimed_stop" 1 '' 2>&1 || true)
  claimed_state=$(compgen -G "$claimed_stop/backups/.retention-state-*" || true)
  [ -n "$claimed_state" ] || fail 'retention did not persist a claim before forced interruption'
  [ ! -d "$claimed_candidate" ] || fail 'forced claimed interruption did not quarantine the candidate'
  run_prune "$claimed_stop" 1 "$claimed_candidate"
  [ -d "$claimed_candidate" ] || fail 'retention did not restore an interrupted claim before pruning'
  [ -z "$(compgen -G "$claimed_stop/backups/.retention-*" || true)" ] ||
    fail 'retention did not clear reconciled claim state'

  deleting_stop=$sandbox/deleting-stop
  prepare_retention_fixture "$deleting_stop"
  deleting_candidate=$deleting_stop/backups/pre-deleting-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$deleting_candidate" \
    "$deleting_stop/backups/pre-deleting-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$deleting_stop/backups/pre-deleting-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=deleting run_prune "$deleting_stop" 1 '' 2>&1 || true)
  deleting_state=$(compgen -G "$deleting_stop/backups/.retention-state-*" || true)
  [ -n "$deleting_state" ] || fail 'retention did not persist the deleting phase before forced interruption'
  run_prune "$deleting_stop" 1 ''
  [ ! -e "$deleting_candidate" ] || fail 'retention did not resume an interrupted deletion'
  [ -z "$(compgen -G "$deleting_stop/backups/.retention-state-*" || true)" ] ||
    fail 'retention did not clear resumed deletion state'
  [ -z "$(compgen -G "$deleting_stop/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after resumed deletion'

  partial_stop=$sandbox/partial-stop
  prepare_retention_fixture "$partial_stop"
  partial_candidate=$partial_stop/backups/pre-partial-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$partial_candidate" \
    "$partial_stop/backups/pre-partial-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$partial_stop/backups/pre-partial-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' first-payload >"$partial_candidate/first"
  printf '%s' second-payload >"$partial_candidate/second"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=partial run_prune "$partial_stop" 1 '' 2>&1 || true)
  partial_state=$(compgen -G "$partial_stop/backups/.retention-state-*" || true)
  [ -n "$partial_state" ] || fail 'partial traversal interruption did not retain deleting state'
  run_prune "$partial_stop" 1 ''
  [ -z "$(compgen -G "$partial_stop/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after resuming partial traversal'

  payload_stop=$sandbox/payload-stop
  prepare_retention_fixture "$payload_stop"
  payload_candidate=$payload_stop/backups/pre-payload-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$payload_candidate" \
    "$payload_stop/backups/pre-payload-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$payload_stop/backups/pre-payload-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' payload-data >"$payload_candidate/payload"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=payload-complete run_prune "$payload_stop" 1 '' 2>&1 || true)
  payload_state=$(compgen -G "$payload_stop/backups/.retention-state-*" || true)
  [ -n "$payload_state" ] || fail 'payload-complete interruption did not retain durable state'
  run_prune "$payload_stop" 1 ''
  [ -z "$(compgen -G "$payload_stop/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after resuming a payload-complete claim'

  tombstoned_stop=$sandbox/tombstoned-stop
  prepare_retention_fixture "$tombstoned_stop"
  tombstoned_candidate=$tombstoned_stop/backups/pre-tombstoned-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$tombstoned_candidate" \
    "$tombstoned_stop/backups/pre-tombstoned-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$tombstoned_stop/backups/pre-tombstoned-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' tombstoned-data >"$tombstoned_candidate/payload"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=tombstoned run_prune "$tombstoned_stop" 1 '' 2>&1 || true)
  tombstoned_state=$(compgen -G "$tombstoned_stop/backups/.retention-state-*" || true)
  tombstoned_tombstone=$(compgen -G "$tombstoned_stop/backups/.retention-tombstone-*" || true)
  [ -n "$tombstoned_state" ] && [ -n "$tombstoned_tombstone" ] && [ ! -e "$tombstoned_tombstone/payload" ] ||
    fail 'tombstoned interruption did not retain its older durable state'
  run_prune "$tombstoned_stop" 1 ''
  [ -z "$(compgen -G "$tombstoned_stop/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after reconciling a tombstoned claim'

  completed_stop=$sandbox/completed-stop
  prepare_retention_fixture "$completed_stop"
  completed_candidate=$completed_stop/backups/pre-completed-stop-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$completed_candidate" \
    "$completed_stop/backups/pre-completed-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$completed_stop/backups/pre-completed-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s' completed-payload >"$completed_candidate/payload"
  output=$(TELEMETRY_TEST_RETENTION_STOP_AT=completed run_prune "$completed_stop" 1 '' 2>&1 || true)
  completed_state=$(compgen -G "$completed_stop/backups/.retention-state-*" || true)
  completed_tombstone=$(compgen -G "$completed_stop/backups/.retention-tombstone-*" || true)
  [ -n "$completed_state" ] && [ -n "$completed_tombstone" ] && [ ! -e "$completed_tombstone/payload" ] ||
    fail 'completed interruption did not leave a durable payload outcome and state'
  run_prune "$completed_stop" 1 ''
  [ -z "$(compgen -G "$completed_stop/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after reconciling completed deletion state'

  for state_phase in payload-complete completed restored; do
    for state_boundary in fsync exchange; do
      assert_state_write_recovery "$state_boundary" "$state_phase"
    done
  done

  unique_temp=$sandbox/unique-temp
  prepare_retention_fixture "$unique_temp"
  unique_quarantine=.retention-123-0123456789abcdef0123456789abcdef
  unique_state_temp=.retention-state-tmp-321-fedcba9876543210fedcba9876543210
  unique_claim=$unique_temp/backups/$unique_quarantine
  mkdir -p "$unique_claim"
  printf '%s' unique-temp-payload >"$unique_claim/payload"
  python3 - "$unique_claim" "$unique_temp/backups/$unique_state_temp" <<'PY'
import base64
import json
import os
import sys

claim = os.stat(sys.argv[1], follow_symlinks=False)
state = {
    "format": 1,
    "original": base64.b64encode(b"pre-unique-temp-20200101-000000Z").decode("ascii"),
    "quarantine": ".retention-123-0123456789abcdef0123456789abcdef",
    "dev": claim.st_dev,
    "ino": claim.st_ino,
    "phase": "deleting",
}
with open(sys.argv[2], "xb") as target:
    target.write((json.dumps(state, sort_keys=True, separators=(",", ":")) + "\n").encode("ascii"))
os.chmod(sys.argv[2], 0o600)
PY
  run_prune "$unique_temp" 1 ''
  [ -z "$(compgen -G "$unique_temp/backups/.retention-*" || true)" ] ||
    fail 'retention retained structural residue after promoting a unique valid temporary state'

  malicious_original=$sandbox/malicious-original
  prepare_retention_fixture "$malicious_original"
  malicious_quarantine=.retention-123-0123456789abcdef0123456789abcdef
  malicious_state_name=.retention-state-123-0123456789abcdef0123456789abcdef
  malicious_claim=$malicious_original/backups/$malicious_quarantine
  mkdir -p "$malicious_claim" "$malicious_original/outside"
  printf '%s' outside-data >"$malicious_original/outside/keep"
  python3 - "$malicious_claim" "$malicious_original/backups/$malicious_state_name" <<'PY'
import base64
import json
import os
import sys

claim = os.stat(sys.argv[1], follow_symlinks=False)
state = {
    "format": 1,
    "original": base64.b64encode(b"pre-safe/../../outside/pre-x-20200101-000000Z").decode("ascii"),
    "quarantine": ".retention-123-0123456789abcdef0123456789abcdef",
    "dev": claim.st_dev,
    "ino": claim.st_ino,
    "phase": "deleting",
}
with open(sys.argv[2], "x", encoding="ascii") as target:
    json.dump(state, target)
    target.write("\n")
os.chmod(sys.argv[2], 0o600)
PY
  output=$(run_prune "$malicious_original" 1 '' 2>&1)
  [[ $output == *'invalid retention state'* ]] || fail 'retention accepted a multi-component original state name'
  [ "$(<"$malicious_original/outside/keep")" = outside-data ] ||
    fail 'malicious original state escaped the backup root'

  malicious_quarantine=$sandbox/malicious-quarantine
  prepare_retention_fixture "$malicious_quarantine"
  malicious_claim=$malicious_quarantine/backups/.retention-01-0123456789abcdef0123456789abcdef
  mkdir -p "$malicious_claim"
  python3 - "$malicious_claim" \
    "$malicious_quarantine/backups/.retention-state-01-0123456789abcdef0123456789abcdef" <<'PY'
import base64
import json
import os
import sys

claim = os.stat(sys.argv[1], follow_symlinks=False)
state = {
    "format": 1,
    "original": base64.b64encode(b"pre-safe-20200101-000000Z").decode("ascii"),
    "quarantine": ".retention-01-0123456789abcdef0123456789abcdef",
    "dev": claim.st_dev,
    "ino": claim.st_ino,
    "phase": "deleting",
}
with open(sys.argv[2], "x", encoding="ascii") as target:
    json.dump(state, target)
    target.write("\n")
os.chmod(sys.argv[2], 0o600)
PY
  output=$(run_prune "$malicious_quarantine" 1 '' 2>&1)
  [[ $output == *'invalid retention state'* ]] || fail 'retention accepted a noncanonical quarantine state name'
  [ -d "$malicious_claim" ] || fail 'retention changed a noncanonical quarantine claim'

  corrupt_state=$sandbox/corrupt-state
  prepare_retention_fixture "$corrupt_state"
  corrupt_old=$corrupt_state/backups/pre-corrupt-old-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)
  mkdir -p "$corrupt_old" \
    "$corrupt_state/backups/pre-corrupt-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$corrupt_state/backups/pre-corrupt-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  printf '%s\n' '{not-json' >"$corrupt_state/backups/.retention-state-bad"
  chmod 600 "$corrupt_state/backups/.retention-state-bad"
  output=$(run_prune "$corrupt_state" 1 '' 2>&1)
  [[ $output == *'invalid retention state'* ]] || fail 'retention did not fail closed on corrupt claim state'
  [ -d "$corrupt_old" ] ||
    fail 'retention pruned while corrupt claim state was present'

  unknown_claim=$sandbox/unknown-claim
  prepare_retention_fixture "$unknown_claim"
  mkdir -p "$unknown_claim/backups/.retention-untracked" \
    "$unknown_claim/backups/pre-unknown-old-$(date -u -d '35 days ago' +%Y%m%d-%H%M%SZ)" \
    "$unknown_claim/backups/pre-unknown-newer-$(date -u -d '34 days ago' +%Y%m%d-%H%M%SZ)" \
    "$unknown_claim/backups/pre-unknown-newest-$(date -u -d '33 days ago' +%Y%m%d-%H%M%SZ)"
  output=$(run_prune "$unknown_claim" 1 '' 2>&1)
  [[ $output == *'unknown retained backup claim'* ]] || fail 'retention did not fail closed on an unknown claim'
  [ -d "$unknown_claim/backups/.retention-untracked" ] || fail 'retention changed an unknown claim'

  rm -rf "$sandbox"
  trap - EXIT HUP INT TERM
  printf '%s\n' 'PASS: maintenance retention contract'
}

case "${1:-}" in
  cli)
    run_cli_suite
    ;;
  backup)
    run_backup_suite
    ;;
  restore-portability)
    run_restore_portability_suite
    ;;
  resolution)
    run_resolution_suite
    ;;
  recovery)
    run_recovery_suite
    ;;
  activation)
    run_activation_suite
    ;;
  activation-real)
    run_activation_real_suite
    ;;
  retention)
    run_retention_suite
    ;;
  *)
    fail 'usage: maintenance-contract.sh <activation|activation-real|backup|cli|recovery|resolution|restore-portability|retention>'
    ;;
esac
