#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
fixture_root=$work_dir/source
dist=$work_dir/dist
checksum_dist=$work_dir/checksum-dist
release_workflow=$repo_root/.github/workflows/release.yaml

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
[ "$(stat -c '%a' "$dist/ai-agent-telemetry-backend.tar.gz")" = 644 ]
[ "$(stat -c '%a' "$dist/backup-backend.sh")" = 755 ]
[ "$(stat -c '%a' "$dist/update-backend.sh")" = 755 ]

expected_assets=$work_dir/expected-assets.txt
awk '
  /cat > \.\.\/expected-assets\.txt <<'\''EOF'\''/ { in_assets = 1; next }
  in_assets && /^[[:space:]]*EOF$/ { exit }
  in_assets { sub(/^[[:space:]]*/, ""); print }
' "$release_workflow" >"$expected_assets"

upload_paths=$(sed -n 's/^[[:space:]]*item-path: //p' "$release_workflow")
for asset in ai-agent-telemetry-backend.tar.gz backup-backend.sh update-backend.sh; do
  [ "$(grep -Fxc "$asset" "$expected_assets")" = 1 ] || {
    printf 'FAIL: release expected-assets must contain %s exactly once\n' "$asset" >&2
    exit 1
  }
  [ "$(printf '%s\n' "$upload_paths" | tr ',' '\n' | grep -Fxc "dist/$asset")" = 1 ] || {
    printf 'FAIL: release upload paths must contain dist/%s exactly once\n' "$asset" >&2
    exit 1
  }
done

required_checksum_command='run: sha256sum ai-agent-telemetry-* backup-backend.sh update-backend.sh install.sh install.ps1 > SHA256SUMS'
[ "$(grep -Fxc "        $required_checksum_command" "$release_workflow")" = 1 ] || {
  printf 'FAIL: release workflow must checksum every release asset except SHA256SUMS\n' >&2
  exit 1
}

mkdir -p "$checksum_dist"
for asset in \
  ai-agent-telemetry-backend.tar.gz \
  ai-agent-telemetry-darwin-amd64 \
  ai-agent-telemetry-darwin-arm64 \
  ai-agent-telemetry-linux-amd64 \
  ai-agent-telemetry-linux-arm64 \
  ai-agent-telemetry-windows-amd64.exe \
  ai-agent-telemetry-windows-arm64.exe \
  backup-backend.sh \
  install.ps1 \
  install.sh \
  update-backend.sh; do
  printf 'fixture for %s\n' "$asset" >"$checksum_dist/$asset"
done
(
  cd "$checksum_dist"
  sha256sum ai-agent-telemetry-* backup-backend.sh update-backend.sh install.sh install.ps1 >SHA256SUMS
)
find "$checksum_dist" -maxdepth 1 -type f ! -name SHA256SUMS -printf '%f\n' | LC_ALL=C sort \
  >"$work_dir/expected-checksum-assets.txt"
awk '{print $2}' "$checksum_dist/SHA256SUMS" | LC_ALL=C sort >"$work_dir/actual-checksum-assets.txt"
diff -u "$work_dir/expected-checksum-assets.txt" "$work_dir/actual-checksum-assets.txt"

printf '%s\n' 'PASS: backend release packaging contract'
