#!/bin/sh
# Component handlers are invoked dynamically from COMPONENT_REGISTRY.
# shellcheck disable=SC2317,SC2329
set -u

PROGRAM=qubership-dev-install.sh

COMPONENT_REGISTRY='apm|1|1|apm
telemetry|1|1|telemetry
git-hooks|1|0|git_hooks'

DEFAULT_COMPONENTS=apm,telemetry,git-hooks
DEFAULT_HARNESSES=claude,codex,cursor
MINIMUM_JAVA_MAJOR=21

usage() {
  cat <<'EOF'
Install or uninstall the baseline Qubership developer tools.

Usage:
  qubership-dev-install.sh [options]

Options:
  --components <list>   Select these components: apm, telemetry, git-hooks, or all.
  --skip <list>         Exclude components from the selected set.
  --harnesses <list>    Configure these harnesses: claude, codex, cursor, or all.
  --force-git-hooks     Replace an existing global Git hooks path.
  --force-update        Force update operations for every selected component.
  --non-interactive     Do not prompt for missing prerequisites.
  --uninstall           Uninstall the selected Qubership developer tools.
  --purge               Remove telemetry config and cache during uninstall.
  -h, --help            Show this help text.
EOF
}

argument_error() {
  printf '%s: %s\n' "$PROGRAM" "$1" >&2
  exit 2
}

contains_csv() {
  case ",$1," in
    *",$2,"*) return 0 ;;
    *) return 1 ;;
  esac
}

normalize_list() {
  _kind=$1
  _value=$2
  _allowed=$3
  [ -n "$_value" ] || argument_error "$_kind list must not be empty"
  case $_value in
    ,*|*,|*,,*) argument_error "$_kind list contains an empty value" ;;
  esac
  if [ "$_value" = all ]; then
    printf '%s' "$_allowed"
    return
  fi
  contains_csv "$_value" all && argument_error "all must be used by itself in the $_kind list"

  _result=
  _old_ifs=$IFS
  IFS=,
  # Intentional word splitting converts the comma-separated CLI value into items.
  # shellcheck disable=SC2086
  set -- $_value
  IFS=$_old_ifs
  for _item in "$@"; do
    [ -n "$_item" ] || argument_error "$_kind list contains an empty value"
    if ! contains_csv "$_allowed" "$_item"; then
      case $_kind in
        component) argument_error "unknown component \"$_item\"" ;;
        harness) argument_error "unknown harness \"$_item\"" ;;
      esac
    fi
    if ! contains_csv "$_result" "$_item"; then
      if [ -n "$_result" ]; then
        _result="$_result,$_item"
      else
        _result=$_item
      fi
    fi
  done
  printf '%s' "$_result"
}

remove_items() {
  _selected=$1
  _removed=$2
  _result=
  _old_ifs=$IFS
  IFS=,
  # shellcheck disable=SC2086
  set -- $_selected
  IFS=$_old_ifs
  for _item in "$@"; do
    if ! contains_csv "$_removed" "$_item"; then
      if [ -n "$_result" ]; then
        _result="$_result,$_item"
      else
        _result=$_item
      fi
    fi
  done
  printf '%s' "$_result"
}

get_java_major_version() {
  _java_settings=$(java -XshowSettings:properties -version 2>&1) || return 1
  _java_specification_version=$(printf '%s\n' "$_java_settings" | awk -F '=' '
    /^[[:space:]]*java\.specification\.version[[:space:]]*=/ {
      value = $2
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      print value
      exit
    }
  ')
  case $_java_specification_version in
    1.*) _java_major=${_java_specification_version#1.} ;;
    *) _java_major=$_java_specification_version ;;
  esac
  _java_major=${_java_major%%.*}
  case $_java_major in
    ''|*[!0-9]*) return 1 ;;
  esac
  printf '%s\n' "$_java_major"
}

add_missing_prerequisite() {
  if [ -n "$MISSING_PREREQUISITES" ]; then
    MISSING_PREREQUISITES="$MISSING_PREREQUISITES,$1"
  else
    MISSING_PREREQUISITES=$1
  fi
}

