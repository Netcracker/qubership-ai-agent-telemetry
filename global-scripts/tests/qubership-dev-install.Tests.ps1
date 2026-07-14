$ErrorActionPreference = 'Stop'

$TestDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$Installer = Join-Path (Split-Path -Parent $TestDir) 'qubership-dev-install.ps1'
$PowerShell = (Get-Process -Id $PID).Path

function Fail([string]$Message) {
  throw "FAIL: $Message"
}

function Assert-Contains([string]$Text, [string]$Expected) {
  if (-not $Text.Contains($Expected)) {
    Fail "expected output to contain '$Expected'. Output: $Text"
  }
}

function Assert-NotContains([string]$Text, [string]$Unexpected) {
  if ($Text.Contains($Unexpected)) {
    Fail "expected output not to contain '$Unexpected'. Output: $Text"
  }
}

function Invoke-Installer([string[]]$Arguments) {
  $output = & $PowerShell -NoProfile -File $Installer @Arguments 2>&1 | Out-String
  return @{ Code = $LASTEXITCODE; Output = $output }
}

function Assert-LogContains([string]$Expected) {
  $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
  Assert-Contains $log $Expected
}

function Setup-ComponentFixture {
  $script:FixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) "qdi-$([guid]::NewGuid())"
  $script:SavedPath = $env:PATH
  $env:HOME = Join-Path $FixtureRoot 'home'
  $env:USERPROFILE = $env:HOME
  $env:LOCALAPPDATA = Join-Path $FixtureRoot 'local-app-data'
  $env:QDI_TEST_LOG = Join-Path $FixtureRoot 'commands.log'
  $env:QDI_GIT_CONFIG = Join-Path $FixtureRoot 'git-hooks-path'
  $env:QDI_APM_STATE = Join-Path $FixtureRoot 'apm-installed'
  $env:QDI_MARKETPLACE_STATE = Join-Path $FixtureRoot 'marketplace-added'
  $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL = Join-Path $FixtureRoot 'telemetry-installer.ps1'
  $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY = 'https://example.test/pre-commit-global.git'
  $env:QUBERSHIP_DEV_GIT_HOOKS_DIR = Join-Path $FixtureRoot 'data/pre-commit-global'
  $bin = Join-Path $FixtureRoot 'bin'
  New-Item -ItemType Directory -Force -Path $env:HOME, $bin | Out-Null
  [System.IO.File]::WriteAllText($env:QDI_TEST_LOG, '')
  Remove-Item Env:CYBER_FERRET_PASSWORD -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_APM_COMMAND -ErrorAction SilentlyContinue

  @'
$line = "apm " + ($args -join ' ')
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value $line
if ($env:QDI_FAIL_APM_COMMAND -eq $args[0]) { exit 9 }
$joined = $args -join ' '
if ($joined -eq 'marketplace list') {
  if (Test-Path $env:QDI_MARKETPLACE_STATE) {
    Write-Output 'qubership-ai-packages Netcracker/qubership-ai-packages'
  }
  exit 0
}
if ($joined -eq 'marketplace add Netcracker/qubership-ai-packages') {
  New-Item -ItemType File -Force -Path $env:QDI_MARKETPLACE_STATE | Out-Null
  exit 0
}
if ($args[0] -eq 'view') {
  if (Test-Path $env:QDI_APM_STATE) { exit 0 }
  exit 1
}
if ($args[0] -eq 'install') {
  New-Item -ItemType File -Force -Path $env:QDI_APM_STATE | Out-Null
}
exit 0
'@ | Set-Content -LiteralPath (Join-Path $bin 'apm.ps1')

  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("java " + ($args -join ' '))
exit 0
'@ | Set-Content -LiteralPath (Join-Path $bin 'java.ps1')

  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("git " + ($args -join ' '))
$joined = $args -join ' '
if ($joined -eq 'config --global --get core.hooksPath') {
  if (Test-Path $env:QDI_GIT_CONFIG) { Get-Content -Raw -LiteralPath $env:QDI_GIT_CONFIG; exit 0 }
  exit 1
}
if ($args.Count -ge 4 -and $args[0] -eq 'config' -and $args[1] -eq '--global' -and $args[2] -eq 'core.hooksPath') {
  [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, [string]$args[3])
  exit 0
}
if ($args[0] -eq 'clone') {
  New-Item -ItemType Directory -Force -Path (Join-Path $args[2] 'hooks-global') | Out-Null
  exit 0
}
exit 0
'@ | Set-Content -LiteralPath (Join-Path $bin 'git.ps1')

  @'
