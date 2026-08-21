#!/usr/bin/env bash
# Validation chains deliberately run their error block when any prerequisite fails.
# shellcheck disable=SC2015

set -euo pipefail

readonly GITHUB_REPOSITORY='Netcracker/qubership-ai-agent-telemetry'
readonly GITHUB_API_DEFAULT='https://api.github.com'
readonly RELEASE_ASSET='ai-agent-telemetry-backend.tar.gz'
readonly CHECKSUM_ASSET='SHA256SUMS'
readonly ACTIVE_MARKER='.deployment-success'
readonly NORMALIZED_CHECKSUMS='.normalized-content-checksums'
readonly -a RESERVED_SOURCE_PATHS=(
  .env .deployment-manifest .deployment-success .maintenance-compose.yml .maintenance-transaction \
  .maintenance-active "$NORMALIZED_CHECKSUMS"
)
readonly SEMVER_PATTERN='^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
declare -a UPDATE_TEMP_DIRS=()
UPDATE_TEMP_COUNT=0

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
# shellcheck disable=SC1091 # The maintenance library is installed beside this command.
TELEMETRY_SOURCE_ONLY=1 source "$script_dir/backup-backend.sh"

update_error() {
  maintenance_error "$@"
}

register_update_temp() {
  UPDATE_TEMP_DIRS+=("$1")
}

create_update_temp() {
  local template=${1:-}

  UPDATE_TEMP_COUNT=$((UPDATE_TEMP_COUNT + 1))
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] &&
    [ "${TELEMETRY_TEST_FAIL_TEMP_AT:-}" = "$UPDATE_TEMP_COUNT" ]; then
    update_error "injected temporary-directory failure at allocation $UPDATE_TEMP_COUNT"
    return 1
  fi
  if [ -n "$template" ]; then
    UPDATE_TEMP_DIR=$(mktemp -d "$template") || return 1
  else
    UPDATE_TEMP_DIR=$(mktemp -d) || return 1
  fi
  register_update_temp "$UPDATE_TEMP_DIR"
}

cleanup_update_temps() {
  local path

  for path in "${UPDATE_TEMP_DIRS[@]:-}"; do
    [ -d "$path" ] || continue
    rm -rf -- "$path"
  done
  UPDATE_TEMP_DIRS=()
}