check_git_hook_prerequisites() {
  MISSING_PREREQUISITES=
  if ! command -v git >/dev/null 2>&1; then
    printf '%s: Git is required for the git-hooks component. Install it from https://git-scm.com/install/.\n' \
      "$PROGRAM" >&2
    add_missing_prerequisite git
  fi
  if ! command -v java >/dev/null 2>&1; then
    printf '%s: Java %s or newer is required for the git-hooks component. Install a supported JRE or JDK.\n' \
      "$PROGRAM" "$MINIMUM_JAVA_MAJOR" >&2
    add_missing_prerequisite java
  elif _java_major=$(get_java_major_version); then
    if [ "$_java_major" -lt "$MINIMUM_JAVA_MAJOR" ]; then
      printf '%s: Detected Java %s. Java %s or newer is required for the git-hooks component.\n' \
        "$PROGRAM" "$_java_major" "$MINIMUM_JAVA_MAJOR" >&2
      add_missing_prerequisite java
    fi
  else
    printf '%s: Could not determine the Java version. Java %s or newer is required for the git-hooks component.\n' \
      "$PROGRAM" "$MINIMUM_JAVA_MAJOR" >&2
    add_missing_prerequisite java
  fi
  [ -z "$MISSING_PREREQUISITES" ]
}

require_git_hook_prerequisites() {
  check_git_hook_prerequisites && return 0
  if [ "$NON_INTERACTIVE" -eq 1 ]; then
    printf '%s: Installation stopped because required tools are missing.\n' "$PROGRAM" >&2
    return 1
  fi

  printf 'Install or update the required tools in another terminal. Have you installed them? [y/N] ' >&2
  _answer=
  if [ -r /dev/tty ]; then
    if ! IFS= read -r _answer </dev/tty 2>/dev/null; then
      printf '%s: could not read prerequisite confirmation from the terminal.\n' "$PROGRAM" >&2
      return 1
    fi
  else
    printf '%s: an interactive terminal is required to confirm prerequisite installation.\n' "$PROGRAM" >&2
    return 1
  fi
  case $_answer in
    y|Y|yes|YES|Yes) ;;
    *)
      printf '%s: Installation stopped by the user.\n' "$PROGRAM" >&2
      return 1
      ;;
  esac

  if ! check_git_hook_prerequisites; then
    printf '%s: Installation stopped because required tools are still missing.\n' "$PROGRAM" >&2
    return 1
  fi
}

registry_value() {
  _component=$1
  _column=$2
  printf '%s\n' "$COMPONENT_REGISTRY" | awk -F '|' -v component="$_component" -v column="$_column" \
    '$1 == component { print $column; exit }'
}

download_file() {
  _url=$1
  _destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$_url" -o "$_destination"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$_destination" "$_url"
  else
    printf '%s: curl or wget is required to download components.\n' "$PROGRAM" >&2
    return 1
  fi
}

run_shell_installer() {
  _url=$1
  shift
  _temporary_file=$(mktemp "${TMPDIR:-/tmp}/qubership-dev-install.XXXXXX") || return 1
  if ! download_file "$_url" "$_temporary_file"; then
    rm -f "$_temporary_file"
    return 1
  fi
  sh "$_temporary_file" "$@"
  _code=$?
  rm -f "$_temporary_file"
  return "$_code"
}

apm_install() {
  APM_WAS_INSTALLED=0
  if command -v apm >/dev/null 2>&1; then
    APM_WAS_INSTALLED=1
  else
    run_shell_installer "${QUBERSHIP_DEV_APM_INSTALL_URL:-https://aka.ms/apm-unix}" || return 1
    PATH="$HOME/.local/bin:$PATH"
    export PATH
    command -v apm >/dev/null 2>&1 || {
      printf '%s: APM installer completed, but apm is not on PATH.\n' "$PROGRAM" >&2
      return 1
    }
  fi
  if [ "$APM_WAS_INSTALLED" -eq 1 ]; then
    apm self-update
  fi
}

