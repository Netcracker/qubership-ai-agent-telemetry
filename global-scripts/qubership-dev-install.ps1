param(
  [string[]]$Components = @('all'),
  [string[]]$Skip = @(),
  [string[]]$Harnesses = @('all'),
  [switch]$ForceGitHooks,
  [switch]$ForceUpdate,
  [switch]$NonInteractive,
  [switch]$Uninstall,
  [switch]$Purge,
  [switch]$Help
)

$ErrorActionPreference = 'Stop'
$Program = 'qubership-dev-install.ps1'
$MinimumJavaMajor = 21

$ComponentRegistry = [ordered]@{
  'apm' = @{ Default = $true; UsesHarnesses = $true; Prefix = 'Apm' }
  'telemetry' = @{ Default = $true; UsesHarnesses = $true; Prefix = 'Telemetry' }
  'git-hooks' = @{ Default = $true; UsesHarnesses = $false; Prefix = 'GitHooks' }
}
$HarnessRegistry = @('claude', 'codex', 'cursor')

function Show-Usage {
  @'
Install or uninstall the baseline Qubership developer tools.

Usage:
  qubership-dev-install.ps1 [options]

Options:
  -Components <list>   Install only these components: apm, telemetry, git-hooks, or all.
  -Skip <list>         Exclude components from the selected set.
  -Harnesses <list>    Configure these harnesses: claude, codex, cursor, or all.
  -ForceGitHooks       Replace an existing global Git hooks path.
  -ForceUpdate         Force update operations for every selected component.
  -NonInteractive      Do not prompt for missing prerequisites.
  -Uninstall           Uninstall the selected Qubership developer tools.
  -Purge               Remove telemetry config and cache during uninstall.
  -Help                Show this help text.
'@
}

function Stop-ArgumentError([string]$Message) {
  [Console]::Error.WriteLine("${Program}: $Message")
  exit 2
}

