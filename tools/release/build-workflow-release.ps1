[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$SourcePath,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory,

    [string]$ArtifactName = '',

    [string]$LockName = '',

    [string]$AgentExecutable = 'himind-agent',

    [string]$AgentProfile = ''
)

# 这里只做一件事：把 Workflow 源码交给 Agent 编译、测试、确认，产出不可变制品
# 与扩展锁。命名与发布清单不在这里决定 —— 名字来自 himind-release-plan，
# 清单由 himind-release-plan -manifest-out 写。构建与发布之间没有第二份事实。
$ErrorActionPreference = 'Stop'

function Resolve-ExecutablePath([string]$Path) {
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        return (Resolve-Path -LiteralPath $Path).Path
    }
    $command = Get-Command $Path -ErrorAction SilentlyContinue
    if (-not $command) {
        throw "Agent executable was not found: $Path"
    }
    return $command.Source
}

function Invoke-AgentJson {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $AgentExecutable
    $startInfo.WorkingDirectory = (Get-Location).Path
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    if (-not [string]::IsNullOrWhiteSpace($AgentProfile)) {
        $startInfo.EnvironmentVariables["HIMIND_AGENT_PROFILE"] = $AgentProfile
    }
    $startInfo.Arguments = (@($Arguments | ForEach-Object { '"' + ($_ -replace '"', '\"') + '"' }) -join ' ')
    $process = [Diagnostics.Process]::Start($startInfo)
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) {
        throw "himind-agent $($Arguments -join ' ') failed: $stderr $stdout"
    }
    return $stdout | ConvertFrom-Json
}

$AgentExecutable = Resolve-ExecutablePath $AgentExecutable
$source = (Resolve-Path -LiteralPath $SourcePath).Path
$output = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $output | Out-Null

$draft = Invoke-AgentJson -Arguments @("workflow", "author-save", $source)
$packageId = [string]$draft.package_id
$version = [string]$draft.version
if (-not $packageId -or -not $version) {
    throw "Workflow authoring draft did not return a stable package id and version."
}

$tested = Invoke-AgentJson -Arguments @("workflow", "author-test", $packageId, $version)
$confirmed = Invoke-AgentJson -Arguments @("workflow", "author-confirm", $packageId, $version)
if ([string]$confirmed.state -ne "confirmed") {
    throw "Workflow candidate was not confirmed."
}

$candidatePath = [string]$confirmed.candidate_path
$candidateLockPath = [string]$confirmed.lock_path
if (-not (Test-Path -LiteralPath $candidatePath -PathType Leaf)) {
    throw "Workflow candidate artifact is missing: $candidatePath"
}
if (-not (Test-Path -LiteralPath $candidateLockPath -PathType Leaf)) {
    throw "Workflow extension lock is missing: $candidateLockPath"
}

if ([string]::IsNullOrWhiteSpace($ArtifactName)) { $ArtifactName = "$packageId-$version.hmwf" }
if ([string]::IsNullOrWhiteSpace($LockName)) { $LockName = "$packageId-$version.extension-lock.json" }
if ([IO.Path]::GetFileName($ArtifactName) -ne $ArtifactName) { throw "ArtifactName must be a file name." }
if ([IO.Path]::GetFileName($LockName) -ne $LockName) { throw "LockName must be a file name." }

$artifactTarget = Join-Path $output $ArtifactName
$lockTarget = Join-Path $output $LockName
Copy-Item -LiteralPath $candidatePath -Destination $artifactTarget -Force
$artifactSha256 = (Get-FileHash -LiteralPath $artifactTarget -Algorithm SHA256).Hash.ToLowerInvariant()
if ($artifactSha256 -ne [string]$confirmed.candidate_sha256) {
    throw "Workflow candidate SHA-256 changed while copying the immutable artifact."
}

# 扩展锁里的根摘要必须指向最终制品字节，先发布清单、后签名都会让这条对不上。
$lock = Get-Content -LiteralPath $candidateLockPath -Raw -Encoding UTF8 | ConvertFrom-Json
$lock.root.sha256 = $artifactSha256
[IO.File]::WriteAllText($lockTarget, ($lock | ConvertTo-Json -Depth 100), [Text.UTF8Encoding]::new($false))

[ordered]@{
    passed = $true
    kind = "workflow"
    id = $packageId
    version = $version
    artifact_path = $artifactTarget
    artifact_name = $ArtifactName
    artifact_sha256 = $artifactSha256
    artifact_size = (Get-Item -LiteralPath $artifactTarget).Length
    lock_path = $lockTarget
    lock_name = $LockName
    lock_sha256 = (Get-FileHash -LiteralPath $lockTarget -Algorithm SHA256).Hash.ToLowerInvariant()
    test_report = $confirmed.test_report
} | ConvertTo-Json -Depth 20