github_get() {
  local url=$1 output=$2
  local -a headers=(-H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28')

  [[ -z ${GH_TOKEN:-} ]] || headers+=(-H "Authorization: Bearer $GH_TOKEN")
  curl --fail --silent --show-error --location "${headers[@]}" --output "$output" "$url"
}

json_query() {
  local response_dir=$1 query=$2

  docker run --rm --mount "type=bind,src=$response_dir,dst=/input,readonly" "$JSON_IMAGE" \
    jq -r "$query" /input/response.json
}

encode_ref() (
  local requested=$1 response_dir encoded

  create_update_temp || exit 1
  response_dir=$UPDATE_TEMP_DIR
  trap 'rm -rf -- "$response_dir"' EXIT
  printf '%s\n' '{}' >"$response_dir/response.json"
  if ! encoded=$(docker run --rm --mount "type=bind,src=$response_dir,dst=/input,readonly" "$JSON_IMAGE" \
    jq -nr --arg ref "$requested" '$ref|@uri'); then
    exit 1
  fi
  printf '%s\n' "$encoded"
)

is_semver() {
  [[ $1 =~ $SEMVER_PATTERN ]]
}

validate_sha() {
  [[ $1 =~ ^[0-9a-f]{40}$ ]] || {
    update_error "GitHub returned an invalid commit SHA: $1"
    return 1
  }
}

require_single_value() {
  local description=$1
  shift
  [ "$#" -eq 1 ] && [ -n "$1" ] && [ "$1" != null ] || {
    update_error "$description is missing or ambiguous"
    return 1
  }
  printf '%s\n' "$1"
}

release_target_id() {
  local tag=$1 digest

  digest=$(printf '%s' "$tag" | sha256sum | awk '{print $1}')
  printf 'release-%s\n' "${digest:0:20}"
}

safe_extract_backend() {
  local archive=$1 destination=$2 checksums=$3 archive_format=$4

  python3 - "$archive" "$destination" "$checksums" "$archive_format" "${RESERVED_SOURCE_PATHS[@]}" <<'PY'
import hashlib
import os
import pathlib
import sys
import tarfile

archive, destination, checksum_path, archive_format, *reserved = sys.argv[1:]
if archive_format not in {"release", "commit"}:
    raise SystemExit(f"unsupported backend archive format: {archive_format!r}")
reserved_set = set(reserved)
entries = []
seen = set()
regular_paths = set()
root = None

try:
    bundle = tarfile.open(archive, "r:*")
except (OSError, tarfile.TarError) as error:
    raise SystemExit(f"source archive is invalid: {error}")

with bundle:
    for member in bundle:
        raw = member.name
        if not raw or raw.startswith("/"):
            raise SystemExit(f"source archive contains an unsafe path: {raw!r}")
        parts = raw.split("/")
        if parts[-1] == "":
            parts.pop()
        if not parts or any(part in {"", ".", ".."} for part in parts):
            raise SystemExit(f"source archive contains an unsafe path: {raw!r}")
        if archive_format == "commit":
            if root is None:
                root = parts[0]
            if parts[0] != root:
                raise SystemExit(f"source archive has an unexpected layout: {raw!r}")
            if len(parts) == 1 and not member.isdir():
                raise SystemExit(f"source archive root is not a directory: {raw!r}")
            if len(parts) > 1 and parts[1] != "telemetry-backend":
                continue
            if len(parts) == 2 and not member.isdir():
                raise SystemExit(f"source archive backend root is not a directory: {raw!r}")
            relative = "/".join(parts[2:])
        else:
            relative = "/".join(parts)
        if not (member.isdir() or member.isreg()):
            raise SystemExit(f"source archive contains an unsupported entry type: {raw!r}")
        canonical = "/".join(parts) if archive_format == "commit" else relative
        if canonical in seen:
            raise SystemExit(f"source archive contains a duplicate path: {canonical!r}")
        seen.add(canonical)
        if relative in reserved_set:
            raise SystemExit(f"source archive contains a reserved generated path: {relative!r}")
        if relative:
            entries.append((relative, member))
            if member.isreg():
                regular_paths.add(relative)

    if archive_format == "commit" and root is None:
        raise SystemExit("source archive is empty")
    if "docker-compose.yml" not in regular_paths:
        raise SystemExit("backend archive lacks docker-compose.yml")
    for relative, member in entries:
        ancestors = pathlib.PurePosixPath(relative).parents
        if any(str(parent) in regular_paths for parent in ancestors):
            raise SystemExit(f"source archive has a file/directory collision: {relative!r}")

    backend = pathlib.Path(destination) / "backend"
    backend.mkdir(parents=True, exist_ok=False)
    for relative, member in sorted(entries, key=lambda item: os.fsencode(item[0])):
        path = backend.joinpath(*relative.split("/"))
        if member.isdir():
            path.mkdir(parents=True, exist_ok=False)
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        source = bundle.extractfile(member)
        if source is None:
            raise SystemExit(f"source archive cannot read regular file: {relative!r}")
        digest = hashlib.sha256()
        with source, open(path, "xb") as target:
            while chunk := source.read(1024 * 1024):
                digest.update(chunk)
                target.write(chunk)

    # Hash the retained tree, not the archive's directory-header choices. Empty directories are retained and framed.
    frames = []
    for current, directories, filenames in os.walk(backend):
        directories.sort(key=os.fsencode)
        filenames.sort(key=os.fsencode)
        current_path = pathlib.Path(current)
        for directory in directories:
            relative = os.fsencode((current_path / directory).relative_to(backend).as_posix())
            frames.append((b"D", relative, b""))
        for filename in filenames:
            path = current_path / filename
            relative = os.fsencode(path.relative_to(backend).as_posix())
            file_hash = hashlib.sha256()
            with open(path, "rb") as retained_file:
                while chunk := retained_file.read(1024 * 1024):
                    file_hash.update(chunk)
            digest = file_hash.digest()
            frames.append((b"F", relative, digest))

content = hashlib.sha256()
with open(checksum_path, "w", encoding="ascii") as checksums:
    for entry_type, encoded_path, digest in frames:
        content.update(entry_type)
        content.update(len(encoded_path).to_bytes(8, "big"))
        content.update(encoded_path)
        content.update(digest)
        checksums.write(f"{entry_type.decode()} {encoded_path.hex()} {digest.hex()}\n")
print(content.hexdigest())
PY
}

manifest_value() {
  local manifest=$1 wanted=$2 key value matches=0 result=

  while IFS='=' read -r key value; do
    [ "$key" = "$wanted" ] || continue
    matches=$((matches + 1))
    result=$value
  done <"$manifest"
  [ "$matches" -eq 1 ] || return 1
  printf '%s\n' "$result"
}

release_compose() {
  local release_dir=$1 override_file
  local -a command

  shift
  override_file=$release_dir/.maintenance-compose.yml
  command=(docker compose --project-name "$PROJECT_NAME" --project-directory "$release_dir" --env-file "$release_dir/.env" \
    -f "$release_dir/docker-compose.yml")
  [ ! -f "$override_file" ] || command+=(-f "$override_file")
  "${command[@]}" "$@"
}

compose_rows() {
  local release_dir=$1 response_dir

  create_update_temp || return 1
  response_dir=$UPDATE_TEMP_DIR
  release_compose "$release_dir" config --format json >"$response_dir/response.json" || return 1
  json_query "$response_dir" \
    '.services | to_entries[] | {service: .key, image: (.value.image // null), build: has("build")} | @json' \
    >"$response_dir/entries.jsonl" || return 1
  [ -s "$response_dir/entries.jsonl" ] || {
    update_error 'Compose configuration declares no services'
    return 1
  }
  COMPOSE_MODEL_FILE=$response_dir/entries.jsonl
}

compose_entry_field() (
  local entry=$1 query=$2 response_dir value

  create_update_temp || exit 1
  response_dir=$UPDATE_TEMP_DIR
  trap 'rm -rf -- "$response_dir"' EXIT
  printf '%s\n' "$entry" >"$response_dir/response.json"
  if ! value=$(json_query "$response_dir" "$query"); then
    exit 1
  fi
  printf '%s\n' "$value"
)

repository_from_image() {
  local image=$1 repository=${1%@*}

  if [[ ${repository##*/} == *:* ]]; then
    repository=${repository%:*}
  fi
  printf '%s\n' "$repository"
}

inspect_image_document() {
  local image=$1 response_dir=$2

  docker image inspect "$image" >"$response_dir/response.json"
}

resolve_registry_image() (
  local image=$1 response_dir repository candidate id verified_id
  local -a candidates

  create_update_temp || exit 1
  response_dir=$UPDATE_TEMP_DIR
  trap 'rm -rf -- "$response_dir"' EXIT
  inspect_image_document "$image" "$response_dir" || exit 1
  id=$(json_query "$response_dir" '.[0].Id') || exit 1
  json_query "$response_dir" '.[0].RepoDigests[]' >"$response_dir/digests" || exit 1
  mapfile -t candidates <"$response_dir/digests"
  repository=$(repository_from_image "$image")
  # Keep only exact repository matches after obtaining the digest and local ID from the same inspection document.
  local -a matching=()
  for candidate in "${candidates[@]}"; do
    [ "${candidate%@*}" = "$repository" ] && matching+=("$candidate")
  done
  candidate=$(require_single_value "repository digest for $image" "${matching[@]}") || exit 1
  verified_id=$(docker image inspect --format '{{.Id}}' "$candidate") || exit 1
  [ "$verified_id" = "$id" ] || {
    update_error "repository digest changed while staging $image"
    exit 1
  }
  printf '%s|%s\n' "$candidate" "$id"
)

prepare_images() {
  local release_dir=$1 target_id=$2 entry service image build identity reference local_id override_tmp
  local -a services
  local -A effective_images=() local_ids=()

  compose_rows "$release_dir" || {
    update_error 'cannot parse Compose configuration before image staging'
    return 1
  }
  while IFS= read -r entry; do
    service=$(compose_entry_field "$entry" .service) || return 1
    image=$(compose_entry_field "$entry" '.image // ""') || return 1
    build=$(compose_entry_field "$entry" .build) || return 1
    [[ $service =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ ]] || {
      update_error "Compose declares an invalid service name: $service"
      return 1
    }
    [ -z "${effective_images[$service]:-}" ] || {
      update_error "Compose declares a duplicate service: $service"
      return 1
    }
    services+=("$service")
    if [ "$build" = true ]; then
      [ "$service" = grafana ] && [ -z "$image" ] || {
        update_error "unsupported build service configuration: $service"
        return 1
      }
      effective_images[$service]="${PROJECT_NAME}-grafana:$target_id"
      continue
    fi
    [ -n "$image" ] || {
      update_error "Compose service has neither image nor supported build: $service"
      return 1
    }
    docker pull "$image"
    if ! identity=$(resolve_registry_image "$image"); then
      return 1
    fi
    reference=${identity%%|*}
    local_id=${identity#*|}
    effective_images[$service]=$reference
    local_ids[$service]=$local_id
  done <"$COMPOSE_MODEL_FILE"
  override_tmp=$(mktemp "$release_dir/.maintenance-compose.yml.XXXXXX")
  {
    printf 'services:\n'
    for service in "${services[@]}"; do
      printf '  %s:\n    image: %s\n' "$service" "${effective_images[$service]}"
    done
  } >"$override_tmp"
  mv -Tf -- "$override_tmp" "$release_dir/.maintenance-compose.yml"
  release_compose "$release_dir" config --quiet
  if [[ " ${services[*]} " == *' grafana '* ]]; then
    release_compose "$release_dir" build grafana
    local_ids[grafana]=$(docker image inspect --format '{{.Id}}' "${effective_images[grafana]}")
  fi
  for service in "${services[@]}"; do
    printf 'image.%s=%s|%s\n' "$service" "${effective_images[$service]}" "${local_ids[$service]}"
  done
}

resolve_source() {
  local ref=$1 api_base encoded_ref response_dir response_file download_dir archive checksum_file asset_file
  local tag sha asset_name asset_url checksum_line checksum
  local -a asset_rows archives checksum_assets

  kind=
  requested_ref=$ref
  resolved_identity=
  content_identity=
  download_url=
  target_id=
  transport_checksum=
  api_base=${TELEMETRY_GITHUB_API_URL:-$GITHUB_API_DEFAULT}
  create_update_temp || return 1
  response_dir=$UPDATE_TEMP_DIR
  create_update_temp || return 1
  download_dir=$UPDATE_TEMP_DIR
  response_file=$response_dir/response.json
  docker pull "$JSON_IMAGE"
  if [ "$requested_ref" = latest ] || is_semver "$requested_ref"; then
    kind=release
    if [ "$requested_ref" = latest ]; then
      github_get "$api_base/repos/$GITHUB_REPOSITORY/releases/latest" "$response_file"
    else
      encoded_ref=$(encode_ref "$requested_ref")
      github_get "$api_base/repos/$GITHUB_REPOSITORY/releases/tags/$encoded_ref" "$response_file"
    fi
    tag=$(json_query "$response_dir" .tag_name)
    is_semver "$tag" || {
      update_error "GitHub returned an invalid release tag: $tag"
      return 1
    }
    resolved_identity=$tag
    target_id=$(release_target_id "$tag")
    asset_file=$response_dir/assets
    json_query "$response_dir" '.assets[] | [.name, .browser_download_url] | @tsv' >"$asset_file"
    mapfile -t asset_rows <"$asset_file"
    for checksum_line in "${asset_rows[@]}"; do
      asset_name=${checksum_line%%$'\t'*}
      asset_url=${checksum_line#*$'\t'}
      case "$asset_name" in
        "$RELEASE_ASSET") archives+=("$asset_url") ;;
        "$CHECKSUM_ASSET") checksum_assets+=("$asset_url") ;;
      esac
    done
    download_url=$(require_single_value "release archive" "${archives[@]}")
    checksum_file=$download_dir/SHA256SUMS
    github_get "$(require_single_value "release checksum asset" "${checksum_assets[@]}")" "$checksum_file"
    checksum_line=$(awk -v name="$RELEASE_ASSET" '$2 == name { count++; value=$1 } END { if (count == 1) print value; else exit 1 }' "$checksum_file") || {
      update_error 'release checksum asset must contain exactly one archive checksum'
      return 1
    }
    [[ $checksum_line =~ ^[0-9a-fA-F]{64}$ ]] || {
      update_error 'release checksum asset contains an invalid checksum'
      return 1
    }
    archive=$download_dir/backend.tar.gz
    github_get "$download_url" "$archive"
    checksum=$(sha256sum "$archive" | awk '{print $1}')
    [ "${checksum,,}" = "${checksum_line,,}" ] || {
      update_error 'release archive checksum does not match SHA256SUMS'
      return 1
    }
    transport_checksum=$checksum
  else
    kind=commit
    encoded_ref=$(encode_ref "$requested_ref")
    github_get "$api_base/repos/$GITHUB_REPOSITORY/commits/$encoded_ref" "$response_file"
    sha=$(json_query "$response_dir" .sha)
    validate_sha "$sha"
    resolved_identity=$sha
    target_id=${sha:0:12}
    download_url="$api_base/repos/$GITHUB_REPOSITORY/tarball/$sha"
    archive=$download_dir/backend.tar.gz
    github_get "$download_url" "$archive"
    transport_checksum=$(sha256sum "$archive" | awk '{print $1}')
  fi
  RESOLVED_CHECKSUMS_FILE=$download_dir/$NORMALIZED_CHECKSUMS
  content_identity=$(safe_extract_backend "$archive" "$download_dir/extract" "$RESOLVED_CHECKSUMS_FILE" "$kind")
  RESOLVED_BACKEND_DIR=$download_dir/extract/backend
  rm -rf -- "$response_dir"
}

is_active_target() {
  local release_dir=$1 release_id

  [ -L "$BACKEND_ROOT/latest" ] || return 1
  release_id=$(basename -- "$release_dir")
  [ "$(readlink -- "$BACKEND_ROOT/latest")" = "$release_id" ]
}

validate_reusable_target() {
  local release_dir=$1 entry service image build record reference recorded_id actual_id key
  local -a required=(.env .deployment-manifest .maintenance-compose.yml "$NORMALIZED_CHECKSUMS" docker-compose.yml)

  for required in "${required[@]}"; do
    [ -f "$release_dir/$required" ] || {
      update_error "existing target lacks required staged file: $required"
      return 1
    }
  done
  [ "$(stat -c '%a' "$release_dir/.env")" = 600 ] &&
    [ "$(stat -c '%a' "$release_dir/.deployment-manifest")" = 600 ] || {
      update_error 'existing target has unsafe secret or manifest permissions'
      return 1
    }
  for key in format kind requested_ref resolved_identity content_identity transport_checksum download_url; do
    manifest_value "$release_dir/.deployment-manifest" "$key" >/dev/null || {
      update_error "existing target lacks required manifest key: $key"
      return 1
    }
  done
  [ "$(manifest_value "$release_dir/.deployment-manifest" format)" = 1 ] &&
    [ "$(manifest_value "$release_dir/.deployment-manifest" kind)" = "$kind" ] &&
    [ "$(manifest_value "$release_dir/.deployment-manifest" resolved_identity)" = "$resolved_identity" ] &&
    [ "$(manifest_value "$release_dir/.deployment-manifest" content_identity)" = "$content_identity" ] || {
      update_error "release target exists with different or incomplete immutable content: $release_dir"
      return 1
    }
  cmp -s -- "$release_dir/$NORMALIZED_CHECKSUMS" "$RESOLVED_CHECKSUMS_FILE" || {
    update_error 'existing target normalized content checksums changed'
    return 1
  }
  compose_rows "$release_dir" || {
    update_error 'cannot parse existing target Compose configuration'
    return 1
  }
  while IFS= read -r entry; do
    service=$(compose_entry_field "$entry" .service) || return 1
    image=$(compose_entry_field "$entry" '.image // ""') || return 1
    build=$(compose_entry_field "$entry" .build) || return 1
    [ "$build" = false ] || [ "$service" = grafana ] || {
      update_error "existing target has an unsupported build service: $service"
      return 1
    }
    [ -n "$image" ] || {
      update_error "existing target service lacks an effective image: $service"
      return 1
    }
    record=$(manifest_value "$release_dir/.deployment-manifest" "image.$service") || {
      update_error "existing target lacks an image record for service: $service"
      return 1
    }
    reference=${record%%|*}
    recorded_id=${record#*|}
    [ "$reference" = "$image" ] || {
      update_error "existing target image differs from its manifest: $service"
      return 1
    }
    [ "$reference" != "$recorded_id" ] && [ -n "$recorded_id" ] || {
      update_error "existing target has an invalid image record for service: $service"
      return 1
    }
    actual_id=$(docker image inspect --format '{{.Id}}' "$reference") || {
      update_error "existing target image is unavailable: $reference"
      return 1
    }
    [ "$actual_id" = "$recorded_id" ] || {
      update_error "existing target image ID changed: $reference"
      return 1
    }
  done <"$COMPOSE_MODEL_FILE"
}

replace_latest() {
  local release_id=$1 temporary_link transaction_id

  validate_target_label "$release_id" || return 1
  [ -d "$BACKEND_ROOT/$release_id" ] && [ ! -L "$BACKEND_ROOT/$release_id" ] || {
    update_error "release target is missing or unsafe: $BACKEND_ROOT/$release_id"
    return 1
  }
  transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
  [ -n "$transaction_id" ] || transaction_id="link-$$-$RANDOM"
  validate_target_label "$transaction_id" || {
    update_error 'transaction ID cannot be used for link replacement'
    return 1
  }
  temporary_link=$BACKEND_ROOT/.latest.$transaction_id
  rm -f -- "$temporary_link"
  ln -s -- "$release_id" "$temporary_link"
  if ! mv -Tf -- "$temporary_link" "$BACKEND_ROOT/latest"; then
    rm -f -- "$temporary_link"
    return 1
  fi
  sync -f "$BACKEND_ROOT"
}

verify_running_image_ids() {
  local release_dir=$1 transaction_file=$2 image_role=${3:-PREVIOUS} service container expected_id key
  local -a services containers

  mapfile -t services < <(release_compose "$release_dir" config --services)
  [ "${#services[@]}" -eq 5 ] || {
    update_error 'restored Compose configuration must declare exactly five services'
    return 1
  }
  for service in caddy collector grafana victorialogs victoriametrics; do
    [[ " ${services[*]} " == *" $service "* ]] || {
      update_error "restored Compose configuration lacks required service: $service"
      return 1
    }
    key=${image_role}_IMAGE_${service^^}
    expected_id=$(read_transaction "$key" 2>/dev/null || true)
    [ -n "$expected_id" ] || {
      update_error "transaction lacks the previous image ID for service: $service"
      return 1
    }
    mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service")
    [ "${#containers[@]}" -eq 1 ] || {
      update_error "restored service mapping is missing or ambiguous: $service"
      return 1
    }
    container=${containers[0]}
    [ "$(docker inspect --format '{{.Image}}' "$container")" = "$expected_id" ] || {
      update_error "restored service uses an unexpected image ID: $service"
      return 1
    }
  done
  : "$transaction_file"
}

rollback_transaction() {
  local previous_id target_id previous_dir target_dir backup_path failed_target transaction_file transaction_id

  transaction_id=$(read_transaction TRANSACTION_ID 2>/dev/null || true)
  previous_id=$(read_transaction PREVIOUS_RELEASE 2>/dev/null || true)
  target_id=$(read_transaction TARGET_RELEASE 2>/dev/null || true)
  backup_path=$(read_transaction BACKUP_PATH 2>/dev/null || true)
  if ! validate_target_label "$previous_id" || ! validate_target_label "$target_id"; then
    update_error 'cannot roll back an incomplete maintenance transaction'
    return 1
  fi
  previous_dir=$BACKEND_ROOT/$previous_id
  target_dir=$BACKEND_ROOT/$target_id
  # shellcheck disable=SC2153 # maintenance_init initializes this sourced-library global.
  transaction_file=$TRANSACTION_FILE
  failed_target=$target_id
  cleanup_transaction_helpers "$transaction_id" || return 1
  if ! release_compose "$target_dir" down; then
    update_error "cannot stop failed release $failed_target; transaction retained"
    return 1
  fi
  if ! replace_latest "$previous_id" ||
    ! release_compose "$previous_dir" up -d --no-build --pull never ||
    ! verify_running_image_ids "$previous_dir" "$transaction_file" ||
    ! strict_health_gate "$previous_dir" ||
    ! assert_transaction_helpers_absent "$transaction_id"; then
    update_error "rollback of $failed_target to $previous_id is not healthy; backup retained at ${backup_path:-unknown}"
    return 1
  fi
  clear_transaction
  update_error "update to $failed_target failed; restored $previous_id; backup retained at ${backup_path:-unknown}"
}

write_deployment_success() {
  local release_dir=$1 transaction_id=$2 marker marker_tmp
  local resolved_identity content_identity manifest_checksum

  marker=$release_dir/$ACTIVE_MARKER
  resolved_identity=$(manifest_value "$release_dir/.deployment-manifest" resolved_identity 2>/dev/null || true)
  content_identity=$(manifest_value "$release_dir/.deployment-manifest" content_identity 2>/dev/null || true)
  manifest_checksum=$(sha256sum "$release_dir/.deployment-manifest" | awk '{print $1}')
  marker_tmp=$(mktemp "$release_dir/.deployment-success.XXXXXX")
  {
    printf 'format=1\n'
    printf 'transaction_id=%s\n' "$transaction_id"
    printf 'resolved_identity=%s\n' "$resolved_identity"
    printf 'content_identity=%s\n' "$content_identity"
    printf 'manifest_sha256=%s\n' "$manifest_checksum"
    printf 'verified_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    sed -n '/^image\.[a-zA-Z0-9_-]*=/p' "$release_dir/.deployment-manifest"
  } >"$marker_tmp"
  chmod 600 "$marker_tmp"
  if ! sync -f "$marker_tmp" || ! mv -Tf -- "$marker_tmp" "$marker"; then
    rm -f -- "$marker_tmp"
    return 1
  fi
  sync -f "$release_dir"
}

activate_transaction() {
  local phase transaction_id target_id target_dir

  phase=$(read_transaction PHASE 2>/dev/null || true)
  [ "$phase" = activation-prepared ] || {
    update_error "cannot activate maintenance transaction phase: ${phase:-missing}"
    return 1
  }
  transaction_id=$(read_transaction TRANSACTION_ID)
  target_id=$(read_transaction TARGET_RELEASE)
  validate_target_label "$transaction_id" && validate_target_label "$target_id" || return 1
  target_dir=$BACKEND_ROOT/$target_id
  test_kill_checkpoint activation-prepared
  replace_latest "$target_id" || return 1
  test_kill_checkpoint symlink-replaced
  rewrite_transaction_state activated "$(read_transaction BACKUP_PATH 2>/dev/null || true)" || return 1
  release_compose "$target_dir" up -d --no-build --pull never || return 1
  test_kill_checkpoint target-started
  verify_running_image_ids "$target_dir" "$TRANSACTION_FILE" TARGET || return 1
  strict_health_gate "$target_dir" || return 1
  assert_transaction_helpers_absent "$transaction_id" || return 1
  test_kill_checkpoint health-passed
  write_deployment_success "$target_dir" "$transaction_id" || return 1
  test_kill_checkpoint success-marker-written
  rewrite_transaction_state committed "$(read_transaction BACKUP_PATH 2>/dev/null || true)" || return 1
  test_kill_checkpoint committed-written
  assert_transaction_helpers_absent "$transaction_id" || return 1
  clear_transaction
}

prune_backups() {
  local explicit_prune=$1 current_backup=${2:-} preclaim_hook='' postclaim_hook='' beforedelete_hook=''
  local entryremove_hook='' finalremove_hook='' stop_at='' hook_name hook_path

  for hook_name in TELEMETRY_TEST_RETENTION_HOOK_PRECLAIM TELEMETRY_TEST_RETENTION_HOOK_POSTCLAIM \
    TELEMETRY_TEST_RETENTION_HOOK_BEFORE_DELETE TELEMETRY_TEST_RETENTION_HOOK_BEFORE_ENTRY_REMOVE \
    TELEMETRY_TEST_RETENTION_HOOK_BEFORE_FINAL_REMOVE; do
    hook_path=${!hook_name:-}
    [ -z "$hook_path" ] && continue
    [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] || {
      update_error 'test-only backup retention controls require TELEMETRY_MAINTENANCE_TEST_MODE=1'
      return 0
    }
    hook_path=$(validate_test_path "$hook_name" "$hook_path") || return 0
    [ -f "$hook_path" ] && [ -x "$hook_path" ] || {
      update_error 'backup retention test hooks must be executable files inside /tmp'
      return 0
    }
    case "$hook_name" in
      TELEMETRY_TEST_RETENTION_HOOK_PRECLAIM) preclaim_hook=$hook_path ;;
      TELEMETRY_TEST_RETENTION_HOOK_POSTCLAIM) postclaim_hook=$hook_path ;;
      TELEMETRY_TEST_RETENTION_HOOK_BEFORE_DELETE) beforedelete_hook=$hook_path ;;
      TELEMETRY_TEST_RETENTION_HOOK_BEFORE_ENTRY_REMOVE) entryremove_hook=$hook_path ;;
      TELEMETRY_TEST_RETENTION_HOOK_BEFORE_FINAL_REMOVE) finalremove_hook=$hook_path ;;
    esac
  done

  stop_at=${TELEMETRY_TEST_RETENTION_STOP_AT:-}
  if [ -n "$stop_at" ]; then
    [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] || {
      update_error 'test-only backup retention controls require TELEMETRY_MAINTENANCE_TEST_MODE=1'
      return 0
    }
    case "$stop_at" in
      claimed|deleting|partial|payload-complete|tombstoned|completed|\
        state-fsync-payload-complete|state-exchange-payload-complete|\
        state-fsync-completed|state-exchange-completed|\
        state-fsync-restored|state-exchange-restored) ;;
      *)
        update_error \
          'TELEMETRY_TEST_RETENTION_STOP_AT names an unsupported retention checkpoint'
        return 0
        ;;
    esac
  fi

  if ! python3 - "$BACKUP_ROOT" "$current_backup" "$explicit_prune" "$preclaim_hook" "$postclaim_hook" \
    "$beforedelete_hook" "$entryremove_hook" "$finalremove_hook" "$stop_at" <<'PY'
