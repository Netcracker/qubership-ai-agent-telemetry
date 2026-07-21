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
  $savedErrorActionPreference = $ErrorActionPreference
  try {
    # Windows PowerShell 5.1 promotes native stderr to a terminating error when this is Stop.
    $ErrorActionPreference = 'Continue'
    $output = & $PowerShell -NoProfile -File $Installer @Arguments 2>&1 | Out-String -Width 4096
    $code = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $savedErrorActionPreference
  }
  return @{ Code = $code; Output = $output }
}

function Assert-LogContains([string]$Expected) {
  $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
  Assert-Contains $log $Expected
}

function Assert-LogNotContains([string]$Unexpected) {
  $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
  Assert-NotContains $log $Unexpected
}

function Setup-ComponentFixture {
  $script:FixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) "qdi-$([guid]::NewGuid())"
  $script:SavedPath = $env:PATH
  $env:HOME = Join-Path $FixtureRoot 'home'
  $env:USERPROFILE = $env:HOME
  $env:LOCALAPPDATA = Join-Path $FixtureRoot 'local-app-data'
  $env:XDG_STATE_HOME = Join-Path $FixtureRoot 'state'
  $env:XDG_CACHE_HOME = Join-Path $FixtureRoot 'cache'
  $env:QDI_TEST_LOG = Join-Path $FixtureRoot 'commands.log'
  $env:QDI_GIT_CONFIG = Join-Path $FixtureRoot 'git-hooks-path'
  $env:QDI_MARKETPLACE_STATE = Join-Path $FixtureRoot 'marketplace-added'
  $env:QDI_GIT_ORIGIN_FILE = Join-Path $FixtureRoot 'git-origin'
  $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL = Join-Path $FixtureRoot 'telemetry-installer.ps1'
  $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY = 'https://example.test/pre-commit-global.git'
  $env:QUBERSHIP_DEV_GIT_HOOKS_DIR = Join-Path $FixtureRoot 'data/pre-commit-global'
  $env:QDI_TELEMETRY_RECEIPT = Join-Path $env:XDG_STATE_HOME 'ai-agent-telemetry/hooks-uninstalled'
  $env:QDI_TELEMETRY_CONFIG_DIR = Join-Path $env:HOME '.config/ai-agent-telemetry'
  $env:QDI_TELEMETRY_CACHE_DIR = Join-Path $env:XDG_CACHE_HOME 'ai-agent-telemetry'
  $env:QDI_TELEMETRY_HOOK = Join-Path $env:HOME '.codex/hooks.json'
  $env:QDI_MANAGED_TELEMETRY_BIN = Join-Path $env:HOME '.local/bin/ai-agent-telemetry.exe'
  $bin = Join-Path $FixtureRoot 'bin'
  $configDir = Join-Path $env:HOME '.config/ai-agent-telemetry'
  New-Item -ItemType Directory -Force -Path $env:HOME, $bin, $configDir | Out-Null
  Set-Content -LiteralPath (Join-Path $configDir 'env') -Value 'AI_AGENT_TELEMETRY_ENDPOINT=https://telemetry.example.test'
  [System.IO.File]::WriteAllText($env:QDI_TEST_LOG, '')
  Remove-Item Env:CYBER_FERRET_PASSWORD -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_APM_COMMAND -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_TEST_JAVA_EXIT_CODE -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_TEST_JAVA_SPEC_VERSION -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_TELEMETRY_HOOKS -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_GIT_ORIGIN -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_GIT_STATUS -ErrorAction SilentlyContinue
  Remove-Item Env:QDI_FAIL_GIT_CONFIG_READ -ErrorAction SilentlyContinue

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
  exit 1
}
if ($args[0] -eq 'install') {
  Write-Output 'fake APM install output'
}
if ($args[0] -eq 'compile') {
  Write-Output 'fake APM compile output'
}
exit 0
'@ | Set-Content -LiteralPath (Join-Path $bin 'apm.ps1')

  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("java " + ($args -join ' '))
if ($env:QDI_TEST_JAVA_EXIT_CODE) { exit [int]$env:QDI_TEST_JAVA_EXIT_CODE }
$version = if ($env:QDI_TEST_JAVA_SPEC_VERSION) { $env:QDI_TEST_JAVA_SPEC_VERSION } else { '21' }
Write-Error "    java.specification.version = $version" -ErrorAction Continue
'@ | Set-Content -LiteralPath (Join-Path $bin 'java.ps1')

  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("git " + ($args -join ' '))
$joined = $args -join ' '
if ($joined -eq 'config --global --get core.hooksPath' -or
    $joined -eq 'config --global --get-all core.hooksPath') {
  if ($env:QDI_FAIL_GIT_CONFIG_READ) { exit 8 }
  if (Test-Path $env:QDI_GIT_CONFIG) {
    if ($joined -eq 'config --global --get-all core.hooksPath') {
      Get-Content -LiteralPath $env:QDI_GIT_CONFIG
    } else {
      Get-Content -Raw -LiteralPath $env:QDI_GIT_CONFIG
    }
    exit 0
  }
  exit 1
}
if ($args.Count -ge 4 -and $args[0] -eq 'config' -and $args[1] -eq '--global' -and
    $args[2] -eq '--unset-all' -and $args[3] -eq 'core.hooksPath') {
  if ($args.Count -eq 4) {
    Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $env:QDI_GIT_CONFIG
    exit 0
  }
  $valuePattern = $args[4]
  $configured = @(Get-Content -LiteralPath $env:QDI_GIT_CONFIG)
  $matches = @($configured | Where-Object { $_ -match $valuePattern })
  if ($matches.Count -eq 0) { exit 5 }
  $remaining = @($configured | Where-Object { $_ -notmatch $valuePattern })
  if ($remaining.Count -eq 0) {
    Remove-Item -Force -LiteralPath $env:QDI_GIT_CONFIG
  } else {
    [System.IO.File]::WriteAllLines($env:QDI_GIT_CONFIG, [string[]]$remaining)
  }
  exit 0
}
if ($args.Count -ge 4 -and $args[0] -eq 'config' -and $args[1] -eq '--global' -and $args[2] -eq 'core.hooksPath') {
  [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, [string]$args[3])
  exit 0
}
if ($args[0] -eq 'clone') {
  New-Item -ItemType Directory -Force -Path (Join-Path $args[2] '.git'), (Join-Path $args[2] 'hooks-global') | Out-Null
  Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $args[1]
  exit 0
}
if ($args[0] -eq '-C' -and $args[2] -eq 'rev-parse') {
  if (-not (Test-Path (Join-Path $args[1] '.git'))) { exit 1 }
  Write-Output 'true'
  exit 0
}
if ($args[0] -eq '-C' -and $args[2] -eq 'remote' -and $args[3] -eq 'get-url') {
  if ($env:QDI_FAIL_GIT_ORIGIN) { exit 8 }
  if (-not (Test-Path $env:QDI_GIT_ORIGIN_FILE)) { exit 1 }
  Get-Content -Raw -LiteralPath $env:QDI_GIT_ORIGIN_FILE
  exit 0
}
if ($args[0] -eq '-C' -and $args[2] -eq 'status') {
  if ($env:QDI_FAIL_GIT_STATUS) { exit 8 }
  if ($env:QDI_GIT_STATUS) { Write-Output $env:QDI_GIT_STATUS }
  exit 0
}
if ($args[0] -eq '-C' -and $args[2] -eq 'pull') {
  if ($env:QDI_GIT_PULL_FAIL) { exit 1 }
  exit 0
}
exit 0
'@ | Set-Content -LiteralPath (Join-Path $bin 'git.ps1')

  @'