apm_configure() {
  if apm marketplace list | grep -Eq '(^|[[:space:]])qubership-ai-packages([[:space:]]|$)'; then
    if [ "$FORCE_UPDATE" -eq 1 ]; then
      apm marketplace update qubership-ai-packages || return 1
    fi
  else
    apm marketplace add Netcracker/qubership-ai-packages || return 1
  fi

  if [ "$FORCE_UPDATE" -eq 1 ]; then
    apm install --update qubership-global-essentials@qubership-ai-packages \
      -g --target "$HARNESSES" || return 1
  else
    apm install qubership-global-essentials@qubership-ai-packages -g --target "$HARNESSES" || return 1
  fi
  apm compile -g
}

apm_verify() {
  apm deps list -g >/dev/null
}

apm_uninstall() {
  [ -f "$HOME/.apm/apm.yml" ] || return 10
  command -v apm >/dev/null 2>&1 || {
    printf '%s: apm: cannot remove the global package because apm is not on PATH.\n' "$PROGRAM" >&2
    return 1
  }
  apm uninstall -g qubership-global-essentials@qubership-ai-packages
}

telemetry_install() {
  _telemetry_url=${QUBERSHIP_DEV_TELEMETRY_INSTALL_URL:-https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh}
  _telemetry_was_installed=0
  if command -v ai-agent-telemetry >/dev/null 2>&1 || [ -x "$HOME/.local/bin/ai-agent-telemetry" ]; then
    _telemetry_was_installed=1
  fi
  if [ "$FORCE_UPDATE" -eq 1 ] || [ "$_telemetry_was_installed" -eq 1 ]; then
    run_shell_installer "$_telemetry_url" --skip-config --force
  else
    run_shell_installer "$_telemetry_url" --skip-config
  fi
}

resolve_telemetry_bin() {
  if [ -x "$HOME/.local/bin/ai-agent-telemetry" ]; then
    TELEMETRY_BIN=$HOME/.local/bin/ai-agent-telemetry
  elif command -v ai-agent-telemetry >/dev/null 2>&1; then
    TELEMETRY_BIN=ai-agent-telemetry
  else
    printf '%s: telemetry installer completed, but ai-agent-telemetry was not found.\n' "$PROGRAM" >&2
    return 1
  fi
}

telemetry_configure() {
  resolve_telemetry_bin || return 1
  _config_dir=${XDG_CONFIG_HOME:-$HOME/.config}/ai-agent-telemetry
  _endpoint=
  if [ -f "$_config_dir/env" ]; then
    _endpoint=$(awk -F= '$1 == "AI_AGENT_TELEMETRY_ENDPOINT" {sub(/^[^=]*=/, ""); print; exit}' \
      "$_config_dir/env")
  fi
  if [ -n "$_endpoint" ]; then
    "$TELEMETRY_BIN" hooks install "--target=$HARNESSES"
  elif [ "$NON_INTERACTIVE" -eq 1 ]; then
    printf '%s: telemetry configuration is required; run ai-agent-telemetry configure and retry.\n' \
      "$PROGRAM" >&2
    return 1
  else
    "$TELEMETRY_BIN" configure "--hooks=$HARNESSES"
  fi
}

telemetry_verify() {
  resolve_telemetry_bin || return 1
  "$TELEMETRY_BIN" status || return 1
  "$TELEMETRY_BIN" selftest
}

telemetry_receipt_path() {
  printf '%s/ai-agent-telemetry/hooks-uninstalled' "${XDG_STATE_HOME:-$HOME/.local/state}"
}

telemetry_receipt_valid() {
  _path=$(telemetry_receipt_path)
  [ -f "$_path" ] || return 1
  _value=$(cat "$_path" && printf x) || return 1
  [ "$_value" = "$(printf 'version=1\nstate=uninstalled\nx')" ]
}

write_telemetry_receipt() {
  _path=$(telemetry_receipt_path)
  _dir=$(dirname "$_path")
  mkdir -p "$_dir" || return 1
  _tmp=$(mktemp "$_dir/.hooks-uninstalled.tmp.XXXXXX") || return 1
  (umask 077 && printf 'version=1\nstate=uninstalled\n' > "$_tmp") || {
    rm -f "$_tmp"
    return 1
  }
  mv -f "$_tmp" "$_path" || {
    rm -f "$_tmp"
    return 1
  }
}