import calendar
import base64
import ctypes
import datetime
import errno
import json
import os
import re
import secrets
import stat
import subprocess
import sys


TIMESTAMP = re.compile(rb"[0-9]{8}-[0-9]{6}Z\Z")
ORIGINAL_NAME = re.compile(rb"pre-[A-Za-z0-9][A-Za-z0-9._-]*-[0-9]{8}-[0-9]{6}Z\Z")
QUARANTINE_NAME = re.compile(r"\.retention-([1-9][0-9]*)-([0-9a-f]{32})\Z")
STATE_NAME = re.compile(r"\.retention-state-([1-9][0-9]*)-([0-9a-f]{32})\Z")
TEMP_STATE_NAME = re.compile(r"\.retention-state-tmp-([1-9][0-9]*)-([0-9a-f]{32})\Z")
TOMBSTONE_NAME = re.compile(r"\.retention-tombstone-([1-9][0-9]*)-([0-9a-f]{32})\Z")
LEDGER_NAME = re.compile(
    r"\.maintenance-retention-ledger-(?:obsolete|cleared|interrupted)-([1-9][0-9]*)-([0-9a-f]{32})\Z"
)
TEST_STOP_AT = sys.argv[9]


class RetentionError(Exception):
    pass


def report(message):
    print(f"ERROR: Backup retention skipped: {message}", file=sys.stderr)


