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
  [[ -f "$backend_dir/$member" ]] || {
    printf 'Missing backend release member: %s\n' "$member" >&2
    exit 1
  }
done <"$member_file"

mkdir -p "$dist_dir"
tar -C "$backend_dir" -czf "$dist_dir/ai-agent-telemetry-backend.tar.gz" -T "$member_file"
chmod 0644 "$dist_dir/ai-agent-telemetry-backend.tar.gz"
install -m 0755 "$backend_dir/scripts/backup-backend.sh" "$dist_dir/backup-backend.sh"
install -m 0755 "$backend_dir/scripts/update-backend.sh" "$dist_dir/update-backend.sh"