telemetry_hook_path_state() {
  _probe_path=$1
  if [ -e "$_probe_path" ] || [ -L "$_probe_path" ]; then
    return 0
  fi

  _probe_ancestor=$(dirname "$_probe_path")
  while :; do
    if [ -e "$_probe_ancestor" ] || [ -L "$_probe_ancestor" ]; then
      if [ ! -d "$_probe_ancestor" ]; then
        printf '%s: telemetry: cannot inspect native hook path %s because parent path %s is not a directory.\n' \
          "$PROGRAM" "$_probe_path" "$_probe_ancestor" >&2
        return 2
      fi
      if [ ! -x "$_probe_ancestor" ]; then
        printf '%s: telemetry: cannot inspect native hook path %s because parent path %s is not searchable.\n' \
          "$PROGRAM" "$_probe_path" "$_probe_ancestor" >&2
        return 2
      fi
      return 1
    fi
    _probe_parent=$(dirname "$_probe_ancestor")
    if [ "$_probe_parent" = "$_probe_ancestor" ]; then
      printf '%s: telemetry: cannot inspect native hook path %s.\n' "$PROGRAM" "$_probe_path" >&2
      return 2
    fi
    _probe_ancestor=$_probe_parent
  done
}

telemetry_hooks_may_exist() {
  for _hook_path in \
    "$HOME/.claude/settings.json" \
    "$HOME/.codex/hooks.json" \
    "$HOME/.cursor/hooks.json" \
    "$HOME/.codex/rules/ai-agent-telemetry.rules"; do
    if telemetry_hook_path_state "$_hook_path"; then
      return 0
    else
      _hook_state=$?
      [ "$_hook_state" -eq 1 ] || return 2
    fi
  done
  return 1
}

telemetry_uninstall() {
  _managed_bin=$HOME/.local/bin/ai-agent-telemetry
  _telemetry_bin=
  if [ -x "$_managed_bin" ]; then
    _telemetry_bin=$_managed_bin
  elif command -v ai-agent-telemetry >/dev/null 2>&1; then
    _telemetry_bin=ai-agent-telemetry
  fi

  if [ -n "$_telemetry_bin" ]; then
    "$_telemetry_bin" hooks uninstall || return 1
  elif telemetry_receipt_valid; then
    :
  else
    if telemetry_hooks_may_exist; then
      printf '%s: telemetry: native hook files exist, but no telemetry CLI or valid removal receipt is available.\n' \
        "$PROGRAM" >&2
      return 1
    else
      _hook_state=$?
      [ "$_hook_state" -eq 1 ] || return 1
      write_telemetry_receipt || return 1
    fi
  fi

  rm -f "$_managed_bin" || return 1
  if [ "$PURGE" -eq 1 ]; then
    _config_dir=${XDG_CONFIG_HOME:-$HOME/.config}/ai-agent-telemetry
    _cache_dir=${XDG_CACHE_HOME:-$HOME/.cache}/ai-agent-telemetry
    rm -rf "$_config_dir" "$_cache_dir" || return 1
  fi
}

