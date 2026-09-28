param(
    [Parameter(Mandatory = $true)][ValidateSet('plugin', 'skill', 'workflow')][string]$Kind,
    [Parameter(Mandatory = $true)][string]$ExtensionPath,
    [Parameter(Mandatory = $true)][string]$OutputDirectory,
    [string]$Repository = '',
    [string]$Channel = 'stable',
    # 依赖所在仓的市场索引：跨分发依赖靠它 pin 到发货仓。缺省读 extensions.json
    # 的 dependency_catalogs。
    [string[]]$DependencyCatalog = @(),
    [string]$AgentExecutable = 'himind-agent',
    [string]$AgentProfile = ''
)

# 打包只管「把源码变成制品」：名字从 himind-release-plan 取，脚本不拼 tag 与文件名。
# 这样本地产出的制品名与安装器解析的名字来自同一份规则。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-config.ps1')
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$source = (Resolve-Path (Join-Path $repoRoot $ExtensionPath)).Path
$outputRoot = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else {
    [IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory))
}
if (-not $source.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Extension path must stay inside the repository.' }
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

$manifestName = switch ($Kind) {
    'plugin' { 'plugin.json' }
    'skill' { 'skill.json' }
    'workflow' { 'workflow.json' }
}
$manifestPath = Join-Path $source $manifestName
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw "$manifestName was not found in $ExtensionPath" }

Push-Location $repoRoot
try {
    $repoConfig = Read-ExtensionRepoConfig -RepoRoot $repoRoot
    $Repository = Resolve-ReleaseRepository -Config $repoConfig -Override $Repository
    # 发布计划与锁 pin 共用同一组定位参数：两个命令各自重新解析一遍依赖事实，
    # 清单里的 pin 与锁里的 pin 因此不会漂移。这里只放参数，不放 `go run <入口>`，
    # 否则第二个命令会把 `run` 当成第一个位置参数，后面所有开关都读不到。
    $planFlags = @(
        '-kind', $Kind,
        '-path', $ExtensionPath,
        '-repository', $Repository,
        '-channel', $Channel,
        '-catalog', (Join-Path $repoRoot '.himind/catalog.json')
    )
    $dependencyCatalogs = @(Resolve-DependencyCatalogs -Config $repoConfig -RepoRoot $repoRoot -Override $DependencyCatalog)
    foreach ($dependencyCatalog in $dependencyCatalogs) {
        $planFlags += @('-dependency-catalog', $dependencyCatalog)
    }
    # 锁 pin 的依赖定位与发布清单同源，只是额外给出本地制品候选目录：命中时不必
    # 从 GitHub 再下一遍（发布资产偶发超时不该让整次发布失败），摘要仍以清单为准。
    $pinFlags = $planFlags
    foreach ($artifactDir in @(Resolve-DependencyArtifactDirs -CatalogPaths $dependencyCatalogs -RepoRoot $repoRoot)) {
        $pinFlags += @('-artifact-dir', $artifactDir)
    }
    $planJson = & go run ./tools/cmd/himind-release-plan @planFlags
}
finally { Pop-Location }
if ($LASTEXITCODE -ne 0) { throw 'Release plan failed.' }
$plan = $planJson | ConvertFrom-Json

$artifact = Join-Path $outputRoot ([string]$plan.artifact_name)
$lock = Join-Path $outputRoot ([string]$plan.lock_name)
$artifactSha256 = ''
$lockSha256 = ''