function Normalize-Selection(
  [string]$Kind,
  [string[]]$Values,
  [string[]]$Allowed
) {
  $raw = ($Values -join ',').Trim()
  if ([string]::IsNullOrWhiteSpace($raw)) {
    Stop-ArgumentError "$Kind list must not be empty"
  }
  if ($raw.StartsWith(',') -or $raw.EndsWith(',') -or $raw.Contains(',,')) {
    Stop-ArgumentError "$Kind list contains an empty value"
  }
  if ($raw -eq 'all') { return @($Allowed) }

  $items = @($raw.Split(','))
  if ($items -contains 'all') {
    Stop-ArgumentError "all must be used by itself in the $Kind list"
  }
  $result = [System.Collections.Generic.List[string]]::new()
  foreach ($item in $items) {
    if ($Allowed -notcontains $item) {
      Stop-ArgumentError "unknown $Kind `"$item`""
    }
    if (-not $result.Contains($item)) { $result.Add($item) }
  }
  return @($result)
}

function Get-JavaMajorVersion {
  $previousErrorActionPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = 'Continue'
    $global:LASTEXITCODE = 0
    $output = @(& java -XshowSettings:properties -version 2>&1)
    if ($LASTEXITCODE -ne 0) { return $null }
  } catch {
    return $null
  } finally {
    $ErrorActionPreference = $previousErrorActionPreference
  }

  foreach ($line in $output) {
    $match = [regex]::Match([string]$line, '^\s*java\.specification\.version\s*=\s*(\S+)\s*$')
    if (-not $match.Success) { continue }
    $value = $match.Groups[1].Value
    if ($value -match '^1\.(\d+)(?:\..*)?$') { return [int]$Matches[1] }
    if ($value -match '^(\d+)(?:\..*)?$') { return [int]$Matches[1] }
    return $null
  }
  return $null
}

function Test-GitHookPrerequisites {
  $missing = [System.Collections.Generic.List[string]]::new()
  if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    [Console]::Error.WriteLine("${Program}: Git is required for the git-hooks component. Install it from https://git-scm.com/install/.")
    $missing.Add('git')
  }
  if (-not (Get-Command java -ErrorAction SilentlyContinue)) {
    [Console]::Error.WriteLine("${Program}: Java $MinimumJavaMajor or newer is required for the git-hooks component. Install a supported JRE or JDK.")
    $missing.Add('java')
  } else {
    $javaMajor = Get-JavaMajorVersion
    if ($null -eq $javaMajor) {
      [Console]::Error.WriteLine("${Program}: Could not determine the Java version. Java $MinimumJavaMajor or newer is required for the git-hooks component.")
      $missing.Add('java')
    } elseif ($javaMajor -lt $MinimumJavaMajor) {
      [Console]::Error.WriteLine("${Program}: Detected Java $javaMajor. Java $MinimumJavaMajor or newer is required for the git-hooks component.")
      $missing.Add('java')
    }
  }
  return $missing.Count -eq 0
}

function Confirm-GitHookPrerequisites {
  if (Test-GitHookPrerequisites) { return $true }
  if ($NonInteractive) {
    [Console]::Error.WriteLine("${Program}: Installation stopped because required tools are missing.")
    return $false
  }
  $answer = Read-Host 'Install or update the required tools in another terminal. Have you installed them? [y/N]'
  if ($answer -notmatch '^(?i:y|yes)$') {
    [Console]::Error.WriteLine("${Program}: Installation stopped by the user.")
    return $false
  }
  if (-not (Test-GitHookPrerequisites)) {
    [Console]::Error.WriteLine("${Program}: Installation stopped because required tools are still missing.")
    return $false
  }
  return $true
}

function Invoke-Checked([string]$Command, [string[]]$Arguments) {
  $global:LASTEXITCODE = 0
  & $Command @Arguments | Out-Host
  if ($LASTEXITCODE -ne 0) {
    throw "Command failed with exit code ${LASTEXITCODE}: $Command $($Arguments -join ' ')"
  }
}

function Download-File([string]$Source, [string]$Destination) {
  if (Test-Path -LiteralPath $Source) {
    Copy-Item -Force -LiteralPath $Source -Destination $Destination
    return
  }
  Invoke-WebRequest -UseBasicParsing -Uri $Source -OutFile $Destination
}

function Invoke-PowerShellInstaller([string]$Source, [hashtable]$Parameters) {
  $temporaryFile = Join-Path ([System.IO.Path]::GetTempPath()) "qdi-$([guid]::NewGuid()).ps1"
  try {
    Download-File $Source $temporaryFile
    $global:LASTEXITCODE = 0
    & $temporaryFile @Parameters | Out-Host
    if ($LASTEXITCODE -ne 0) {
      throw "Installer failed with exit code ${LASTEXITCODE}: $Source"
    }
  } finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $temporaryFile
  }
}

function Install-Apm {
  $script:ApmCommand = (Get-Command apm -ErrorAction SilentlyContinue).Source
  $wasInstalled = -not [string]::IsNullOrWhiteSpace($script:ApmCommand)
  if (-not $wasInstalled) {
    $source = if ($env:QUBERSHIP_DEV_APM_INSTALL_URL) {
      $env:QUBERSHIP_DEV_APM_INSTALL_URL
    } else {
      'https://aka.ms/apm-windows'
    }
    Invoke-PowerShellInstaller $source @{}
    $binDir = Join-Path $env:USERPROFILE '.local/bin'
    $env:PATH = "$binDir$([System.IO.Path]::PathSeparator)$env:PATH"
    $script:ApmCommand = (Get-Command apm -ErrorAction SilentlyContinue).Source
    if ([string]::IsNullOrWhiteSpace($script:ApmCommand)) {
      throw 'APM installer completed, but apm is not on PATH.'
    }
  }
  if ($wasInstalled) {
    Invoke-Checked $script:ApmCommand @('self-update')
  }
}

function Configure-Apm {
  $marketplaces = & $script:ApmCommand marketplace list 2>&1 | Out-String
  if ($LASTEXITCODE -ne 0) { throw 'Cannot list APM marketplaces.' }
  if ($marketplaces -match '(?m)(^|\s)qubership-ai-packages(\s|$)') {
    if ($ForceUpdate) {
      Invoke-Checked $script:ApmCommand @('marketplace', 'update', 'qubership-ai-packages')
    }
  } else {
    Invoke-Checked $script:ApmCommand @('marketplace', 'add', 'Netcracker/qubership-ai-packages')
  }

  $targets = $selectedHarnesses -join ','
  if ($ForceUpdate) {
    Invoke-Checked $script:ApmCommand @(
      'install', '--update', 'qubership-global-essentials@qubership-ai-packages',
      '-g', '--target', $targets
    )
  } else {
    Invoke-Checked $script:ApmCommand @(
      'install', 'qubership-global-essentials@qubership-ai-packages', '-g', '--target', $targets
    )
  }
  Invoke-Checked $script:ApmCommand @('compile', '-g')
}

function Test-Apm {
  Invoke-Checked $script:ApmCommand @('deps', 'list', '-g')
}

function Uninstall-Apm {
  $manifest = Join-Path $env:USERPROFILE '.apm/apm.yml'
  if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
    $script:ComponentSkipped = $true
    return
  }
  $command = (Get-Command apm -ErrorAction SilentlyContinue).Source
  if ([string]::IsNullOrWhiteSpace($command)) {
    throw 'cannot remove the global package because apm is not on PATH.'
  }
  Invoke-Checked $command @('uninstall', '-g', 'qubership-global-essentials@qubership-ai-packages')
}

function Install-Telemetry {
  $wasInstalled = -not [string]::IsNullOrWhiteSpace((Find-TelemetryCommand))
  $source = if ($env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL) {
    $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL
  } else {
    'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'
  }
  $parameters = @{ SkipConfig = $true }
  if ($ForceUpdate -or $wasInstalled) { $parameters.Force = $true }
  Invoke-PowerShellInstaller $source $parameters
}

function Find-TelemetryCommand {
  $command = (Get-Command ai-agent-telemetry -ErrorAction SilentlyContinue).Source
  if (-not [string]::IsNullOrWhiteSpace($command)) { return $command }

  $binDir = Join-Path $env:USERPROFILE '.local/bin'
  foreach ($name in @('ai-agent-telemetry.exe', 'ai-agent-telemetry.ps1', 'ai-agent-telemetry')) {
    $candidate = Join-Path $binDir $name
    if (Test-Path -LiteralPath $candidate) { return $candidate }
  }
  return $null
}

function Resolve-TelemetryCommand {
  $binDir = Join-Path $env:USERPROFILE '.local/bin'
  $env:PATH = "$binDir$([System.IO.Path]::PathSeparator)$env:PATH"
  $script:TelemetryCommand = Find-TelemetryCommand
  if ([string]::IsNullOrWhiteSpace($script:TelemetryCommand)) {
    throw 'Telemetry installer completed, but ai-agent-telemetry was not found.'
  }
}

function Configure-Telemetry {
  Resolve-TelemetryCommand
  $configRoot = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $env:USERPROFILE '.config' }
  $envFile = Join-Path $configRoot 'ai-agent-telemetry/env'
  $configured = $false
  if (Test-Path -LiteralPath $envFile) {
    $configured = [bool](Get-Content -LiteralPath $envFile | Where-Object {
      $_ -match '^AI_AGENT_TELEMETRY_ENDPOINT=.+$'
    } | Select-Object -First 1)
  }
  $targets = $selectedHarnesses -join ','
  if ($configured) {
    Invoke-Checked $script:TelemetryCommand @('hooks', 'install', "--target=$targets")
  } elseif ($NonInteractive) {
    throw 'telemetry configuration is required; run ai-agent-telemetry configure and retry.'
  } else {
    Invoke-Checked $script:TelemetryCommand @('configure', "--hooks=$targets")
  }
}

function Test-Telemetry {
  Resolve-TelemetryCommand
  Invoke-Checked $script:TelemetryCommand @('status')
  Invoke-Checked $script:TelemetryCommand @('selftest')
}

function Get-TelemetryReceiptPath {
  $stateRoot = if ($env:XDG_STATE_HOME) {
    $env:XDG_STATE_HOME
  } else {
    Join-Path $env:USERPROFILE '.local/state'
  }
  return Join-Path $stateRoot 'ai-agent-telemetry/hooks-uninstalled'
}

function Test-TelemetryReceipt {
  $path = Get-TelemetryReceiptPath
  if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return $false }
  return [System.IO.File]::ReadAllText($path) -eq "version=1`nstate=uninstalled`n"
}

function Write-TelemetryReceipt {
  $path = Get-TelemetryReceiptPath
  $dir = Split-Path -Parent $path
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  $temp = "$path.tmp-$PID-$([guid]::NewGuid().ToString('N'))"
  [System.IO.File]::WriteAllText($temp, "version=1`nstate=uninstalled`n")
  Move-Item -Force -LiteralPath $temp -Destination $path
}

function Test-NativePathEntry([string]$Path) {
  return $null -ne (Get-Item -Force -LiteralPath $Path -ErrorAction SilentlyContinue)
}

function Test-TelemetryHooksMayExist {
  foreach ($relativePath in @(
    '.claude/settings.json',
    '.codex/hooks.json',
    '.cursor/hooks.json',
    '.codex/rules/ai-agent-telemetry.rules'
  )) {
    if (Test-NativePathEntry (Join-Path $env:USERPROFILE $relativePath)) { return $true }
  }
  return $false
}

function Uninstall-Telemetry {
  $managedExecutable = Join-Path $env:USERPROFILE '.local/bin/ai-agent-telemetry.exe'
  $telemetryCommand = (Get-Command ai-agent-telemetry -ErrorAction SilentlyContinue).Source
  if ([string]::IsNullOrWhiteSpace($telemetryCommand) -and
      (Test-Path -LiteralPath $managedExecutable -PathType Leaf)) {
    $telemetryCommand = $managedExecutable
  }

  if (-not [string]::IsNullOrWhiteSpace($telemetryCommand)) {
    Invoke-Checked $telemetryCommand @('hooks', 'uninstall')
  } elseif (Test-TelemetryReceipt) {
    # The receipt proves that the Go hook uninstaller completed successfully.
  } elseif (Test-TelemetryHooksMayExist) {
    throw 'native hook files exist, but no telemetry CLI or valid removal receipt is available.'
  } else {
    Write-TelemetryReceipt
  }

  if (Test-NativePathEntry $managedExecutable) {
    Remove-Item -Force -ErrorAction Stop -LiteralPath $managedExecutable
  }
  if ($Purge) {
    $configRoot = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $env:USERPROFILE '.config' }
    $cacheRoot = if ($env:XDG_CACHE_HOME) { $env:XDG_CACHE_HOME } else { Join-Path $env:USERPROFILE '.cache' }
    foreach ($path in @(
      (Join-Path $configRoot 'ai-agent-telemetry'),
      (Join-Path $cacheRoot 'ai-agent-telemetry')
    )) {
      if (Test-NativePathEntry $path) {
        Remove-Item -Recurse -Force -ErrorAction Stop -LiteralPath $path
      }
    }
  }
}

function Initialize-GitHooks {
  $script:GitHooksDir = if ($env:QUBERSHIP_DEV_GIT_HOOKS_DIR) {
    $env:QUBERSHIP_DEV_GIT_HOOKS_DIR
  } else {
    Join-Path $env:LOCALAPPDATA 'Qubership/pre-commit-global'
  }
  $script:GitHooksRepository = if ($env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY) {
    $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
  } else {
    'https://github.com/exadmin/pre-commit-global.git'
  }
}

function Get-ResolvedGitHooksPath {
  $path = Join-Path $script:GitHooksDir 'hooks-global'
  if (Test-Path -LiteralPath $path) { return (Resolve-Path -LiteralPath $path).Path }
  return [System.IO.Path]::GetFullPath($path)
}

function Install-GitHooks {
  Initialize-GitHooks

  $hooksDir = Join-Path $script:GitHooksDir 'hooks-global'
  $prospectivePath = Get-ResolvedGitHooksPath
  $currentOutput = & git config --global --get core.hooksPath 2>$null
  $currentPath = if ($LASTEXITCODE -eq 0) { ($currentOutput | Out-String).Trim() } else { '' }
  if ($currentPath -and $currentPath -ne $prospectivePath -and -not $ForceGitHooks) {
    [Console]::Error.WriteLine(
      "${Program}: core.hooksPath is already set to $currentPath; global Git hooks installation was skipped."
    )
    $script:ComponentSkipped = $true
    return
  }
  if (-not (Test-Path $script:GitHooksDir)) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $script:GitHooksDir) | Out-Null
    Invoke-Checked 'git' @('clone', $script:GitHooksRepository, $script:GitHooksDir)
  }
  & git -C $script:GitHooksDir rev-parse --is-inside-work-tree *> $null
  if ($LASTEXITCODE -ne 0) { throw "$($script:GitHooksDir) is not the managed Git repository." }
  $origin = (& git -C $script:GitHooksDir remote get-url origin 2>$null | Out-String).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Cannot read the Git hooks repository origin.' }
  if ($origin -ne $script:GitHooksRepository) { throw "Git hooks repository has unexpected origin $origin." }
  $gitStatus = (& git -C $script:GitHooksDir status --porcelain --untracked-files=all 2>$null | Out-String).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect the Git hooks repository status.' }
  if ($gitStatus) { throw 'Git hooks repository has local changes; refusing to activate or update it.' }
  if ($ForceUpdate) {
    Invoke-Checked 'git' @('-C', $script:GitHooksDir, 'pull', '--ff-only')
  }
  if (-not (Test-Path $hooksDir)) { throw "hooks-global was not found in $($script:GitHooksDir)." }
}

function Configure-GitHooks {
  $desiredPath = (Resolve-Path (Join-Path $script:GitHooksDir 'hooks-global')).Path
  $currentOutput = & git config --global --get core.hooksPath 2>$null
  $currentPath = if ($LASTEXITCODE -eq 0) { ($currentOutput | Out-String).Trim() } else { '' }
  if ($currentPath -and $currentPath -ne $desiredPath) {
    if (-not $ForceGitHooks) {
      [Console]::Error.WriteLine(
        "${Program}: core.hooksPath is already set to $currentPath; global Git hooks installation was skipped."
      )
      $script:ComponentSkipped = $true
      return
    }
    [Console]::Error.WriteLine("${Program}: replacing core.hooksPath: $currentPath -> $desiredPath")
  }
  if ($currentPath -ne $desiredPath) {
    Invoke-Checked 'git' @('config', '--global', 'core.hooksPath', $desiredPath)
  }
  if ([string]::IsNullOrWhiteSpace($env:CYBER_FERRET_PASSWORD)) {
    [Console]::Error.WriteLine("${Program}: CYBER_FERRET_PASSWORD is not set; CyberFerret checks require it.")
    [Console]::Error.WriteLine('Set it for your Windows user in PowerShell:')
    [Console]::Error.WriteLine(
      "  [Environment]::SetEnvironmentVariable('CYBER_FERRET_PASSWORD', '<password>', 'User')"
    )
    [Console]::Error.WriteLine('Then restart your terminal and IDE.')
    [Console]::Error.WriteLine(
      'More options: https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/global-scripts/README.md#cyberferret-password'
    )
  }
}

function Test-GitHooks {
  $desiredPath = (Resolve-Path (Join-Path $script:GitHooksDir 'hooks-global')).Path
  $currentOutput = & git config --global --get core.hooksPath 2>$null
  $currentPath = if ($LASTEXITCODE -eq 0) { ($currentOutput | Out-String).Trim() } else { '' }
  if ($currentPath -ne $desiredPath) { throw "core.hooksPath is not set to $desiredPath." }
}

function Uninstall-GitHooks {
  if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw 'cannot uninstall because Git is not on PATH. Install Git and retry.'
  }
  Initialize-GitHooks
  $desiredPath = Get-ResolvedGitHooksPath
  $global:LASTEXITCODE = 0
  $currentOutput = & git config --global --get core.hooksPath 2>$null
  $currentPath = if ($LASTEXITCODE -eq 0) { ($currentOutput | Out-String).Trim() } else { '' }
  $resolvedCurrentPath = $currentPath
  if ($currentPath -and [System.IO.Path]::IsPathRooted($currentPath)) {
    $resolvedCurrentPath = if (Test-Path -LiteralPath $currentPath) {
      (Resolve-Path -LiteralPath $currentPath).Path
    } else {
      [System.IO.Path]::GetFullPath($currentPath)
    }
  }
  if ($currentPath -and [System.IO.Path]::IsPathRooted($currentPath) -and
      $resolvedCurrentPath -eq $desiredPath) {
    Invoke-Checked 'git' @('config', '--global', '--unset-all', 'core.hooksPath')
  }

  if (-not (Test-Path -LiteralPath $script:GitHooksDir)) { return }
  $global:LASTEXITCODE = 0
  & git -C $script:GitHooksDir rev-parse --is-inside-work-tree *> $null
  if ($LASTEXITCODE -ne 0) {
    throw "preserving $($script:GitHooksDir) because it is not a Git worktree."
  }

  $global:LASTEXITCODE = 0
  $originOutput = & git -C $script:GitHooksDir remote get-url origin 2>$null
  if ($LASTEXITCODE -ne 0) {
    throw "cannot read origin for $($script:GitHooksDir). Preserving the directory."
  }
  $origin = ($originOutput | Out-String).Trim()
  if ($origin -ne $script:GitHooksRepository) {
    throw "preserving $($script:GitHooksDir) because its origin is $origin."
  }

  $global:LASTEXITCODE = 0
  $statusOutput = & git -C $script:GitHooksDir status --porcelain 2>$null
  if ($LASTEXITCODE -ne 0) {
    throw "cannot inspect worktree status for $($script:GitHooksDir). Preserving the directory."
  }
  $status = ($statusOutput | Out-String).Trim()
  if ($status) { throw "preserving modified worktree $($script:GitHooksDir)." }
  Remove-Item -Recurse -Force -LiteralPath $script:GitHooksDir
}

function Invoke-Component([string]$Component) {
  $prefix = $ComponentRegistry[$Component].Prefix
  $script:ComponentSkipped = $false
  if ($Uninstall) {
    Write-Host "`n[$Component] UNINSTALLING"
    try {
      & "Uninstall-$prefix"
      if ($script:ComponentSkipped) { return 'SKIPPED' }
      return 'OK'
    } catch {
      [Console]::Error.WriteLine("${Program}: ${Component}: $($_.Exception.Message)")
      return 'FAILED'
    }
  }
  Write-Host "`n[$Component] INSTALLING"
  try {
    & "Install-$prefix"
    if ($script:ComponentSkipped) { return 'SKIPPED' }
    Write-Host "[$Component] CONFIGURING"
    & "Configure-$prefix"
    if ($script:ComponentSkipped) { return 'SKIPPED' }
    Write-Host "[$Component] VERIFYING"
    & "Test-$prefix"
    return 'OK'
  } catch {
    [Console]::Error.WriteLine("${Program}: ${Component}: $($_.Exception.Message)")
    return 'FAILED'
  }
}

if ($args.Count -gt 0) {
  Stop-ArgumentError "unknown option `"$($args[0])`""
}

if ($Help) {
  Show-Usage
  exit 0
}

if ($Purge -and -not $Uninstall) { Stop-ArgumentError '-Purge requires -Uninstall' }
if ($Uninstall) {
  if ($PSBoundParameters.ContainsKey('Harnesses')) {
    Stop-ArgumentError '-Harnesses is not valid with -Uninstall'
  }
  if ($PSBoundParameters.ContainsKey('ForceUpdate')) {
    Stop-ArgumentError '-ForceUpdate is not valid with -Uninstall'
  }
  if ($PSBoundParameters.ContainsKey('ForceGitHooks')) {
    Stop-ArgumentError '-ForceGitHooks is not valid with -Uninstall'
  }
  if ($PSBoundParameters.ContainsKey('NonInteractive')) {
    Stop-ArgumentError '-NonInteractive is not valid with -Uninstall'
  }
}

$selectedComponents = @(Normalize-Selection 'component' $Components @($ComponentRegistry.Keys))
if ($Skip.Count -gt 0) {
  $skippedComponents = @(Normalize-Selection 'component' $Skip @($ComponentRegistry.Keys))
  $selectedComponents = @($selectedComponents | Where-Object { $skippedComponents -notcontains $_ })
}
if ($selectedComponents.Count -eq 0) { Stop-ArgumentError 'no components selected' }
$selectedHarnesses = if ($Uninstall) { @() } else { @(Normalize-Selection 'harness' $Harnesses $HarnessRegistry) }

if (-not $Uninstall -and $selectedComponents -contains 'git-hooks') {
  if (-not (Confirm-GitHookPrerequisites)) { exit 1 }
}

$results = [ordered]@{}
foreach ($component in $selectedComponents) {
  $results[$component] = Invoke-Component $component
}

$summaryTitle = if ($Uninstall) { 'Uninstall summary' } else { 'Installation summary' }
Write-Host "`n$summaryTitle"
$hasFailures = $false
foreach ($component in $selectedComponents) {
  $status = $results[$component]
  Write-Host ('{0,-16} {1}' -f $component, $status)
  if ($status -eq 'FAILED') { $hasFailures = $true }
}
if ($hasFailures) { exit 1 }
exit 0
