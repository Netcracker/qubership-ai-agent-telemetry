$ErrorActionPreference = 'Stop'

$Installer = Join-Path $PSScriptRoot 'install.ps1'
$Pwsh = (Get-Process -Id $PID).Path
$SystemTemp = [IO.Path]::GetTempPath()

function Assert-True([bool]$Condition, [string]$Message) {
  if (-not $Condition) { throw "FAIL: $Message" }
}

function Assert-Contains([string]$Value, [string]$Expected, [string]$Message) {
  Assert-True ($Value.Contains($Expected)) "$Message; output: $Value"
}

function Assert-NotContains([string]$Value, [string]$Unexpected, [string]$Message) {
  Assert-True (-not $Value.Contains($Unexpected)) "$Message; output: $Value"
}

function New-Fixture {
  $env:TEMP = $SystemTemp
  $env:TMP = $SystemTemp
  $env:TMPDIR = $SystemTemp
  $env:QDI_DOWNLOAD_FAIL = '0'
  $env:QDI_RESPONSE_BODY = 'private-response-body'
  $env:QDI_BINARY_EXIT = '0'
  $env:PROCESSOR_ARCHITECTURE = 'AMD64'
  $root = Join-Path $SystemTemp ("telemetry-bootstrap-test-" + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $root | Out-Null
  $temp = Join-Path $root 'tmp'
  New-Item -ItemType Directory -Path $temp | Out-Null
  $asset = Join-Path $root 'asset.exe'
  $assetSource = Join-Path $root 'asset.go'
  @'
package main

import (
  "os"
  "strconv"
  "strings"
)

func main() {
  arguments := strings.Join(os.Args[1:], "\n") + "\n"
  if err := os.WriteFile(os.Getenv("QDI_EXEC_LOG"), []byte(arguments), 0o600); err != nil {
    os.Exit(70)
  }
  code, err := strconv.Atoi(os.Getenv("QDI_BINARY_EXIT"))
  if err != nil {
    os.Exit(71)
  }
  os.Exit(code)
}
'@ | Set-Content -LiteralPath $assetSource -Encoding ASCII
  & go build -trimpath -o $asset $assetSource
  if ($LASTEXITCODE -ne 0) { throw "failed to build native bootstrap fixture: exit $LASTEXITCODE" }
  $assetName = 'ai-agent-telemetry-windows-amd64.exe'
  $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $asset).Hash.ToLowerInvariant()
  $sums = Join-Path $root 'SHA256SUMS'
  Set-Content -LiteralPath $sums -Value "$digest  $assetName"
  $wrapper = Join-Path $root 'wrapper.ps1'
  @'
$ErrorActionPreference = 'Stop'
function global:Invoke-WebRequest {
  param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
  Add-Content -LiteralPath $env:QDI_DOWNLOAD_LOG -Value $Uri
  if ($env:QDI_DOWNLOAD_FAIL -eq '1') {
    Write-Output $env:QDI_RESPONSE_BODY
    throw 'mock response body must remain private'
  }
  if ($Uri.EndsWith('/SHA256SUMS')) {
    Copy-Item -LiteralPath $env:QDI_SUMS_FILE -Destination $OutFile
  } else {
    Copy-Item -LiteralPath $env:QDI_ASSET_FILE -Destination $OutFile
  }
}
& $env:QDI_TARGET_SCRIPT @args
exit $LASTEXITCODE
'@ | Set-Content -LiteralPath $wrapper
  return @{
    Root = $root
    Temp = $temp
    Asset = $asset
    Sums = $sums
    Wrapper = $wrapper
    DownloadLog = (Join-Path $root 'download.log')
    ExecLog = (Join-Path $root 'exec.log')
  }
}

function Invoke-Fixture([hashtable]$Fixture, [string]$Target, [string[]]$ForwardArgs) {
  $env:TMPDIR = $Fixture.Temp
  $env:TEMP = $Fixture.Temp
  $env:TMP = $Fixture.Temp
  $env:AI_AGENT_TELEMETRY_INSTALL_BASE_URL = 'https://release.example.test/releases'
  $env:QDI_ASSET_FILE = $Fixture.Asset
  $env:QDI_SUMS_FILE = $Fixture.Sums
  $env:QDI_DOWNLOAD_LOG = $Fixture.DownloadLog
  $env:QDI_EXEC_LOG = $Fixture.ExecLog
  $env:QDI_TARGET_SCRIPT = $Target
  $previousErrorAction = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    $output = & $Pwsh -NoProfile -File $Fixture.Wrapper @ForwardArgs 2>&1 | Out-String
    $code = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousErrorAction
  }
  return @{ Code = $code; Output = $output }
}