if ($Kind -eq 'skill') {
    Push-Location $repoRoot
    try { & go run ./tools/cmd/himind-skill-package -input $source -output $artifact }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'Skill packaging failed.' }
}
elseif ($Kind -eq 'plugin') {
    $staging = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-$([guid]::NewGuid().ToString('N'))"
    try {
        New-Item -ItemType Directory -Force -Path $staging | Out-Null
        $pluginManifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
        $entry = [string]$pluginManifest.entry
        if ([string]::IsNullOrWhiteSpace($entry) -or [IO.Path]::IsPathRooted($entry) -or $entry.Contains('..')) { throw 'Plugin entry is invalid.' }
        $binary = Join-Path $staging $entry
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $binary) | Out-Null
        # 先测试再构建，与 Agent 侧 extension.plugin.build 的语义一致：能打包就必须
        # 先通过测试，避免「本地制品是好的、发布制品是坏的」这种只在发布后才暴露的差异。
        Push-Location $source
        try { & go test ./... }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin tests failed.' }
        Push-Location $repoRoot
        try { & go build -o $binary "./$($ExtensionPath.Replace('\', '/'))" }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin build failed.' }
        # 随包文件按 pluginpack 的排除法整份摊平：这里曾经是「plugin.json + ui/ +
        # miniprogram-ci-runner.js」三行白名单，作者新增的运行期文件会被静默丢掉。
        Push-Location $repoRoot
        try { & go run ./tools/cmd/himind-plugin-stage -path $source -output $staging -entry $entry }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin staging failed.' }
        Push-Location $repoRoot
        try { & go run ./tools/cmd/himind-plugin-package -path $staging -output $artifact }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin packaging failed.' }
    }
    finally {
        if ($staging -and (Test-Path -LiteralPath $staging)) { Remove-Item -LiteralPath $staging -Recurse -Force }
    }
}
else {
    $workflowResult = & (Join-Path $PSScriptRoot 'build-workflow-release.ps1') `
        -AgentExecutable $AgentExecutable `
        -AgentProfile $AgentProfile `
        -SourcePath $source `
        -OutputDirectory $outputRoot `
        -ArtifactName ([string]$plan.artifact_name) `
        -LockName ([string]$plan.lock_name)
    if ($LASTEXITCODE -ne 0) { throw 'Workflow packaging failed.' }
    $workflow = $workflowResult | ConvertFrom-Json
    $artifactSha256 = [string]$workflow.artifact_sha256
    $lockSha256 = [string]$workflow.lock_sha256
    # 锁里的依赖摘要必须来自依赖**制品**的字节。作者机器上的本地扩展源目录还留着
    # 源码、历史制品与安装期状态，按那份目录算出的摘要与别人从制品安装后算出的值
    # 不同，组织分发时会被判成 “content changed”。依赖定位参数与发布清单同源，
    # 锁里记的版本与来源因此不会与清单漂移。
    Push-Location $repoRoot
    try {
        & go run ./tools/cmd/himind-lock-pin -lock $lock @pinFlags | Out-Null
    }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'Workflow extension lock pin failed.' }
    $lockSha256 = (Get-FileHash -LiteralPath $lock -Algorithm SHA256).Hash.ToLowerInvariant()
}

if (-not (Test-Path -LiteralPath $artifact -PathType Leaf)) {
    throw "Packaging did not create the expected artifact: $artifact"
}
if (-not $artifactSha256) {
    $artifactSha256 = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
}
if ($Kind -eq 'workflow') {
    if (-not (Test-Path -LiteralPath $lock -PathType Leaf)) {
        throw "Workflow packaging did not create the expected extension lock: $lock"
    }
    if (-not $lockSha256) {
        $lockSha256 = (Get-FileHash -LiteralPath $lock -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

$outputs = @(
    "artifact_path=$artifact",
    "artifact_name=$([string]$plan.artifact_name)",
    "artifact_sha256=$artifactSha256",
    "extension_id=$([string]$plan.id)",
    "version=$([string]$plan.version)",
    "release_tag=$([string]$plan.tag)",
    "manifest_name=$([string]$plan.manifest_name)"
)
if ($Kind -eq 'workflow') {
    $outputs += "lock_path=$lock"
    $outputs += "lock_name=$([string]$plan.lock_name)"
    $outputs += "lock_sha256=$lockSha256"
}
if ($env:GITHUB_OUTPUT) { $outputs | Out-File -FilePath $env:GITHUB_OUTPUT -Encoding utf8 -Append }
else { $outputs | Write-Output }