param([switch]$Force, [switch]$SkipConfig)
$line = 'telemetry-installer'
if ($SkipConfig) { $line += ' -SkipConfig' }
if ($Force) { $line += ' -Force' }
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value $line
Write-Output 'fake telemetry installer output'
$binDir = Join-Path $env:USERPROFILE '.local/bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
Copy-Item -Force -LiteralPath $env:QDI_TELEMETRY_CLI -Destination (Join-Path $binDir 'ai-agent-telemetry.ps1')
'@ | Set-Content -LiteralPath $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL

  $env:QDI_TELEMETRY_CLI = Join-Path $FixtureRoot 'ai-agent-telemetry.ps1'
  @'
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("ai-agent-telemetry " + ($args -join ' '))
if ($env:QDI_FAIL_TELEMETRY_HOOKS -and ($args -join ' ') -eq 'hooks uninstall') { exit 9 }
exit 0
'@ | Set-Content -LiteralPath $env:QDI_TELEMETRY_CLI

  $env:PATH = $bin
}

function Teardown-ComponentFixture {
  $env:PATH = $SavedPath
  Remove-Item -Recurse -Force $FixtureRoot
  foreach ($name in @(
    'HOME', 'USERPROFILE', 'LOCALAPPDATA', 'XDG_STATE_HOME', 'XDG_CACHE_HOME',
    'QDI_TEST_LOG', 'QDI_GIT_CONFIG',
    'QDI_MARKETPLACE_STATE', 'QDI_TELEMETRY_CLI', 'QDI_FAIL_APM_COMMAND', 'QDI_GIT_ORIGIN_FILE',
    'QDI_GIT_STATUS', 'QDI_GIT_PULL_FAIL', 'QDI_FAIL_GIT_ORIGIN', 'QDI_FAIL_GIT_STATUS',
    'QDI_FAIL_GIT_CONFIG_READ',
    'QDI_TEST_JAVA_EXIT_CODE', 'QDI_TEST_JAVA_SPEC_VERSION', 'QDI_FAIL_TELEMETRY_HOOKS',
    'QDI_TELEMETRY_RECEIPT', 'QDI_TELEMETRY_CONFIG_DIR', 'QDI_TELEMETRY_CACHE_DIR',
    'QDI_TELEMETRY_HOOK', 'QDI_MANAGED_TELEMETRY_BIN',
    'QUBERSHIP_DEV_TELEMETRY_INSTALL_URL', 'QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY',
    'QUBERSHIP_DEV_GIT_HOOKS_DIR', 'CYBER_FERRET_PASSWORD'
  )) {
    Remove-Item "Env:$name" -ErrorAction SilentlyContinue
  }
}