def entry_timestamp(name):
    encoded = os.fsencode(name)
    if ORIGINAL_NAME.fullmatch(encoded) is None:
        return None
    timestamp = encoded[-16:]
    if encoded[-17:-16] != b"-" or TIMESTAMP.fullmatch(timestamp) is None:
        return None
    try:
        parsed = datetime.datetime.strptime(timestamp.decode("ascii"), "%Y%m%d-%H%M%SZ")
    except ValueError:
        return None
    return calendar.timegm(parsed.timetuple())


def root_matches(root_path, root_stat):
    try:
        current = os.stat(root_path, follow_symlinks=False)
    except OSError:
        return False
    return stat.S_ISDIR(current.st_mode) and (current.st_dev, current.st_ino) == root_stat


def list_directory(directory_fd):
    os.lseek(directory_fd, 0, os.SEEK_SET)
    return os.listdir(directory_fd)


def completed_backups(root_fd):
    try:
        names = list_directory(root_fd)
    except OSError as error:
        raise RetentionError(f"cannot list the backup root: {error}") from error

    completed = []
    for name in names:
        timestamp = entry_timestamp(name)
        if timestamp is None:
            continue
        try:
            entry = os.stat(name, dir_fd=root_fd, follow_symlinks=False)
        except OSError as error:
            raise RetentionError(f"cannot inspect backup entry {os.fsencode(name)!r}: {error}") from error
        if stat.S_ISDIR(entry.st_mode):
            completed.append((timestamp, os.fsencode(name), name, entry.st_dev, entry.st_ino))
    completed.sort(key=lambda entry: (entry[0], entry[1]))
    return completed


def current_backup_name(root_path, current_backup):
    if not current_backup:
        return None
    root = os.path.normpath(os.fsencode(root_path))
    current = os.path.normpath(os.fsencode(current_backup))
    relative = os.path.relpath(current, root)
    if os.path.dirname(relative) or relative in {b".", b".."}:
        return None
    return os.fsdecode(relative)


def selectable(completed, cutoff_epoch, current_name):
    protected = {entry[2] for entry in completed[-2:]}
    return [
        entry for entry in completed[:-2]
        if entry[0] < cutoff_epoch and entry[2] != current_name and entry[2] not in protected
    ]


def print_candidates(candidates, root_path):
    print("Candidate backups:")
    for _, _, name, _, _ in candidates:
        print(f"  {os.fsencode(os.path.join(root_path, name))!r}")


def validate_candidate(root_path, root_fd, root_stat, original, cutoff_epoch, current_name):
    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed before deletion")
    completed = completed_backups(root_fd)
    refreshed = next((entry for entry in completed if entry[2] == original[2]), None)
    if refreshed is None:
        return None
    if refreshed[3:] != original[3:]:
        return None
    if refreshed not in selectable(completed, cutoff_epoch, current_name):
        return None
    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed before deletion")
    try:
        checked = os.stat(refreshed[2], dir_fd=root_fd, follow_symlinks=False)
    except OSError as error:
        raise RetentionError(f"cannot inspect backup entry {refreshed[1]!r}: {error}") from error
    if not stat.S_ISDIR(checked.st_mode) or (checked.st_dev, checked.st_ino) != refreshed[3:]:
        return None
    return refreshed


def rename_no_replace(root_fd, source, destination):
    try:
        renameat2 = ctypes.CDLL(None, use_errno=True).renameat2
    except AttributeError as error:
        raise RetentionError("the Python runtime cannot claim backups without replacement") from error
    renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    renameat2.restype = ctypes.c_int
    if renameat2(root_fd, os.fsencode(source), root_fd, os.fsencode(destination), 1) != 0:
        error_number = ctypes.get_errno()
        raise RetentionError(f"cannot claim backup without replacement: {os.strerror(error_number)}")


def rename_exchange(root_fd, first, second):
    try:
        renameat2 = ctypes.CDLL(None, use_errno=True).renameat2
    except AttributeError as error:
        raise RetentionError("the Python runtime cannot exchange retention entries atomically") from error
    renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    renameat2.restype = ctypes.c_int
    if renameat2(root_fd, os.fsencode(first), root_fd, os.fsencode(second), 2) != 0:
        error_number = ctypes.get_errno()
        raise RetentionError(f"cannot exchange retention entries atomically: {os.strerror(error_number)}")


def run_hook(hook, root_path, candidate_name, quarantine_name=None):
    if not hook:
        return
    environment = os.environ.copy()
    environment["RETENTION_CANDIDATE_PATH"] = os.fsdecode(os.path.join(os.fsencode(root_path), os.fsencode(candidate_name)))
    if quarantine_name is not None:
        environment["RETENTION_QUARANTINE_PATH"] = os.fsdecode(
            os.path.join(os.fsencode(root_path), os.fsencode(quarantine_name))
        )
    try:
        subprocess.run([hook], check=True, env=environment)
    except OSError as error:
        raise RetentionError(f"cannot run the retention test hook: {error}") from error
    except subprocess.CalledProcessError as error:
        raise RetentionError(f"the retention test hook failed with status {error.returncode}") from error


def quarantine_name():
    return f".retention-{os.getpid()}-{secrets.token_hex(16)}"


def state_name(quarantine):
    match = QUARANTINE_NAME.fullmatch(quarantine)
    if match is None:
        raise RetentionError("cannot derive state name from a noncanonical quarantine name")
    return f".retention-state-{match.group(1)}-{match.group(2)}"


def tombstone_name(quarantine):
    match = QUARANTINE_NAME.fullmatch(quarantine)
    if match is None:
        raise RetentionError("cannot derive tombstone name from a noncanonical quarantine name")
    return f".retention-tombstone-{match.group(1)}-{match.group(2)}"


def remove_empty_tombstone(root_fd, tombstone, expected):
    try:
        entry = os.stat(tombstone, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        return True
    if not stat.S_ISDIR(entry.st_mode) or (entry.st_dev, entry.st_ino) != (expected.st_dev, expected.st_ino):
        raise RetentionError(f"retention tombstone changed identity: {os.fsencode(tombstone)!r}")
    try:
        os.rmdir(tombstone, dir_fd=root_fd)
    except OSError as error:
        if error.errno in {errno.ENOTEMPTY, errno.EEXIST}:
            return False
        raise
    sync_root(root_fd)
    return True


def sync_root(root_fd):
    try:
        os.fsync(root_fd)
    except OSError as error:
        raise RetentionError(f"cannot synchronize retention state: {error}") from error


def unlink_regular_entry(root_fd, name, expected=None):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=root_fd)
    try:
        opened = os.fstat(fd)
        if not stat.S_ISREG(opened.st_mode) or stat.S_IMODE(opened.st_mode) != 0o600:
            raise RetentionError(f"unsafe retention ledger entry: {os.fsencode(name)!r}")
        if expected is not None and (opened.st_dev, opened.st_ino) != (expected.st_dev, expected.st_ino):
            raise RetentionError(f"retention ledger changed identity: {os.fsencode(name)!r}")
        current = os.stat(name, dir_fd=root_fd, follow_symlinks=False)
        if (current.st_dev, current.st_ino) != (opened.st_dev, opened.st_ino):
            raise RetentionError(f"retention ledger name changed identity: {os.fsencode(name)!r}")
        os.unlink(name, dir_fd=root_fd)
    finally:
        os.close(fd)
    sync_root(root_fd)


def state_payload(quarantine, original, opened, phase):
    return {
        "format": 1,
        "original": base64.b64encode(original[1]).decode("ascii"),
        "quarantine": quarantine,
        "dev": opened.st_dev,
        "ino": opened.st_ino,
        "phase": phase,
    }


def write_state(root_fd, name, state):
    temporary = f".retention-state-tmp-{os.getpid()}-{secrets.token_hex(16)}"
    residue = f".maintenance-retention-ledger-obsolete-{os.getpid()}-{secrets.token_hex(16)}"
    payload = (json.dumps(state, sort_keys=True, separators=(",", ":")) + "\n").encode("ascii")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
    existing = None
    try:
        try:
            existing_fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=root_fd)
        except FileNotFoundError:
            existing_fd = None
        if existing_fd is not None:
            try:
                existing = os.fstat(existing_fd)
                if not stat.S_ISREG(existing.st_mode) or stat.S_IMODE(existing.st_mode) != 0o600:
                    raise RetentionError(f"unsafe retention state entry: {os.fsencode(name)!r}")
            finally:
                os.close(existing_fd)
        fd = os.open(temporary, flags, 0o600, dir_fd=root_fd)
        try:
            written = 0
            while written < len(payload):
                written += os.write(fd, payload[written:])
            os.fsync(fd)
        finally:
            os.close(fd)
        if TEST_STOP_AT == f"state-fsync-{state['phase']}":
            os._exit(88)
        if existing is None:
            rename_no_replace(root_fd, temporary, name)
        else:
            rename_exchange(root_fd, temporary, name)
            if TEST_STOP_AT == f"state-exchange-{state['phase']}":
                os._exit(88)
            moved = os.stat(temporary, dir_fd=root_fd, follow_symlinks=False)
            if (moved.st_dev, moved.st_ino) != (existing.st_dev, existing.st_ino):
                raise RetentionError(f"retention state changed identity during update: {os.fsencode(name)!r}")
            rename_no_replace(root_fd, temporary, residue)
        sync_root(root_fd)
        if existing is not None:
            unlink_regular_entry(root_fd, residue, existing)
    except RetentionError:
        raise
    except OSError as error:
        raise RetentionError(f"cannot persist retention state: {error}") from error


