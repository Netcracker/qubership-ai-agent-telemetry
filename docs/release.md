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
ai-agent-telemetry-darwin-amd64
ai-agent-telemetry-darwin-arm64
ai-agent-telemetry-linux-amd64
ai-agent-telemetry-linux-arm64
ai-agent-telemetry-windows-amd64.exe
ai-agent-telemetry-windows-arm64.exe
bootstrap.ps1
bootstrap.sh
install.ps1
install.sh
```

`SHA256SUMS` must include one entry for every asset above except itself. The
workflow verifies this before upload.

## Smoke checks

After the workflow succeeds, install the published version on one Unix-like machine:

```bash
INSTALLER_URL=https://github.com/Netcracker/qubership-ai-agent-telemetry
INSTALLER_URL=$INSTALLER_URL/releases/latest/download/install.sh
export AI_AGENT_TELEMETRY_INSTALL_VERSION=vX.Y.Z
curl -fsSL "$INSTALLER_URL" | sh -s -- --skip-config --force
ai-agent-telemetry version
```

On Windows PowerShell:

```powershell
$env:AI_AGENT_TELEMETRY_INSTALL_VERSION = 'vX.Y.Z'
$Installer = 'https://github.com/Netcracker/qubership-ai-agent-telemetry'
$Installer = "$Installer/releases/latest/download/install.ps1"
iex "& { $(irm $Installer) } -SkipConfig -Force"
ai-agent-telemetry version
```

Both commands should print `vX.Y.Z`.