function Assert-TempClean([hashtable]$Fixture) {
  $entries = @(Get-ChildItem -Force -LiteralPath $Fixture.Temp)
  Assert-True ($entries.Count -eq 0) "private temporary directory remains: $($entries.FullName -join ', ')"
}

function Test-Syntax {
  $tokens = $null
  $errors = $null
  [void][Management.Automation.Language.Parser]::ParseFile($Installer, [ref]$tokens, [ref]$errors)
  Assert-True ($errors.Count -eq 0) "PowerShell syntax errors in ${Installer}: $($errors -join '; ')"
}

function Test-DefaultingAndExactArguments {
  $fixture = New-Fixture
  try {
    $result = Invoke-Fixture $fixture $Installer @()
    Assert-True ($result.Code -eq 0) "default install failed: $($result.Output)"
    Assert-True ((Get-Content -Raw $fixture.ExecLog).Trim() -eq 'install') 'no arguments did not default to install'
    $result = Invoke-Fixture $fixture $Installer @('--components', 'telemetry', '--non-interactive')
    Assert-True ($result.Code -eq 0) "option-first install failed: $($result.Output)"
    $want = "install`n--components`ntelemetry`n--non-interactive"
    Assert-True ((Get-Content -Raw $fixture.ExecLog).Trim() -eq $want) 'option-first arguments changed'
    $result = Invoke-Fixture $fixture $Installer @('update', '--components', 'telemetry,apm')
    Assert-True ($result.Code -eq 0) "explicit update failed: $($result.Output)"
    $want = "update`n--components`ntelemetry,apm"
    Assert-True ((Get-Content -Raw $fixture.ExecLog).Trim() -eq $want) 'explicit update arguments changed'
  } finally {
    Remove-Item -Recurse -Force $fixture.Root -ErrorAction SilentlyContinue
  }
}

function Test-UrlsChecksumExitAndCleanup {
  $fixture = New-Fixture
  try {
    $result = Invoke-Fixture $fixture $Installer @('update')
    Assert-True ($result.Code -eq 0) "latest bootstrap failed: $($result.Output)"
    $urls = Get-Content -LiteralPath $fixture.DownloadLog
    Assert-True ($urls -contains 'https://release.example.test/releases/latest/download/ai-agent-telemetry-windows-amd64.exe') 'latest AMD64 asset URL missing'
    Clear-Content -LiteralPath $fixture.DownloadLog

    $env:PROCESSOR_ARCHITECTURE = 'ARM64'
    $env:AI_AGENT_TELEMETRY_INSTALL_VERSION = 'v1.2.3'
    $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $fixture.Asset).Hash.ToLowerInvariant()
    Set-Content -LiteralPath $fixture.Sums -Value "$digest  ai-agent-telemetry-windows-arm64.exe"
    $result = Invoke-Fixture $fixture $Installer @('update')
    Assert-True ($result.Code -eq 0) "versioned bootstrap failed: $($result.Output)"
    $urls = Get-Content -LiteralPath $fixture.DownloadLog
    Assert-True ($urls -contains 'https://release.example.test/releases/download/v1.2.3/ai-agent-telemetry-windows-arm64.exe') 'versioned ARM64 asset URL missing'
    Assert-True ($urls -contains 'https://release.example.test/releases/download/v1.2.3/SHA256SUMS') 'versioned checksum URL missing'
    Assert-TempClean $fixture

    $env:QDI_BINARY_EXIT = '43'
    $result = Invoke-Fixture $fixture $Installer @('update')
    Assert-True ($result.Code -eq 43) "binary exit 43 became $($result.Code): $($result.Output)"
    Assert-TempClean $fixture

    $env:PROCESSOR_ARCHITECTURE = 'AMD64'
    Set-Content -LiteralPath $fixture.Sums -Value "$('0' * 64)  ai-agent-telemetry-windows-amd64.exe"
    Remove-Item -Force $fixture.ExecLog -ErrorAction SilentlyContinue
    $result = Invoke-Fixture $fixture $Installer @('install')
    Assert-True ($result.Code -ne 0) 'checksum mismatch succeeded'
    Assert-Contains $result.Output 'checksum mismatch' 'checksum mismatch diagnostic missing'
    Assert-True (-not (Test-Path $fixture.ExecLog)) 'binary executed after checksum mismatch'
    Assert-TempClean $fixture

    Set-Content -LiteralPath $fixture.Sums -Value 'deadbeef  another.exe'
    $result = Invoke-Fixture $fixture $Installer @('install')
    Assert-True ($result.Code -ne 0) 'missing checksum entry succeeded'
    Assert-Contains $result.Output 'no checksum entry' 'missing checksum diagnostic missing'
    Assert-TempClean $fixture
  } finally {
    Remove-Item Env:AI_AGENT_TELEMETRY_INSTALL_VERSION -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $fixture.Root -ErrorAction SilentlyContinue
  }
}

