param(
  [string[]]$Components = @('all'),
  [string[]]$Skip = @(),
  [string[]]$Harnesses = @('all'),
  [switch]$ForceGitHooks,
  [switch]$ForceUpdate,
  [switch]$NonInteractive,
  [switch]$Help
)

$ErrorActionPreference = 'Stop'
$Program = 'qubership-dev-install.ps1'

$ComponentRegistry = [ordered]@{
  'apm' = @{ Default = $true; UsesHarnesses = $true; Prefix = 'Apm' }
  'telemetry' = @{ Default = $true; UsesHarnesses = $true; Prefix = 'Telemetry' }
  'git-hooks' = @{ Default = $true; UsesHarnesses = $false; Prefix = 'GitHooks' }
}
$HarnessRegistry = @('claude', 'codex', 'cursor')

function Show-Usage {
  @'
Install the baseline Qubership developer tools.

Usage:
  qubership-dev-install.ps1 [options]

Options:
  -Components <list>   Install only these components: apm, telemetry, git-hooks, or all.
  -Skip <list>         Exclude components from the selected set.
  -Harnesses <list>    Configure these harnesses: claude, codex, cursor, or all.
  -ForceGitHooks       Replace an existing global Git hooks path.
  -ForceUpdate         Update selected components even when they are already installed.
  -NonInteractive      Do not prompt for missing prerequisites.
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

function Test-GitHookPrerequisites {
  $missing = [System.Collections.Generic.List[string]]::new()
  if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    [Console]::Error.WriteLine("${Program}: Git is required for the git-hooks component. Install it from https://git-scm.com/install/.")
    $missing.Add('git')
  }
  if (-not (Get-Command java -ErrorAction SilentlyContinue)) {
    [Console]::Error.WriteLine("${Program}: Java is required for the git-hooks component. Install a supported JRE or JDK.")
    $missing.Add('java')
  }
  return $missing.Count -eq 0
}

function Confirm-GitHookPrerequisites {
  if (Test-GitHookPrerequisites) { return $true }
  if ($NonInteractive) {
    [Console]::Error.WriteLine("${Program}: Installation stopped because required tools are missing.")
    return $false
  }
  $answer = Read-Host 'Install the missing tools in another terminal. Have you installed them? [y/N]'
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
  if ($ForceUpdate -and $wasInstalled) {
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

  & $script:ApmCommand view qubership-global-essentials -g *> $null
  $packageInstalled = $LASTEXITCODE -eq 0
  $targets = $selectedHarnesses -join ','
  if ($packageInstalled -and $ForceUpdate) {
    Invoke-Checked $script:ApmCommand @(
      'update', 'qubership-global-essentials', '-g', '--yes', '--target', $targets
    )
  } elseif ($packageInstalled) {
    Invoke-Checked $script:ApmCommand @('install', '-g', '--target', $targets)
  } else {
    Invoke-Checked $script:ApmCommand @(
      'install', 'qubership-global-essentials@qubership-ai-packages', '-g', '--target', $targets
    )
  }
  Invoke-Checked $script:ApmCommand @('compile', '-g')
}

function Test-Apm {
  Invoke-Checked $script:ApmCommand @('view', 'qubership-global-essentials', '-g')
}

function Install-Telemetry {
  $source = if ($env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL) {
    $env:QUBERSHIP_DEV_TELEMETRY_INSTALL_URL
  } else {
    'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1'
  }
  $parameters = @{ SkipConfig = $true }
  if ($ForceUpdate) { $parameters.Force = $true }
  Invoke-PowerShellInstaller $source $parameters
}

function Resolve-TelemetryCommand {
  $binDir = Join-Path $env:USERPROFILE '.local/bin'
  $env:PATH = "$binDir$([System.IO.Path]::PathSeparator)$env:PATH"
  $script:TelemetryCommand = (Get-Command ai-agent-telemetry -ErrorAction SilentlyContinue).Source
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

function Install-GitHooks {
  $script:GitHooksDir = if ($env:QUBERSHIP_DEV_GIT_HOOKS_DIR) {
    $env:QUBERSHIP_DEV_GIT_HOOKS_DIR
  } else {
    Join-Path $env:LOCALAPPDATA 'Qubership/pre-commit-global'
  }
  $repository = if ($env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY) {
    $env:QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
  } else {
    'https://github.com/exadmin/pre-commit-global.git'
  }

  $hooksDir = Join-Path $script:GitHooksDir 'hooks-global'
  $prospectivePath = [System.IO.Path]::GetFullPath($hooksDir)
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
    Invoke-Checked 'git' @('clone', $repository, $script:GitHooksDir)
  }
  & git -C $script:GitHooksDir rev-parse --is-inside-work-tree *> $null
  if ($LASTEXITCODE -ne 0) { throw "$($script:GitHooksDir) is not the managed Git repository." }
  $origin = (& git -C $script:GitHooksDir remote get-url origin 2>$null | Out-String).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Cannot read the Git hooks repository origin.' }
  if ($origin -ne $repository) { throw "Git hooks repository has unexpected origin $origin." }
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
    [Console]::Error.WriteLine(
      "${Program}: CYBER_FERRET_PASSWORD is not set; CyberFerret checks will require configuration."
    )
  }
}

function Test-GitHooks {
  $desiredPath = (Resolve-Path (Join-Path $script:GitHooksDir 'hooks-global')).Path
  $currentOutput = & git config --global --get core.hooksPath 2>$null
  $currentPath = if ($LASTEXITCODE -eq 0) { ($currentOutput | Out-String).Trim() } else { '' }
  if ($currentPath -ne $desiredPath) { throw "core.hooksPath is not set to $desiredPath." }
}

function Invoke-Component([string]$Component) {
  $prefix = $ComponentRegistry[$Component].Prefix
  $script:ComponentSkipped = $false
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

if ($Help) {
  Show-Usage
  exit 0
}

$selectedComponents = @(Normalize-Selection 'component' $Components @($ComponentRegistry.Keys))
if ($Skip.Count -gt 0) {
  $skippedComponents = @(Normalize-Selection 'component' $Skip @($ComponentRegistry.Keys))
  $selectedComponents = @($selectedComponents | Where-Object { $skippedComponents -notcontains $_ })
}
if ($selectedComponents.Count -eq 0) { Stop-ArgumentError 'no components selected' }
$selectedHarnesses = @(Normalize-Selection 'harness' $Harnesses $HarnessRegistry)

if ($selectedComponents -contains 'git-hooks') {
  if (-not (Confirm-GitHookPrerequisites)) { exit 1 }
}

$results = [ordered]@{}
foreach ($component in $selectedComponents) {
  $results[$component] = Invoke-Component $component
}

Write-Host "`nInstallation summary"
$hasFailures = $false
foreach ($component in $selectedComponents) {
  $status = $results[$component]
  Write-Host ('{0,-16} {1}' -f $component, $status)
  if ($status -eq 'FAILED') { $hasFailures = $true }
}
if ($hasFailures) { exit 1 }
exit 0
