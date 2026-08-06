# Release guide

Release from `main` after the change set has been merged. Do not create or
push the tag by hand. The `Release` workflow validates the input, creates the
tag, publishes the GitHub Release, uploads assets, and verifies the staged
asset list.

## Version

Use a SemVer tag with a leading `v`:

```text
vMAJOR.MINOR.PATCH
```

For example, the first release after `v0.1.0` is `v0.2.0`.

The workflow input is the source of truth for the release version. Do not bump
`VERSION` in `Makefile` as a release step. The release workflow does not call
`make`; it passes the workflow input directly to `go build` with
`-X main.version=<version>` and stamps the same version into the installer
scripts.

For local release-like builds, pass the version explicitly:

```bash
make build VERSION=vX.Y.Z
```

## Pre-release checks

Run the same checks locally before dispatching the workflow:

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
```

The release workflow runs these checks again before it builds binaries.

## Dispatch

Start the release from the Actions tab, or run:

```bash
gh workflow run release.yaml --ref main -f version=vX.Y.Z
```

The workflow must run from `main`. It fails before building if the version
does not match `vMAJOR.MINOR.PATCH` or if the tag already exists.

## Expected assets

The release must contain exactly these assets:

```text
SHA256SUMS
ai-agent-telemetry-backend.tar.gz
ai-agent-telemetry-darwin-amd64
ai-agent-telemetry-darwin-arm64
ai-agent-telemetry-linux-amd64
ai-agent-telemetry-linux-arm64
ai-agent-telemetry-windows-amd64.exe
ai-agent-telemetry-windows-arm64.exe
backup-backend.sh
install.ps1
install.sh
update-backend.sh
```

`SHA256SUMS` must include one entry for every asset above except itself. The workflow verifies this before upload.

`ai-agent-telemetry-backend.tar.gz` contains the deployable backend files at the archive root, including Compose, Caddy, Collector, Grafana, dashboards, and maintenance scripts. It has no repository-name or `telemetry-backend/` wrapper directory.

## First backend installation

Install the release asset directly when the host does not have an active backend release. The updater requires an
existing `/opt/ai-agent-telemetry-backend/latest` link and is only for subsequent updates.

Run these commands as `root`, replacing `vX.Y.Z` with the verified release tag:

```bash
release_id=vX.Y.Z
release_url="https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download/$release_id"
backend_root=/opt/ai-agent-telemetry-backend
release_dir=$backend_root/$release_id

install -d -m 0755 "$release_dir"
curl -fsSLo /tmp/ai-agent-telemetry-backend.tar.gz "$release_url/ai-agent-telemetry-backend.tar.gz"
curl -fsSLo /tmp/SHA256SUMS "$release_url/SHA256SUMS"
(
  cd /tmp
  awk '$2 == "ai-agent-telemetry-backend.tar.gz"' SHA256SUMS | sha256sum -c -
)
tar -xzf /tmp/ai-agent-telemetry-backend.tar.gz -C "$release_dir"
install -m 0600 "$release_dir/.env.example" "$release_dir/.env"
vi "$release_dir/.env"
ln -s "$release_id" "$backend_root/.latest.tmp"
mv -Tf -- "$backend_root/.latest.tmp" "$backend_root/latest"
docker compose --project-name ai-agent-telemetry-backend \
  --project-directory "$release_dir" \
  --env-file "$release_dir/.env" \
  -f "$release_dir/docker-compose.yml" \
  up -d --build
docker compose --project-name ai-agent-telemetry-backend \
  --project-directory "$release_dir" \
  --env-file "$release_dir/.env" \
  -f "$release_dir/docker-compose.yml" \
  ps
```

Confirm that all five services are running and verify the dashboard, log, and metric endpoints before using the
standalone maintenance commands.

## Backend updater bootstrap

Download and verify both standalone maintenance scripts before updating an existing installation. Run these commands
as `root` on the backend host:

```bash
release_url=https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download
curl -fsSLO "$release_url/update-backend.sh"
curl -fsSLO "$release_url/backup-backend.sh"
curl -fsSLO "$release_url/SHA256SUMS"
awk '$2 == "update-backend.sh" || $2 == "backup-backend.sh"' SHA256SUMS | sha256sum -c -
chmod 0755 update-backend.sh backup-backend.sh
./update-backend.sh --ref latest
```

Pass a release tag, branch name, or full commit SHA to `--ref` when you do not want the latest release. The updater resolves branch names and commit references through the GitHub API before downloading an immutable source archive:

```bash
./update-backend.sh --ref v1.2.0
./update-backend.sh --ref main
./update-backend.sh --ref <full-commit-sha>
```

The updater stages downloads and images before downtime, creates a self-contained backup under `/opt/ai-agent-telemetry-backups`, activates the target through the `latest` symlink, checks backend health, and restores the previous release if activation fails.

After a successful health check, an interactive update offers to remove backups older than 14 days. The two newest
backups are always retained. Noninteractive runs keep old backups unless you pass `--prune-backups`:

```bash
./update-backend.sh --ref latest --prune-backups
```

## Smoke checks

### Canonical lifecycle installer

After the workflow succeeds, install the published version on one Unix-like machine:

```bash
INSTALLER_URL=https://github.com/Netcracker/qubership-ai-agent-telemetry
INSTALLER_URL=$INSTALLER_URL/releases/latest/download/install.sh
export AI_AGENT_TELEMETRY_INSTALL_VERSION=vX.Y.Z
curl -fsSL "$INSTALLER_URL" | sh -s -- --components telemetry --non-interactive
ai-agent-telemetry version
```

From Windows Command Prompt:

```bat
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command ^
  "$r='https://github.com/Netcracker';" ^
  "$r+='/qubership-ai-agent-telemetry/releases/latest/download';" ^
  "$env:AI_AGENT_TELEMETRY_INSTALL_VERSION='vX.Y.Z';" ^
  "& ([scriptblock]::Create((Invoke-RestMethod ($r+'/install.ps1')))) --components telemetry --non-interactive"