function New-ManagedTelemetryExecutable {
  $path = $env:QDI_MANAGED_TELEMETRY_BIN
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
  if ($env:OS -eq 'Windows_NT') {
    $className = "ManagedTelemetry$([guid]::NewGuid().ToString('N'))"
    $source = @"
using System;
using System.IO;
public static class $className {
  public static int Main(string[] args) {
    File.AppendAllText(
      Environment.GetEnvironmentVariable("QDI_TEST_LOG"),
      "managed-ai-agent-telemetry " + String.Join(" ", args) + Environment.NewLine);
    return Environment.GetEnvironmentVariable("QDI_FAIL_TELEMETRY_HOOKS") == "1" ? 9 : 0;
  }
}
"@
    if ($PSVersionTable.PSEdition -eq 'Core') {
      # PowerShell 7 cannot emit console applications with Add-Type. Use the
      # Windows PowerShell compiler for this Windows-only test fixture.
      $windowsPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
      $escapedSource = $source.Replace("'", "''")
      $escapedPath = $path.Replace("'", "''")
      $compilerScript = "Add-Type -TypeDefinition '$escapedSource' -Language CSharp " +
        "-OutputAssembly '$escapedPath' -OutputType ConsoleApplication"
      $encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($compilerScript))
      & $windowsPowerShell -NoProfile -NonInteractive -EncodedCommand $encodedCommand | Out-Null
      if ($LASTEXITCODE -ne 0) {
        throw "Windows PowerShell failed to compile the managed telemetry fixture"
      }
    } else {
      Add-Type -TypeDefinition $source -Language CSharp -OutputAssembly $path -OutputType ConsoleApplication
    }
  } else {
    [System.IO.File]::WriteAllText($path, @'
#!/usr/bin/env pwsh
Add-Content -LiteralPath $env:QDI_TEST_LOG -Value ("managed-ai-agent-telemetry " + ($args -join ' '))
if ($env:QDI_FAIL_TELEMETRY_HOOKS -eq '1') { exit 9 }
exit 0
'@)
    $mode = [System.IO.File]::GetUnixFileMode($path)
    $executeMode = [System.IO.UnixFileMode]::UserExecute -bor `
      [System.IO.UnixFileMode]::GroupExecute -bor [System.IO.UnixFileMode]::OtherExecute
    [System.IO.File]::SetUnixFileMode($path, $mode -bor $executeMode)
  }
  return $path
}

function New-DirectoryReparseFixture([string]$Path, [string]$Target) {
  New-Item -ItemType Directory -Force -Path $Target | Out-Null
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path) | Out-Null
  try {
    New-Item -ItemType SymbolicLink -Path $Path -Target $Target -ErrorAction Stop | Out-Null
    return $true
  } catch {
    if ($env:OS -ne 'Windows_NT') { return $false }
  }
  try {
    New-Item -ItemType Junction -Path $Path -Target $Target -ErrorAction Stop | Out-Null
    return $true
  } catch {
    return $false
  }
}

function Test-NativeFixtureEntry([string]$Path) {
  return $null -ne (Get-Item -Force -LiteralPath $Path -ErrorAction SilentlyContinue)
}

function Test-HelpDescribesPublicOptions {
  $result = Invoke-Installer @('-Help')
  if ($result.Code -ne 0) { Fail "-Help returned $($result.Code): $($result.Output)" }
  foreach ($option in @(
    '-Components', '-Skip', '-Harnesses', '-ForceGitHooks', '-ForceUpdate', '-NonInteractive',
    '-Uninstall', '-Purge'
  )) {
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

function Test-UninstallOptionCombinationsFailBeforeChanges {
  $cases = @(
    @{ Arguments = @('-Purge'); Message = '-Purge requires -Uninstall' }
    @{ Arguments = @('-Uninstall', '-Harnesses', 'claude'); Message = '-Harnesses is not valid with -Uninstall' }
    @{ Arguments = @('-Uninstall', '-ForceUpdate'); Message = '-ForceUpdate is not valid with -Uninstall' }
    @{ Arguments = @('-Uninstall', '-ForceGitHooks'); Message = '-ForceGitHooks is not valid with -Uninstall' }
    @{ Arguments = @('-Uninstall', '-NonInteractive'); Message = '-NonInteractive is not valid with -Uninstall' }
  )
  foreach ($case in $cases) {
    Setup-ComponentFixture
    try {
      $result = Invoke-Installer $case.Arguments
      if ($result.Code -ne 2) { Fail "expected exit 2, got $($result.Code): $($result.Output)" }
      Assert-Contains $result.Output $case.Message
      if ((Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG).Length -ne 0) {
        Fail 'invalid uninstall options caused component side effects'
      }
    } finally { Teardown-ComponentFixture }
  }
}

function Test-UnknownParameterFailsBeforeInstallation {
  Setup-ComponentFixture
  try {
    $result = Invoke-Installer @('-Componentz', 'telemetry', '-NonInteractive')
    if ($result.Code -ne 2) { Fail "expected exit 2, got $($result.Code): $($result.Output)" }
    Assert-Contains $result.Output 'unknown option "-Componentz"'
    if ((Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG).Length -ne 0) {
      Fail 'unknown parameter caused component side effects'
    }
  } finally { Teardown-ComponentFixture }
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
    Assert-Contains $gitHooks.Output 'Java 21 or newer is required'

    $telemetry = Invoke-Installer @('-Components', 'telemetry', '-NonInteractive')
    Assert-NotContains $telemetry.Output 'Git is required'
    Assert-NotContains $telemetry.Output 'Java is required'
  } finally {
    $env:PATH = $savedPath
    Remove-Item -Recurse -Force $emptyPath
  }
}

function Test-Java20IsRejected {
  Setup-ComponentFixture
  try {
    $env:QDI_TEST_JAVA_SPEC_VERSION = '20'
    $result = Invoke-Installer @('-Components', 'git-hooks', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected Java 20 rejection: $($result.Output)" }
    Assert-Contains $result.Output 'Detected Java 20'
    Assert-Contains $result.Output 'Java 21 or newer is required'
    Assert-LogNotContains 'git clone'
  } finally { Teardown-ComponentFixture }
}

function Test-Java21AndNewerAreAccepted {
  foreach ($version in @('21', '26')) {
    Setup-ComponentFixture
    try {
      $env:QDI_TEST_JAVA_SPEC_VERSION = $version
      $result = Invoke-Installer @('-Components', 'git-hooks', '-NonInteractive')
      if ($result.Code -ne 0) { Fail "expected Java $version acceptance: $($result.Output)" }
    } finally { Teardown-ComponentFixture }
  }
}

function Test-UnrecognizedOrFailingJavaIsRejected {
  foreach ($case in @(
    @{ Version = 'unknown'; ExitCode = $null },
    @{ Version = '21'; ExitCode = '1' }
  )) {
    Setup-ComponentFixture
    try {
      $env:QDI_TEST_JAVA_SPEC_VERSION = $case.Version
      if ($case.ExitCode) { $env:QDI_TEST_JAVA_EXIT_CODE = $case.ExitCode }
      $result = Invoke-Installer @('-Components', 'git-hooks', '-NonInteractive')
      if ($result.Code -ne 1) { Fail "expected Java detection failure: $($result.Output)" }
      Assert-Contains $result.Output 'Could not determine the Java version'
    } finally { Teardown-ComponentFixture }
  }
}

function Test-DefaultInstallRunsEveryComponent {
  Setup-ComponentFixture
  try {
    $result = Invoke-Installer @('-NonInteractive')
    if ($result.Code -ne 0) { Fail "default install failed: $($result.Output)" }
    Assert-LogContains 'apm self-update'
    Assert-LogContains 'apm install qubership-global-essentials@qubership-ai-packages -g --target claude,codex,cursor'
    Assert-LogContains 'apm compile -g'
    Assert-LogContains 'apm deps list -g'
    Assert-LogContains 'telemetry-installer -SkipConfig'
    Assert-LogNotContains 'telemetry-installer -SkipConfig -Force'
    Assert-LogContains 'ai-agent-telemetry hooks install --target=claude,codex,cursor'
    Assert-LogContains 'ai-agent-telemetry status'
    Assert-LogContains 'ai-agent-telemetry selftest'
    Assert-LogContains "git clone https://example.test/pre-commit-global.git $env:QUBERSHIP_DEV_GIT_HOOKS_DIR"
    Assert-Contains $result.Output 'apm              OK'
    Assert-Contains $result.Output 'telemetry        OK'
    Assert-Contains $result.Output 'git-hooks        OK'
    Assert-Contains $result.Output "SetEnvironmentVariable('CYBER_FERRET_PASSWORD', '<password>', 'User')"
    Assert-Contains $result.Output 'restart your terminal and IDE'
    Assert-Contains $result.Output 'global-scripts/README.md#cyberferret-password'
  } finally { Teardown-ComponentFixture }
}

function Test-ExistingClisAreUpdatedByDefault {
  Setup-ComponentFixture
  try {
    $bin = Split-Path -Parent (Get-Command apm).Source
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination (Join-Path $bin 'ai-agent-telemetry.ps1')
    $result = Invoke-Installer @('-Components', 'apm,telemetry', '-NonInteractive')
    if ($result.Code -ne 0) { Fail "existing CLI update failed: $($result.Output)" }
    Assert-LogContains 'apm self-update'
    Assert-LogContains 'telemetry-installer -SkipConfig -Force'
  } finally { Teardown-ComponentFixture }
}

function Test-SelectionAndHarnessesAreForwarded {
  Setup-ComponentFixture
  try {
    $result = Invoke-Installer @(
      '-Components', 'apm,telemetry', '-Skip', 'apm', '-Harnesses', 'codex', '-NonInteractive'
    )
    if ($result.Code -ne 0) { Fail "selected install failed: $($result.Output)" }
    Assert-LogContains 'telemetry-installer -SkipConfig'
    Assert-LogContains 'ai-agent-telemetry hooks install --target=codex'
    $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
    Assert-NotContains $log 'apm '
    Assert-NotContains $log 'git '
  } finally { Teardown-ComponentFixture }
}

function Test-ForceUpdateRefreshesSelectedComponents {
  Setup-ComponentFixture
  try {
    New-Item -ItemType File -Path $env:QDI_MARKETPLACE_STATE | Out-Null
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR '.git'), `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
    $result = Invoke-Installer @(
      '-ForceUpdate', '-ForceGitHooks', '-Harnesses', 'claude', '-NonInteractive'
    )
    if ($result.Code -ne 0) { Fail "force update failed: $($result.Output)" }
    Assert-LogContains 'apm self-update'
    Assert-LogContains 'apm marketplace update qubership-ai-packages'
    Assert-LogContains `
      'apm install --update qubership-global-essentials@qubership-ai-packages -g --target claude'
    Assert-LogContains 'telemetry-installer -SkipConfig -Force'
    Assert-LogContains 'ai-agent-telemetry hooks install --target=claude'
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
    $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
    Assert-NotContains $log 'git clone'
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
    Assert-LogContains 'telemetry-installer -SkipConfig'
    Assert-LogContains 'ai-agent-telemetry hooks install --target=claude,codex,cursor'
  } finally { Teardown-ComponentFixture }
}

function Test-UnconfiguredTelemetryBehavior {
  Setup-ComponentFixture
  try {
    Remove-Item -LiteralPath (Join-Path $env:HOME '.config/ai-agent-telemetry/env')
    $result = Invoke-Installer @('-Components', 'telemetry', '-Harnesses', 'cursor', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected non-interactive configuration failure: $($result.Output)" }
    Assert-Contains $result.Output 'telemetry configuration is required'
    Assert-NotContains (Get-Content -Raw $env:QDI_TEST_LOG) 'ai-agent-telemetry configure'

    $result = Invoke-Installer @('-Components', 'telemetry', '-Harnesses', 'cursor')
    if ($result.Code -ne 0) { Fail "interactive configuration failed: $($result.Output)" }
    Assert-LogContains 'ai-agent-telemetry configure --hooks=cursor'
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksRejectUnsafeExistingDirectories {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR '.git'), `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value 'https://example.test/unrelated.git'
    $result = Invoke-Installer @('-Components', 'git-hooks', '-ForceGitHooks', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected wrong-origin failure: $($result.Output)" }
    Assert-Contains $result.Output 'unexpected origin'
    Assert-NotContains (Get-Content -Raw $env:QDI_TEST_LOG) 'git config --global core.hooksPath'

    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
    $env:QDI_GIT_STATUS = ' M hooks-global/pre-commit'
    $result = Invoke-Installer @('-Components', 'git-hooks', '-ForceGitHooks', '-ForceUpdate', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected dirty-clone failure: $($result.Output)" }
    Assert-Contains $result.Output 'local changes'
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksRejectNonRepositoryAndDivergence {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
    $result = Invoke-Installer @('-Components', 'git-hooks', '-ForceGitHooks', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected non-repository failure: $($result.Output)" }
    Assert-Contains $result.Output 'not the managed Git repository'

    New-Item -ItemType Directory -Force -Path (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR '.git') | Out-Null
    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
    $env:QDI_GIT_PULL_FAIL = '1'
    $result = Invoke-Installer @('-Components', 'git-hooks', '-ForceGitHooks', '-ForceUpdate', '-NonInteractive')
    if ($result.Code -ne 1) { Fail "expected divergent update failure: $($result.Output)" }
    Assert-Contains $result.Output 'git-hooks        FAILED'
  } finally { Teardown-ComponentFixture }
}

function Test-ApmUninstallBehavior {
  Setup-ComponentFixture
  try {
    $missing = Invoke-Installer @('-Uninstall', '-Components', 'apm')
    if ($missing.Code -ne 0) { Fail "missing-manifest uninstall failed: $($missing.Output)" }
    Assert-Contains $missing.Output 'apm              SKIPPED'
    Assert-LogNotContains 'apm uninstall'

    New-Item -ItemType Directory -Force -Path (Join-Path $env:HOME '.apm') | Out-Null
    New-Item -ItemType File -Force -Path (Join-Path $env:HOME '.apm/apm.yml') | Out-Null
    New-Item -ItemType File -Force -Path $env:QDI_MARKETPLACE_STATE | Out-Null
    $present = Invoke-Installer @('-Uninstall', '-Components', 'apm')
    if ($present.Code -ne 0) { Fail "APM uninstall failed: $($present.Output)" }
    Assert-LogContains 'apm uninstall -g qubership-global-essentials@qubership-ai-packages'
    if (-not (Test-Path -LiteralPath (Get-Command apm).Source)) { Fail 'removed APM CLI' }
    if (-not (Test-Path -LiteralPath $env:QDI_MARKETPLACE_STATE)) { Fail 'removed marketplace marker' }
  } finally { Teardown-ComponentFixture }
}

function Test-ApmUninstallFailureDoesNotStopTelemetry {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path (Join-Path $env:HOME '.apm') | Out-Null
    New-Item -ItemType File -Force -Path (Join-Path $env:HOME '.apm/apm.yml') | Out-Null
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination `
      (Join-Path (Split-Path -Parent (Get-Command apm).Source) 'ai-agent-telemetry.ps1')
    $env:QDI_FAIL_APM_COMMAND = 'uninstall'
    $result = Invoke-Installer @('-Uninstall', '-Components', 'apm,telemetry')
    if ($result.Code -ne 1) { Fail "expected aggregated uninstall failure: $($result.Output)" }
    Assert-Contains $result.Output 'apm              FAILED'
    Assert-Contains $result.Output 'telemetry        OK'
    Assert-LogContains 'ai-agent-telemetry hooks uninstall'
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryUninstallLifecycle {
  Setup-ComponentFixture
  try {
    $externalCommand = Join-Path (Split-Path -Parent (Get-Command apm).Source) 'ai-agent-telemetry.ps1'
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination $externalCommand
    New-Item -ItemType Directory -Force -Path `
      (Split-Path -Parent $env:QDI_MANAGED_TELEMETRY_BIN), $env:QDI_TELEMETRY_CACHE_DIR | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_MANAGED_TELEMETRY_BIN, 'dummy managed executable')
    New-Item -ItemType File -Force -Path (Join-Path $env:QDI_TELEMETRY_CACHE_DIR 'cache.db') | Out-Null

    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 0) { Fail "telemetry uninstall failed: $($result.Output)" }
    Assert-LogContains 'ai-agent-telemetry hooks uninstall'
    if (Test-Path -LiteralPath $env:QDI_MANAGED_TELEMETRY_BIN) { Fail 'managed telemetry executable remains' }
    if (-not (Test-Path -LiteralPath $externalCommand)) { Fail 'removed external telemetry command' }
    if (-not (Test-Path -LiteralPath $env:QDI_TELEMETRY_CONFIG_DIR)) { Fail 'normal uninstall removed config' }
    if (-not (Test-Path -LiteralPath $env:QDI_TELEMETRY_CACHE_DIR)) { Fail 'normal uninstall removed cache' }
    Assert-Contains $result.Output 'Uninstall summary'
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryPrefersInvokableManagedExecutable {
  Setup-ComponentFixture
  try {
    $externalCommand = Join-Path (Split-Path -Parent (Get-Command apm).Source) 'ai-agent-telemetry.ps1'
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination $externalCommand
    New-ManagedTelemetryExecutable | Out-Null
    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 0) { Fail "managed telemetry uninstall failed: $($result.Output)" }
    $logLines = @(Get-Content -LiteralPath $env:QDI_TEST_LOG)
    if ($logLines -notcontains 'managed-ai-agent-telemetry hooks uninstall') {
      Fail 'managed telemetry executable was not invoked first'
    }
    if ($logLines -contains 'ai-agent-telemetry hooks uninstall') {
      Fail 'external telemetry command was invoked while the managed executable was available'
    }
    if (-not (Test-Path -LiteralPath $externalCommand -PathType Leaf)) {
      Fail 'removed external telemetry command'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryUsesAndPreservesExternalCommandWhenManagedIsAbsent {
  Setup-ComponentFixture
  try {
    $externalCommand = Join-Path (Split-Path -Parent (Get-Command apm).Source) 'ai-agent-telemetry.ps1'
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination $externalCommand
    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 0) { Fail "external telemetry uninstall failed: $($result.Output)" }
    Assert-LogContains 'ai-agent-telemetry hooks uninstall'
    if (-not (Test-Path -LiteralPath $externalCommand -PathType Leaf)) {
      Fail 'removed external telemetry command'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryHookFailurePreservesManagedExecutable {
  Setup-ComponentFixture
  try {
    $externalCommand = Join-Path (Split-Path -Parent (Get-Command apm).Source) 'ai-agent-telemetry.ps1'
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination $externalCommand
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:QDI_MANAGED_TELEMETRY_BIN) | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_MANAGED_TELEMETRY_BIN, 'dummy managed executable')
    $env:QDI_FAIL_TELEMETRY_HOOKS = '1'
    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 1) { Fail "expected telemetry hook failure: $($result.Output)" }
    if (-not (Test-Path -LiteralPath $env:QDI_MANAGED_TELEMETRY_BIN)) {
      Fail 'removed managed executable after hook failure'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryReceiptAndOwnershipBehavior {
  Setup-ComponentFixture
  try {
    $first = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($first.Code -ne 0) { Fail "receipt-only telemetry uninstall failed: $($first.Output)" }
    $receipt = [System.IO.File]::ReadAllText($env:QDI_TELEMETRY_RECEIPT)
    if ($receipt -ne "version=1`nstate=uninstalled`n") { Fail 'telemetry receipt has unexpected content' }

    $repeat = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($repeat.Code -ne 0) { Fail "repeat telemetry uninstall failed: $($repeat.Output)" }

    Remove-Item -Force -LiteralPath $env:QDI_TELEMETRY_RECEIPT
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:QDI_TELEMETRY_HOOK) | Out-Null
    New-Item -ItemType File -Force -Path $env:QDI_TELEMETRY_HOOK | Out-Null
    $unsafe = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($unsafe.Code -ne 1) { Fail "expected unsafe telemetry uninstall failure: $($unsafe.Output)" }
    Assert-Contains $unsafe.Output 'native hook files exist'
    if (-not (Test-Path -LiteralPath $env:QDI_TELEMETRY_HOOK)) { Fail 'removed hook without ownership proof' }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryDanglingHookIsExistingState {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:QDI_TELEMETRY_HOOK) | Out-Null
    try {
      New-Item -ItemType SymbolicLink -Path $env:QDI_TELEMETRY_HOOK `
        -Target (Join-Path $FixtureRoot 'missing-hook-target') -ErrorAction Stop | Out-Null
    } catch {
      return
    }
    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 1) { Fail "expected dangling-hook telemetry failure: $($result.Output)" }
    Assert-Contains $result.Output 'native hook files exist'
    if (Test-Path -LiteralPath $env:QDI_TELEMETRY_RECEIPT) { Fail 'wrote receipt with dangling native hook' }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryHookInspectionFailureDoesNotWriteReceipt {
  Setup-ComponentFixture
  try {
    $blockedParent = Join-Path $env:HOME '.claude'
    [System.IO.File]::WriteAllText($blockedParent, 'not a directory')
    $hookPath = Join-Path $blockedParent 'settings.json'
    $result = Invoke-Installer @('-Uninstall', '-Components', 'telemetry')
    if ($result.Code -ne 1) { Fail "expected hook inspection failure: $($result.Output)" }
    Assert-Contains $result.Output "cannot inspect native hook path $hookPath"
    if (Test-Path -LiteralPath $env:QDI_TELEMETRY_RECEIPT) {
      Fail 'wrote telemetry receipt after hook inspection failure'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryPurgeRemovesOnlyPackageDirectories {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path `
      $env:QDI_TELEMETRY_CONFIG_DIR, $env:QDI_TELEMETRY_CACHE_DIR, `
      (Split-Path -Parent $env:QDI_TELEMETRY_RECEIPT) | Out-Null
    New-Item -ItemType File -Force -Path `
      (Join-Path $env:QDI_TELEMETRY_CONFIG_DIR 'config.yaml'), `
      (Join-Path $env:QDI_TELEMETRY_CACHE_DIR 'cache.db'), $env:QDI_MARKETPLACE_STATE | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_TELEMETRY_RECEIPT, "version=1`nstate=uninstalled`n")
    $result = Invoke-Installer @('-Uninstall', '-Purge', '-Components', 'telemetry')
    if ($result.Code -ne 0) { Fail "telemetry purge failed: $($result.Output)" }
    if (Test-Path -LiteralPath $env:QDI_TELEMETRY_CONFIG_DIR) { Fail 'telemetry config remains' }
    if (Test-Path -LiteralPath $env:QDI_TELEMETRY_CACHE_DIR) { Fail 'telemetry cache remains' }
    if (-not (Test-Path -LiteralPath $env:QDI_TELEMETRY_RECEIPT)) { Fail 'purge removed receipt' }
    if (-not (Test-Path -LiteralPath $env:QDI_MARKETPLACE_STATE)) { Fail 'purge removed marketplace marker' }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryPurgePreservesDirectoryReparsePoint {
  Setup-ComponentFixture
  try {
    $target = Join-Path $FixtureRoot 'external-config-target'
    Remove-Item -Recurse -Force -LiteralPath $env:QDI_TELEMETRY_CONFIG_DIR
    if (-not (New-DirectoryReparseFixture $env:QDI_TELEMETRY_CONFIG_DIR $target)) { return }
    New-Item -ItemType File -Path (Join-Path $target 'sentinel') | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:QDI_TELEMETRY_RECEIPT) | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_TELEMETRY_RECEIPT, "version=1`nstate=uninstalled`n")
    $result = Invoke-Installer @('-Uninstall', '-Purge', '-Components', 'telemetry')
    if ($result.Code -ne 1) { Fail "expected telemetry reparse-point failure: $($result.Output)" }
    Assert-Contains $result.Output "preserving $env:QDI_TELEMETRY_CONFIG_DIR"
    Assert-Contains $result.Output 'directory reparse point'
    if (-not (Test-NativeFixtureEntry $env:QDI_TELEMETRY_CONFIG_DIR)) {
      Fail 'removed telemetry config reparse point'
    }
    if (-not (Test-Path -LiteralPath (Join-Path $target 'sentinel'))) {
      Fail 'recursed into telemetry config reparse target'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-TelemetryPurgePreservesNestedDirectoryReparsePoint {
  Setup-ComponentFixture
  try {
    $target = Join-Path $FixtureRoot 'external-nested-config-target'
    $nestedPath = Join-Path $env:QDI_TELEMETRY_CONFIG_DIR 'nested-link'
    if (-not (New-DirectoryReparseFixture $nestedPath $target)) { return }
    New-Item -ItemType File -Path (Join-Path $target 'sentinel') | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $env:QDI_TELEMETRY_RECEIPT) | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_TELEMETRY_RECEIPT, "version=1`nstate=uninstalled`n")
    $result = Invoke-Installer @('-Uninstall', '-Purge', '-Components', 'telemetry')
    if ($result.Code -ne 1) { Fail "expected nested telemetry reparse-point failure: $($result.Output)" }
    Assert-Contains $result.Output "preserving $env:QDI_TELEMETRY_CONFIG_DIR"
    Assert-Contains $result.Output $nestedPath
    Assert-Contains $result.Output 'directory reparse point'
    if (-not (Test-NativeFixtureEntry $env:QDI_TELEMETRY_CONFIG_DIR)) {
      Fail 'removed telemetry config root containing a nested reparse point'
    }
    if (-not (Test-NativeFixtureEntry $nestedPath)) { Fail 'removed nested telemetry reparse point' }
    if (-not (Test-Path -LiteralPath (Join-Path $target 'sentinel'))) {
      Fail 'recursed into nested telemetry config reparse target'
    }
  } finally { Teardown-ComponentFixture }
}

function Initialize-CleanGitHooksClone {
  New-Item -ItemType Directory -Force -Path `
    (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR '.git'), `
    (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
  Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
}

function Get-ManagedGitHooksPath {
  return [System.IO.Path]::GetFullPath((Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global'))
}

function Test-GitHooksUninstallDeactivatesOnlyExactManagedPath {
  Setup-ComponentFixture
  try {
    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, (Get-ManagedGitHooksPath))
    $managed = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($managed.Code -ne 0) { Fail "Git hooks deactivation failed: $($managed.Output)" }
    Assert-LogContains 'git config --global --unset-all core.hooksPath'
    Assert-LogNotContains 'java '

    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, 'pre-commit-global/hooks-global')
    $relative = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($relative.Code -ne 0) { Fail "relative Git hooks uninstall failed: $($relative.Output)" }
    if ((Get-Content -Raw -LiteralPath $env:QDI_GIT_CONFIG) -ne 'pre-commit-global/hooks-global') {
      Fail 'changed relative core.hooksPath'
    }

    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, '/other/hooks')
    $unrelated = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($unrelated.Code -ne 0) { Fail "unrelated Git hooks uninstall failed: $($unrelated.Output)" }
    if ((Get-Content -Raw -LiteralPath $env:QDI_GIT_CONFIG) -ne '/other/hooks') {
      Fail 'changed unrelated core.hooksPath'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallPreservesMixedConfigValues {
  Setup-ComponentFixture
  try {
    $managedPath = Get-ManagedGitHooksPath
    [System.IO.File]::WriteAllLines($env:QDI_GIT_CONFIG, [string[]]@(
      'relative/hooks', $managedPath, '/other/hooks'
    ))
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 0) { Fail "mixed Git config uninstall failed: $($result.Output)" }
    $remaining = @(Get-Content -LiteralPath $env:QDI_GIT_CONFIG)
    if ($remaining.Count -ne 2 -or $remaining[0] -ne 'relative/hooks' -or $remaining[1] -ne '/other/hooks') {
      $log = Get-Content -Raw -LiteralPath $env:QDI_TEST_LOG
      Fail "changed unrelated Git hook values: $($remaining -join ','). Log: $log"
    }
    Assert-LogContains 'git config --global --get-all core.hooksPath'
    Assert-LogContains 'git config --global --unset-all core.hooksPath'
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallPreservesWhitespaceDistinctValue {
  Setup-ComponentFixture
  try {
    $configuredPath = ' ' + (Get-ManagedGitHooksPath) + ' '
    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, $configuredPath)
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 0) { Fail "whitespace-distinct Git config uninstall failed: $($result.Output)" }
    if ((Get-Content -Raw -LiteralPath $env:QDI_GIT_CONFIG) -ne $configuredPath) {
      Fail 'changed whitespace-distinct core.hooksPath value'
    }
    Assert-LogNotContains 'git config --global --unset-all core.hooksPath'
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallReportsConfigReadFailure {
  Setup-ComponentFixture
  try {
    Initialize-CleanGitHooksClone
    $env:QDI_FAIL_GIT_CONFIG_READ = '1'
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 1) { Fail "expected Git config read failure: $($result.Output)" }
    Assert-Contains $result.Output 'cannot read global core.hooksPath values'
    if (-not (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) {
      Fail 'removed Git hooks clone after config read failure'
    }
    Assert-LogNotContains "git -C $env:QUBERSHIP_DEV_GIT_HOOKS_DIR rev-parse"
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallOwnershipChecks {
  Setup-ComponentFixture
  try {
    $missing = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($missing.Code -ne 0) { Fail "missing Git hooks clone uninstall failed: $($missing.Output)" }

    Initialize-CleanGitHooksClone
    $clean = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($clean.Code -ne 0) { Fail "clean Git hooks clone uninstall failed: $($clean.Output)" }
    if (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR) { Fail 'clean managed clone remains' }

    Initialize-CleanGitHooksClone
    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value 'https://example.test/unrelated.git'
    $wrongOrigin = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($wrongOrigin.Code -ne 1) { Fail "expected wrong-origin failure: $($wrongOrigin.Output)" }
    Assert-Contains $wrongOrigin.Output 'because its origin is https://example.test/unrelated.git'
    if (-not (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) { Fail 'removed wrong-origin clone' }

    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
    $env:QDI_GIT_STATUS = ' M hooks-global/pre-commit'
    $dirty = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($dirty.Code -ne 1) { Fail "expected dirty-clone failure: $($dirty.Output)" }
    Assert-Contains $dirty.Output 'preserving modified worktree'
    if (-not (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) { Fail 'removed dirty clone' }
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallPreservesDirectoryReparsePoint {
  Setup-ComponentFixture
  try {
    $target = Join-Path $FixtureRoot 'external-git-hooks-target'
    if (-not (New-DirectoryReparseFixture $env:QUBERSHIP_DEV_GIT_HOOKS_DIR $target)) { return }
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $target '.git'), (Join-Path $target 'hooks-global') | Out-Null
    New-Item -ItemType File -Path (Join-Path $target 'sentinel') | Out-Null
    Set-Content -LiteralPath $env:QDI_GIT_ORIGIN_FILE -Value $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 1) { Fail "expected Git reparse-point failure: $($result.Output)" }
    Assert-Contains $result.Output "preserving $env:QUBERSHIP_DEV_GIT_HOOKS_DIR"
    Assert-Contains $result.Output 'directory reparse point'
    if (-not (Test-NativeFixtureEntry $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) {
      Fail 'removed Git hooks reparse point'
    }
    if (-not (Test-Path -LiteralPath (Join-Path $target 'sentinel'))) {
      Fail 'recursed into Git hooks reparse target'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallPreservesNestedDirectoryReparsePoint {
  Setup-ComponentFixture
  try {
    Initialize-CleanGitHooksClone
    $target = Join-Path $FixtureRoot 'external-nested-git-hooks-target'
    $nestedPath = Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'nested-link'
    if (-not (New-DirectoryReparseFixture $nestedPath $target)) { return }
    New-Item -ItemType File -Path (Join-Path $target 'sentinel') | Out-Null
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 1) { Fail "expected nested Git reparse-point failure: $($result.Output)" }
    Assert-Contains $result.Output "preserving $env:QUBERSHIP_DEV_GIT_HOOKS_DIR"
    Assert-Contains $result.Output $nestedPath
    Assert-Contains $result.Output 'directory reparse point'
    if (-not (Test-NativeFixtureEntry $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) {
      Fail 'removed managed Git hooks root containing a nested reparse point'
    }
    if (-not (Test-NativeFixtureEntry $nestedPath)) { Fail 'removed nested Git hooks reparse point' }
    if (-not (Test-Path -LiteralPath (Join-Path $target 'sentinel'))) {
      Fail 'recursed into nested Git hooks reparse target'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallReportsInspectionFailures {
  foreach ($case in @(
    @{ Variable = 'QDI_FAIL_GIT_ORIGIN'; Message = 'cannot read origin for' }
    @{ Variable = 'QDI_FAIL_GIT_STATUS'; Message = 'cannot inspect worktree status for' }
  )) {
    Setup-ComponentFixture
    try {
      Initialize-CleanGitHooksClone
      Set-Item -Path "Env:$($case.Variable)" -Value '1'
      $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
      if ($result.Code -ne 1) { Fail "expected Git inspection failure: $($result.Output)" }
      Assert-Contains $result.Output "$($case.Message) $env:QUBERSHIP_DEV_GIT_HOOKS_DIR"
      if (-not (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) {
        Fail 'removed clone after Git inspection failure'
      }
    } finally { Teardown-ComponentFixture }
  }
}

function Test-GitHooksUninstallDeactivatesBeforeValidationFailure {
  Setup-ComponentFixture
  try {
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $env:QUBERSHIP_DEV_GIT_HOOKS_DIR 'hooks-global') | Out-Null
    [System.IO.File]::WriteAllText($env:QDI_GIT_CONFIG, (Get-ManagedGitHooksPath))
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks')
    if ($result.Code -ne 1) { Fail "expected non-worktree failure: $($result.Output)" }
    Assert-Contains $result.Output 'because it is not a Git worktree'
    if (Test-Path -LiteralPath $env:QDI_GIT_CONFIG) { Fail 'core.hooksPath remains after validation failure' }
    if (-not (Test-Path -LiteralPath $env:QUBERSHIP_DEV_GIT_HOOKS_DIR)) {
      Fail 'removed non-worktree directory'
    }
  } finally { Teardown-ComponentFixture }
}

function Test-GitHooksUninstallWithoutGitContinues {
  Setup-ComponentFixture
  try {
    $isolatedBin = Join-Path $FixtureRoot 'no-git-bin'
    New-Item -ItemType Directory -Force -Path $isolatedBin | Out-Null
    Copy-Item -LiteralPath $env:QDI_TELEMETRY_CLI -Destination `
      (Join-Path $isolatedBin 'ai-agent-telemetry.ps1')
    $env:PATH = $isolatedBin
    $result = Invoke-Installer @('-Uninstall', '-Components', 'git-hooks,telemetry')
    if ($result.Code -ne 1) { Fail "expected missing-Git failure: $($result.Output)" }
    Assert-Contains $result.Output 'git-hooks: cannot uninstall because Git is not on PATH'
    Assert-Contains $result.Output 'git-hooks        FAILED'
    Assert-Contains $result.Output 'telemetry        OK'
  } finally { Teardown-ComponentFixture }
}

$testCases = @(
  'Test-HelpDescribesPublicOptions',
  'Test-InvalidSelectionsFailBeforeInstallation',
  'Test-UninstallOptionCombinationsFailBeforeChanges',
  'Test-UnknownParameterFailsBeforeInstallation',
  'Test-PrerequisitesApplyOnlyToGitHooks',
  'Test-Java20IsRejected',
  'Test-Java21AndNewerAreAccepted',
  'Test-UnrecognizedOrFailingJavaIsRejected',
  'Test-DefaultInstallRunsEveryComponent',
  'Test-ExistingClisAreUpdatedByDefault',
  'Test-SelectionAndHarnessesAreForwarded',
  'Test-ForceUpdateRefreshesSelectedComponents',
  'Test-UnrelatedGitHooksAreSkipped',
  'Test-ComponentFailureDoesNotStopIndependentComponents',
  'Test-UnconfiguredTelemetryBehavior',
  'Test-GitHooksRejectUnsafeExistingDirectories',
  'Test-GitHooksRejectNonRepositoryAndDivergence',
  'Test-ApmUninstallBehavior',
  'Test-ApmUninstallFailureDoesNotStopTelemetry',
  'Test-TelemetryUninstallLifecycle',
  'Test-TelemetryPrefersInvokableManagedExecutable',
  'Test-TelemetryUsesAndPreservesExternalCommandWhenManagedIsAbsent',
  'Test-TelemetryHookFailurePreservesManagedExecutable',
  'Test-TelemetryReceiptAndOwnershipBehavior',
  'Test-TelemetryDanglingHookIsExistingState',
  'Test-TelemetryHookInspectionFailureDoesNotWriteReceipt',
  'Test-TelemetryPurgeRemovesOnlyPackageDirectories',
  'Test-TelemetryPurgePreservesDirectoryReparsePoint',
  'Test-TelemetryPurgePreservesNestedDirectoryReparsePoint',
  'Test-GitHooksUninstallDeactivatesOnlyExactManagedPath',
  'Test-GitHooksUninstallPreservesMixedConfigValues',
  'Test-GitHooksUninstallPreservesWhitespaceDistinctValue',
  'Test-GitHooksUninstallReportsConfigReadFailure',
  'Test-GitHooksUninstallOwnershipChecks',
  'Test-GitHooksUninstallPreservesDirectoryReparsePoint',
  'Test-GitHooksUninstallPreservesNestedDirectoryReparsePoint',
  'Test-GitHooksUninstallReportsInspectionFailures',
  'Test-GitHooksUninstallDeactivatesBeforeValidationFailure',
  'Test-GitHooksUninstallWithoutGitContinues'
)
foreach ($testCase in $testCases) {
  if (-not $env:QDI_TEST_FILTER -or $testCase -eq $env:QDI_TEST_FILTER) { & $testCase }
}
Write-Host 'PASS: PowerShell developer installer tests'