def remove_state(root_fd, name):
    residue = f".maintenance-retention-ledger-cleared-{os.getpid()}-{secrets.token_hex(16)}"
    try:
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=root_fd)
        try:
            opened = os.fstat(fd)
            if not stat.S_ISREG(opened.st_mode) or stat.S_IMODE(opened.st_mode) != 0o600:
                raise RetentionError(f"unsafe retention state entry: {os.fsencode(name)!r}")
        finally:
            os.close(fd)
        rename_no_replace(root_fd, name, residue)
        moved = os.stat(residue, dir_fd=root_fd, follow_symlinks=False)
        if (moved.st_dev, moved.st_ino) != (opened.st_dev, opened.st_ino):
            raise RetentionError(f"retention state changed identity during cleanup: {os.fsencode(name)!r}")
        sync_root(root_fd)
        unlink_regular_entry(root_fd, residue, opened)
    except RetentionError:
        raise
    except OSError as error:
        raise RetentionError(f"cannot clear completed retention state: {error}") from error


def read_state(root_fd, name, require_claim_name=True):
    flags = os.O_RDONLY | os.O_NOFOLLOW
    try:
        fd = os.open(name, flags, dir_fd=root_fd)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
                raise RetentionError(f"unsafe retention state entry: {os.fsencode(name)!r}")
            payload = os.read(fd, 16384)
            if os.read(fd, 1):
                raise RetentionError(f"retention state is too large: {os.fsencode(name)!r}")
        finally:
            os.close(fd)
    except RetentionError:
        raise
    except OSError as error:
        raise RetentionError(f"cannot read retention state {os.fsencode(name)!r}: {error}") from error
    try:
        state = json.loads(payload.decode("ascii"))
        required = {"format", "original", "quarantine", "dev", "ino", "phase"}
        phases = {"prepared", "claimed", "deleting", "payload-complete", "completed", "restored"}
        if set(state) != required or type(state["format"]) is not int or state["format"] != 1 or \
                state["phase"] not in phases:
            raise ValueError("invalid fields")
        original = base64.b64decode(state["original"], validate=True)
        quarantine = state["quarantine"]
        if (not isinstance(quarantine, str) or QUARANTINE_NAME.fullmatch(quarantine) is None or
                type(state["dev"]) is not int or type(state["ino"]) is not int or
                state["dev"] < 0 or state["ino"] <= 0 or entry_timestamp(original) is None or
                base64.b64encode(original).decode("ascii") != state["original"]):
            raise ValueError("invalid values")
        canonical = (json.dumps(state, sort_keys=True, separators=(",", ":")) + "\n").encode("ascii")
        if payload != canonical:
            raise ValueError("noncanonical encoding")
    except (UnicodeDecodeError, ValueError, TypeError, KeyError) as error:
        raise RetentionError(f"invalid retention state: {os.fsencode(name)!r}") from error
    if require_claim_name and (STATE_NAME.fullmatch(name) is None or name != state_name(quarantine)):
        raise RetentionError(f"retention state name does not match its claim: {os.fsencode(name)!r}")
    return state, original, info


def state_claim_identity(state):
    return tuple(state[key] for key in ("format", "original", "quarantine", "dev", "ino"))


def claim_namespace_outcome(root_fd, state, original):
    names = {
        "original": os.fsdecode(original),
        "quarantine": state["quarantine"],
        "tombstone": tombstone_name(state["quarantine"]),
    }
    present = []
    for outcome, name in names.items():
        try:
            entry = os.stat(name, dir_fd=root_fd, follow_symlinks=False)
        except FileNotFoundError:
            continue
        except OSError as error:
            raise RetentionError(f"cannot inspect interrupted retention namespace: {error}") from error
        if not stat.S_ISDIR(entry.st_mode) or (entry.st_dev, entry.st_ino) != (state["dev"], state["ino"]):
            raise RetentionError(f"interrupted retention state conflicts with {outcome}: {os.fsencode(name)!r}")
        present.append(outcome)
    if len(present) != 1:
        raise RetentionError("interrupted retention state has an ambiguous namespace outcome")
    return present[0]


def validate_temporary_state_relationship(root_fd, temporary, original, canonical=None):
    outcome = claim_namespace_outcome(root_fd, temporary, original)
    if canonical is None:
        expected = {
            "prepared": "original",
            "claimed": "quarantine",
            "deleting": "quarantine",
            "payload-complete": "quarantine",
            "completed": "tombstone",
            "restored": "original",
        }[temporary["phase"]]
        if outcome != expected:
            raise RetentionError("temporary retention state does not match its namespace outcome")
        return
    if state_claim_identity(temporary) != state_claim_identity(canonical):
        raise RetentionError("temporary retention state conflicts with the canonical claim identity")
    phases = frozenset((temporary["phase"], canonical["phase"]))
    legal_outcomes = {
        frozenset(("prepared",)): {"original"},
        frozenset(("claimed",)): {"quarantine"},
        frozenset(("deleting",)): {"quarantine"},
        frozenset(("payload-complete",)): {"quarantine"},
        frozenset(("completed",)): {"tombstone"},
        frozenset(("restored",)): {"original"},
        frozenset(("prepared", "claimed")): {"quarantine"},
        frozenset(("claimed", "deleting")): {"quarantine"},
        frozenset(("deleting", "payload-complete")): {"quarantine"},
        frozenset(("deleting", "completed")): {"tombstone"},
        frozenset(("payload-complete", "completed")): {"tombstone"},
        frozenset(("prepared", "restored")): {"original"},
        frozenset(("claimed", "restored")): {"original"},
    }.get(phases)
    if legal_outcomes is None or outcome not in legal_outcomes:
        raise RetentionError("temporary retention state has an illegal phase or namespace relationship")


def ledger_temporary_state(root_fd, temporary_name, opened):
    residue = f".maintenance-retention-ledger-interrupted-{os.getpid()}-{secrets.token_hex(16)}"
    rename_no_replace(root_fd, temporary_name, residue)
    moved = os.stat(residue, dir_fd=root_fd, follow_symlinks=False)
    if (moved.st_dev, moved.st_ino) != (opened.st_dev, opened.st_ino):
        raise RetentionError(f"temporary retention state changed identity during archival: {os.fsencode(temporary_name)!r}")
    sync_root(root_fd)
    unlink_regular_entry(root_fd, residue, opened)


def cleanup_ledger_residues(root_fd, names):
    candidates = [name for name in names if os.fsencode(name).startswith(b".maintenance-retention-ledger-")]
    for name in candidates:
        if LEDGER_NAME.fullmatch(name) is None:
            raise RetentionError(f"invalid retention ledger name: {os.fsencode(name)!r}")
        unlink_regular_entry(root_fd, name)


def reconcile_temporary_states(root_path, root_fd, root_stat, names):
    temporary_names = [name for name in names if os.fsencode(name).startswith(b".retention-state-tmp-")]
    if not temporary_names:
        return
    if len(temporary_names) != 1:
        raise RetentionError("multiple unfinished retention state writes found; refusing to prune")
    temporary_name = temporary_names[0]
    if TEMP_STATE_NAME.fullmatch(temporary_name) is None:
        raise RetentionError(f"invalid temporary retention state name: {os.fsencode(temporary_name)!r}")
    temporary, original, opened = read_state(root_fd, temporary_name, require_claim_name=False)
    canonical_name = state_name(temporary["quarantine"])
    if canonical_name in names:
        canonical, canonical_original, _ = read_state(root_fd, canonical_name)
        if original != canonical_original:
            raise RetentionError("temporary retention state conflicts with the canonical original name")
        validate_temporary_state_relationship(root_fd, temporary, original, canonical)
        if not root_matches(root_path, root_stat):
            raise RetentionError("the backup root changed during temporary state reconciliation")
        ledger_temporary_state(root_fd, temporary_name, opened)
        return
    validate_temporary_state_relationship(root_fd, temporary, original)
    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed during temporary state promotion")
    rename_no_replace(root_fd, temporary_name, canonical_name)
    moved = os.stat(canonical_name, dir_fd=root_fd, follow_symlinks=False)
    if (moved.st_dev, moved.st_ino) != (opened.st_dev, opened.st_ino):
        raise RetentionError(f"temporary retention state changed identity during promotion: {os.fsencode(temporary_name)!r}")
    sync_root(root_fd)


