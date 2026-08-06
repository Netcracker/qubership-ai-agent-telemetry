#!/usr/bin/env bash
# Validation chains deliberately run their error block when any prerequisite fails.
# shellcheck disable=SC2015

set -euo pipefail

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

maintenance_error() {
  printf 'ERROR: %s\n' "$*" >&2
}

validate_project_name() {
  [[ $1 =~ ^[a-zA-Z0-9_-]+$ ]] || {
    maintenance_error "invalid Compose project name: $1"
    return 1
  }
}

validate_target_label() {
  [[ $1 =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || {
    maintenance_error "invalid backup target label: $1"
    return 1
  }
}

resolve_active_backend_dir() {
  local backend_root=$1 active_link release_id release_dir

  active_link=$backend_root/latest
  [ -L "$active_link" ] || {
    maintenance_error "active release link is missing: $active_link"
    return 1
  }
  release_id=$(readlink -- "$active_link") || return 1
  case "$release_id" in
    */) release_id=${release_id%/} ;;
  esac
  validate_target_label "$release_id" || return 1
  release_dir=$backend_root/$release_id
  [ -d "$release_dir" ] && [ ! -L "$release_dir" ] || {
    maintenance_error "active release directory is missing or unsafe: $release_dir"
    return 1
  }
  printf '%s\n' "$release_dir"
}

validate_test_path() {
  local name=$1 candidate=$2

  candidate=$(realpath -m -- "$candidate")
  case "$candidate" in
    /tmp/*)
      printf '%s\n' "$candidate"
      ;;
    *)
      maintenance_error "$name must remain inside /tmp"
      return 1
      ;;
  esac
}

validate_production_runtime() {
  [ "${BASH_VERSINFO[0]}" -ge 4 ] || {
    maintenance_error 'Bash 4 or newer is required'
    return 1
  }
  [ "${EUID}" -eq 0 ] || {
    maintenance_error 'run maintenance commands as root'
    return 1
  }
}

maintenance_init() {
  local identity_mode=${1:-ordinary}
  local test_mode=${TELEMETRY_MAINTENANCE_TEST_MODE:-} kill_at=${TELEMETRY_TEST_KILL_AT:-}

  umask 077
  : "$HELPER_IMAGE" "$JSON_IMAGE" "$TRANSACTION_LABEL" "$ROLE_LABEL" "$LARGE_BACKUP_BYTES"
  case "$identity_mode" in
    ordinary) ;;
    legacy-source)
      if [ "${TELEMETRY_TEST_BACKEND_ROOT+x}" = x ] || [ "${TELEMETRY_TEST_BACKUP_ROOT+x}" = x ] ||
        [ "${TELEMETRY_TEST_PROJECT_NAME+x}" = x ] || [ "${TELEMETRY_TEST_LOCK_FILE+x}" = x ]; then
        maintenance_error '--legacy-source cannot be combined with test-mode identity overrides'
        return 1
      fi
      ;;
    *)
      maintenance_error "unsupported maintenance identity mode: $identity_mode"
      return 1
      ;;
  esac
  if [ -n "$test_mode" ] && [ "$identity_mode" = ordinary ]; then
    [ "$test_mode" = 1 ] || {
      maintenance_error 'TELEMETRY_MAINTENANCE_TEST_MODE must be 1 when set'
      return 1
    }
    : "${TELEMETRY_TEST_BACKEND_ROOT:?TELEMETRY_TEST_BACKEND_ROOT is required in test mode}"
    BACKEND_ROOT=$(validate_test_path TELEMETRY_TEST_BACKEND_ROOT "$TELEMETRY_TEST_BACKEND_ROOT") || return 1
    BACKUP_ROOT=$(validate_test_path TELEMETRY_TEST_BACKUP_ROOT \
      "${TELEMETRY_TEST_BACKUP_ROOT:-$(dirname -- "$BACKEND_ROOT")/backups}") || return 1
    LOCK_FILE=$(validate_test_path TELEMETRY_TEST_LOCK_FILE \
      "${TELEMETRY_TEST_LOCK_FILE:-$(dirname -- "$BACKEND_ROOT")/maintenance.lock}") || return 1
    PROJECT_NAME=${TELEMETRY_TEST_PROJECT_NAME:-$PROJECT_NAME_DEFAULT}
    case "$kill_at" in
      ''|backup-helper-running|activation-prepared|symlink-replaced|target-started|health-passed|\
        success-marker-written|committed-written) ;;
      *)
        maintenance_error "TELEMETRY_TEST_KILL_AT has an unsupported phase: $kill_at"
        return 1
        ;;
    esac
    if [ -n "${TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS:-}" ]; then
      [[ $TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS =~ ^[0-9]+([.][0-9]+)?$ ]] || {
        maintenance_error 'TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS must be a nonnegative number'
        return 1
      }
    fi
  else
    if [ "$identity_mode" = legacy-source ]; then
      BACKEND_ROOT=$LEGACY_BACKEND_ROOT
      BACKUP_ROOT=$BACKUP_ROOT_DEFAULT
      LOCK_FILE=$LEGACY_LOCK_FILE
      PROJECT_NAME=$LEGACY_PROJECT_NAME
    else
      BACKEND_ROOT=$BACKEND_ROOT_DEFAULT
      BACKUP_ROOT=$BACKUP_ROOT_DEFAULT
      LOCK_FILE=$LOCK_FILE_DEFAULT
      PROJECT_NAME=$PROJECT_NAME_DEFAULT
    fi
    [ -z "$kill_at" ] && [ -z "${TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS:-}" ] || {
      maintenance_error 'test-only maintenance controls require TELEMETRY_MAINTENANCE_TEST_MODE=1'
      return 1
    }
    validate_production_runtime || return 1
  fi

  validate_project_name "$PROJECT_NAME" || return 1
  MAINTENANCE_IDENTITY_MODE=$identity_mode
  CURRENT_BACKEND_DIR=$BACKEND_ROOT
  if [ "$identity_mode" = legacy-source ]; then
    CURRENT_BACKEND_DIR=$(resolve_active_backend_dir "$BACKEND_ROOT") || return 1
  fi
  COMPOSE_FILE=$CURRENT_BACKEND_DIR/docker-compose.yml
  ENV_FILE=$CURRENT_BACKEND_DIR/.env
  GENERATED_OVERRIDE_FILE=$CURRENT_BACKEND_DIR/.maintenance-compose.yml
  TRANSACTION_FILE=$BACKEND_ROOT/.maintenance-transaction
  TEST_COMMAND_LOG=
  if [ -n "$test_mode" ]; then
    COMPOSE_FILE=$(validate_test_path COMPOSE_FILE "$COMPOSE_FILE") || return 1
    ENV_FILE=$(validate_test_path ENV_FILE "$ENV_FILE") || return 1
    GENERATED_OVERRIDE_FILE=$(validate_test_path GENERATED_OVERRIDE_FILE "$GENERATED_OVERRIDE_FILE") || return 1
    TRANSACTION_FILE=$(validate_test_path TRANSACTION_FILE "$TRANSACTION_FILE") || return 1
    TEST_COMMAND_LOG=${TELEMETRY_TEST_COMMAND_LOG:-}
    if [ -n "$TEST_COMMAND_LOG" ]; then
      TEST_COMMAND_LOG=$(validate_test_path TELEMETRY_TEST_COMMAND_LOG "$TEST_COMMAND_LOG") || return 1
    fi
  fi

  [ -f "$COMPOSE_FILE" ] || {
    maintenance_error "Compose file is missing: $COMPOSE_FILE"
    return 1
  }
  [ -f "$ENV_FILE" ] || {
    maintenance_error "environment file is missing: $ENV_FILE"
    return 1
  }
}

record_test_event() {
  [ -n "${TEST_COMMAND_LOG:-}" ] || return 0
  printf '%s\n' "$1" >>"$TEST_COMMAND_LOG"
}

compose() {
  local release_dir=$1
  shift
  local -a command=(
    docker compose
    --project-name "$PROJECT_NAME"
    --project-directory "$release_dir"
    --env-file "$release_dir/.env"
    -f "$release_dir/docker-compose.yml"
  )

  if [ -f "$release_dir/.maintenance-compose.yml" ]; then
    command+=(-f "$release_dir/.maintenance-compose.yml")
  fi
  if [ "${1:-}" != config ]; then
    "${command[@]}" config --quiet || return 1
  fi
  if [ "${1:-}" = down ]; then
    record_test_event COMPOSE_DOWN || return 1
  fi
  if [ "${1:-}" = up ]; then
    record_test_event COMPOSE_UP || return 1
  fi
  "${command[@]}" "$@"
}

test_kill_checkpoint() {
  local phase=$1

  [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] || return 0
  [ "${TELEMETRY_TEST_KILL_AT:-}" = "$phase" ] || return 0
  record_test_event "KILL_AT=$phase"
  kill -STOP "$$"
}

write_transaction() {
  local entry key value transaction_dir transaction_tmp

  transaction_dir=$(dirname -- "$TRANSACTION_FILE")
  mkdir -p -- "$transaction_dir" || return 1
  transaction_tmp=$(mktemp "${TRANSACTION_FILE}.XXXXXX") || return 1
  for entry in "$@"; do
    case "$entry" in
      *=*)
        key=${entry%%=*}
        value=${entry#*=}
        ;;
      *)
        maintenance_error 'transaction entries must use KEY=VALUE format'
        rm -f -- "$transaction_tmp"
        return 1
        ;;
    esac
    [[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] && [[ $value != *$'\n'* ]] || {
      maintenance_error 'transaction contains an invalid key or value'
      rm -f -- "$transaction_tmp"
      return 1
    }
    printf '%s=%s\n' "$key" "$value" >>"$transaction_tmp" || {
      rm -f -- "$transaction_tmp"
      return 1
    }
  done
  if ! sync -f "$transaction_tmp" || ! mv -Tf -- "$transaction_tmp" "$TRANSACTION_FILE"; then
    rm -f -- "$transaction_tmp"
    return 1
  fi
  sync -f "$transaction_dir" || return 1
}

read_transaction() {
  local wanted=$1 key value found=

  [ -f "$TRANSACTION_FILE" ] || return 1
  while IFS='=' read -r key value; do
    case "$key" in
      "$wanted")
        printf '%s\n' "$value"
        found=1
        ;;
      *)
        ;;
    esac
  done <"$TRANSACTION_FILE"
  [ -n "$found" ]
}

clear_transaction() {
  local transaction_dir

  if [ -f "$TRANSACTION_FILE" ]; then
    transaction_dir=$(dirname -- "$TRANSACTION_FILE")
    rm -f -- "$TRANSACTION_FILE" || return 1
    sync -f "$transaction_dir" || return 1
    record_test_event TRANSACTION_CLEAR || return 1
  fi
}

rewrite_transaction_state() {
  local phase=$1 backup_path=${2:-} key value
  local -a entries=()

  [ -f "$TRANSACTION_FILE" ] || return 1
  while IFS='=' read -r key value; do
    case "$key" in
      PHASE|BACKUP_PATH) ;;
      *) entries+=("$key=$value") ;;
    esac
  done <"$TRANSACTION_FILE"
  entries+=("PHASE=$phase")
  [ -z "$backup_path" ] || entries+=("BACKUP_PATH=$backup_path")
  write_transaction "${entries[@]}"
}

assert_transaction_helpers_absent() {
  local transaction_id=$1 role

  validate_target_label "$transaction_id" >/dev/null 2>&1 || {
    maintenance_error "invalid maintenance transaction ID: $transaction_id"
    return 1
  }
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
    [ "${TELEMETRY_TEST_FAIL_HELPER_ASSERTION:-}" = 1 ]; then
    maintenance_error 'injected transaction-helper absence failure'
    return 1
  fi
  for role in backup health; do
    list_transaction_helpers "$transaction_id" "$role" || return 1
    [ "${#TRANSACTION_HELPER_IDS[@]}" -eq 0 ] || {
      maintenance_error "transaction helper remains after cleanup: $transaction_id ($role)"
      return 1
    }
  done
  record_test_event "HELPERS_ABSENT=$transaction_id"
}

list_transaction_helpers() {
  local transaction_id=$1 role=$2 output helper_id

  output=$(docker ps -aq --filter "label=$TRANSACTION_LABEL=$transaction_id" \
    --filter "label=$ROLE_LABEL=$role") || {
    maintenance_error "cannot query transaction helpers: $transaction_id ($role)"
    return 1
  }
  TRANSACTION_HELPER_IDS=()
  while IFS= read -r helper_id; do
    [ -z "$helper_id" ] || TRANSACTION_HELPER_IDS+=("$helper_id")
  done <<<"$output"
}

cleanup_transaction_helpers() {
  local transaction_id=$1 role
  local -a helper_ids=() role_ids

  validate_target_label "$transaction_id" >/dev/null 2>&1 || {
    maintenance_error "invalid maintenance transaction ID: $transaction_id"
    return 1
  }
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
    [ "${TELEMETRY_TEST_FAIL_HELPER_CLEANUP:-}" = 1 ]; then
    maintenance_error 'injected transaction-helper cleanup failure'
    return 1
  fi
  for role in backup health; do
    list_transaction_helpers "$transaction_id" "$role" || return 1
    role_ids=("${TRANSACTION_HELPER_IDS[@]}")
    helper_ids+=("${role_ids[@]}")
  done
  [ "${#helper_ids[@]}" -le 1 ] || {
    maintenance_error "maintenance transaction has multiple helper containers: $transaction_id"
    return 1
  }
  [ "${#helper_ids[@]}" -eq 0 ] || docker rm -f "${helper_ids[0]}" >/dev/null || {
    maintenance_error "cannot remove transaction helper: ${helper_ids[0]}"
    return 1
  }
  assert_transaction_helpers_absent "$transaction_id"
}

derive_release_id() {
  local release_dir=$1 release_id

  release_id=$(basename -- "$release_dir")
  validate_target_label "$release_id" || {
    maintenance_error "invalid immutable release ID: $release_id"
    return 1
  }
  printf '%s\n' "$release_id"
}

pin_active_images() {
  local release_dir=$1 generated_override_file
  local -a declared_services=(caddy collector grafana victorialogs victoriametrics)
  local -a configured_services containers
  local -A configured_images=()
  local service container running_image configured_image repository digest override_tmp release_id candidate
  local match_count configured_services_output configured_images_output container_output digest_output

  generated_override_file=$release_dir/.maintenance-compose.yml
  configured_services_output=$(compose "$release_dir" config --services) || return 1
  configured_services=()
  while IFS= read -r service; do
    [ -z "$service" ] || configured_services+=("$service")
  done <<<"$configured_services_output"
  [ "${#configured_services[@]}" -eq "${#declared_services[@]}" ] || {
    maintenance_error 'Compose service image configuration is incomplete'
    return 1
  }
  docker image inspect "$JSON_IMAGE" >/dev/null 2>&1 || docker pull "$JSON_IMAGE" || return 1
  configured_images_output=$(
    compose "$release_dir" config --format json |
      docker run --rm -i "$JSON_IMAGE" -er '
        ["caddy", "collector", "grafana", "victorialogs", "victoriametrics"][] as $service
        | [$service, (.services[$service].image // error("missing image for " + $service))]
        | @tsv
      '
  ) || return 1
  while IFS=$'\t' read -r service configured_image; do
    [ -n "$service" ] && [ -n "$configured_image" ] && [ -z "${configured_images[$service]:-}" ] || {
      maintenance_error 'Compose service image configuration is invalid'
      return 1
    }
    configured_images[$service]=$configured_image
  done <<<"$configured_images_output"
  [ "${#configured_images[@]}" -eq "${#declared_services[@]}" ] || {
    maintenance_error 'Compose service image configuration is incomplete'
    return 1
  }
  release_id=$(derive_release_id "$release_dir") || return 1
  override_tmp=$(mktemp "${generated_override_file}.XXXXXX") || return 1
  # shellcheck disable=SC2094 # Error paths remove the temporary file, not the input stream.
  {
    printf 'services:\n' || {
      rm -f -- "$override_tmp"
      return 1
    }
    for service in "${declared_services[@]}"; do
      [[ " ${configured_services[*]} " == *" $service "* ]] || {
        maintenance_error "required service is missing: $service"
        rm -f -- "$override_tmp"
        return 1
      }
      configured_image=${configured_images[$service]}
      container_output=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
        --filter "label=com.docker.compose.service=$service") || {
        rm -f -- "$override_tmp"
        return 1
      }
      containers=()
      while IFS= read -r container; do
        [ -z "$container" ] || containers+=("$container")
      done <<<"$container_output"
      [ "${#containers[@]}" -eq 1 ] || {
        maintenance_error "running service mapping is missing or ambiguous: $service"
        rm -f -- "$override_tmp"
        return 1
      }
      container=${containers[0]}
      running_image=$(docker inspect --format '{{.Image}}' "$container") || {
        maintenance_error "cannot inspect the running image for service: $service"
        rm -f -- "$override_tmp"
        return 1
      }
      [[ $running_image =~ ^sha256:[0-9a-f]{64}$ ]] || {
        maintenance_error "running service has a noncanonical image ID: $service"
        rm -f -- "$override_tmp"
        return 1
      }
      if [ "$service" = grafana ]; then
        digest="${PROJECT_NAME}-grafana:${release_id}"
        docker tag "$running_image" "$digest" || {
          rm -f -- "$override_tmp"
          return 1
        }
      else
        repository=${configured_image%@*}
        if [[ ${repository##*/} == *:* ]]; then
          repository=${repository%:*}
        fi
        digest=
        match_count=0
        digest_output=$(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' \
          "$running_image") || {
          maintenance_error "cannot inspect the running image digest for service: $service"
          rm -f -- "$override_tmp"
          return 1
        }
        while IFS= read -r candidate; do
          [ "${candidate%@*}" = "$repository" ] || continue
          digest=$candidate
          match_count=$((match_count + 1))
        done <<<"$digest_output"
        [ "$match_count" -eq 1 ] || {
          maintenance_error "running image digest is missing or ambiguous: $service"
          rm -f -- "$override_tmp"
          return 1
        }
      fi
      printf '  %s:\n    image: %s\n' "$service" "$digest" || {
        rm -f -- "$override_tmp"
        return 1
      }
    done
  } >"$override_tmp"
  if ! sync -f "$override_tmp" || ! mv -Tf -- "$override_tmp" "$generated_override_file"; then
    rm -f -- "$override_tmp"
    return 1
  fi
  sync -f "$release_dir" || return 1
  compose "$release_dir" config --quiet || return 1
}

