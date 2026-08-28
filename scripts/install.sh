#!/bin/sh
set -eu

# Release workflows may stamp the default without changing an environment override.
DEFAULT_BINARY_VERSION=latest
BINARY_VERSION=${AI_AGENT_TELEMETRY_INSTALL_VERSION:-$DEFAULT_BINARY_VERSION}
BASE_URL=${AI_AGENT_TELEMETRY_INSTALL_BASE_URL:-https://github.com/Netcracker/qubership-ai-agent-telemetry/releases}
TEMP_DIR=

die() {
  printf 'ai-agent-telemetry: %s\n' "$1" >&2
  exit 1
}

# shellcheck disable=SC2317,SC2329 # Invoked indirectly by the traps below.
cleanup() {
  if [ -n "$TEMP_DIR" ]; then
    rm -rf -- "$TEMP_DIR"
  fi
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

download_url() {
  if [ "$BINARY_VERSION" = latest ]; then
    printf '%s/latest/download/%s' "$BASE_URL" "$1"
  else
    printf '%s/download/%s/%s' "$BASE_URL" "$BINARY_VERSION" "$1"
  fi
}

download() {
  label=$1
  destination=$2
  printf 'Downloading %s...\n' "$label" >&2
  if ! curl -fsSL "$(download_url "$label")" -o "$destination" >/dev/null; then
    die "could not download $label"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die 'SHA-256 verification requires sha256sum or shasum'
  fi
}

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS $(uname -s)" ;;
esac

case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if [ "$#" -eq 0 ]; then
  set -- install
else
  case $1 in
    install|update|uninstall) ;;
    -*) set -- install "$@" ;;
  esac
fi

asset=ai-agent-telemetry-$os-$arch
umask 077
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/ai-agent-telemetry-bootstrap.XXXXXXXX") ||
  die 'could not create a private temporary directory'
binary=$TEMP_DIR/$asset
sums=$TEMP_DIR/SHA256SUMS

download "$asset" "$binary"
download SHA256SUMS "$sums"

printf 'Verifying %s checksum...\n' "$asset" >&2
expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset {print $1; exit}' "$sums")
[ -n "$expected" ] || die "no checksum entry for $asset"
actual=$(sha256_of "$binary")
if [ "$actual" != "$expected" ]; then
  die "checksum mismatch for $asset (expected $expected, got $actual)"
fi
chmod 700 "$binary"

printf 'Starting ai-agent-telemetry %s...\n' "$1" >&2
set +e
if (: </dev/tty) 2>/dev/null; then
  "$binary" "$@" </dev/tty
  status=$?
else
  "$binary" "$@"
  status=$?
fi
set -e
exit "$status"
