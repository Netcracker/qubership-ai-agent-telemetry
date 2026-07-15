param(
  [switch]$Force,
  [switch]$SkipConfig
)

$ErrorActionPreference = 'Stop'

$BinaryVersion = if ($env:AI_AGENT_TELEMETRY_INSTALL_VERSION) {
  $env:AI_AGENT_TELEMETRY_INSTALL_VERSION
} else {
  'latest'
}
$BaseUrl = if ($env:AI_AGENT_TELEMETRY_INSTALL_BASE_URL) {
  $env:AI_AGENT_TELEMETRY_INSTALL_BASE_URL
} else {
  'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases'
}

function Download-Url([string]$Asset) {
  if ($BinaryVersion -eq 'latest') {
    return "$BaseUrl/latest/download/$Asset"
  }
  return "$BaseUrl/download/$BinaryVersion/$Asset"
}

function Config-Dir {
  if ($env:XDG_CONFIG_HOME) {
    return (Join-Path $env:XDG_CONFIG_HOME 'ai-agent-telemetry')
  }
  return (Join-Path $env:USERPROFILE '.config\ai-agent-telemetry')
}

function Read-EnvFile([string]$Path) {
  $out = @{}
  if (-not (Test-Path $Path)) { return $out }
  foreach ($line in Get-Content -LiteralPath $Path) {
    if ([string]::IsNullOrWhiteSpace($line) -or $line.TrimStart().StartsWith('#')) { continue }
    $idx = $line.IndexOf('=')
    if ($idx -lt 1) { continue }
    $out[$line.Substring(0, $idx).Trim()] = $line.Substring($idx + 1).Trim()
  }
  return $out
}

function Ensure-Path([string]$BinDir) {
  $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
  $onPath = ($userPath -split ';') -contains $BinDir
  if ($onPath) { return }
  try {
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $BinDir } else { "$userPath;$BinDir" }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    [Console]::Error.WriteLine("ai-agent-telemetry: added $BinDir to your user PATH -- restart your agent")
  } catch {
    [Console]::Error.WriteLine("ai-agent-telemetry: could not update user PATH automatically.")
    [Console]::Error.WriteLine("  Add '$BinDir' to your PATH, then restart your agent.")
  }
}

function Configure-OrRefreshHooks([string]$Bin) {
  $values = Read-EnvFile (Join-Path (Config-Dir) 'env')
  $endpoint = if ($env:AI_AGENT_TELEMETRY_ENDPOINT) {
    $env:AI_AGENT_TELEMETRY_ENDPOINT
  } else {
    $values['AI_AGENT_TELEMETRY_ENDPOINT']
  }
  if ([string]::IsNullOrWhiteSpace($endpoint)) {
    & $Bin configure
  } else {
    & $Bin hooks install
  }
  if ($LASTEXITCODE -ne 0) { throw "hook configuration failed with exit code $LASTEXITCODE" }
}

$binDir = Join-Path $env:USERPROFILE '.local\bin'
$bin = Join-Path $binDir 'ai-agent-telemetry.exe'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "ai-agent-telemetry-windows-$arch.exe"

try {
  if ($Force -or -not (Test-Path $bin)) {
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $tmp = "$bin.tmp"
    Invoke-WebRequest -UseBasicParsing -Uri (Download-Url $asset) -OutFile $tmp
    $resp = Invoke-WebRequest -UseBasicParsing -Uri (Download-Url 'SHA256SUMS')
    $sums = if ($resp.Content -is [byte[]]) {
      [System.Text.Encoding]::ASCII.GetString($resp.Content)
    } else {
      [string]$resp.Content
    }
    $line = ($sums -split "`n") |
      Where-Object { $_.Trim() -match "\s$([regex]::Escape($asset))$" } |
      Select-Object -First 1
    if (-not $line) { Remove-Item -Force $tmp; throw "no checksum entry for $asset" }
    $want = (($line.Trim() -split '\s+')[0]).ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLower()
    if ($got -ne $want) { Remove-Item -Force $tmp; throw "checksum mismatch for $asset (expected $want, got $got)" }
    Move-Item -Force $tmp $bin
    [Console]::Error.WriteLine("ai-agent-telemetry: installed $bin ($BinaryVersion) -- checksum verified")
  } else {
    [Console]::Error.WriteLine("ai-agent-telemetry: already installed at $bin (use -Force to reinstall)")
  }
} catch {
  Write-Error "ai-agent-telemetry: install failed: $_"
  exit 1
}

Ensure-Path $binDir
if (-not $SkipConfig) {
  Configure-OrRefreshHooks $bin
}