compose_environment_value() {
  local environment_file=$1 wanted=$2 key value matches=0 result=

  while IFS='=' read -r key value; do
    [ "$key" = "$wanted" ] || continue
    matches=$((matches + 1))
    result=$value
  done <"$environment_file"
  [ "$matches" -eq 1 ] && [ -n "$result" ] || {
    maintenance_error "Compose environment lacks exactly one value for $wanted"
    return 1
  }
  printf '%s\n' "$result"
}

metadata_value() {
  local metadata_file=$1 wanted=$2 key value matches=0 result=

  while IFS='=' read -r key value; do
    [ "$key" = "$wanted" ] || continue
    matches=$((matches + 1))
    result=$value
  done <"$metadata_file"
  [ "$matches" -eq 1 ] || return 1
  printf '%s\n' "$result"
}

metric_sample_is_fresh() {
  local probe_output=$1 expected_value=$2 window_start=$3 window_end=$4 line sample
  local value_query_timestamp sample_value raw_query_timestamp raw_sample_timestamp
  local value_matches=0 raw_timestamp_matches=0

  [[ $expected_value =~ ^[0-9]+$ ]] && [[ $window_start =~ ^[0-9]+$ ]] && [[ $window_end =~ ^[0-9]+$ ]] || return 1
  while IFS= read -r line; do
    case "$line" in
      METRIC_VALUE_SAMPLE=*)
        value_matches=$((value_matches + 1))
        sample=${line#METRIC_VALUE_SAMPLE=}
        value_query_timestamp=${sample%%|*}
        sample_value=${sample#*|}
        ;;
      METRIC_RAW_TIMESTAMP_SAMPLE=*)
        raw_timestamp_matches=$((raw_timestamp_matches + 1))
        sample=${line#METRIC_RAW_TIMESTAMP_SAMPLE=}
        raw_query_timestamp=${sample%%|*}
        raw_sample_timestamp=${sample#*|}
        ;;
      *)
        ;;
    esac
  done <<<"$probe_output"
  [ "$value_matches" -eq 1 ] && [ "$raw_timestamp_matches" -eq 1 ] || return 1
  [[ $value_query_timestamp =~ ^[0-9]+([.][0-9]+)?$ ]] && [ "$sample_value" = "$expected_value" ] || return 1
  [[ $raw_query_timestamp =~ ^[0-9]+([.][0-9]+)?$ ]] &&
    [[ $raw_sample_timestamp =~ ^[0-9]+([.][0-9]+)?$ ]] || return 1
  raw_sample_timestamp=${raw_sample_timestamp%%.*}
  [ "$raw_sample_timestamp" -ge "$window_start" ] && [ "$raw_sample_timestamp" -le "$window_end" ]
}

run_telemetry_health_probes() (
  local release_dir=$1 transaction_id=$2 environment_file site_address https_port ingest_token dashboard_user network
  local probe_id probe_value probe_started_at probe_observed_at probe_output ca_path attempt max_attempts=${3:-12}
  local probe_epoch
  local network_output candidate_network
  local -a curl_tls=()
  local -a networks

  environment_file=$(mktemp "$BACKEND_ROOT/.health-environment.XXXXXX") || exit 1
  trap 'rm -f -- "$environment_file"' EXIT
  chmod 600 "$environment_file" || exit 1
  compose "$release_dir" config --environment >"$environment_file" || exit 1
  site_address=$(compose_environment_value "$environment_file" SITE_ADDRESS) || exit 1
  https_port=$(compose_environment_value "$environment_file" HTTPS_PORT) || exit 1
  ingest_token=$(compose_environment_value "$environment_file" INGEST_TOKEN) || exit 1
  dashboard_user=$(compose_environment_value "$environment_file" DASHBOARD_AUTH_USER) || exit 1
  [[ $https_port =~ ^[0-9]+$ ]] || {
    maintenance_error 'Compose environment HTTPS_PORT must be numeric'
    exit 1
  }
  [[ $ingest_token =~ ^[a-zA-Z0-9_-]+$ ]] || {
    maintenance_error 'Compose environment INGEST_TOKEN contains unsupported characters'
    exit 1
  }
  [[ $dashboard_user =~ ^[a-zA-Z0-9][a-zA-Z0-9._@-]*$ ]] || {
    maintenance_error 'Compose environment DASHBOARD_AUTH_USER is not a valid auth-proxy identity'
    exit 1
  }
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ -n "${TELEMETRY_TEST_CA_CERT:-}" ]; then
    ca_path=$(realpath -e -- "$TELEMETRY_TEST_CA_CERT") || exit 1
    case "$ca_path" in
      /tmp/*) curl_tls=(--cacert "$ca_path") ;;
      *) maintenance_error 'TELEMETRY_TEST_CA_CERT must resolve inside /tmp'; exit 1 ;;
    esac
  fi
  probe_started_at=$(date +%s) || exit 1
  probe_epoch=$(date -u +%s) || exit 1
  probe_id="maintenance-${transaction_id}-${probe_epoch}-$RANDOM"
  probe_value=$((1000000000 + RANDOM * 1000 + RANDOM))
  printf 'header = "Authorization: Bearer %s"\n' "$ingest_token" | curl --config - \
    --fail --silent --show-error "${curl_tls[@]}" -H 'Content-Type: application/json' \
    --data "{\"resourceLogs\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"maintenance-health\"}}]},\"scopeLogs\":[{\"scope\":{\"name\":\"maintenance-health\"},\"logRecords\":[{\"timeUnixNano\":\"$(date +%s%N)\",\"body\":{\"stringValue\":\"$probe_id\"}}]}]}]}" \
    "https://$site_address:$https_port/v1/logs" >/dev/null || {
      maintenance_error 'Caddy rejected the OTLP log health probe'
      exit 1
    }
  printf 'header = "Authorization: Bearer %s"\n' "$ingest_token" | curl --config - \
    --fail --silent --show-error "${curl_tls[@]}" -H 'Content-Type: application/json' \
    --data "{\"resourceMetrics\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"maintenance-health\"}}]},\"scopeMetrics\":[{\"scope\":{\"name\":\"maintenance-health\"},\"metrics\":[{\"name\":\"telemetry_maintenance_health\",\"gauge\":{\"dataPoints\":[{\"timeUnixNano\":\"$(date +%s%N)\",\"asInt\":\"$probe_value\"}]}}]}]}]}" \
    "https://$site_address:$https_port/v1/metrics" >/dev/null || {
      maintenance_error 'Caddy rejected the OTLP gauge health probe'
      exit 1
    }
  network_output=$(docker network ls -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
    --filter 'label=com.docker.compose.network=backend') || exit 1
  networks=()
  while IFS= read -r candidate_network; do
    [ -z "$candidate_network" ] || networks+=("$candidate_network")
  done <<<"$network_output"
  [ "${#networks[@]}" -eq 1 ] || {
    maintenance_error 'Compose backend network mapping is missing or ambiguous'
    exit 1
  }
  network=${networks[0]}
  for ((attempt = 1; attempt <= max_attempts; attempt++)); do
    if probe_output=$(docker run --rm --pull never --network "$network" \
      --label "$TRANSACTION_LABEL=$transaction_id" --label "$ROLE_LABEL=health" \
      "$HELPER_IMAGE" sh -eu -c '
        extract_single_sample() {
          samples=$(printf "%s\n" "$1" | grep -o "\"value\":\[[0-9][0-9.]*,\"[^\"]*\"\]" || true)
          sample_count=$(printf "%s\n" "$samples" | awk "NF { count++ } END { print count + 0 }")
          [ "$sample_count" -eq 1 ] || return 1
          printf "%s\n" "$samples" |
            sed -n "s/^\"value\":\[\([0-9][0-9.]*\),\"\([^\"]*\)\"\]$/\1|\2/p"
        }
        logs=$(wget -qO- "http://victorialogs:9428/select/logsql/query?query=_msg:%22$1%22")
        case "$logs" in *"$1"*) ;; *) exit 1 ;; esac
        metric_values=$(wget -qO- "http://victoriametrics:8428/api/v1/query?query=telemetry_maintenance_health")
        value_sample=$(extract_single_sample "$metric_values")
        metric_timestamps=$(wget -qO- \
          "http://victoriametrics:8428/api/v1/query?query=timestamp%28telemetry_maintenance_health%29")
        raw_timestamp_sample=$(extract_single_sample "$metric_timestamps")
        grafana=$(wget -qO- --header "X-WEBAUTH-USER: $2" http://grafana:3000/api/health)
        case "$grafana" in *\"database\"*\"ok\"*) ;; *) exit 1 ;; esac
        datasource=$(wget -qO- --header "X-WEBAUTH-USER: $2" \
          http://grafana:3000/api/datasources/uid/victoriametrics)
        case "$datasource" in *\"uid\":\"victoriametrics\"*) ;; *) exit 1 ;; esac
        dashboard=$(wget -qO- --header "X-WEBAUTH-USER: $2" \
          http://grafana:3000/api/dashboards/uid/ai-agent-health)
        case "$dashboard" in *\"uid\":\"ai-agent-health\"*) ;; *) exit 1 ;; esac
        dashboard=$(wget -qO- --header "X-WEBAUTH-USER: $2" \
          http://grafana:3000/api/dashboards/uid/ai-agent-telemetry-adoption)
        case "$dashboard" in *\"uid\":\"ai-agent-telemetry-adoption\"*) ;; *) exit 1 ;; esac
        dashboard=$(wget -qO- --header "X-WEBAUTH-USER: $2" \
          http://grafana:3000/api/dashboards/uid/native-agent-metrics-overview)
        case "$dashboard" in *\"uid\":\"native-agent-metrics-overview\"*) ;; *) exit 1 ;; esac
        dashboard=$(wget -qO- --header "X-WEBAUTH-USER: $2" \
          http://grafana:3000/api/dashboards/uid/codex-native-metrics)
        case "$dashboard" in *\"uid\":\"codex-native-metrics\"*) ;; *) exit 1 ;; esac
        printf "METRIC_VALUE_SAMPLE=%s\n" "$value_sample"
        printf "METRIC_RAW_TIMESTAMP_SAMPLE=%s\n" "$raw_timestamp_sample"
      ' _ "$probe_id" "$dashboard_user" "$probe_value"); then
      probe_observed_at=$(date +%s) || exit 1
      if metric_sample_is_fresh "$probe_output" "$probe_value" "$probe_started_at" "$probe_observed_at"; then
        exit 0
      fi
    fi
    sleep 1
  done
  maintenance_error 'fresh telemetry or Grafana health did not become visible before the deadline'
  exit 1
)

strict_health_gate() {
  local release_dir=$1
  local -a expected containers
  local service container state health attempt=0 max_attempts=120 healthy transaction_id stability_seconds=10
  local deadline remaining forced_target=${TELEMETRY_TEST_FORCE_TARGET_UNHEALTHY:-}
  local expected_output container_output state_output

  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ -n "${TELEMETRY_TEST_HEALTH_ATTEMPTS:-}" ]; then
    [[ $TELEMETRY_TEST_HEALTH_ATTEMPTS =~ ^[1-9][0-9]*$ ]] || {
      maintenance_error 'TELEMETRY_TEST_HEALTH_ATTEMPTS must be a positive integer'
      return 1
    }
    max_attempts=$TELEMETRY_TEST_HEALTH_ATTEMPTS
  fi
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ -n "${TELEMETRY_TEST_STABILITY_SECONDS:-}" ]; then
    [[ $TELEMETRY_TEST_STABILITY_SECONDS =~ ^[0-9]+$ ]] || {
      maintenance_error 'TELEMETRY_TEST_STABILITY_SECONDS must be a nonnegative integer'
      return 1
    }
    stability_seconds=$TELEMETRY_TEST_STABILITY_SECONDS
  fi
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ -n "$forced_target" ]; then
    validate_target_label "$forced_target" >/dev/null || {
      maintenance_error 'TELEMETRY_TEST_FORCE_TARGET_UNHEALTHY must be a release ID'
      return 1
    }
  fi
  expected_output=$(compose "$release_dir" config --services) || return 1
  expected=()
  while IFS= read -r service; do
    [ -z "$service" ] || expected+=("$service")
  done <<<"$expected_output"
  [ "${#expected[@]}" -eq 5 ] || {
    maintenance_error 'Compose configuration must declare exactly five services'
    return 1
  }
  for service in caddy collector grafana victorialogs victoriametrics; do
    [[ " ${expected[*]} " == *" $service "* ]] || {
      maintenance_error "required service is missing: $service"
      return 1
    }
  done
  deadline=$((SECONDS + max_attempts))
  while [ "$attempt" -lt "$max_attempts" ]; do
    healthy=1
    for service in "${expected[@]}"; do
      container_output=$(docker ps -aq --filter "label=com.docker.compose.project=$PROJECT_NAME" \
        --filter "label=com.docker.compose.service=$service") || return 1
      containers=()
      while IFS= read -r container; do
        [ -z "$container" ] || containers+=("$container")
      done <<<"$container_output"
      [ "${#containers[@]}" -eq 1 ] || {
        healthy=0
        break
      }
      container=${containers[0]}
      if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
        { [ "${TELEMETRY_TEST_FORCE_UNHEALTHY:-}" = 1 ] ||
          { [ -n "$forced_target" ] && [ "$(basename -- "$release_dir")" = "$forced_target" ]; }; }; then
        state=running
        health=unhealthy
      else
        state_output=$(docker inspect --format \
          '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' \
          "$container") || return 1
        read -r state health <<<"$state_output" || return 1
      fi
      if [ "$state" != running ] || { [ "$health" != none ] && [ "$health" != healthy ]; }; then
        healthy=0
        break
      fi
    done
    [ "$healthy" -eq 1 ] && break
    attempt=$((attempt + 1))
    sleep 1
  done
  [ "$healthy" -eq 1 ] || {
    maintenance_error "services did not become healthy after $max_attempts seconds"
    return 1
  }
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" != 1 ] || \
    [ "${TELEMETRY_TEST_SKIP_REMOTE_PROBES:-}" != 1 ]; then
    transaction_id=${TELEMETRY_HEALTH_TRANSACTION_ID:-}
    [ -n "$transaction_id" ] || transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
    [ -n "$transaction_id" ] || transaction_id="health-$$-$RANDOM"
    validate_target_label "$transaction_id" >/dev/null 2>&1 || {
      maintenance_error 'health probe transaction ID is invalid'
      return 1
    }
    remaining=$((deadline - SECONDS))
    [ "$remaining" -gt 0 ] || remaining=1
    run_telemetry_health_probes "$release_dir" "$transaction_id" "$remaining" || return 1
  fi
  sleep "$stability_seconds"
  for service in "${expected[@]}"; do
    container_output=$(docker ps -aq --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || return 1
    containers=()
    while IFS= read -r container; do
      [ -z "$container" ] || containers+=("$container")
    done <<<"$container_output"
    [ "${#containers[@]}" -eq 1 ] || {
      maintenance_error "service mapping changed during stability interval: $service"
      return 1
    }
    state_output=$(docker inspect --format \
      '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' \
      "${containers[0]}") || return 1
    read -r state health <<<"$state_output" || return 1
    if [ "$state" != running ] || { [ "$health" != none ] && [ "$health" != healthy ]; }; then
      maintenance_error "service became unhealthy during stability interval: $service"
      return 1
    fi
  done
}

transaction_release_dir() {
  local release_id=$1 candidate=$BACKEND_ROOT/$1

  if [ -d "$candidate" ] && [ ! -L "$candidate" ]; then
    printf '%s\n' "$candidate"
  elif [ "$release_id" = "$(basename -- "$BACKEND_ROOT")" ]; then
    printf '%s\n' "$BACKEND_ROOT"
  else
    maintenance_error "transaction release directory is missing: $candidate"
    return 1
  fi
}

latest_points_to_release() {
  local release_id=$1

  [ -L "$BACKEND_ROOT/latest" ] && [ "$(readlink -- "$BACKEND_ROOT/latest")" = "$release_id" ] &&
    [ -d "$BACKEND_ROOT/$release_id" ] && [ ! -L "$BACKEND_ROOT/$release_id" ]
}

validate_transaction_backup_path() {
  local backup_path=$1 phase=$2 canonical backup_root

  if [ -z "$backup_path" ]; then
    [ "$phase" = backup-offline ] || {
      maintenance_error "maintenance transaction lacks a backup path in phase: $phase"
      return 1
    }
    return 0
  fi
  canonical=$(realpath -m -- "$backup_path") || return 1
  backup_root=$(realpath -m -- "$BACKUP_ROOT") || return 1
  [ "$canonical" = "$backup_path" ] && [[ $canonical == "$backup_root"/pre-* ]] || {
    maintenance_error "maintenance transaction has an unsafe backup path: $backup_path"
    return 1
  }
  [ ! -L "$backup_path" ] && [ ! -L "${backup_path}.incomplete" ] || {
    maintenance_error "maintenance transaction backup path must not be a symlink: $backup_path"
    return 1
  }
  if [ "$phase" != backup-offline ]; then
    [ -d "$backup_path" ] || {
      maintenance_error "maintenance transaction backup is missing: $backup_path"
      return 1
    }
  fi
}

validate_transaction_state() {
  local key value record operation phase transaction_id previous_release target_release backup_path required service
  local previous_images=0 target_images=0
  local -A seen=()
  local -a image_roles=(PREVIOUS) transaction_records

  [ -f "$TRANSACTION_FILE" ] && [ ! -L "$TRANSACTION_FILE" ] &&
    [ "$(stat -c '%a' "$TRANSACTION_FILE")" = 600 ] || {
    maintenance_error "maintenance transaction file is missing or unsafe: $TRANSACTION_FILE"
    return 1
  }
  [ -s "$TRANSACTION_FILE" ] || {
    maintenance_error 'maintenance transaction is empty'
    return 1
  }
  local final_byte
  final_byte=$(tail -c 1 -- "$TRANSACTION_FILE" | od -An -tx1 | tr -d '[:space:]') || {
    maintenance_error 'cannot read the final maintenance transaction byte'
    return 1
  }
  [ "$final_byte" = 0a ] || {
    maintenance_error 'maintenance transaction has an unterminated final record'
    return 1
  }
  if ! LC_ALL=C od -An -v -tx1 -- "$TRANSACTION_FILE" | awk '
  {
      for (field = 1; field <= NF; field++) {
        if ($field != "0a" && $field !~ /^[2-6][0-9a-f]$/ && $field !~ /^7[0-9a-e]$/) {
          exit 1
        }
      }
    }
  '; then
    maintenance_error 'maintenance transaction contains non-printable bytes'
    return 1
  fi
  mapfile -t transaction_records <"$TRANSACTION_FILE" || {
    maintenance_error 'cannot read the maintenance transaction'
    return 1
  }
  for record in "${transaction_records[@]}"; do
    IFS='=' read -r key value <<<"$record" || return 1
    [ -n "$key" ] && [ -z "${seen[$key]:-}" ] || {
      maintenance_error "maintenance transaction contains a duplicate or empty field: ${key:-empty}"
      return 1
    }
    case "$key" in
      FORMAT_VERSION|TRANSACTION_ID|OPERATION|PHASE|PREVIOUS_RELEASE|TARGET_RELEASE|BACKUP_PATH)
        ;;
      PREVIOUS_IMAGE_CADDY|PREVIOUS_IMAGE_COLLECTOR|PREVIOUS_IMAGE_GRAFANA|\
        PREVIOUS_IMAGE_VICTORIALOGS|PREVIOUS_IMAGE_VICTORIAMETRICS)
        previous_images=$((previous_images + 1))
        ;;
      TARGET_IMAGE_CADDY|TARGET_IMAGE_COLLECTOR|TARGET_IMAGE_GRAFANA|\
        TARGET_IMAGE_VICTORIALOGS|TARGET_IMAGE_VICTORIAMETRICS)
        target_images=$((target_images + 1))
        ;;
      *)
        maintenance_error "maintenance transaction contains an unexpected field: $key"
        return 1
        ;;
    esac
    seen[$key]=1
  done
  for required in FORMAT_VERSION TRANSACTION_ID OPERATION PHASE PREVIOUS_RELEASE BACKUP_PATH; do
    [ -n "${seen[$required]:-}" ] || {
      maintenance_error "maintenance transaction lacks a required field: $required"
      return 1
    }
  done
  [ "$(read_transaction FORMAT_VERSION)" = 1 ] || {
    maintenance_error 'unsupported maintenance transaction format'
    return 1
  }
  transaction_id=$(read_transaction TRANSACTION_ID) || return 1
  operation=$(read_transaction OPERATION) || return 1
  phase=$(read_transaction PHASE) || return 1
  previous_release=$(read_transaction PREVIOUS_RELEASE) || return 1
  target_release=$(read_transaction TARGET_RELEASE 2>/dev/null || true)
  backup_path=$(read_transaction BACKUP_PATH) || return 1
  if ! validate_target_label "$transaction_id" >/dev/null 2>&1 ||
    ! validate_target_label "$previous_release" >/dev/null 2>&1; then
    maintenance_error 'maintenance transaction contains an invalid ID'
    return 1
  fi
  transaction_release_dir "$previous_release" >/dev/null || return 1
  validate_transaction_backup_path "$backup_path" "$phase" || return 1
  case "$operation" in
    backup)
      [ "$phase" = backup-offline ] && [ -z "$target_release" ] &&
        [ "$previous_images" -eq 5 ] && [ "$target_images" -eq 0 ] || {
        maintenance_error 'backup transaction fields do not match its phase'
        return 1
      }
      ;;
    update)
      validate_target_label "$target_release" >/dev/null 2>&1 &&
        transaction_release_dir "$target_release" >/dev/null &&
        [ "$previous_images" -eq 5 ] && [ "$target_images" -eq 5 ] || {
        maintenance_error 'update transaction has incomplete release or image identities'
        return 1
      }
      ;;
    *)
      maintenance_error "unsupported maintenance transaction operation: $operation"
      return 1
      ;;
  esac
  [ "$operation" != update ] || image_roles+=(TARGET)
  for required in "${image_roles[@]}"; do
    for service in CADDY COLLECTOR GRAFANA VICTORIALOGS VICTORIAMETRICS; do
      value=$(read_transaction "${required}_IMAGE_${service}" 2>/dev/null || true)
      [[ $value =~ ^sha256:[0-9a-f]{64}$ ]] || {
        maintenance_error "maintenance transaction contains an invalid image ID: ${required}_IMAGE_${service}"
        return 1
      }
    done
  done
}

replace_active_release() {
  local release_id=$1 transaction_id temporary_link

  validate_target_label "$release_id" || return 1
  transaction_release_dir "$release_id" >/dev/null || return 1
  transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
  validate_target_label "$transaction_id" || return 1
  temporary_link=$BACKEND_ROOT/.latest.$transaction_id
  rm -f -- "$temporary_link" || return 1
  ln -s -- "$release_id" "$temporary_link" || return 1
  if ! mv -Tf -- "$temporary_link" "$BACKEND_ROOT/latest"; then
    rm -f -- "$temporary_link"
    return 1
  fi
  sync -f "$BACKEND_ROOT" || return 1
}

verify_transaction_image_ids() {
  local image_role=$1 service container expected_id key container_output running_image
  local -a containers

  for service in caddy collector grafana victorialogs victoriametrics; do
    key=${image_role}_IMAGE_${service^^}
    expected_id=$(read_transaction "$key" 2>/dev/null || true)
    [ -n "$expected_id" ] || {
      maintenance_error "transaction lacks the $image_role image ID for service: $service"
      return 1
    }
    container_output=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || return 1
    containers=()
    while IFS= read -r container; do
      [ -z "$container" ] || containers+=("$container")
    done <<<"$container_output"
    [ "${#containers[@]}" -eq 1 ] || return 1
    container=${containers[0]}
    running_image=$(docker inspect --format '{{.Image}}' "$container") || return 1
    [ "$running_image" = "$expected_id" ] || {
      maintenance_error "transaction image ID does not match the running service: $service"
      return 1
    }
  done
}

validate_deployment_marker() {
  local release_dir=$1 expected_transaction_id=${2:-} manifest marker manifest_checksum marker_transaction
  local key value service record reference image_id image_count=0

  manifest=$release_dir/.deployment-manifest
  marker=$release_dir/.deployment-success
  [ -f "$manifest" ] && [ ! -L "$manifest" ] && [ "$(stat -c '%a' "$manifest")" = 600 ] || return 1
  [ -f "$marker" ] && [ ! -L "$marker" ] && [ "$(stat -c '%a' "$marker")" = 600 ] || return 1
  [ "$(metadata_value "$manifest" format 2>/dev/null || true)" = 1 ] || return 1
  [ "$(metadata_value "$marker" format 2>/dev/null || true)" = 1 ] || return 1
  manifest_checksum=$(sha256sum "$manifest" | awk '{print $1}')
  [ "$(metadata_value "$marker" manifest_sha256 2>/dev/null || true)" = "$manifest_checksum" ] || return 1
  marker_transaction=$(metadata_value "$marker" transaction_id 2>/dev/null || true)
  validate_target_label "$marker_transaction" >/dev/null 2>&1 || return 1
  [ -z "$expected_transaction_id" ] || [ "$marker_transaction" = "$expected_transaction_id" ] || return 1
  for key in resolved_identity content_identity; do
    value=$(metadata_value "$manifest" "$key" 2>/dev/null || true)
    [ -n "$value" ] && [ "$(metadata_value "$marker" "$key" 2>/dev/null || true)" = "$value" ] || return 1
  done
  while IFS='=' read -r key value; do
    case "$key" in
      image.caddy|image.collector|image.grafana|image.victorialogs|image.victoriametrics)
        image_count=$((image_count + 1))
        ;;
      image.*)
        return 1
        ;;
      *)
        ;;
    esac
  done <"$marker"
  [ "$image_count" -eq 5 ] || return 1
  for service in caddy collector grafana victorialogs victoriametrics; do
    record=$(metadata_value "$manifest" "image.$service" 2>/dev/null || true)
    reference=${record%%|*}
    image_id=${record#*|}
    [ -n "$reference" ] && [ -n "$image_id" ] && [ "$reference" != "$image_id" ] || return 1
    [ "$(metadata_value "$marker" "image.$service" 2>/dev/null || true)" = "$record" ] || return 1
  done
}

verify_manifest_image_ids() {
  local release_dir=$1 manifest service record expected_id container services_output container_output running_image
  local -a services containers

  manifest=$release_dir/.deployment-manifest
  services_output=$(compose "$release_dir" config --services) || return 1
  services=()
  while IFS= read -r service; do
    [ -z "$service" ] || services+=("$service")
  done <<<"$services_output"
  [ "${#services[@]}" -eq 5 ] || return 1
  for service in caddy collector grafana victorialogs victoriametrics; do
    [[ " ${services[*]} " == *" $service "* ]] || return 1
    record=$(metadata_value "$manifest" "image.$service" 2>/dev/null || true)
    expected_id=${record#*|}
    [ -n "$expected_id" ] && [ "$expected_id" != "$record" ] || return 1
    container_output=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || return 1
    containers=()
    while IFS= read -r container; do
      [ -z "$container" ] || containers+=("$container")
    done <<<"$container_output"
    [ "${#containers[@]}" -eq 1 ] || return 1
    container=${containers[0]}
    running_image=$(docker inspect --format '{{.Image}}' "$container") || return 1
    [ "$running_image" = "$expected_id" ] || return 1
  done
}

validate_committed_release() {
  local release_dir=$1 expected_release=$2 expected_transaction_id=${3:-} service record manifest_id transaction_id

  [ -L "$BACKEND_ROOT/latest" ] && [ "$(readlink -- "$BACKEND_ROOT/latest")" = "$expected_release" ] || return 1
  validate_deployment_marker "$release_dir" "$expected_transaction_id" || return 1
  if [ -n "$expected_transaction_id" ]; then
    transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
    [ "$transaction_id" = "$expected_transaction_id" ] || return 1
    for service in caddy collector grafana victorialogs victoriametrics; do
      record=$(metadata_value "$release_dir/.deployment-manifest" "image.$service" 2>/dev/null || true)
      manifest_id=${record#*|}
      [ "$(read_transaction "TARGET_IMAGE_${service^^}" 2>/dev/null || true)" = "$manifest_id" ] || return 1
    done
    verify_transaction_image_ids TARGET || return 1
  else
    verify_manifest_image_ids "$release_dir" || return 1
  fi
  strict_health_gate "$release_dir"
}

rollback_existing_transaction() {
  local previous_release target_release previous_dir target_dir transaction_id

  transaction_id=$(read_transaction TRANSACTION_ID) || return 1
  previous_release=$(read_transaction PREVIOUS_RELEASE) || return 1
  target_release=$(read_transaction TARGET_RELEASE) || return 1
  if ! validate_target_label "$previous_release" || ! validate_target_label "$target_release"; then
    return 1
  fi
  previous_dir=$(transaction_release_dir "$previous_release") || return 1
  target_dir=$(transaction_release_dir "$target_release") || return 1
  cleanup_transaction_helpers "$transaction_id" || return 1
  compose "$target_dir" down || return 1
  replace_active_release "$previous_release" || return 1
  compose "$previous_dir" up -d --no-build --pull never || return 1
  verify_transaction_image_ids PREVIOUS || return 1
  strict_health_gate "$previous_dir" || return 1
  assert_transaction_helpers_absent "$transaction_id" || return 1
  clear_transaction || return 1
}

recover_transaction() {
  local transaction_phase operation previous_release target_release previous_dir target_dir transaction_id backup_path

  [ -f "$TRANSACTION_FILE" ] || return 0
  validate_transaction_state || return 1
  transaction_phase=$(read_transaction PHASE) || return 1
  operation=$(read_transaction OPERATION) || return 1
  transaction_id=$(read_transaction TRANSACTION_ID) || return 1
  previous_release=$(read_transaction PREVIOUS_RELEASE) || return 1
  previous_dir=$(transaction_release_dir "$previous_release") || return 1
  case "$transaction_phase" in
    backup-offline)
      cleanup_transaction_helpers "$transaction_id" || return 1
      latest_points_to_release "$previous_release" || {
        maintenance_error "latest does not identify the previous transaction release: $previous_release"
        return 1
      }
      compose "$previous_dir" up -d --no-build --pull never || return 1
      verify_transaction_image_ids PREVIOUS || return 1
      strict_health_gate "$previous_dir" || return 1
      assert_transaction_helpers_absent "$transaction_id" || return 1
      clear_transaction || return 1
      ;;
    activation-prepared|activated)
      [ "$operation" = update ] || return 1
      target_release=$(read_transaction TARGET_RELEASE) || return 1
      target_dir=$(transaction_release_dir "$target_release") || return 1
      cleanup_transaction_helpers "$transaction_id" || return 1
      if [ "$transaction_phase" = activated ] &&
        validate_committed_release "$target_dir" "$target_release" "$transaction_id"; then
        assert_transaction_helpers_absent "$transaction_id" || return 1
        backup_path=$(read_transaction BACKUP_PATH) || return 1
        rewrite_transaction_state committed "$backup_path" || return 1
        clear_transaction || return 1
      else
        rollback_existing_transaction || return 1
      fi
      ;;
    committed)
      [ "$operation" = update ] || return 1
      target_release=$(read_transaction TARGET_RELEASE) || return 1
      target_dir=$(transaction_release_dir "$target_release") || return 1
      cleanup_transaction_helpers "$transaction_id" || return 1
      if validate_committed_release "$target_dir" "$target_release" "$transaction_id"; then
        assert_transaction_helpers_absent "$transaction_id" || return 1
        clear_transaction || return 1
      else
        rollback_existing_transaction || return 1
      fi
      ;;
    *)
      maintenance_error "Unsupported maintenance transaction phase: $transaction_phase"
      return 1
      ;;
  esac
}

acquire_maintenance_lock() {
  mkdir -p -- "$(dirname -- "$LOCK_FILE")" || return 1
  exec {maintenance_lock_fd}>"$LOCK_FILE" || return 1
  if ! flock -n "$maintenance_lock_fd"; then
    maintenance_error 'another maintenance operation is already running'
    exec {maintenance_lock_fd}>&-
    unset maintenance_lock_fd
    return 1
  fi
}

validate_inherited_maintenance_lock() {
  local lock_fd=${TELEMETRY_UPDATE_LOCK_FD:-} expected_transaction=${TELEMETRY_UPDATE_TRANSACTION_ID:-}
  local expected_previous=${TELEMETRY_UPDATE_PREVIOUS_RELEASE:-}

  [[ $lock_fd =~ ^[0-9]+$ ]] &&
    [ "$(readlink -f -- "/proc/$BASHPID/fd/$lock_fd" 2>/dev/null || true)" = "$(readlink -f -- "$LOCK_FILE")" ] || {
    maintenance_error 'inherited maintenance lock descriptor is invalid'
    return 1
  }
  flock -n "$lock_fd" || {
    maintenance_error 'inherited maintenance lock descriptor does not own the lock'
    return 1
  }
  if ! validate_target_label "$expected_transaction" >/dev/null 2>&1 ||
    ! validate_target_label "$expected_previous" >/dev/null 2>&1; then
    maintenance_error 'inherited maintenance identity is invalid'
    return 1
  fi
  [ "$(read_transaction OPERATION 2>/dev/null || true)" = update ] &&
    [ "$(read_transaction TRANSACTION_ID 2>/dev/null || true)" = "$expected_transaction" ] &&
    [ "$(read_transaction PREVIOUS_RELEASE 2>/dev/null || true)" = "$expected_previous" ] || {
    maintenance_error 'inherited maintenance identity does not match the update transaction'
    return 1
  }
}

release_maintenance_lock() {
  if [ -n "${maintenance_lock_fd:-}" ]; then
    flock -u "$maintenance_lock_fd" || true
    exec {maintenance_lock_fd}>&-
    unset maintenance_lock_fd
  fi
}

terminate_maintenance() {
  local signal=$1

  trap - HUP INT TERM
  kill -s "$signal" "$$"
}

resolve_volume_name() {
  local logical_name=$1 volume_output volume_name
  local -a volume_names

  volume_output=$(docker volume ls -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
    --filter "label=com.docker.compose.volume=$logical_name") || return 1
  volume_names=()
  while IFS= read -r volume_name; do
    [ -z "$volume_name" ] || volume_names+=("$volume_name")
  done <<<"$volume_output"
  [ "${#volume_names[@]}" -eq 1 ] || {
    maintenance_error "volume mapping is missing or ambiguous: $logical_name"
    return 1
  }
  printf '%s\n' "${volume_names[0]}"
}

archive_source() {
  local backup_work_dir=$1 release_dir=${2:-$BACKEND_ROOT} member_list grep_status

  tar -C "$release_dir" -czf "$backup_work_dir/telemetry-backend-source.tar.gz" \
    --exclude=.env --exclude=./.env --exclude=.maintenance-transaction --exclude=./.maintenance-transaction \
    --exclude='*.incomplete' . || return 1
  member_list=$(mktemp "$backup_work_dir/.source-members.XXXXXX") || return 1
  tar -tzf "$backup_work_dir/telemetry-backend-source.tar.gz" >"$member_list" || {
    rm -f -- "$member_list" || maintenance_error "cannot remove source archive member list: $member_list"
    maintenance_error 'cannot read the source archive member list'
    return 1
  }
  if grep -Eq '(^|/)\.env$' "$member_list"; then
    grep_status=0
  else
    grep_status=$?
  fi
  case "$grep_status" in
    0)
      rm -f -- "$member_list" || {
        maintenance_error "cannot remove source archive member list: $member_list"
        return 1
      }
      maintenance_error 'source archive unexpectedly contains .env'
      return 1
      ;;
    1)
      ;;
    *)
      rm -f -- "$member_list" || {
        maintenance_error "cannot remove source archive member list: $member_list"
        return 1
      }
      maintenance_error 'cannot verify the source archive member list'
      return 1
      ;;
  esac
  rm -f -- "$member_list" || {
    maintenance_error "cannot remove source archive member list: $member_list"
    return 1
  }
  record_test_event VERIFY_SOURCE_ARCHIVE || return 1
}

bundle_maintenance_library() {
  local backup_work_dir=$1 library_source

  library_source=$(realpath -e -- "${BASH_SOURCE[0]}") || return 1
  [ -f "$library_source" ] && [ ! -L "$library_source" ] || {
    maintenance_error "executing maintenance library is missing or unsafe: $library_source"
    return 1
  }
  install -m 700 -- "$library_source" "$backup_work_dir/backup-backend.sh" || return 1
  cmp -s -- "$library_source" "$backup_work_dir/backup-backend.sh" || {
    maintenance_error 'bundled maintenance library differs from the executing library'
    return 1
  }
  sync -f "$backup_work_dir/backup-backend.sh" || return 1
}

write_static_checksums() {
  local backup_work_dir=$1

  (
    cd "$backup_work_dir" || exit 1
    sha256sum backend.env backup-backend.sh telemetry-backend-source.tar.gz manifest.txt RESTORE.md restore-backend.sh \
      >SHA256SUMS || exit 1
    sha256sum -c SHA256SUMS || exit 1
  ) || return 1
  sync -f "$backup_work_dir" || return 1
  record_test_event PRELIMINARY_CHECKSUMS || return 1
}

write_restore_helper() {
  local backup_work_dir=$1

  cat >"$backup_work_dir/restore-backend.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly RESTORE_HELPER_IMAGE='docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc'

restore_error() {
  printf 'ERROR: %s\n' "$*" >&2
}

restore_compose() {
  docker compose --project-name "$PROJECT_NAME" --project-directory "$RELEASE_DIR" \
    --env-file "$RELEASE_DIR/.env" -f "$RELEASE_DIR/docker-compose.yml" \
    -f "$RELEASE_DIR/.maintenance-compose.yml" "$@"
}

[ "$#" -eq 2 ] || {
  printf 'Usage: %s BACKUP_DIR RELEASE_DIR\n' "${0##*/}" >&2
  exit 2
}
BACKUP_DIR=$(realpath -e -- "$1") || exit 1
RELEASE_DIR=$(realpath -m -- "$2") || exit 1
BACKEND_ROOT=$(dirname -- "$RELEASE_DIR") || exit 1
release_id=$(basename -- "$RELEASE_DIR") || exit 1
[[ $release_id =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || {
  restore_error "invalid immutable release ID: $release_id"
  exit 1
}
[ -d "$BACKUP_DIR" ] && [ ! -L "$BACKUP_DIR" ] || {
  restore_error "backup directory is missing or unsafe: $BACKUP_DIR"
  exit 1
}
[ ! -e "$RELEASE_DIR" ] || {
  restore_error "release path already exists: $RELEASE_DIR"
  exit 1
}

cd "$BACKUP_DIR"
sha256sum -c SHA256SUMS
[ -f "$BACKUP_DIR/backup-backend.sh" ] && [ ! -L "$BACKUP_DIR/backup-backend.sh" ] && \
  [ "$(stat -c '%a' "$BACKUP_DIR/backup-backend.sh")" = 700 ] || {
  restore_error 'verified backup maintenance library is missing or unsafe'
  exit 1
}
PROJECT_NAME=$(sed -n 's/^PROJECT_NAME=//p' manifest.txt)
[[ $PROJECT_NAME =~ ^[a-zA-Z0-9_-]+$ ]] || {
  restore_error 'manifest contains an invalid Compose project name'
  exit 1
}
[ "$(sed -n 's/^PROJECT_NAME=//p' manifest.txt | wc -l)" -eq 1 ] || {
  restore_error 'manifest must contain exactly one Compose project name'
  exit 1
}

declare -a expected_services=(caddy collector grafana victorialogs victoriametrics)
declare -A image_references=() image_ids=() runtime_image_ids=()
image_count=0
while IFS= read -r image_record; do
  IFS='|' read -r service reference image_id extra <<<"$image_record"
  expected_service=${expected_services[image_count]:-}
  [ "$service" = "$expected_service" ] && [ -n "$reference" ] && [ -z "${extra:-}" ] && \
    [ -z "${image_references[$service]:-}" ] && [[ $image_id =~ ^sha256:[0-9a-f]{64}$ ]] || {
    restore_error 'manifest image records are invalid'
    exit 1
  }
  case "$service" in
    grafana)
      [[ $reference =~ ^${PROJECT_NAME}-grafana:[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || {
        restore_error 'manifest image records are invalid'
        exit 1
      }
      ;;
    caddy|collector|victorialogs|victoriametrics)
      [[ $reference =~ ^[a-zA-Z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]] || {
        restore_error 'manifest image records are invalid'
        exit 1
      }
      ;;
    *)
      restore_error 'manifest image records are invalid'
      exit 1
      ;;
  esac
  image_references[$service]=$reference
  image_ids[$service]=$image_id
  image_count=$((image_count + 1))
done < <(sed -n 's/^IMAGE=//p' manifest.txt)
[ "$image_count" -eq 5 ] || {
  restore_error 'manifest image records are invalid'
  exit 1
}

install -d -m 700 "$RELEASE_DIR"
tar -xzf telemetry-backend-source.tar.gz -C "$RELEASE_DIR"
install -m 600 backend.env "$RELEASE_DIR/.env"
install -D -m 700 "$BACKUP_DIR/backup-backend.sh" "$RELEASE_DIR/scripts/backup-backend.sh"
[ -f "$RELEASE_DIR/docker-compose.yml" ] && [ -f "$RELEASE_DIR/.maintenance-compose.yml" ] || {
  restore_error 'restored source is missing required maintenance files'
  exit 1
}
override_tmp=$(mktemp "$RELEASE_DIR/.maintenance-compose.yml.XXXXXX") || exit 1
{
  printf 'services:\n'
  for service in "${expected_services[@]}"; do
    printf '  %s:\n    image: %s\n' "$service" "${image_references[$service]}"
  done
} >"$override_tmp"
chmod 600 "$override_tmp"
mv -Tf -- "$override_tmp" "$RELEASE_DIR/.maintenance-compose.yml"
sync -f "$RELEASE_DIR"
restore_compose config --quiet
restore_compose pull --ignore-buildable
restore_compose build --pull grafana
for service in caddy collector victorialogs victoriametrics; do
  runtime_image_ids[$service]=$(docker image inspect --format '{{.Id}}' "${image_references[$service]}") || exit 1
  [ "${runtime_image_ids[$service]}" = "${image_ids[$service]}" ] || {
    restore_error "registry image identity differs from the manifest: $service"
    exit 1
  }
done
runtime_image_ids[grafana]=$(docker image inspect --format '{{.Id}}' "${image_references[grafana]}") || exit 1
docker image inspect "$RESTORE_HELPER_IMAGE" >/dev/null 2>&1 || docker pull "$RESTORE_HELPER_IMAGE"

volume_count=0
while IFS='|' read -r logical actual archive; do
  [ -n "$logical" ] || continue
  [[ $logical =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] && \
    [[ $actual =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] && \
    [[ $archive =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*[.]tar[.]gz$ ]] || {
    restore_error 'manifest contains an unsafe volume record'
    exit 1
  }
  [ -f "$BACKUP_DIR/volumes/$archive" ] && [ ! -L "$BACKUP_DIR/volumes/$archive" ] || {
    restore_error "volume archive is missing or unsafe: $archive"
    exit 1
  }
  if docker volume inspect "$actual" >/dev/null 2>&1; then
    restore_error "target volume already exists: $actual"
    exit 1
  fi
  docker volume create --label "com.docker.compose.project=$PROJECT_NAME" \
    --label "com.docker.compose.volume=$logical" "$actual" >/dev/null
  docker run --rm --mount "type=volume,src=$actual,dst=/target" \
    --mount "type=bind,src=$BACKUP_DIR/volumes,dst=/backup,readonly" \
    "$RESTORE_HELPER_IMAGE" sh -eu -c "cd /target && tar -xzf /backup/$archive"
  volume_count=$((volume_count + 1))
done < <(sed -n 's/^VOLUME=//p' manifest.txt)
[ "$volume_count" -gt 0 ] || {
  restore_error 'manifest contains no volume records'
  exit 1
}

temporary_link="$BACKEND_ROOT/.latest.restore-$$"
rm -f -- "$temporary_link"
ln -s -- "$release_id" "$temporary_link"
mv -Tf -- "$temporary_link" "$BACKEND_ROOT/latest"
sync -f "$BACKEND_ROOT"
restore_compose up -d --no-build --pull never
for service in "${expected_services[@]}"; do
  mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
    --filter "label=com.docker.compose.service=$service")
  [ "${#containers[@]}" -eq 1 ] || {
    restore_error "restored service mapping is missing or ambiguous: $service"
    exit 1
  }
  running_image=$(docker inspect --format '{{.Image}}' "${containers[0]}") || exit 1
  [ "$running_image" = "${runtime_image_ids[$service]}" ] || {
    restore_error "restored service image identity differs from the prepared image: $service"
    exit 1
  }
done

# Reuse the shipped maintenance health implementation so update and disaster recovery cannot drift.
# shellcheck disable=SC1091
TELEMETRY_SOURCE_ONLY=1 source "$BACKUP_DIR/backup-backend.sh"
BACKUP_ROOT=$BACKUP_DIR
LOCK_FILE="$BACKEND_ROOT/.restore-maintenance.lock"
COMPOSE_FILE="$RELEASE_DIR/docker-compose.yml"
ENV_FILE="$RELEASE_DIR/.env"
GENERATED_OVERRIDE_FILE="$RELEASE_DIR/.maintenance-compose.yml"
TRANSACTION_FILE="$BACKEND_ROOT/.maintenance-transaction"
TEST_COMMAND_LOG=
health_transaction_id="restore-health-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
export BACKUP_DIR RELEASE_DIR BACKEND_ROOT BACKUP_ROOT LOCK_FILE PROJECT_NAME COMPOSE_FILE ENV_FILE GENERATED_OVERRIDE_FILE
export TRANSACTION_FILE TEST_COMMAND_LOG TELEMETRY_HEALTH_TRANSACTION_ID="$health_transaction_id"
trap 'cleanup_transaction_helpers "$health_transaction_id" >/dev/null 2>&1 || true' EXIT HUP INT TERM
if ! timeout --foreground --signal=TERM 120 bash -c '
  set -euo pipefail
  TELEMETRY_SOURCE_ONLY=1 source "$BACKUP_DIR/backup-backend.sh"
  strict_health_gate "$RELEASE_DIR"
'; then
  cleanup_transaction_helpers "$health_transaction_id" || true
  restore_error 'restored backend did not pass the strict health gate within 120 seconds'
  exit 1
fi
cleanup_transaction_helpers "$health_transaction_id"
trap - EXIT HUP INT TERM
printf 'Restored telemetry backend release %s and passed the strict health gate.\n' "$release_id"
EOF
  chmod 700 "$backup_work_dir/restore-backend.sh"
}

write_restore_instructions() {
  local backup_work_dir=$1

  cat >"$backup_work_dir/RESTORE.md" <<'EOF'
# Restore the telemetry backend

Install Docker Engine and Docker Compose on the recovery host. Registry access is required because Docker images are
not stored in the backup. Set `BACKUP_DIR` to this backup directory and set `RELEASE_DIR` to a new immutable release
directory. The checksums cover a separate copy of the maintenance library, so recovery does not depend on the archived
release containing that script. The checked restore helper uses the manifest for every volume identity, requires the
exact registry image IDs, rebuilds Grafana from the archived context, verifies the rebuilt image, starts the exact
Compose project, and runs the same strict 120-second health gate as an update.

```bash
BACKUP_DIR=/path/to/pre-backup
RELEASE_DIR=/opt/ai-agent-telemetry-backend/restored-release
cd "$BACKUP_DIR"
sha256sum -c SHA256SUMS
./restore-backend.sh "$BACKUP_DIR" "$RELEASE_DIR"
```

The helper refuses to overwrite an existing release directory or Docker volume. If recovery stops before completion,
inspect the printed error, remove only the incomplete release and newly created volumes after confirming their exact
names, and rerun the helper. It restores `backend.env` as `.env` with mode `0600`; no credentials are printed.
EOF
}

measure_volume_bytes() {
  local volume_name=$1 transaction_id=$2 ordinal=$3 volume_bytes

  volume_bytes=$(docker run --rm --name "skills-telemetry-backup-${transaction_id}-measure-${ordinal}" \
    --label "$TRANSACTION_LABEL=$transaction_id" --label "$ROLE_LABEL=backup" \
    --mount "type=volume,src=$volume_name,dst=/source,readonly" "$HELPER_IMAGE" \
    sh -eu -c 'du -sb /source | cut -f1') || return 1
  record_test_event "VOLUME_MEASURE=$volume_name" || return 1
  printf '%s\n' "$volume_bytes"
}

check_backup_capacity() {
  local total_bytes=$1 allow_large_backup=$2 available_bytes required_bytes threshold=${LARGE_BACKUP_BYTES}

  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ -n "${TELEMETRY_TEST_LARGE_BACKUP_BYTES:-}" ]; then
    [[ $TELEMETRY_TEST_LARGE_BACKUP_BYTES =~ ^[0-9]+$ ]] || {
      maintenance_error 'TELEMETRY_TEST_LARGE_BACKUP_BYTES must be a nonnegative integer'
      return 1
    }
    threshold=$TELEMETRY_TEST_LARGE_BACKUP_BYTES
  fi
  available_bytes=$(df -PB1 "$BACKUP_ROOT" | awk 'NR == 2 {print $4}') || return 1
  [[ $available_bytes =~ ^[0-9]+$ ]] || return 1
  required_bytes=$((total_bytes + (total_bytes + 9) / 10))
  [ "$available_bytes" -gt "$required_bytes" ] || {
    maintenance_error "insufficient backup space: need more than $required_bytes bytes"
    return 1
  }
  if [ "$total_bytes" -gt "$threshold" ] && [ "$allow_large_backup" -ne 1 ]; then
    if [ ! -t 0 ]; then
      maintenance_error "backup input exceeds $threshold bytes; rerun with --allow-large-backup"
      return 1
    fi
    printf 'Backup input is %s bytes. Continue? [y/N] ' "$total_bytes" >&2
    local answer
    read -r answer
    [[ $answer =~ ^[Yy]([Ee][Ss])?$ ]] || {
      maintenance_error 'large backup was not confirmed'
      return 1
    }
  fi
}

restart_original_stack() {
  local release_dir=$1

  compose "$release_dir" up -d --no-build --pull never && strict_health_gate "$release_dir"
}

assert_backend_service_containers_absent() {
  local service container_output

  for service in caddy collector grafana victorialogs victoriametrics; do
    container_output=$(docker ps -aq --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || {
      maintenance_error "cannot verify the stopped backend service: $service"
      return 1
    }
    [ -z "$container_output" ] || {
      maintenance_error "Compose left a backend service container after shutdown: $service"
      return 1
    }
  done
  record_test_event BACKEND_CONTAINERS_ABSENT
}

capture_running_image_entries() {
  local service container image_id container_output
  local -a containers

  for service in caddy collector grafana victorialogs victoriametrics; do
    container_output=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || return 1
    containers=()
    while IFS= read -r container; do
      [ -z "$container" ] || containers+=("$container")
    done <<<"$container_output"
    [ "${#containers[@]}" -eq 1 ] || {
      maintenance_error "active service mapping is missing or ambiguous: $service"
      return 1
    }
    container=${containers[0]}
    image_id=$(docker inspect --format '{{.Image}}' "$container") || return 1
    [[ $image_id =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
    printf 'PREVIOUS_IMAGE_%s=%s\n' "${service^^}" "$image_id" || return 1
  done
}

capture_backup_image_records() {
  local release_dir=$1 service reference image_id container release_id container_output references_output
  local -a containers
  local -A references=()

  release_id=$(derive_release_id "$release_dir") || return 1
  references_output=$(
    compose "$release_dir" config --format json |
      docker run --rm -i "$JSON_IMAGE" -er '
        ["caddy", "collector", "grafana", "victorialogs", "victoriametrics"][] as $service
        | [$service, (.services[$service].image // error("missing image for " + $service))]
        | @tsv
      '
  ) || return 1
  while IFS=$'\t' read -r service reference; do
    [ -n "$service" ] && [ -n "$reference" ] && [ -z "${references[$service]:-}" ] || {
      maintenance_error 'effective Compose image mapping is invalid'
      return 1
    }
    references[$service]=$reference
  done <<<"$references_output"
  [ "${#references[@]}" -eq 5 ] || {
    maintenance_error 'effective Compose image mapping is incomplete'
    return 1
  }

  for service in caddy collector grafana victorialogs victoriametrics; do
    reference=${references[$service]:-}
    if [ "$service" = grafana ]; then
      [ "$reference" = "${PROJECT_NAME}-grafana:${release_id}" ] || {
        maintenance_error 'effective Grafana image reference is not release-specific'
        return 1
      }
    else
      [[ $reference =~ ^[a-zA-Z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]] || {
        maintenance_error "effective registry image reference is mutable: $service"
        return 1
      }
    fi
    container_output=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service") || return 1
    containers=()
    while IFS= read -r container; do
      [ -z "$container" ] || containers+=("$container")
    done <<<"$container_output"
    [ "${#containers[@]}" -eq 1 ] || {
      maintenance_error "active service mapping is missing or ambiguous: $service"
      return 1
    }
    image_id=$(docker inspect --format '{{.Image}}' "${containers[0]}") || return 1
    [[ $image_id =~ ^sha256:[0-9a-f]{64}$ ]] || {
      maintenance_error "active service has a noncanonical image ID: $service"
      return 1
    }
    printf '%s|%s|%s\n' "$service" "$reference" "$image_id" || return 1
  done
}

backup_main() {
  local target_label=manual leave_stopped=0 allow_large_backup=0
  local identity_mode=ordinary legacy_source_seen=0
  local completed_dir backup_work_dir backup_parent transaction_id target_release previous_release volume_bytes total_bytes
  local logical_volume actual_volume archive_name ordinal failed=0 helper_id helper_status helper_delay=0
  local handoff=0 supervised_legacy=0 release_dir logical_volume_output image_output image_record service reference
  local image_id extra
  local created_at compose_version
  local -a logical_volumes actual_volumes image_records previous_image_entries

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --target-label)
        [ "$#" -ge 2 ] || {
          maintenance_error '--target-label requires a label'
          return 1
        }
        target_label=$2
        shift 2
        ;;
      --leave-stopped)
        leave_stopped=1
        shift
        ;;
      --allow-large-backup)
        allow_large_backup=1
        shift
        ;;
      --legacy-source)
        [ "$legacy_source_seen" -eq 0 ] || {
          maintenance_error '--legacy-source may be specified only once'
          return 1
        }
        legacy_source_seen=1
        identity_mode=legacy-source
        shift
        ;;
      *)
        maintenance_error "unknown option: $1"
        return 1
        ;;
    esac
  done

  validate_target_label "$target_label" || return 1
  maintenance_init "$identity_mode" || return 1
  if [ "${TELEMETRY_UPDATE_LOCK_HELD:-}" = 1 ]; then
    validate_inherited_maintenance_lock || return 1
  else
    acquire_maintenance_lock || return 1
    trap release_maintenance_lock EXIT
  fi
  trap 'terminate_maintenance HUP' HUP
  trap 'terminate_maintenance INT' INT
  trap 'terminate_maintenance TERM' TERM
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
    [ -n "${TELEMETRY_TEST_HOLD_LOCK_SECONDS:-}" ]; then
    [[ $TELEMETRY_TEST_HOLD_LOCK_SECONDS =~ ^[0-9]+([.][0-9]+)?$ ]] || {
      maintenance_error 'TELEMETRY_TEST_HOLD_LOCK_SECONDS must be a nonnegative number'
      return 1
    }
    sleep "$TELEMETRY_TEST_HOLD_LOCK_SECONDS"
  fi
  if [ "$leave_stopped" -eq 1 ]; then
    if [ "$MAINTENANCE_IDENTITY_MODE" = legacy-source ]; then
      [ "${TELEMETRY_UPDATE_LOCK_HELD:-}" != 1 ] || {
        maintenance_error '--legacy-source cannot use an updater-owned maintenance lock'
        return 1
      }
      recover_transaction || return 1
      supervised_legacy=1
      transaction_id="backup-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
      release_dir=$CURRENT_BACKEND_DIR
      previous_release=$(basename -- "$release_dir") || return 1
      validate_target_label "$previous_release" || return 1
      [ "$release_dir" = "$BACKEND_ROOT/$previous_release" ] &&
        [ "$(resolve_active_backend_dir "$BACKEND_ROOT")" = "$release_dir" ] || {
        maintenance_error "active release directory is missing or unsafe: $release_dir"
        return 1
      }
    else
      [ "${TELEMETRY_UPDATE_LOCK_HELD:-}" = 1 ] || {
        maintenance_error '--leave-stopped is available only for an updater handoff or --legacy-source'
        return 1
      }
      [ "$(read_transaction OPERATION 2>/dev/null || true)" = update ] &&
        [ "$(read_transaction PHASE 2>/dev/null || true)" = backup-offline ] || {
        maintenance_error '--leave-stopped requires an update transaction in backup-offline phase'
        return 1
      }
      transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
      target_release=$(read_transaction TARGET_RELEASE 2>/dev/null || true)
      previous_release=$(read_transaction PREVIOUS_RELEASE 2>/dev/null || true)
      [ -n "$transaction_id" ] && validate_target_label "$target_release" && [ -n "$previous_release" ] || {
        maintenance_error '--leave-stopped requires a complete updater-owned transaction'
        return 1
      }
      handoff=1
      release_dir=$BACKEND_ROOT/$previous_release
    fi
  else
    recover_transaction || return 1
    transaction_id="backup-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
    if [ "$MAINTENANCE_IDENTITY_MODE" = legacy-source ]; then
      release_dir=$CURRENT_BACKEND_DIR
      previous_release=$(basename -- "$release_dir") || return 1
      validate_target_label "$previous_release" || return 1
      [ "$release_dir" = "$BACKEND_ROOT/$previous_release" ] &&
        [ "$(resolve_active_backend_dir "$BACKEND_ROOT")" = "$release_dir" ] || {
        maintenance_error "active release directory is missing or unsafe: $release_dir"
        return 1
      }
    else
      [ -L "$BACKEND_ROOT/latest" ] || {
        maintenance_error "active release link is missing: $BACKEND_ROOT/latest"
        return 1
      }
      previous_release=$(readlink -- "$BACKEND_ROOT/latest") || return 1
      validate_target_label "$previous_release" || return 1
      release_dir=$BACKEND_ROOT/$previous_release
      [ -d "$release_dir" ] && [ ! -L "$release_dir" ] || {
        maintenance_error "active release directory is missing or unsafe: $release_dir"
        return 1
      }
    fi
  fi
  mkdir -p -- "$BACKUP_ROOT" || return 1
  pin_active_images "$release_dir" || return 1
  docker image inspect "$HELPER_IMAGE" >/dev/null 2>&1 || docker pull "$HELPER_IMAGE" || return 1
  logical_volume_output=$(compose "$release_dir" config --volumes) || return 1
  logical_volumes=()
  while IFS= read -r logical_volume; do
    [ -z "$logical_volume" ] || logical_volumes+=("$logical_volume")
  done <<<"$logical_volume_output"
  [ "${#logical_volumes[@]}" -gt 0 ] || {
    maintenance_error 'Compose configuration declares no named volumes'
    return 1
  }
  total_bytes=$(du -sb --exclude=.env --exclude=.maintenance-transaction "$release_dir" | cut -f1) || return 1
  for ordinal in "${!logical_volumes[@]}"; do
    logical_volume=${logical_volumes[ordinal]}
    actual_volume=$(resolve_volume_name "$logical_volume") || return 1
    actual_volumes+=("$actual_volume")
    volume_bytes=$(measure_volume_bytes "$actual_volume" "$transaction_id" "$ordinal") || return 1
    [[ $volume_bytes =~ ^[0-9]+$ ]] || {
      maintenance_error "could not measure volume: $actual_volume"
      return 1
    }
    total_bytes=$((total_bytes + volume_bytes))
  done
  check_backup_capacity "$total_bytes" "$allow_large_backup" || return 1
  backup_parent=$(dirname -- "$BACKUP_ROOT") || return 1
  [ -d "$backup_parent" ] || {
    maintenance_error "backup parent directory is missing: $backup_parent"
    return 1
  }
  created_at=$(date -u +%Y%m%d-%H%M%SZ) || return 1
  completed_dir="$BACKUP_ROOT/pre-$target_label-$created_at"
  backup_work_dir="${completed_dir}.incomplete"
  [ ! -e "$completed_dir" ] && [ ! -e "$backup_work_dir" ] || {
    maintenance_error "backup directory already exists: $completed_dir"
    return 1
  }
  install -d -m 700 "$backup_work_dir/volumes" || return 1
  chmod 700 "$backup_work_dir" || return 1
  install -m 600 "$ENV_FILE" "$backup_work_dir/backend.env" || return 1
  archive_source "$backup_work_dir" "$release_dir" || return 1
  created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ) || return 1
  compose_version=$(docker compose version --short) || return 1
  {
    printf 'FORMAT_VERSION=1\n'
    printf 'UTC_CREATED=%s\n' "$created_at"
    printf 'SOURCE_RELEASE=%s\n' "$release_dir"
    printf 'PROJECT_NAME=%s\n' "$PROJECT_NAME"
    printf 'COMPOSE_VERSION=%s\n' "$compose_version"
    for ordinal in "${!logical_volumes[@]}"; do
      printf 'VOLUME=%s|%s|%s.tar.gz\n' "${logical_volumes[ordinal]}" "${actual_volumes[ordinal]}" \
        "${actual_volumes[ordinal]}"
    done
  } >"$backup_work_dir/manifest.txt" || return 1
  image_output=$(capture_backup_image_records "$release_dir") || return 1
  image_records=()
  previous_image_entries=()
  while IFS= read -r image_record; do
    [ -z "$image_record" ] || image_records+=("$image_record")
  done <<<"$image_output"
  [ "${#image_records[@]}" -eq 5 ] || {
    maintenance_error 'cannot capture complete backup image identities'
    return 1
  }
  for image_record in "${image_records[@]}"; do
    IFS='|' read -r service reference image_id extra <<<"$image_record"
    [ -n "$service" ] && [ -n "$reference" ] && [ -n "$image_id" ] && [ -z "${extra:-}" ] || {
      maintenance_error 'captured backup image identity is invalid'
      return 1
    }
    printf 'IMAGE=%s\n' "$image_record" >>"$backup_work_dir/manifest.txt" || return 1
    previous_image_entries+=("PREVIOUS_IMAGE_${service^^}=$image_id")
    record_test_event "IMAGE_MANIFEST=$service" || return 1
    if [ "$handoff" -eq 1 ] && \
      [ "$(read_transaction "PREVIOUS_IMAGE_${service^^}" 2>/dev/null || true)" != "$image_id" ]; then
      maintenance_error "updater image identity differs from the running service: $service"
      return 1
    fi
  done
  write_restore_helper "$backup_work_dir" || return 1
  write_restore_instructions "$backup_work_dir" || return 1
  bundle_maintenance_library "$backup_work_dir" || return 1
  write_static_checksums "$backup_work_dir" || return 1
  if [ "$handoff" -eq 1 ]; then
    rewrite_transaction_state backup-offline "$completed_dir" || return 1
  else
    write_transaction "FORMAT_VERSION=1" "TRANSACTION_ID=$transaction_id" "OPERATION=backup" \
      "PHASE=backup-offline" "PREVIOUS_RELEASE=$previous_release" "BACKUP_PATH=$completed_dir" \
      "${previous_image_entries[@]}" || return 1
  fi
  if ! validate_transaction_state; then
    if [ "$handoff" -eq 0 ]; then
      clear_transaction || return 1
    fi
    return 1
  fi
  if ! compose "$release_dir" down; then
    failed=1
  elif ! assert_backend_service_containers_absent; then
    failed=1
  else
    record_test_event COMPOSE_STOPPED || failed=1
    for ordinal in "${!logical_volumes[@]}"; do
      [ "$failed" -eq 0 ] || break
      actual_volume=${actual_volumes[ordinal]}
      archive_name="${actual_volume}.tar.gz"
      if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
        [ "${TELEMETRY_TEST_FAIL_BACKUP_HELPER:-}" = 1 ]; then
        failed=1
        break
      fi
      if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ]; then
        helper_delay=${TELEMETRY_TEST_BACKUP_HELPER_DELAY_SECONDS:-0}
      fi
      if ! helper_id=$(docker run -d --name "skills-telemetry-backup-${transaction_id}-${ordinal}" \
        --label "$TRANSACTION_LABEL=$transaction_id" --label "$ROLE_LABEL=backup" \
        --env "BACKUP_FILE=$archive_name" --env "HELPER_DELAY_SECONDS=$helper_delay" \
        --mount "type=volume,src=$actual_volume,dst=/source,readonly" \
        --mount "type=bind,src=$backup_work_dir/volumes,dst=/backup" \
        "$HELPER_IMAGE" sh -eu -c 'sleep "$HELPER_DELAY_SECONDS"; cd /source; tar -czf "/backup/$BACKUP_FILE" .'); then
        failed=1
        break
      fi
      test_kill_checkpoint backup-helper-running
      if [ -n "${maintenance_lock_fd:-}" ]; then
        helper_status=$(docker wait "$helper_id" {maintenance_lock_fd}>&-) || helper_status=125
      else
        helper_status=$(docker wait "$helper_id") || helper_status=125
      fi
      if [ "$helper_status" != 0 ]; then
        failed=1
      fi
      docker rm "$helper_id" >/dev/null 2>&1 || failed=1
      [ "$failed" -eq 0 ] || break
      tar -tzf "$backup_work_dir/volumes/$archive_name" >/dev/null || {
        failed=1
        break
      }
      record_test_event "VOLUME_ARCHIVE=$actual_volume" || {
        failed=1
        break
      }
    done
  fi
  if [ "$failed" -eq 0 ]; then
    (
      cd "$backup_work_dir" || exit 1
      sha256sum backend.env backup-backend.sh telemetry-backend-source.tar.gz manifest.txt RESTORE.md restore-backend.sh \
        volumes/*.tar.gz >SHA256SUMS || exit 1
      if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
        [ "${TELEMETRY_TEST_CORRUPT_BACKUP_CHECKSUM:-}" = 1 ]; then
        printf 'corrupt\n' >>backend.env || exit 1
      fi
      sha256sum -c SHA256SUMS || exit 1
    ) || failed=1
  fi
  if [ "$failed" -eq 0 ]; then
    if ! mv -T -- "$backup_work_dir" "$completed_dir" || ! sync -f "$BACKUP_ROOT"; then
      failed=1
    else
      record_test_event BACKUP_PUBLISHED || failed=1
    fi
  fi
  if [ "$failed" -ne 0 ]; then
    cleanup_transaction_helpers "$transaction_id" || return 1
    if restart_original_stack "$release_dir"; then
      assert_transaction_helpers_absent "$transaction_id" || return 1
      clear_transaction || return 1
    else
      maintenance_error 'backup failed and the original stack did not recover'
    fi
    return 1
  fi
  cleanup_transaction_helpers "$transaction_id" || return 1
  assert_transaction_helpers_absent "$transaction_id" || return 1
  if [ "$handoff" -eq 1 ]; then
    rewrite_transaction_state activation-prepared "$completed_dir" || return 1
    return 0
  fi
  if [ "$supervised_legacy" -eq 1 ]; then
    clear_transaction || return 1
    return 0
  fi
  if restart_original_stack "$release_dir"; then
    assert_transaction_helpers_absent "$transaction_id" || return 1
    clear_transaction || return 1
    return 0
  fi
  maintenance_error 'backup completed but the original stack did not recover'
  return 1
}

if [ "${TELEMETRY_SOURCE_ONLY:-}" != 1 ]; then
  backup_main "$@"
fi
