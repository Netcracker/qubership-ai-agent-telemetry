#!/bin/sh
# Install the ai-agent-telemetry binary onto PATH, then optionally run it.
#
# The hooks call the binary by its bare name (`ai-agent-telemetry ingest …`), so
# the binary must be a real command on PATH. This installer is the one-time
# provisioning step that puts it there: it downloads the release binary into
# ~/.local/bin, verifies its checksum, makes it executable, and ensures
# ~/.local/bin is on PATH.
#
# It only installs. Provisioning, status, and every other command are the
# binary's job: call `ai-agent-telemetry <cmd>` directly afterwards (by full path
# in the same session, by bare name once a new shell picks up PATH).
#
# It is interactive (not a hook), so its own errors surface with a non-zero
# exit. POSIX sh; only built-in tools plus curl.
#
# The whole body runs inside main(), invoked on the very last line, so a
# truncated download (this script is meant to be piped into `sh`) can never
# execute a partial install: if the file is cut short, `main "$@"` is missing
# and nothing runs.
set -eu

# BINARY_VERSION is the release tag; BASE_URL/<tag>/<asset> is the GitHub
# Releases download layout, so the URL below resolves to a real asset.
BINARY_VERSION="v0.1.0"
BASE_URL="https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download"

die() {
  echo "ai-agent-telemetry: $1" >&2
  exit 1
}

# sha256_of prints the lowercase hex SHA-256 of file $1, or nothing when no
# checksum tool is available (sha256sum on Linux, shasum on macOS).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# verify_checksum aborts unless $1 hashes to the value SHA256SUMS publishes for
# asset $2. A failure removes the temp file so a corrupt or tampered download is
# never installed. If the host has no sha256 tool at all it warns and proceeds,
# rather than blocking the install on a missing utility.
verify_checksum() {
  _tmp="$1"; _asset="$2"
  _sums="$_tmp.SHA256SUMS"
  if ! curl -fsSL "$BASE_URL/$BINARY_VERSION/SHA256SUMS" -o "$_sums"; then
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

main() {
  # --force re-downloads even when a binary is already present, so re-running the
  # latest installer with --force is the update path (the latest installer pins
  # the latest binary). No other arguments are accepted.
  FORCE=0
  for _arg in "$@"; do
    case "$_arg" in
      --force) FORCE=1 ;;
      *) die "unknown option: $_arg (only --force is supported)" ;;
    esac
  done

  case "$(uname -s)" in
    Darwin) OS="darwin" ;;
    Linux)  OS="linux" ;;
    *) die "unsupported OS $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) ARCH="arm64" ;;
    x86_64|amd64)  ARCH="amd64" ;;
    *) die "unsupported arch $(uname -m)" ;;
  esac

  # ~/.local/bin is the uniform install location across every OS. On most Linux
  # distributions it is already on PATH; elsewhere the PATH step below wires it in.
  BIN_DIR="$HOME/.local/bin"
  BIN="$BIN_DIR/ai-agent-telemetry"
  ASSET="ai-agent-telemetry-$OS-$ARCH"

  if [ "$FORCE" = 1 ] || [ ! -x "$BIN" ]; then
    mkdir -p "$BIN_DIR"
    TMP="$BIN.tmp.$$"
    if ! curl -fsSL "$BASE_URL/$BINARY_VERSION/$ASSET" -o "$TMP"; then
      rm -f "$TMP"
      die "download failed ($BASE_URL/$BINARY_VERSION/$ASSET)"
    fi
    verify_checksum "$TMP" "$ASSET"
    chmod +x "$TMP"
    mv -f "$TMP" "$BIN"
    if [ "$FORCE" = 1 ]; then
      echo "ai-agent-telemetry: (re)installed $BIN ($BINARY_VERSION)" >&2
    else
      echo "ai-agent-telemetry: installed $BIN ($BINARY_VERSION)" >&2
    fi
  else
    echo "ai-agent-telemetry: already installed at $BIN (use --force to reinstall)" >&2
  fi

  # Ensure ~/.local/bin is on PATH. If it is already there (this run or a login
  # shell), nothing to do. Otherwise append an export to the user's shell profiles
  # — a per-user file the installer can always write, so no elevated grant is
  # needed. The change takes effect in new shells, so the agent must be restarted
  # before the bare-name hook resolves.
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;; # already on PATH for this process
    *)
      line='export PATH="$HOME/.local/bin:$PATH"'
      added=""
      # Always seed ~/.profile. Also seed the login shell's own rc — creating it if
      # absent — since zsh (the macOS default) never reads ~/.profile, and a fresh
      # account often has no ~/.zshrc yet. Touch other rc files only if they exist.
      seed="$HOME/.profile"
      case "${SHELL:-}" in
        *zsh)  seed="$seed $HOME/.zshrc" ;;
        *bash) seed="$seed $HOME/.bashrc" ;;
      esac
      for rc in $seed "$HOME/.bashrc" "$HOME/.zshrc"; do
        case " $seed " in
          *" $rc "*) : ;;            # always create/seed these
          *) [ -e "$rc" ] || continue ;; # others: only if they already exist
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
        echo "ai-agent-telemetry: added ~/.local/bin to PATH ($added) — restart your shell/agent" >&2
      else
        echo "ai-agent-telemetry: could not update a shell profile automatically." >&2
        echo "  Add this line to your shell profile, then restart your shell/agent:" >&2
        echo "    $line" >&2
      fi
      ;;
  esac

  exit 0
}

main "$@"