def claimed_stat(root_fd, quarantine, state):
    try:
        claimed = os.stat(quarantine, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        return None
    except OSError as error:
        raise RetentionError(f"cannot inspect claimed backup: {error}") from error
    if not stat.S_ISDIR(claimed.st_mode) or (claimed.st_dev, claimed.st_ino) != (state["dev"], state["ino"]):
        raise RetentionError(f"claimed backup changed identity: {os.fsencode(quarantine)!r}")
    return claimed


def claim_candidate(root_path, root_fd, root_stat, candidate, preclaim_hook):
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    try:
        candidate_fd = os.open(candidate[2], flags, dir_fd=root_fd)
    except OSError as error:
        raise RetentionError(f"cannot open backup entry {candidate[1]!r}: {error}") from error
    try:
        opened = os.fstat(candidate_fd)
        if (opened.st_dev, opened.st_ino) != candidate[3:]:
            return None
        if mount_id(candidate_fd) != mount_id(root_fd):
            raise RetentionError(f"backup is a nested mount: {candidate[1]!r}")
        run_hook(preclaim_hook, root_path, candidate[2])
        if not root_matches(root_path, root_stat):
            raise RetentionError("the backup root changed before the retention claim")
        quarantine = quarantine_name()
        state_file = state_name(quarantine)
        state = state_payload(quarantine, candidate, opened, "prepared")
        write_state(root_fd, state_file, state)
        rename_no_replace(root_fd, candidate[2], quarantine)
        claimed = os.stat(quarantine, dir_fd=root_fd, follow_symlinks=False)
        if not stat.S_ISDIR(claimed.st_mode) or (claimed.st_dev, claimed.st_ino) != (opened.st_dev, opened.st_ino):
            report(f"backup claim changed identity and was retained: {candidate[1]!r}")
            os.close(candidate_fd)
            return None
        state["phase"] = "claimed"
        write_state(root_fd, state_file, state)
        return quarantine, state_file, state, candidate, candidate_fd
    except RetentionError:
        os.close(candidate_fd)
        raise
    except OSError as error:
        os.close(candidate_fd)
        raise RetentionError(f"cannot claim backup {candidate[1]!r}: {error}") from error


def claimed_entry(root_fd, quarantine, original, opened):
    try:
        claimed = os.stat(quarantine, dir_fd=root_fd, follow_symlinks=False)
    except OSError as error:
        raise RetentionError(f"cannot inspect claimed backup {original[1]!r}: {error}") from error
    if not stat.S_ISDIR(claimed.st_mode) or (claimed.st_dev, claimed.st_ino) != (opened.st_dev, opened.st_ino):
        return None
    return original


def completed_with_claim(root_fd, claimed):
    completed = completed_backups(root_fd)
    completed.append(claimed)
    completed.sort(key=lambda entry: (entry[0], entry[1]))
    return completed


def restore_claim(root_path, root_fd, root_stat, quarantine, state_file, state, original, opened):
    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed before claim restoration")
    if claimed_entry(root_fd, quarantine, original, opened) is None:
        report(f"claimed backup changed identity and was retained: {original[1]!r}")
        return
    try:
        rename_no_replace(root_fd, quarantine, original[2])
    except RetentionError as error:
        report(f"cannot restore protected backup {original[1]!r}: {error}")
        return
    restored = os.stat(original[2], dir_fd=root_fd, follow_symlinks=False)
    if not stat.S_ISDIR(restored.st_mode) or (restored.st_dev, restored.st_ino) != (opened.st_dev, opened.st_ino):
        report(f"restored backup changed identity and was retained: {original[1]!r}")
        return
    sync_root(root_fd)
    state["phase"] = "restored"
    write_state(root_fd, state_file, state)
    remove_state(root_fd, state_file)


def run_entry_hook(hook, root_path, quarantine, relative, entry_kind):
    if not hook:
        return
    environment = os.environ.copy()
    entry_path = os.path.join(os.fsencode(root_path), os.fsencode(quarantine), *map(os.fsencode, relative))
    environment["RETENTION_ENTRY_PATH"] = os.fsdecode(entry_path)
    environment["RETENTION_ENTRY_KIND"] = entry_kind
    try:
        subprocess.run([hook], check=True, env=environment)
    except OSError as error:
        raise RetentionError(f"cannot run the retention entry test hook: {error}") from error
    except subprocess.CalledProcessError as error:
        raise RetentionError(f"the retention entry test hook failed with status {error.returncode}") from error


def delete_claim(root_path, root_fd, root_stat, quarantine, state_file, state, original, opened, candidate_fd,
                 beforedelete_hook, entryremove_hook, finalremove_hook, stop_at):
    run_hook(beforedelete_hook, root_path, original[2], quarantine)
    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed before claimed deletion")
    if claimed_entry(root_fd, quarantine, original, opened) is None:
        report(f"claimed backup changed identity and was retained: {original[1]!r}")
        return
    try:
        state["phase"] = "deleting"
        write_state(root_fd, state_file, state)
        if stop_at == "deleting":
            os._exit(88)
        expected_mount = mount_id(candidate_fd)
        accepted = validate_claimed_tree(candidate_fd, expected_mount)
        handled = set()
        remove_claimed_tree(candidate_fd, expected_mount, entryremove_hook, root_path, quarantine, (), stop_at,
                            [False], accepted, handled)
        if set(accepted) != handled:
            raise RetentionError("accepted backup payload was not fully reclaimed during traversal")
        audit_claimed_tree(candidate_fd, expected_mount, accepted, handled)
        remove_empty_directories(candidate_fd, expected_mount)
        state["phase"] = "payload-complete"
        write_state(root_fd, state_file, state)
        if stop_at == "payload-complete":
            os._exit(88)
        if claimed_entry(root_fd, quarantine, original, opened) is None:
            report(f"claimed backup changed identity and was retained: {original[1]!r}")
            return
        run_hook(finalremove_hook, root_path, original[2], quarantine)
        tombstone = tombstone_name(quarantine)
        rename_no_replace(root_fd, quarantine, tombstone)
        final_entry = os.stat(tombstone, dir_fd=root_fd, follow_symlinks=False)
        if not stat.S_ISDIR(final_entry.st_mode) or (final_entry.st_dev, final_entry.st_ino) != \
                (opened.st_dev, opened.st_ino):
            report(f"claimed backup changed identity during tombstoning and was retained: {original[1]!r}")
            return
        sync_root(root_fd)
        if stop_at == "tombstoned":
            os._exit(88)
        state["phase"] = "completed"
        write_state(root_fd, state_file, state)
        if stop_at == "completed":
            os._exit(88)
        if not remove_empty_tombstone(root_fd, tombstone, opened):
            report(f"completed backup tombstone is not empty and was retained: {original[1]!r}")
        remove_state(root_fd, state_file)
    except OSError as error:
        report(f"cannot delete claimed backup {original[1]!r}: {error}")


def mount_id(directory_fd):
    try:
        with open(f"/proc/self/fdinfo/{directory_fd}", encoding="ascii") as details:
            for line in details:
                if line.startswith("mnt_id:"):
                    return int(line.split()[1])
    except OSError as error:
        raise RetentionError(f"cannot inspect the backup mount: {error}") from error
    raise RetentionError("the platform cannot prove backup mount containment")


def regular_identity(opened, opened_mount):
    return (
        opened.st_dev,
        opened.st_ino,
        stat.S_IFMT(opened.st_mode),
        stat.S_IMODE(opened.st_mode),
        opened.st_uid,
        opened.st_gid,
        opened.st_rdev,
        opened_mount,
    )


def validate_claimed_tree(directory_fd, expected_mount, accepted=None):
    if accepted is None:
        accepted = {}
    for name in sorted(list_directory(directory_fd), key=os.fsencode):
        before = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        if stat.S_ISDIR(before.st_mode):
            child_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory_fd)
            try:
                opened = os.fstat(child_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup child changed identity: {os.fsencode(name)!r}")
                if mount_id(child_fd) != expected_mount:
                    raise RetentionError(f"backup contains a nested mount: {os.fsencode(name)!r}")
                validate_claimed_tree(child_fd, expected_mount, accepted)
            finally:
                os.close(child_fd)
        elif stat.S_ISREG(before.st_mode):
            file_fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory_fd)
            try:
                opened = os.fstat(file_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup file changed identity: {os.fsencode(name)!r}")
                opened_mount = mount_id(file_fd)
                if opened_mount != expected_mount:
                    raise RetentionError(f"backup contains a mounted file: {os.fsencode(name)!r}")
                if opened.st_nlink != 1:
                    raise RetentionError(f"backup contains a hard-linked regular file: {os.fsencode(name)!r}")
                key = (opened.st_dev, opened.st_ino)
                if key in accepted:
                    raise RetentionError(f"backup repeats a regular file inode: {os.fsencode(name)!r}")
                accepted[key] = regular_identity(opened, opened_mount)
            finally:
                os.close(file_fd)
    return accepted


def remove_claimed_tree(directory_fd, expected_mount, entryremove_hook, root_path, quarantine, relative, stop_at,
                        partial_stopped, accepted, handled):
    for name in sorted(list_directory(directory_fd), key=os.fsencode):
        before = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        child_relative = (*relative, name)
        if stat.S_ISDIR(before.st_mode):
            child_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory_fd)
            try:
                opened = os.fstat(child_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup child changed identity: {os.fsencode(name)!r}")
                if mount_id(child_fd) != expected_mount:
                    raise RetentionError(f"backup contains a nested mount: {os.fsencode(name)!r}")
                run_entry_hook(entryremove_hook, root_path, quarantine, child_relative, "directory")
                final = os.fstat(child_fd)
                if (final.st_dev, final.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"opened backup child changed identity: {os.fsencode(name)!r}")
                remove_claimed_tree(child_fd, expected_mount, entryremove_hook, root_path, quarantine, child_relative,
                                    stop_at, partial_stopped, accepted, handled)
                os.fsync(child_fd)
            finally:
                os.close(child_fd)
        elif stat.S_ISREG(before.st_mode):
            file_fd = os.open(name, os.O_WRONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory_fd)
            try:
                opened = os.fstat(file_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup file changed identity: {os.fsencode(name)!r}")
                opened_mount = mount_id(file_fd)
                if opened_mount != expected_mount:
                    raise RetentionError(f"backup contains a mounted file: {os.fsencode(name)!r}")
                if opened.st_nlink != 1:
                    raise RetentionError(f"backup contains a hard-linked regular file: {os.fsencode(name)!r}")
                key = (opened.st_dev, opened.st_ino)
                identity = regular_identity(opened, opened_mount)
                if key not in accepted:
                    continue
                if accepted[key] != identity:
                    raise RetentionError(f"backup file metadata changed after preflight: {os.fsencode(name)!r}")
                run_entry_hook(entryremove_hook, root_path, quarantine, child_relative, "regular")
                final = os.fstat(file_fd)
                final_mount = mount_id(file_fd)
                if regular_identity(final, final_mount) != identity or final.st_nlink != 1 or \
                        final_mount != expected_mount:
                    raise RetentionError(f"backup file changed identity, mount, or link count: {os.fsencode(name)!r}")
                os.ftruncate(file_fd, 0)
                os.fsync(file_fd)
                synced = os.fstat(file_fd)
                synced_mount = mount_id(file_fd)
                if regular_identity(synced, synced_mount) != identity or synced.st_nlink != 1 or \
                        synced_mount != expected_mount or synced.st_size != 0:
                    raise RetentionError(f"backup file was not safely reclaimed: {os.fsencode(name)!r}")
                handled.add(key)
                current = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
                if stat.S_ISREG(current.st_mode) and (current.st_dev, current.st_ino) == \
                        (opened.st_dev, opened.st_ino):
                    os.unlink(name, dir_fd=directory_fd)
                    os.fsync(directory_fd)
                if stop_at == "partial" and not partial_stopped[0]:
                    partial_stopped[0] = True
                    os._exit(88)
            finally:
                os.close(file_fd)


def audit_claimed_tree(directory_fd, expected_mount, accepted, handled):
    for name in sorted(list_directory(directory_fd), key=os.fsencode):
        before = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        if stat.S_ISDIR(before.st_mode):
            child_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory_fd)
            try:
                opened = os.fstat(child_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup child changed during final audit: {os.fsencode(name)!r}")
                if mount_id(child_fd) != expected_mount:
                    raise RetentionError(f"backup gained a nested mount: {os.fsencode(name)!r}")
                audit_claimed_tree(child_fd, expected_mount, accepted, handled)
            finally:
                os.close(child_fd)
        elif stat.S_ISREG(before.st_mode):
            file_fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory_fd)
            try:
                opened = os.fstat(file_fd)
                if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    raise RetentionError(f"backup file changed during final audit: {os.fsencode(name)!r}")
                opened_mount = mount_id(file_fd)
                if opened_mount != expected_mount:
                    raise RetentionError(f"backup gained a mounted file: {os.fsencode(name)!r}")
                if opened.st_nlink != 1:
                    raise RetentionError(f"backup gained a hard-linked regular file: {os.fsencode(name)!r}")
                key = (opened.st_dev, opened.st_ino)
                if key in accepted:
                    if regular_identity(opened, opened_mount) != accepted[key]:
                        raise RetentionError(f"backup file metadata changed during final audit: {os.fsencode(name)!r}")
                    if key not in handled or opened.st_size != 0:
                        raise RetentionError(f"accepted backup payload remains after traversal: {os.fsencode(name)!r}")
            finally:
                os.close(file_fd)


def remove_empty_directories(directory_fd, expected_mount):
    for name in sorted(list_directory(directory_fd), key=os.fsencode):
        before = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        if not stat.S_ISDIR(before.st_mode):
            continue
        child_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory_fd)
        try:
            opened = os.fstat(child_fd)
            if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                raise RetentionError(f"backup child changed before directory removal: {os.fsencode(name)!r}")
            if mount_id(child_fd) != expected_mount:
                raise RetentionError(f"backup contains a nested mount: {os.fsencode(name)!r}")
            remove_empty_directories(child_fd, expected_mount)
            os.fsync(child_fd)
        finally:
            os.close(child_fd)
        current = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
        if stat.S_ISDIR(current.st_mode) and (current.st_dev, current.st_ino) == (before.st_dev, before.st_ino):
            try:
                os.rmdir(name, dir_fd=directory_fd)
            except OSError as error:
                if error.errno not in {errno.ENOTEMPTY, errno.EEXIST}:
                    raise
            else:
                os.fsync(directory_fd)