param([string]$Hooks = 'all', [switch]$Force)
$line = "telemetry-installer -Hooks $Hooks"
if ($Force) { $line += ' -Force' }
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value $line
$binDir = Join-Path $env:USERPROFILE '.local/bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
Copy-Item -Force -LiteralPath $env:QDI_TELEMETRY_CLI -Destination (Join-Path $binDir 'ai-agent-telemetry.ps1')
'@ | Set-Content -LiteralPath $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL

  $env:QDI_TELEMETRY_CLI = Join-Path $FixtureRoot 'ai-agent-telemetry.ps1'
  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("ai-agent-telemetry " + ($args -join ' '))
exit 0
'@ | Set-Content -LiteralPath $env:QDI_TELEMETRY_CLI

  $env:PATH = "$bin$([System.IO.Path]::PathSeparator)$SavedPath"
}

function Teardown-ComponentFixture {
  $env:PATH = $SavedPath
  Remove-Item -Recurse -Force $FixtureRoot
  foreach ($name in @(
    'HOME', 'USERPROFILE', 'LOCALAPPDATA', 'QDI_TEST_LOG', 'QDI_GIT_CONFIG', 'QDI_APM_STATE',
    'QDI_MARKETPLACE_STATE', 'QDI_TELEMETRY_CLI', 'QDI_FAIL_APM_COMMAND',
    'QUBERSHIP_DEV_TELEMETRY_INSTALL_URL', 'QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY',
    'QUBERSHIP_DEV_GIT_HOOKS_DIR', 'CYBER_FERRET_PASSWORD'
  )) {
    Remove-Item "Env:$name" -ErrorAction SilentlyContinue
  }
}

function Test-HelpDescribesPublicOptions {
  $result = Invoke-Installer @('-Help')
  if ($result.Code -ne 0) { Fail "-Help returned $($result.Code): $($result.Output)" }
  foreach ($option in '-Components', '-Skip', '-Harnesses', '-ForceGitHooks', '-ForceUpdate', '-NonInteractive') {
    Assert-Contains $result.Output $option
  }
}

function Test-InvalidSelectionsFailBeforeInstallation {
  $cases = @(
    @{ Arguments = @('-Components', 'unknown'); Message = 'unknown component "unknown"' }
    @{ Arguments = @('-Harnesses', 'unknown'); Message = 'unknown harness "unknown"' }
    @{ Arguments = @('-Skip', 'all'); Message = 'no components selected' }
    @{ Arguments = @('-Components', 'apm,,telemetry'); Message = 'component list contains an empty value' }
  )
  foreach ($case in $cases) {
    $result = Invoke-Installer $case.Arguments
    if ($result.Code -ne 2) { Fail "expected exit 2, got $($result.Code): $($result.Output)" }
    Assert-Contains $result.Output $case.Message
  }
}

function Test-PrerequisitesApplyOnlyToGitHooks {
  $savedPath = $env:PATH
  $emptyPath = Join-Path ([System.IO.Path]::GetTempPath()) "qdi-empty-$([guid]::NewGuid())"
  New-Item -ItemType Directory -Path $emptyPath | Out-Null
  try {
    $env:PATH = $emptyPath
    $gitHooks = Invoke-Installer @('-Components', 'git-hooks', '-NonInteractive')
    if ($gitHooks.Code -ne 1) { Fail "expected prerequisite exit 1, got $($gitHooks.Code): $($gitHooks.Output)" }
    Assert-Contains $gitHooks.Output 'Git is required'
    Assert-Contains $gitHooks.Output 'Java is required'

    $telemetry = Invoke-Installer @('-Components', 'telemetry', '-NonInteractive')
    Assert-NotContains $telemetry.Output 'Git is required'
    Assert-NotContains $telemetry.Output 'Java is required'
  } finally {
    $env:PATH = $savedPath
    Remove-Item -Recurse -Force $emptyPath
  }
}