function Test-StagedVersionOverridePrecedence {
  $fixture = New-Fixture
  try {
    $staged = Join-Path $fixture.Root 'install.ps1'
    (Get-Content -LiteralPath $Installer -Raw) -replace '(?m)^\$DefaultBinaryVersion = .*',
      '$DefaultBinaryVersion = ''v4.5.6''' | Set-Content -LiteralPath $staged -Encoding UTF8
    $hasStagedDefault = (Get-Content -Raw -LiteralPath $staged).Contains("`$DefaultBinaryVersion = 'v4.5.6'")
    Assert-True $hasStagedDefault 'staged installer did not contain the release default version'

    Remove-Item Env:AI_AGENT_TELEMETRY_INSTALL_VERSION -ErrorAction SilentlyContinue
    $result = Invoke-Fixture $fixture $staged @('install')
    Assert-True ($result.Code -eq 0) "staged default run failed: $($result.Output)"
    $urls = Get-Content -LiteralPath $fixture.DownloadLog
    $hasDefaultURL = $urls -contains 'https://release.example.test/releases/download/v4.5.6/ai-agent-telemetry-windows-amd64.exe'
    Assert-True $hasDefaultURL 'staged default asset URL missing'

    Clear-Content -LiteralPath $fixture.DownloadLog
    $env:AI_AGENT_TELEMETRY_INSTALL_VERSION = 'v7.8.9'
    $result = Invoke-Fixture $fixture $staged @('install')
    Assert-True ($result.Code -eq 0) "staged override run failed: $($result.Output)"
    $urls = Get-Content -LiteralPath $fixture.DownloadLog
    $hasOverrideURL = $urls -contains 'https://release.example.test/releases/download/v7.8.9/ai-agent-telemetry-windows-amd64.exe'
    Assert-True $hasOverrideURL 'staged override asset URL missing'
  } finally {
    Remove-Item Env:AI_AGENT_TELEMETRY_INSTALL_VERSION -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $fixture.Root -ErrorAction SilentlyContinue
  }
}

function Test-NoResponseOrSecretLeakage {
  $fixture = New-Fixture
  try {
    $env:QDI_DOWNLOAD_FAIL = '1'
    $env:AI_AGENT_TELEMETRY_TOKEN = 'transport-secret-value'
    $result = Invoke-Fixture $fixture $Installer @('install')
    Assert-True ($result.Code -ne 0) 'failed download succeeded'
    Assert-NotContains $result.Output $env:QDI_RESPONSE_BODY 'response body leaked'
    Assert-NotContains $result.Output $env:AI_AGENT_TELEMETRY_TOKEN 'secret leaked'
    Assert-TempClean $fixture
  } finally {
    Remove-Item Env:AI_AGENT_TELEMETRY_TOKEN -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $fixture.Root -ErrorAction SilentlyContinue
  }
}

Test-Syntax
Test-DefaultingAndExactArguments
Test-UrlsChecksumExitAndCleanup
Test-StagedVersionOverridePrecedence
Test-NoResponseOrSecretLeakage
Write-Output 'PASS: thin PowerShell bootstrap transport tests'