init_git_hooks() {
  GIT_HOOKS_DIR=${QUBERSHIP_DEV_GIT_HOOKS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/qubership/pre-commit-global}
  GIT_HOOKS_REPOSITORY=${QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY:-https://github.com/exadmin/pre-commit-global.git}
}

git_hooks_desired_path() (
  _hooks_path=$GIT_HOOKS_DIR/hooks-global
  if [ -d "$_hooks_path" ]; then
    CDPATH='' cd -- "$_hooks_path" && pwd -P
    exit
  fi
  case $_hooks_path in
    /*) printf '%s\n' "$_hooks_path" ;;
    *) printf '%s\n' "$PWD/$_hooks_path" ;;
  esac
)

git_hooks_install() {
  init_git_hooks
  _prospective_hooks_path=$(git_hooks_desired_path) || return 1
  _current_hooks_path=$(git config --global --get core.hooksPath 2>/dev/null || :)
  if [ -n "$_current_hooks_path" ] && [ "$_current_hooks_path" != "$_prospective_hooks_path" ] && \
    [ "$FORCE_GIT_HOOKS" -ne 1 ]; then
    printf '%s: core.hooksPath is already set to %s; global Git hooks installation was skipped.\n' \
      "$PROGRAM" "$_current_hooks_path" >&2
    return 10
  fi
  if [ ! -e "$GIT_HOOKS_DIR" ]; then
    mkdir -p "$(dirname "$GIT_HOOKS_DIR")"
    git clone "$GIT_HOOKS_REPOSITORY" "$GIT_HOOKS_DIR" || return 1
  fi
  if ! git -C "$GIT_HOOKS_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf '%s: %s is not the managed Git repository.\n' "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  fi
  _origin=$(git -C "$GIT_HOOKS_DIR" remote get-url origin 2>/dev/null) || {
    printf '%s: cannot read the Git hooks repository origin.\n' "$PROGRAM" >&2
    return 1
  }
  if [ "$_origin" != "$GIT_HOOKS_REPOSITORY" ]; then
    printf '%s: Git hooks repository has unexpected origin %s.\n' "$PROGRAM" "$_origin" >&2
    return 1
  fi
  _git_status=$(git -C "$GIT_HOOKS_DIR" status --porcelain --untracked-files=all) || return 1
  if [ -n "$_git_status" ]; then
    printf '%s: Git hooks repository has local changes; refusing to activate or update it.\n' "$PROGRAM" >&2
    return 1
  fi
  if [ "$FORCE_UPDATE" -eq 1 ]; then
    git -C "$GIT_HOOKS_DIR" pull --ff-only || return 1
  fi
  [ -d "$GIT_HOOKS_DIR/hooks-global" ] || {
    printf '%s: hooks-global was not found in %s.\n' "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  }
}

git_hooks_configure() {
  _desired_hooks_path=$(git_hooks_desired_path) || return 1
  _current_hooks_path=$(git config --global --get core.hooksPath 2>/dev/null || :)
  if [ -n "$_current_hooks_path" ] && [ "$_current_hooks_path" != "$_desired_hooks_path" ]; then
    if [ "$FORCE_GIT_HOOKS" -ne 1 ]; then
      printf '%s: core.hooksPath is already set to %s; global Git hooks installation was skipped.\n' \
        "$PROGRAM" "$_current_hooks_path" >&2
      return 10
    fi
    printf '%s: replacing core.hooksPath: %s -> %s\n' \
      "$PROGRAM" "$_current_hooks_path" "$_desired_hooks_path" >&2
  fi
  if [ "$_current_hooks_path" != "$_desired_hooks_path" ]; then
    git config --global core.hooksPath "$_desired_hooks_path" || return 1
  fi
  if [ -z "${CYBER_FERRET_PASSWORD:-}" ]; then
    printf '%s\n' \
      "$PROGRAM: CYBER_FERRET_PASSWORD is not set; CyberFerret checks require it." \
      "Set it in the environment that runs Git, for example:" \
      "  export CYBER_FERRET_PASSWORD='<password>'" \
      "Persistent and GUI setup:" \
      "  https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/global-scripts/README.md#cyberferret-password" >&2
  fi
}

git_hooks_verify() {
  _configured_hooks_path=$(git config --global --get core.hooksPath 2>/dev/null || :)
  _desired_hooks_path=$(git_hooks_desired_path) || return 1
  [ "$_configured_hooks_path" = "$_desired_hooks_path" ]
}

git_hooks_uninstall() {
  command -v git >/dev/null 2>&1 || {
    printf '%s: git-hooks: cannot uninstall because Git is not on PATH. Install Git and retry.\n' \
      "$PROGRAM" >&2
    return 1
  }
  init_git_hooks
  _desired_hooks_path=$(git_hooks_desired_path) || return 1
  _hooks_values_file=$(mktemp) || return 1
  if git config --global --get-all core.hooksPath > "$_hooks_values_file" 2>/dev/null; then
    _hooks_config_status=0
  else
    _hooks_config_status=$?
  fi
  case $_hooks_config_status in
    0) ;;
    1)
      rm -f "$_hooks_values_file"
      ;;
    *)
      rm -f "$_hooks_values_file"
      printf '%s: git-hooks: cannot read global core.hooksPath values; git config --get-all exited with code %s.\n' \
        "$PROGRAM" "$_hooks_config_status" >&2
      return 1
      ;;
  esac
  if [ "$_hooks_config_status" -eq 0 ]; then
    _unset_values_file=$(mktemp) || {
      rm -f "$_hooks_values_file"
      return 1
    }
    _hooks_unset_status=0
    while IFS= read -r _current_hooks_path || [ -n "$_current_hooks_path" ]; do
      case $_current_hooks_path in
        /*)
          _resolved_hooks_path=$_current_hooks_path
          if [ -d "$_current_hooks_path" ]; then
            _resolved_hooks_path=$(CDPATH='' cd -- "$_current_hooks_path" && pwd -P) || {
              _hooks_unset_status=1
              break
            }
          fi
          if [ "$_resolved_hooks_path" = "$_desired_hooks_path" ] &&
            ! grep -F -x -- "$_current_hooks_path" "$_unset_values_file" >/dev/null 2>&1; then
            _value_pattern=$(printf '%s' "$_current_hooks_path" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
            git config --global --unset-all core.hooksPath "^${_value_pattern}\$" || {
              _hooks_unset_status=1
              break
            }
            printf '%s\n' "$_current_hooks_path" >> "$_unset_values_file"
          fi
          ;;
      esac
    done < "$_hooks_values_file"
    rm -f "$_hooks_values_file" "$_unset_values_file"
    [ "$_hooks_unset_status" -eq 0 ] || return 1
  fi

  [ -e "$GIT_HOOKS_DIR" ] || return 0
  if ! git -C "$GIT_HOOKS_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf '%s: git-hooks: preserving %s because it is not a Git worktree.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  fi
  _origin=$(git -C "$GIT_HOOKS_DIR" remote get-url origin 2>/dev/null) || {
    printf '%s: git-hooks: cannot read origin for %s. Preserving the directory.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  }
  if [ "$_origin" != "$GIT_HOOKS_REPOSITORY" ]; then
    printf '%s: git-hooks: preserving %s because its origin is %s.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" "$_origin" >&2
    return 1
  fi
  _status=$(git -C "$GIT_HOOKS_DIR" status --porcelain) || {
    printf '%s: git-hooks: cannot inspect worktree status for %s. Preserving the directory.\n' \
      "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  }
  if [ -n "$_status" ]; then
    printf '%s: git-hooks: preserving modified worktree %s.\n' "$PROGRAM" "$GIT_HOOKS_DIR" >&2
    return 1
  fi
  rm -rf "$GIT_HOOKS_DIR"
}

SUMMARY=
HAS_FAILURES=0

record_result() {
  _component=$1
  _status=$2
  SUMMARY="${SUMMARY}${_component}|${_status}\n"
}

run_component() {
  _component=$1
  _prefix=$(registry_value "$_component" 4)
  if [ "$MODE" = uninstall ]; then
    printf '\n[%s] UNINSTALLING\n' "$_component"
    "${_prefix}_uninstall"
    _code=$?
    record_component_code "$_component" "$_code"
    return
  fi
  run_install_component "$_component" "$_prefix"
}

record_component_code() {
  _component=$1
  _code=$2
  if [ "$_code" -eq 10 ]; then
    record_result "$_component" SKIPPED
  elif [ "$_code" -eq 0 ]; then
    record_result "$_component" OK
  else
    record_result "$_component" FAILED
    HAS_FAILURES=1
  fi
}

run_install_component() {
  _component=$1
  _prefix=$2
  printf '\n[%s] INSTALLING\n' "$_component"
  "${_prefix}_install"
  _code=$?
  if [ "$_code" -eq 0 ]; then
    printf '[%s] CONFIGURING\n' "$_component"
    "${_prefix}_configure"
    _code=$?
  fi
  [ "$_code" -ne 10 ] || {
    record_component_code "$_component" "$_code"
    return
  }
  if [ "$_code" -eq 0 ]; then
    printf '[%s] VERIFYING\n' "$_component"
    "${_prefix}_verify"
    _code=$?
  fi
  record_component_code "$_component" "$_code"
}

print_summary() {
  if [ "$MODE" = uninstall ]; then
    printf '\nUninstall summary\n'
  else
    printf '\nInstallation summary\n'
  fi
  printf '%b' "$SUMMARY" | while IFS='|' read -r _component _status; do
    [ -n "$_component" ] || continue
    printf '%-16s %s\n' "$_component" "$_status"
  done
}

option_value() {
  _option=$1
  _remaining=$2
  [ -n "$_remaining" ] || argument_error "$_option requires a value"
  printf '%s' "$_remaining"
}

COMPONENTS=$DEFAULT_COMPONENTS
SKIP_COMPONENTS=
HARNESSES=$DEFAULT_HARNESSES
FORCE_GIT_HOOKS=0
FORCE_UPDATE=0
NON_INTERACTIVE=0
HARNESSES_SET=0
FORCE_GIT_HOOKS_SET=0
FORCE_UPDATE_SET=0
NON_INTERACTIVE_SET=0
MODE=install
PURGE=0

while [ "$#" -gt 0 ]; do
  case $1 in
    -h|--help)
      usage
      exit 0
      ;;
    --components=*) COMPONENTS=${1#--components=} ;;
    --components)
      shift
      COMPONENTS=$(option_value --components "${1:-}") || exit $?
      ;;
    --skip=*) SKIP_COMPONENTS=${1#--skip=} ;;
    --skip)
      shift
      SKIP_COMPONENTS=$(option_value --skip "${1:-}") || exit $?
      ;;
    --harnesses=*)
      HARNESSES=${1#--harnesses=}
      HARNESSES_SET=1
      ;;
    --harnesses)
      shift
      HARNESSES=$(option_value --harnesses "${1:-}") || exit $?
      HARNESSES_SET=1
      ;;
    --force-git-hooks)
      FORCE_GIT_HOOKS=1
      FORCE_GIT_HOOKS_SET=1
      ;;
    --force-update)
      FORCE_UPDATE=1
      FORCE_UPDATE_SET=1
      ;;
    --non-interactive)
      NON_INTERACTIVE=1
      NON_INTERACTIVE_SET=1
      ;;
    --uninstall) MODE=uninstall ;;
    --purge) PURGE=1 ;;
    *) argument_error "unknown option \"$1\"" ;;
  esac
  shift
done

[ "$PURGE" -eq 0 ] || [ "$MODE" = uninstall ] || argument_error '--purge requires --uninstall'
if [ "$MODE" = uninstall ]; then
  [ "$HARNESSES_SET" -eq 0 ] || argument_error '--harnesses is not valid with --uninstall'
  [ "$FORCE_UPDATE_SET" -eq 0 ] || argument_error '--force-update is not valid with --uninstall'
  [ "$FORCE_GIT_HOOKS_SET" -eq 0 ] || argument_error '--force-git-hooks is not valid with --uninstall'
  [ "$NON_INTERACTIVE_SET" -eq 0 ] || argument_error '--non-interactive is not valid with --uninstall'
fi

COMPONENTS=$(normalize_list component "$COMPONENTS" "$DEFAULT_COMPONENTS") || exit $?
if [ -n "$SKIP_COMPONENTS" ]; then
  SKIP_COMPONENTS=$(normalize_list component "$SKIP_COMPONENTS" "$DEFAULT_COMPONENTS") || exit $?
  COMPONENTS=$(remove_items "$COMPONENTS" "$SKIP_COMPONENTS")
fi
[ -n "$COMPONENTS" ] || argument_error "no components selected"
if [ "$MODE" = install ]; then
  HARNESSES=$(normalize_list harness "$HARNESSES" "$DEFAULT_HARNESSES") || exit $?
fi

if [ "$MODE" = install ] && contains_csv "$COMPONENTS" git-hooks; then
  require_git_hook_prerequisites || exit 1
fi

_old_ifs=$IFS
IFS=,
# shellcheck disable=SC2086
set -- $COMPONENTS
IFS=$_old_ifs
for _component in "$@"; do
  run_component "$_component"
done
print_summary
if [ "$HAS_FAILURES" -eq 1 ]; then
  exit 1
fi
exit 0