def reconcile_claim(root_path, root_fd, root_stat, state_file, state, original_name):
    quarantine = state["quarantine"]
    tombstone = tombstone_name(quarantine)
    original = original_name
    claimed = claimed_stat(root_fd, quarantine, state)
    try:
        existing = os.stat(original, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        existing = None
    except OSError as error:
        raise RetentionError(f"cannot inspect interrupted retention claim: {error}") from error

    try:
        completed = os.stat(tombstone, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        completed = None
    except OSError as error:
        raise RetentionError(f"cannot inspect completed retention claim: {error}") from error

    if not root_matches(root_path, root_stat):
        raise RetentionError("the backup root changed during claim reconciliation")

    if state["phase"] == "restored":
        if claimed is not None or completed is not None or existing is None or not stat.S_ISDIR(existing.st_mode) or \
                (existing.st_dev, existing.st_ino) != (state["dev"], state["ino"]):
            raise RetentionError(f"restored retention claim has an inconsistent outcome: {os.fsencode(original)!r}")
        sync_root(root_fd)
        remove_state(root_fd, state_file)
        return

    if state["phase"] in {"prepared", "claimed"}:
        if completed is not None:
            raise RetentionError(f"restoring retention claim conflicts with a tombstone: {os.fsencode(tombstone)!r}")
        if claimed is None:
            if existing is not None and stat.S_ISDIR(existing.st_mode) and \
                    (existing.st_dev, existing.st_ino) == (state["dev"], state["ino"]):
                sync_root(root_fd)
                state["phase"] = "restored"
                write_state(root_fd, state_file, state)
                remove_state(root_fd, state_file)
                return
            raise RetentionError(f"interrupted retention claim is missing: {os.fsencode(quarantine)!r}")
        if existing is not None:
            raise RetentionError(f"cannot restore interrupted retention claim over an existing entry: {os.fsencode(original)!r}")
        rename_no_replace(root_fd, quarantine, original)
        restored = os.stat(original, dir_fd=root_fd, follow_symlinks=False)
        if not stat.S_ISDIR(restored.st_mode) or (restored.st_dev, restored.st_ino) != (state["dev"], state["ino"]):
            raise RetentionError(f"restored interrupted retention claim changed identity: {os.fsencode(original)!r}")
        sync_root(root_fd)
        state["phase"] = "restored"
        write_state(root_fd, state_file, state)
        remove_state(root_fd, state_file)
        return

    if state["phase"] == "completed":
        if claimed is not None or existing is not None:
            raise RetentionError(f"completed retention claim has an inconsistent outcome: {os.fsencode(tombstone)!r}")
        if completed is not None:
            if not stat.S_ISDIR(completed.st_mode) or \
                    (completed.st_dev, completed.st_ino) != (state["dev"], state["ino"]):
                raise RetentionError(
                    f"completed retention claim has an inconsistent outcome: {os.fsencode(tombstone)!r}"
                )
            if not remove_empty_tombstone(root_fd, tombstone, completed):
                report(f"completed backup tombstone is not empty and was retained: {os.fsencode(original)!r}")
        sync_root(root_fd)
        remove_state(root_fd, state_file)
        return

    if claimed is None:
        if existing is None and completed is not None and stat.S_ISDIR(completed.st_mode) and \
                (completed.st_dev, completed.st_ino) == (state["dev"], state["ino"]):
            sync_root(root_fd)
            state["phase"] = "completed"
            write_state(root_fd, state_file, state)
            if not remove_empty_tombstone(root_fd, tombstone, completed):
                report(f"completed backup tombstone is not empty and was retained: {os.fsencode(original)!r}")
            remove_state(root_fd, state_file)
            return
        if existing is None:
            raise RetentionError(f"interrupted deleting claim is missing: {os.fsencode(quarantine)!r}")
        if stat.S_ISDIR(existing.st_mode) and (existing.st_dev, existing.st_ino) == (state["dev"], state["ino"]):
            raise RetentionError(f"deleting retention claim was unexpectedly restored: {os.fsencode(original)!r}")
        raise RetentionError(f"deleting retention claim was replaced: {os.fsencode(original)!r}")
    if existing is not None:
        raise RetentionError(f"deleting retention claim conflicts with an existing entry: {os.fsencode(original)!r}")
    if completed is not None:
        raise RetentionError(f"deleting retention claim conflicts with a tombstone: {os.fsencode(tombstone)!r}")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    try:
        candidate_fd = os.open(quarantine, flags, dir_fd=root_fd)
        try:
            opened = os.fstat(candidate_fd)
            if (opened.st_dev, opened.st_ino) != (state["dev"], state["ino"]):
                raise RetentionError(f"deleting retention claim changed identity: {os.fsencode(quarantine)!r}")
            if mount_id(candidate_fd) != mount_id(root_fd):
                raise RetentionError(f"deleting retention claim is a nested mount: {os.fsencode(quarantine)!r}")
            expected_mount = mount_id(candidate_fd)
            accepted = validate_claimed_tree(candidate_fd, expected_mount)
            handled = set()
            remove_claimed_tree(candidate_fd, expected_mount, "", root_path, quarantine, (), "", [False],
                                accepted, handled)
            if set(accepted) != handled:
                raise RetentionError("accepted backup payload was not fully reclaimed during reconciliation")
            audit_claimed_tree(candidate_fd, expected_mount, accepted, handled)
            remove_empty_directories(candidate_fd, expected_mount)
            state["phase"] = "payload-complete"
            write_state(root_fd, state_file, state)
        finally:
            os.close(candidate_fd)
        final = claimed_stat(root_fd, quarantine, state)
        if final is None:
            raise RetentionError(f"deleting retention claim disappeared: {os.fsencode(quarantine)!r}")
        rename_no_replace(root_fd, quarantine, tombstone)
        completed = os.stat(tombstone, dir_fd=root_fd, follow_symlinks=False)
        if not stat.S_ISDIR(completed.st_mode) or (completed.st_dev, completed.st_ino) != \
                (state["dev"], state["ino"]):
            raise RetentionError(f"completed retention claim changed identity: {os.fsencode(tombstone)!r}")
        sync_root(root_fd)
        state["phase"] = "completed"
        write_state(root_fd, state_file, state)
        if not remove_empty_tombstone(root_fd, tombstone, completed):
            report(f"completed backup tombstone is not empty and was retained: {os.fsencode(original)!r}")
        remove_state(root_fd, state_file)
    except OSError as error:
        raise RetentionError(f"cannot resume deletion of claimed backup: {error}") from error


def reconcile_claims(root_path, root_fd, root_stat):
    try:
        names = list_directory(root_fd)
    except OSError as error:
        raise RetentionError(f"cannot list retention state: {error}") from error
    cleanup_ledger_residues(root_fd, names)
    try:
        names = list_directory(root_fd)
    except OSError as error:
        raise RetentionError(f"cannot list retention state after ledger cleanup: {error}") from error
    reconcile_temporary_states(root_path, root_fd, root_stat, names)
    try:
        names = list_directory(root_fd)
    except OSError as error:
        raise RetentionError(f"cannot list reconciled retention state: {error}") from error
    state_files = [name for name in names if os.fsencode(name).startswith(b".retention-state-") and
                   not os.fsencode(name).startswith(b".retention-state-tmp-")]
    tombstones = {name for name in names if TOMBSTONE_NAME.fullmatch(name) is not None}
    quarantines = {name for name in names if os.fsencode(name).startswith(b".retention-") and
                   not os.fsencode(name).startswith(b".retention-state-") and name not in tombstones}
    claimed_names = set()
    for state_file in state_files:
        state, original, _ = read_state(root_fd, state_file)
        if state["quarantine"] in claimed_names:
            raise RetentionError("multiple retention state files claim the same backup")
        claimed_names.add(state["quarantine"])
        reconcile_claim(root_path, root_fd, root_stat, state_file, state, os.fsdecode(original))
    unknown = quarantines - claimed_names
    if unknown:
        raise RetentionError(f"unknown retained backup claim: {os.fsencode(sorted(unknown, key=os.fsencode)[0])!r}")



def run(root_path, current_backup, explicit_prune, preclaim_hook, postclaim_hook, beforedelete_hook, entryremove_hook,
        finalremove_hook, stop_at):
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    try:
        root_fd = os.open(root_path, flags)
    except OSError as error:
        report(f"cannot open the backup root safely: {error}")
        return
    try:
        root_stat = os.fstat(root_fd)
        if not stat.S_ISDIR(root_stat.st_mode) or not root_matches(root_path, (root_stat.st_dev, root_stat.st_ino)):
            report("the backup root changed while retention was starting")
            return
        reconcile_claims(root_path, root_fd, (root_stat.st_dev, root_stat.st_ino))
        cutoff_epoch = calendar.timegm((datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=14)).utctimetuple())
        current_name = current_backup_name(root_path, current_backup)
        candidates = selectable(completed_backups(root_fd), cutoff_epoch, current_name)
        if not candidates:
            return
        print_candidates(candidates, root_path)
        if not explicit_prune:
            if not sys.stdin.isatty():
                print("Backups were not deleted.")
                return
            answer = input("Delete the listed backups? [y/N] ")
            if answer not in {"y", "Y"}:
                print("Backups were not deleted.")
                return
        for candidate in candidates:
            try:
                verified = validate_candidate(root_path, root_fd, (root_stat.st_dev, root_stat.st_ino), candidate,
                                              cutoff_epoch, current_name)
            except RetentionError as error:
                report(str(error))
                return
            if verified is None:
                continue
            try:
                claimed = claim_candidate(root_path, root_fd, (root_stat.st_dev, root_stat.st_ino), verified,
                                          preclaim_hook)
            except RetentionError as error:
                report(str(error))
                return
            if claimed is None:
                continue
            quarantine, state_file, state, original, candidate_fd = claimed
            try:
                if stop_at == "claimed":
                    os._exit(88)
                run_hook(postclaim_hook, root_path, original[2], quarantine)
                checked_claim = claimed_entry(root_fd, quarantine, original, os.fstat(candidate_fd))
                if checked_claim is None:
                    report(f"claimed backup changed identity and was retained: {original[1]!r}")
                    continue
                completed = completed_with_claim(root_fd, checked_claim)
                if checked_claim not in selectable(completed, cutoff_epoch, current_name):
                    restore_claim(root_path, root_fd, (root_stat.st_dev, root_stat.st_ino), quarantine, state_file,
                                  state, original, os.fstat(candidate_fd))
                    continue
                delete_claim(root_path, root_fd, (root_stat.st_dev, root_stat.st_ino), quarantine, state_file,
                             state, original, os.fstat(candidate_fd), candidate_fd, beforedelete_hook,
                             entryremove_hook, finalremove_hook, stop_at)
            except RetentionError as error:
                report(str(error))
                return
            finally:
                os.close(candidate_fd)
    except RetentionError as error:
        report(str(error))
    except OSError as error:
        report(f"backup retention failed: {error}")
    finally:
        os.close(root_fd)


if __name__ == "__main__":
    run(sys.argv[1], sys.argv[2], sys.argv[3] == "1", sys.argv[4], sys.argv[5], sys.argv[6], sys.argv[7],
        sys.argv[8], sys.argv[9])
PY
  then
    update_error 'backup retention helper exited unexpectedly; backups were not deleted'
  fi
}