function Test-DefaultInstallRunsEveryComponent {
  Setup-ComponentFixture
  try {
    $result = Invoke-Installer @('-NonInteractive')
    if ($result.Code -ne 0) { Fail "default install failed: $($result.Output)" }
    Assert-LogContains 'apm install qubership-global-essentials@qubership-ai-packages -g --target claude,codex,cursor'
    Assert-LogContains 'apm compile -g'
    Assert-LogContains 'telemetry-installer -Hooks claude,codex,cursor'
    Assert-LogContains 'ai-agent-telemetry status'
    Assert-LogContains 'ai-agent-telemetry selftest'
    Assert-LogContains "git clone https://example.test/pre-commit-global.git $env:QUBERSHIP_DEV_GIT_HOOKS_DIR"
    Assert-Contains $result.Output 'apm              OK'
    Assert-Contains $result.Output 'telemetry        OK'
    Assert-Contains $result.Output 'git-hooks        OK'
  } finally { Teardown-ComponentFixture }
}

function Test-SelectionAndHarnessesAreForwarded {
  Setup-ComponentFixture
  try {
    $result = Invoke-Installer @(
      '-Components', 'apm,telemetry', '-Skip', 'apm', '-Harnesses', 'codex', '-NonInteractive'
    )
    if ($result.Code -ne 0) { Fail "selected install failed: $($result.Output)" }
    Assert-LogContains 'telemetry-installer -Hooks codex'
    $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
    Assert-NotContains $log 'apm '
    Assert-NotContains $log 'git '
  } finally { Teardown-ComponentFixture }
}

function Test-ForceUpdateRefreshesSelectedComponents {
  Setup-ComponentFixture
  try {
    New-Item -ItemType File -Path $env:QDI_APM_STATE, $env:QDI_MARKETPLACE_STATE | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
    $result = Invoke-Installer @(
      '-ForceUpdate', '-ForceGitHooks', '-Harnesses', 'claude', '-NonInteractive'
    )
    if ($result.Code -ne 0) { Fail "force update failed: $($result.Output)" }
    Assert-LogContains 'apm self-update'
    Assert-LogContains 'apm marketplace update qubership-ai-packages'
    Assert-LogContains 'apm update qubership-global-essentials -g --yes --target claude'
    Assert-LogContains 'telemetry-installer -Hooks claude -Force'
    Assert-LogContains "git -C $env:QUBERSHIP_DEV_GIT_HOOKS_DIR pull --ff-only"
  } finally { Teardown-ComponentFixture }
}

function Test-UnrelatedGitHooksAreSkipped {
  Setup-ComponentFixture
  try {
    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, '/other/hooks')
    $result = Invoke-Installer @('-Components', 'git-hooks', '-NonInteractive')
    if ($result.Code -ne 0) { Fail "Git hook skip failed: $($result.Output)" }
    Assert-Contains $result.Output 'git-hooks        SKIPPED'
    Assert-Contains $result.Output 'core.hooksPath is already set to /other/hooks'
    if ((Get-Content -Raw $env:QDI_GIT_CONFIG) -ne '/other/hooks') { Fail 'overwrote existing Git hooks' }
  } finally { Teardown-ComponentFixture }
}

function Test-ComponentFailureDoesNotStopIndependentComponents {
  Setup-ComponentFixture
  try {
    $env:QDI_FAIL_APM_COMMAND = 'compile'
    $result = Invoke-Installer @('-Components', 'apm,telemetry', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected exit 1, got $($result.Code): $($result.Output)" }
    Assert-Contains $result.Output 'apm              FAILED'
    Assert-Contains $result.Output 'telemetry        OK'
    Assert-LogContains 'telemetry-installer -Hooks claude,codex,cursor'
  } finally { Teardown-ComponentFixture }
}

Test-HelpDescribesPublicOptions
Test-InvalidSelectionsFailBeforeInstallation
Test-PrerequisitesApplyOnlyToGitHooks
Test-DefaultInstallRunsEveryComponent
Test-SelectionAndHarnessesAreForwarded
Test-ForceUpdateRefreshesSelectedComponents
Test-UnrelatedGitHooksAreSkipped
Test-ComponentFailureDoesNotStopIndependentComponents
Write-Host 'PASS: PowerShell developer installer tests'