powershell.exe -NoProfile -Command ^
  "& ($env:USERPROFILE+'\.local\bin\ai-agent-telemetry.exe') version"
```

Set `AI_AGENT_TELEMETRY_ENDPOINT` or seed valid machine configuration before these noninteractive commands. Both
commands should print `vX.Y.Z`.

### Full lifecycle

Run the overall installer on a disposable machine. Complete telemetry
configuration when prompted, and confirm that the summary reports `OK` for
every component.

On macOS or Linux:

```bash
REPOSITORY=https://github.com/Netcracker/qubership-ai-agent-telemetry
RELEASE=$REPOSITORY/releases/latest/download
curl -fsSL "$RELEASE/install.sh" \
  | sh -s -- update
```

From Windows Command Prompt:

```bat
powershell.exe -NoProfile -ExecutionPolicy Bypass -Command ^
  "$r='https://github.com/Netcracker';" ^
  "$r+='/qubership-ai-agent-telemetry/releases/latest/download';" ^
  "& ([scriptblock]::Create((Invoke-RestMethod ($r+'/install.ps1')))) update"
```

Confirm that update refreshes all selected components and that `update --cli-only` refreshes only the managed CLI. On
Windows, also verify direct update handoff and full uninstall through the temporary `install.ps1` bootstrap.

### Cline hook

For a release that adds or changes Cline support, verify the installed hook on macOS or Linux:

```sh
ai-agent-telemetry status --verbose
hook="$HOME/Documents/Cline/Hooks/PostToolUse"
test -x "$hook"
probe_dir=$(mktemp -d)
printf '%s\n' '{"hookName":"PostToolUse","postToolUse":{"toolName":"read_file","parameters":{},"result":"","success":true,"executionTimeMs":1}}' \
  | "$hook" >"$probe_dir/stdout" 2>"$probe_dir/stderr"
test ! -s "$probe_dir/stdout"
test ! -s "$probe_dir/stderr"
rm "$probe_dir/stdout" "$probe_dir/stderr"
rmdir "$probe_dir"
```

On Windows PowerShell:

```powershell
ai-agent-telemetry status --verbose
$hook = Join-Path $HOME 'Documents\Cline\Hooks\PostToolUse.ps1'
if (-not (Test-Path $hook -PathType Leaf)) { throw "Cline hook is missing: $hook" }
$output = [IO.Path]::GetTempFileName()
$payload = '{"hookName":"PostToolUse","postToolUse":{"toolName":"read_file","parameters":{},"result":"","success":true,"executionTimeMs":1}}'
$payload | & $hook *> $output
if ((Get-Item $output).Length -ne 0) { throw "Cline hook wrote output: $output" }
Remove-Item $output
```

Confirm that `status --verbose` reports the Cline hook as `installed`. The unsupported `read_file` payload sends no
event; it checks only registration, execution, and silent output. Then
invoke a temporary skill once from the Cline VS Code Extension or Cline CLI. Confirm that the collector contains one
`skill_executed` record with `agent=cline` and the temporary `skill.name`, and that `status --verbose` reports no
buffered events or delivery error. Remove the temporary skill after the check.

## Breaking-change checks

Release validation must reject removed update-check, self-update, force-update, force, skip-config, `bootstrap.sh`,
`bootstrap.ps1`, and PowerShell named-parameter behavior. Do not retain old tests to preserve those contracts. Replace
deleted coverage with unified lifecycle routing, update handoff, selection, ownership, strict flush, and concrete
completion tests.

When removing obsolete installer tests, keep or add these replacements:

- Cobra command, help, validation, and exit-code tests for `install`, `update`, and `uninstall`;
- component and harness normalization, partial and full uninstall, purge, and CLI-removal ownership tests;
- verified update handoff, Windows swap and rollback, exact stale-image cleanup, and child exit-code tests;
- strict explicit-flush failure and retention tests alongside fail-open ingest tests;
- receipt-owned `PATH` removal and unconditional `~/.local/bin` preservation tests;
- concrete completion candidate and directive tests for every value flag, plus generation smoke tests for four shells;
- bootstrap transport, quoting, stdin, checksum, platform selection, forwarding, and temporary-cleanup tests.

Deleting a test is valid only when the old behavior is intentionally removed or equivalent Go lifecycle coverage is
present and named in the change description.