stage_release() {
  local release_parent=$1 release_dir staging_dir

  release_dir=$release_parent/$target_id
  if [ -e "$release_dir" ] || [ -L "$release_dir" ]; then
    [ -d "$release_dir" ] && [ ! -L "$release_dir" ] || {
      update_error "release target path already exists: $release_dir"
      return 1
    }
    validate_reusable_target "$release_dir"
    if [ -L "$BACKEND_ROOT/latest" ] && ! is_active_target "$release_dir" &&
      [ "$(readlink -f -- "$BACKEND_ROOT/latest" 2>/dev/null || true)" = \
        "$(readlink -f -- "$release_dir" 2>/dev/null || true)" ]; then
      update_error 'active release link must contain one relative release ID'
      return 1
    fi
    if is_active_target "$release_dir"; then
      validate_committed_release "$release_dir" "$target_id" || {
        update_error "active release target has no matching success marker: $release_dir/$ACTIVE_MARKER"
        return 1
      }
    fi
    return 0
  fi
  create_update_temp "$release_parent/.${target_id}.staging.XXXXXX" || return 1
  staging_dir=$UPDATE_TEMP_DIR
  cp -a -- "$RESOLVED_BACKEND_DIR/." "$staging_dir/"
  install -m 600 "$ENV_FILE" "$staging_dir/.env"
  install -m 600 "$RESOLVED_CHECKSUMS_FILE" "$staging_dir/$NORMALIZED_CHECKSUMS"
  {
    printf 'format=1\n'
    printf 'kind=%s\n' "$kind"
    printf 'requested_ref=%s\n' "$requested_ref"
    printf 'resolved_identity=%s\n' "$resolved_identity"
    printf 'content_identity=%s\n' "$content_identity"
    printf 'transport_checksum=%s\n' "$transport_checksum"
    printf 'download_url=%s\n' "$download_url"
    prepare_images "$staging_dir" "$target_id"
  } >"$staging_dir/.deployment-manifest"
  chmod 600 "$staging_dir/.deployment-manifest" "$staging_dir/.env"
  mv -T -n -- "$staging_dir" "$release_dir"
  [ ! -e "$staging_dir" ] || {
    update_error "release target was created concurrently: $release_dir"
    return 1
  }
  sync -f "$release_parent"
}

capture_previous_image_entries() {
  local service container image_id
  local -a containers

  for service in caddy collector grafana victorialogs victoriametrics; do
    mapfile -t containers < <(docker ps -q --filter "label=com.docker.compose.project=$PROJECT_NAME" \
      --filter "label=com.docker.compose.service=$service")
    [ "${#containers[@]}" -eq 1 ] || {
      update_error "active service mapping is missing or ambiguous: $service"
      return 1
    }
    container=${containers[0]}
    image_id=$(docker inspect --format '{{.Image}}' "$container")
    printf 'PREVIOUS_IMAGE_%s=%s\n' "${service^^}" "$image_id"
  done
}

capture_target_image_entries() {
  local release_dir=$1 service record

  for service in caddy collector grafana victorialogs victoriametrics; do
    record=$(manifest_value "$release_dir/.deployment-manifest" "image.$service") || {
      update_error "target manifest lacks image identity for service: $service"
      return 1
    }
    printf 'TARGET_IMAGE_%s=%s\n' "${service^^}" "${record#*|}"
  done
}

recover_update_transaction() {
  recover_transaction
}

handle_update_signal() {
  local signal=$1 phase

  trap - HUP INT TERM
  phase=$(read_transaction PHASE 2>/dev/null || true)
  case "$phase" in
    activation-prepared|activated) rollback_transaction || true ;;
    *) ;;
  esac
  cleanup_update_temps
  release_maintenance_lock
  trap - EXIT
  kill -s "$signal" "$$"
}

update_main() {
  local requested=latest allow_large_backup=0 prune_requested=0 release_parent previous_id target_dir transaction_id
  local current_backup_path=
  local -a transaction_entries previous_image_entries target_image_entries backup_args=(--leave-stopped)

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --ref)
        [ "$#" -ge 2 ] || {
          update_error '--ref requires a value'
          return 1
        }
        requested=$2
        shift 2
        ;;
      --allow-large-backup) allow_large_backup=1; shift ;;
      --prune-backups) prune_requested=1; shift ;;
      *) update_error "unknown option: $1"; return 1 ;;
    esac
  done
  : "$allow_large_backup" "$prune_requested"
  maintenance_init
  acquire_maintenance_lock
  trap 'cleanup_update_temps; release_maintenance_lock' EXIT
  trap 'handle_update_signal HUP' HUP
  trap 'handle_update_signal INT' INT
  trap 'handle_update_signal TERM' TERM
  recover_transaction
  release_parent=$BACKEND_ROOT
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ]; then
    release_parent=$(validate_test_path RELEASE_ROOT "$release_parent")
  fi
  mkdir -p -- "$release_parent"
  resolve_source "$requested"
  stage_release "$release_parent"
  target_dir=$release_parent/$target_id
  if is_active_target "$target_dir"; then
    cleanup_update_temps
    trap - EXIT
    release_maintenance_lock
    return 0
  fi
  if [ "${TELEMETRY_MAINTENANCE_TEST_MODE:-}" = 1 ] && [ "${TELEMETRY_TEST_STAGE_ONLY:-}" = 1 ]; then
    cleanup_update_temps
    trap - EXIT
    release_maintenance_lock
    return 0
  fi
  [ -L "$BACKEND_ROOT/latest" ] || {
    update_error "active release link is missing: $BACKEND_ROOT/latest"
    return 1
  }
  previous_id=$(readlink -- "$BACKEND_ROOT/latest")
  validate_target_label "$previous_id" || {
    update_error 'active release link must contain one relative release ID'
    return 1
  }
  [ -d "$BACKEND_ROOT/$previous_id" ] && [ ! -L "$BACKEND_ROOT/$previous_id" ] || {
    update_error 'active release directory is missing or unsafe'
    return 1
  }
  transaction_id="update-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
  transaction_entries=("FORMAT_VERSION=1" "TRANSACTION_ID=$transaction_id" "OPERATION=update" \
    "PHASE=backup-offline" "PREVIOUS_RELEASE=$previous_id" "TARGET_RELEASE=$target_id" 'BACKUP_PATH=')
  mapfile -t previous_image_entries < <(capture_previous_image_entries)
  mapfile -t target_image_entries < <(capture_target_image_entries "$target_dir")
  [ "${#previous_image_entries[@]}" -eq 5 ] && [ "${#target_image_entries[@]}" -eq 5 ] || {
    update_error 'cannot capture complete previous and target image identities'
    return 1
  }
  transaction_entries+=("${previous_image_entries[@]}" "${target_image_entries[@]}")
  write_transaction "${transaction_entries[@]}"
  [ "$allow_large_backup" -eq 0 ] || backup_args+=(--allow-large-backup)
  # shellcheck disable=SC2154 # acquire_maintenance_lock initializes this shared-library descriptor.
  if ! TELEMETRY_UPDATE_LOCK_HELD=1 TELEMETRY_UPDATE_LOCK_FD=$maintenance_lock_fd \
    TELEMETRY_UPDATE_TRANSACTION_ID=$transaction_id TELEMETRY_UPDATE_PREVIOUS_RELEASE=$previous_id \
    backup_main --target-label "$target_id" "${backup_args[@]}"; then
    trap 'handle_update_signal HUP' HUP
    trap 'handle_update_signal INT' INT
    trap 'handle_update_signal TERM' TERM
    update_error "backup failed before activation of $target_id"
    return 1
  fi
  trap 'handle_update_signal HUP' HUP
  trap 'handle_update_signal INT' INT
  trap 'handle_update_signal TERM' TERM
  current_backup_path=$(read_transaction BACKUP_PATH 2>/dev/null || true)
  if ! activate_transaction; then
    update_error "activation of $target_id failed; starting rollback"
    rollback_transaction || return 1
    return 1
  fi
  prune_backups "$prune_requested" "$current_backup_path"
  cleanup_update_temps
  trap - EXIT
  release_maintenance_lock
}

if [ "${TELEMETRY_SOURCE_ONLY:-}" != 1 ]; then
  update_main "$@"
fi
