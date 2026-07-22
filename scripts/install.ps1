$ErrorActionPreference = 'Stop'

# Release workflows may stamp a version and mirror without changing this transport.
$BinaryVersion = $env:AI_AGENT_TELEMETRY_INSTALL_VERSION
if ([string]::IsNullOrWhiteSpace($BinaryVersion)) {
  $BinaryVersion = 'latest'
}
$BaseUrl = $env:AI_AGENT_TELEMETRY_INSTALL_BASE_URL
if ([string]::IsNullOrWhiteSpace($BaseUrl)) {
  $BaseUrl = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases'
}

$ForwardArgs = @($args)
if ($ForwardArgs.Count -eq 0) {
  $ForwardArgs = @('install')
} elseif ([string]$ForwardArgs[0] -like '-*') {
  $ForwardArgs = @('install') + $ForwardArgs
}

function Get-DownloadUrl([string]$Asset) {
  if ($BinaryVersion -eq 'latest') {
    return "$BaseUrl/latest/download/$Asset"
  }
  return "$BaseUrl/download/$BinaryVersion/$Asset"
}

function Save-ReleaseFile([string]$Asset, [string]$Destination) {
  try {
    $null = Invoke-WebRequest -UseBasicParsing -Uri (Get-DownloadUrl $Asset) -OutFile $Destination
  } catch {
    throw "could not download $Asset"
  }
}

$ExitCode = 1
$TempRoot = $null
try {
  $architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64'; break }
    'ARM64' { 'arm64'; break }
    default { throw "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
  }
  $asset = "ai-agent-telemetry-windows-$architecture.exe"
  $TempRoot = Join-Path ([IO.Path]::GetTempPath()) ("ai-agent-telemetry-bootstrap-" + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $TempRoot | Out-Null
  $binary = Join-Path $TempRoot $asset
  $sumsPath = Join-Path $TempRoot 'SHA256SUMS'

  Save-ReleaseFile $asset $binary
  Save-ReleaseFile 'SHA256SUMS' $sumsPath

  $escapedAsset = [regex]::Escape($asset)
  $checksumLine = Get-Content -LiteralPath $sumsPath |
    Where-Object { $_ -match "^\s*([A-Fa-f0-9]{64})\s+\*?$escapedAsset\s*$" } |
    Select-Object -First 1
  if (-not $checksumLine) {
    throw "no checksum entry for $asset"
  }
  $expected = (($checksumLine.Trim() -split '\s+')[0]).ToLowerInvariant()
  $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $binary).Hash.ToLowerInvariant()
  if ($actual -ne $expected) {
    throw "checksum mismatch for $asset (expected $expected, got $actual)"
  }

  & $binary @ForwardArgs
  $ExitCode = $LASTEXITCODE
} catch {
  [Console]::Error.WriteLine("ai-agent-telemetry: $($_.Exception.Message)")
  $ExitCode = 1
} finally {
  if ($TempRoot -and (Test-Path -LiteralPath $TempRoot)) {
    Remove-Item -Recurse -Force -LiteralPath $TempRoot -ErrorAction SilentlyContinue
  }
}
exit $ExitCode
