#!/bin/sh
set -eu

BINARY_VERSION="${AI_AGENT_TELEMETRY_INSTALL_VERSION:-latest}"
BASE_URL="${AI_AGENT_TELEMETRY_INSTALL_BASE_URL:-https://github.com/Netcracker/qubership-ai-agent-telemetry/releases}"

die() {
  echo "ai-agent-telemetry: $1" >&2
  exit 1
}

download_url() {
  if [ "$BINARY_VERSION" = "latest" ]; then
    printf '%s/latest/download/%s' "$BASE_URL" "$1"
  else
    printf '%s/download/%s/%s' "$BASE_URL" "$BINARY_VERSION" "$1"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    return 0
  fi
}

config_dir() {
  if [ -n "${XDG_CONFIG_HOME:-}" ]; then
    printf '%s/ai-agent-telemetry' "$XDG_CONFIG_HOME"
  else
    printf '%s/.config/ai-agent-telemetry' "$HOME"
  fi
}

env_value() {
  _key="$1"
  _file="$2"
  [ -f "$_file" ] || return 0
  awk -F= -v k="$_key" '$1==k {sub(/^[^=]*=/,""); print; exit}' "$_file"
}

verify_checksum() {
  _tmp="$1"
  _asset="$2"
  _sums="$_tmp.SHA256SUMS"
  if ! curl -fsSL "$(download_url SHA256SUMS)" -o "$_sums"; then
    rm -f "$_tmp" "$_sums"
    die "could not fetch SHA256SUMS to verify the download"
  fi
  _want=$(awk -v a="$_asset" '$2==a {print $1; exit}' "$_sums")
  rm -f "$_sums"
  [ -n "$_want" ] || { rm -f "$_tmp"; die "no checksum entry for $_asset"; }
  _got=$(sha256_of "$_tmp")
  if [ -z "$_got" ]; then
    echo "ai-agent-telemetry: warning: no sha256 tool found; skipping checksum verification" >&2
  elif [ "$_got" != "$_want" ]; then
    rm -f "$_tmp"
    die "checksum mismatch for $_asset (expected $_want, got $_got)"
  fi
}

ensure_path() {
  case ":$PATH:" in
    *":$BIN_DIR:"*) return 0 ;;
  esac
  # shellcheck disable=SC2016
  line='export PATH="$HOME/.local/bin:$PATH"'
  added=""
  seed="$HOME/.profile"
  case "${SHELL:-}" in
    *zsh) seed="$seed $HOME/.zshrc" ;;
    *bash) seed="$seed $HOME/.bashrc" ;;
  esac
  for rc in $seed "$HOME/.bashrc" "$HOME/.zshrc"; do
    case " $seed " in
      *" $rc "*) : ;;
      *) [ -e "$rc" ] || continue ;;
    esac
    if [ -e "$rc" ] && grep -qF '.local/bin' "$rc" 2>/dev/null; then
      added="$rc"
      continue
    fi
    if printf '\n# Added by ai-agent-telemetry installer\n%s\n' "$line" >> "$rc" 2>/dev/null; then
      added="$rc"
    fi
  done
  if [ -n "$added" ]; then
    echo "ai-agent-telemetry: added ~/.local/bin to PATH ($added) -- restart your shell/agent" >&2
  else
    echo "ai-agent-telemetry: add this line to your shell profile, then restart your shell/agent:" >&2
    echo "  $line" >&2
  fi
}

configure_if_missing() {
  env_file="$(config_dir)/env"
  endpoint="$(env_value AI_AGENT_TELEMETRY_ENDPOINT "$env_file")"
  if [ -z "$endpoint" ]; then
    "$BIN" configure
  fi
}

main() {
  FORCE=0
  SKIP_CONFIG=0
  for arg in "$@"; do
    case "$arg" in
      --force) FORCE=1 ;;
      --skip-config) SKIP_CONFIG=1 ;;
      *) die "unknown option: $arg" ;;
    esac
  done

  case "$(uname -s)" in
    Darwin) OS="darwin" ;;
    Linux) OS="linux" ;;
    *) die "unsupported OS $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) ARCH="arm64" ;;
    x86_64|amd64) ARCH="amd64" ;;
    *) die "unsupported arch $(uname -m)" ;;
  esac

  BIN_DIR="$HOME/.local/bin"
  BIN="$BIN_DIR/ai-agent-telemetry"
  ASSET="ai-agent-telemetry-$OS-$ARCH"

  if [ "$FORCE" = 1 ] || [ ! -x "$BIN" ]; then
    mkdir -p "$BIN_DIR"
    TMP="$BIN.tmp.$$"
    if ! curl -fsSL "$(download_url "$ASSET")" -o "$TMP"; then
      rm -f "$TMP"
      die "download failed ($(download_url "$ASSET"))"
    fi
    verify_checksum "$TMP" "$ASSET"
    chmod +x "$TMP"
    mv -f "$TMP" "$BIN"
    echo "ai-agent-telemetry: installed $BIN ($BINARY_VERSION)" >&2
  else
    echo "ai-agent-telemetry: already installed at $BIN (use --force to reinstall)" >&2
  fi

  ensure_path
  if [ "$SKIP_CONFIG" = 0 ]; then
    configure_if_missing
  fi
}

main "$@"
