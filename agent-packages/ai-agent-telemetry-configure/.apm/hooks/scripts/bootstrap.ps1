# Install the ai-agent-telemetry binary onto PATH, then optionally run it.
#
# The hooks call the binary by its bare name (ai-agent-telemetry ingest ...), so
# the binary must be a real command on PATH. This installer is the one-time
# provisioning step that puts it there: it downloads the release binary into
# %USERPROFILE%\.local\bin and ensures that directory is on the user PATH.
#
# It only installs. Provisioning, status, and every other command are the
# binary's job: call ai-agent-telemetry <cmd> directly afterwards (by full path in
# the same session, by bare name once a new process picks up PATH).
#
# It is interactive (not a hook), so its own errors surface with a non-zero
# exit. Windows PowerShell 5.1 built-ins only. ASCII only: PS 5.1 reads scripts
# in the system codepage, so non-ASCII characters would corrupt the parse.
$ErrorActionPreference = 'Stop'

# BinaryVersion is the release tag; BaseUrl/<tag>/<asset> is the GitHub
# Releases download layout, so the URL below resolves to a real asset.
$BinaryVersion = 'v0.1.0'
$BaseUrl = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/download'

# ~/.local/bin is the uniform install location across every OS.
$binDir = Join-Path $env:USERPROFILE '.local\bin'
$bin = Join-Path $binDir 'ai-agent-telemetry.exe'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "ai-agent-telemetry-windows-$arch.exe"

# --force re-downloads even when a binary is already present, so re-running the
# latest installer with --force is the update path (the latest installer pins the
# latest binary).
$force = ($args -contains '--force') -or ($args -contains '-Force')

try {
  if ($force -or -not (Test-Path $bin)) {
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $tmp = "$bin.tmp"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$BinaryVersion/$asset" -OutFile $tmp
    # Verify integrity against the published SHA256SUMS before trusting the
    # binary. A mismatch removes the temp file so nothing corrupt is installed.
    # GitHub serves the extensionless SHA256SUMS as octet-stream, so .Content
    # comes back as bytes -- decode to text before parsing.
    $resp = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$BinaryVersion/SHA256SUMS"
    $sums = if ($resp.Content -is [byte[]]) { [System.Text.Encoding]::ASCII.GetString($resp.Content) } else { [string]$resp.Content }
    $line = ($sums -split "`n") | Where-Object { $_.Trim() -match "\s$([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $line) { Remove-Item -Force $tmp; throw "no checksum entry for $asset" }
    $want = (($line.Trim() -split '\s+')[0]).ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLower()
    if ($got -ne $want) { Remove-Item -Force $tmp; throw "checksum mismatch for $asset (expected $want, got $got)" }
    Move-Item -Force $tmp $bin
    $verb = if ($force) { '(re)installed' } else { 'installed' }
    [Console]::Error.WriteLine("ai-agent-telemetry: $verb $bin ($BinaryVersion) -- checksum verified")
  } else {
    [Console]::Error.WriteLine("ai-agent-telemetry: already installed at $bin (use --force to reinstall)")
  }
} catch {
  Write-Error "ai-agent-telemetry: install failed: $_"
  exit 1
}

# Ensure ~/.local/bin is on the user PATH (HKCU -- no admin grant needed). The
# change lands in the registry and applies to new processes, so the agent must
# be restarted before the bare-name hook resolves. If the write is somehow
# denied, fall back to printing the manual step.
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
$onPath = ($userPath -split ';') -contains $binDir
if (-not $onPath) {
  try {
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $binDir } else { "$userPath;$binDir" }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    [Console]::Error.WriteLine("ai-agent-telemetry: added $binDir to your user PATH -- restart your agent")
  } catch {
    [Console]::Error.WriteLine("ai-agent-telemetry: could not update user PATH automatically.")
    [Console]::Error.WriteLine("  Add '$binDir' to your PATH, then restart your agent.")
  }
}

exit 0
