[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('plugin', 'skill', 'workflow')]
    [string]$Kind,
    [Parameter(Mandatory = $true)]
    [string]$ExtensionPath,
    [string]$Repository = 'MrBaoquan/himind-extensions',
    [string]$OutputDirectory = 'dist',
    [string]$Channel = '',
    [string]$PrivateKeyPath,
    [string]$SigningKeyId,
    [string]$AgentExecutable = 'himind-agent',
    [string]$AgentProfile = '',
    [switch]$SkipTests,
    [switch]$SkipPush,
    [switch]$AllowDirty,
    [switch]$AllowVersionReuse,
    [string]$ResultPath = ''
)

# 一次发布的完整交付：
#   himind-release-plan 定名字与依赖 pin → build 出制品 → 签名 → 写 `<id>@<version>.json`
#   → 创建 Release（制品 + 清单 [+ 工作流锁]）→ 增补市场索引 → 提交索引。
# 名字、摘要、依赖、签名都来自同一份 Plan 与同一个制品字节，脚本只搬运不判断。

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
Set-Location $repoRoot

function Resolve-Setting {
    param([string]$Value, [string]$EnvironmentName)
    if (-not [string]::IsNullOrWhiteSpace($Value)) { return $Value }
    return [Environment]::GetEnvironmentVariable($EnvironmentName, 'Process')
}

# 发布结果是一份机器可读的交付事实。调用方（例如 publish-all.ps1）不应该去解析
# 本脚本的完整输出：go test、仓库校验、gh 都会往同一条输出流里写字。
function Write-Summary {
    param([Parameter(Mandatory = $true)]$Value)

    $json = $Value | ConvertTo-Json -Depth 10
    if (-not [string]::IsNullOrWhiteSpace($ResultPath)) {
        $resultFile = if ([IO.Path]::IsPathRooted($ResultPath)) {
            [IO.Path]::GetFullPath($ResultPath)
        } else {
            [IO.Path]::GetFullPath((Join-Path $repoRoot $ResultPath))
        }
        $parent = Split-Path -Parent $resultFile
        if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }
        [IO.File]::WriteAllText($resultFile, $json, [Text.UTF8Encoding]::new($false))
    }
    Write-Output $json
}

function Invoke-ReleasePlan {
    param([string[]]$Arguments)

    $output = & go run ./tools/cmd/himind-release-plan @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Release plan failed: $($Arguments -join ' ')" }
    return ($output -join "`n") | ConvertFrom-Json
}

# CanonicalVersion 只认规范 tag 的索引条目：历史扁平 tag 不参与版本比较，
# 否则「重做分发」时会被旧命名拦住。
function Get-LatestCanonicalVersion {
    param([string]$CatalogPath, [string]$Kind, [string]$ID)

    if (-not (Test-Path -LiteralPath $CatalogPath -PathType Leaf)) { return '' }
    $catalog = Get-Content -LiteralPath $CatalogPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $entries = switch ($Kind) {
        'plugin' { @($catalog.plugins) }
        'skill' { @($catalog.skills) }
        default { @($catalog.workflows) }
    }
    $idField = switch ($Kind) {
        'plugin' { 'plugin_id' }
        'skill' { 'skill_id' }
        default { 'workflow_id' }
    }
    $versions = @()
    foreach ($entry in $entries) {
        if ([string]$entry.$idField -ne $ID) { continue }
        $version = [string]$entry.version
        if ([string]$entry.release_tag -eq "$Kind/$ID@$version") { $versions += $version }
    }
    if ($versions.Count -eq 0) { return '' }
    return [string]($versions | Sort-Object { [version](($_ -replace '[-+].*$', '')) } -Descending | Select-Object -First 1)
}

if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
    throw 'Repository must be a GitHub owner/repo.'
}
$PrivateKeyPath = Resolve-Setting $PrivateKeyPath 'HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH'
$SigningKeyId = Resolve-Setting $SigningKeyId 'HIMIND_EXTENSION_SIGNING_KEY_ID'

$source = (Resolve-Path (Join-Path $repoRoot $ExtensionPath)).Path
$relativeSource = $source.Substring($repoRoot.Length).TrimStart('\', '/').Replace('\', '/')
$expectedRoot = switch ($Kind) {
    'plugin' { 'plugins/' }
    'skill' { 'skills/' }
    'workflow' { 'workflows/' }
}
if (-not $relativeSource.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw "ExtensionPath must be inside $expectedRoot."
}
$manifestName = switch ($Kind) {
    'plugin' { 'plugin.json' }
    'skill' { 'skill.json' }
    'workflow' { 'workflow.json' }
}
$manifestPath = Join-Path $source $manifestName
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw "$manifestName was not found in $ExtensionPath"
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ([string]::IsNullOrWhiteSpace([string]$manifest.id) -or
    [string]::IsNullOrWhiteSpace([string]$manifest.version) -or
    ($Kind -ne 'workflow' -and [string]::IsNullOrWhiteSpace([string]$manifest.release_notes))) {
    throw "$manifestName must declare id, version and required release_notes."
}
$version = [string]$manifest.version
if ($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9._-]+)?$') {
    throw "Invalid extension version: $version"
}

# 分发落点由扩展清单声明，脚本只按声明执行。清单不声明就报错，避免
# 「忘了写」被当成「按默认发」，让制品悄悄走到作者没选的落点上。
$targets = @()
if ($null -ne $manifest.distribution_targets) {
    $targets = @($manifest.distribution_targets | ForEach-Object { ([string]$_).Trim().ToLowerInvariant() })
}
if ($targets.Count -eq 0) {
    throw "$manifestName must declare distribution_targets: workbench, github or both."
}
$unknownTargets = @($targets | Where-Object { $_ -ne 'workbench' -and $_ -ne 'github' })
if ($unknownTargets.Count -gt 0) {
    throw "Unsupported distribution_targets: $($unknownTargets -join ', ')."
}
if (@($targets | Select-Object -Unique).Count -ne $targets.Count) {
    throw 'distribution_targets must not repeat a target.'
}
$allowGithub = $targets -contains 'github'
$allowWorkbench = $targets -contains 'workbench'

$extensionsConfig = Get-Content -LiteralPath (Join-Path $repoRoot 'extensions.json') -Raw -Encoding UTF8 | ConvertFrom-Json
if ([string]::IsNullOrWhiteSpace($Channel)) { $Channel = [string]$extensionsConfig.channel }
if ([string]::IsNullOrWhiteSpace($Channel)) { $Channel = 'stable' }

# 发布前先算计划：命名、依赖 pin、以及「必需依赖还没发布」这类前置条件都在这里拦下。
$catalogPath = Join-Path $repoRoot '.himind/catalog.json'
$plan = Invoke-ReleasePlan -Arguments @(
    '-kind', $Kind,
    '-path', $relativeSource,
    '-repository', $Repository,
    '-channel', $Channel,
    '-catalog', $catalogPath
)
$tag = [string]$plan.tag

# A release is an immutable hand-off from a clean source tree. An explicit
# override is available for emergency/internal builds, but never the default.
$status = @(git status --porcelain --untracked-files=all)
if ($status.Count -gt 0 -and -not $AllowDirty) {
    throw '工作区存在未提交修改，发布前请先提交或使用 -AllowDirty 明确确认临时发布。'
}

$gh = Get-Command gh -ErrorAction SilentlyContinue
if ($allowGithub -and -not $gh) { throw 'GitHub CLI (gh) is required.' }
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) { throw 'Go is required to publish extensions.' }
if (-not $SkipTests) {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Extension repository tests failed.' }
    & go run ./tools/cmd/himind-repo-check
    if ($LASTEXITCODE -ne 0) { throw 'Extension repository validation failed.' }
}

$outputRoot = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else {
    [IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory))
}
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$artifact = Join-Path $outputRoot ([string]$plan.artifact_name)
$manifestFile = Join-Path $outputRoot ([string]$plan.manifest_name)
# 签名只进发布清单的 signature 字段，中间文件放临时目录，dist 里只留交付物。
$signature = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-signature-$([guid]::NewGuid().ToString('N')).json"
$lock = Join-Path $outputRoot ([string]$plan.lock_name)
$commit = (git rev-parse HEAD).Trim()
$releaseAssets = @($artifact, $manifestFile)

function Resolve-ReleaseCommit {
    param([string]$ReleaseTag)

    $ref = gh api "repos/$Repository/git/ref/tags/$ReleaseTag" 2>$null | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $null -eq $ref.object.sha) {
        throw "Unable to resolve GitHub Release tag: $ReleaseTag"
    }
    if ($ref.object.type -eq 'tag') {
        $tagObject = gh api "repos/$Repository/git/tags/$($ref.object.sha)" 2>$null | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or $null -eq $tagObject.object.sha) {
            throw "Unable to resolve annotated GitHub Release tag: $ReleaseTag"
        }
        return [string]$tagObject.object.sha
    }
    return [string]$ref.object.sha
}

# Release 绑定的是「扩展源码的字节」，不是整个仓库的 HEAD。工具脚本或其它扩展
# 的提交不应该让一个内容未变的版本被判定为必须重新发布。
function Resolve-SourceTree {
    param([string]$Revision, [string]$Path)

    $tree = (git rev-parse "$Revision`:$Path" 2>$null).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($tree)) {
        throw "Unable to resolve source tree for $Path at $Revision."
    }
    return $tree
}

$releaseExists = $false
if ($allowGithub) {
    gh release view $tag --repo $Repository --json publishedAt 2>$null | Out-Null
    $releaseExists = $LASTEXITCODE -eq 0
}

if ($releaseExists) {
    # 同一 tag 只允许对应同一份扩展源码：Release 是不可变交付，复用不等于重发。
    $releaseCommit = Resolve-ReleaseCommit $tag
    $releaseSourceTree = Resolve-SourceTree $releaseCommit $relativeSource
    $currentSourceTree = Resolve-SourceTree $commit $relativeSource
    if ($releaseSourceTree -ne $currentSourceTree) {
        throw "Release $tag already exists for $relativeSource at $releaseCommit ($releaseSourceTree), but HEAD has $currentSourceTree. Increase the extension version or restore the original source."
    }
    Write-Host "Reusing immutable Release $tag."
    $downloadRoot = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-existing-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Force -Path $downloadRoot | Out-Null
    try {
        $downloadArguments = @('release', 'download', $tag, '--repo', $Repository, '--dir', $downloadRoot)
        foreach ($asset in $releaseAssets) {
            $downloadArguments += @('--pattern', [IO.Path]::GetFileName($asset))
        }
        if ($Kind -eq 'workflow') { $downloadArguments += @('--pattern', [string]$plan.lock_name) }
        & gh @downloadArguments
        if ($LASTEXITCODE -ne 0) { throw "Unable to download immutable Release $tag." }
        foreach ($asset in $releaseAssets) {
            Copy-Item -LiteralPath (Join-Path $downloadRoot ([IO.Path]::GetFileName($asset))) -Destination $asset -Force
        }
        if ($Kind -eq 'workflow') {
            Copy-Item -LiteralPath (Join-Path $downloadRoot ([string]$plan.lock_name)) -Destination $lock -Force
        }
    }
    finally {
        Remove-Item -LiteralPath $downloadRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
else {
    if ($allowGithub) {
        $latest = Get-LatestCanonicalVersion -CatalogPath $catalogPath -Kind $Kind -ID ([string]$manifest.id)
        if ($latest -and -not $AllowVersionReuse) {
            if ([version](($version -replace '[-+].*$', '')) -le [version]$latest) {
                throw "版本 $version 未高于索引中的现有版本 $latest，请提升版本后再发布。"
            }
        }
    }
    if ([string]::IsNullOrWhiteSpace($PrivateKeyPath) -or [string]::IsNullOrWhiteSpace($SigningKeyId)) {
        throw 'Creating a new extension Release requires HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH and HIMIND_EXTENSION_SIGNING_KEY_ID.'
    }
    if ($SigningKeyId -notmatch '^[A-Za-z0-9._-]+$') {
        throw 'SigningKeyId contains invalid characters.'
    }
    $PrivateKeyPath = [IO.Path]::GetFullPath($PrivateKeyPath)
    if (-not (Test-Path -LiteralPath $PrivateKeyPath -PathType Leaf)) {
        throw "Private signing key not found: $PrivateKeyPath"
    }
    & (Join-Path $PSScriptRoot 'build-extension.ps1') `
        -Kind $Kind `
        -ExtensionPath $relativeSource `
        -OutputDirectory $outputRoot `
        -Repository $Repository `
        -Channel $Channel `
        -AgentExecutable $AgentExecutable `
        -AgentProfile $AgentProfile | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Extension packaging failed.' }
    & (Join-Path $PSScriptRoot 'sign-extension.ps1') -ArtifactPath $artifact -PrivateKeyPath $PrivateKeyPath -KeyId $SigningKeyId -OutputPath $signature
    if ($LASTEXITCODE -ne 0) { throw 'Extension signing failed.' }

    # 发布清单写盘前会校验：制品名是规范名、签名与制品逐字节一致、必需依赖已 pin 到确定版本。
    & go run ./tools/cmd/himind-release-plan `
        -kind $Kind `
        -path $relativeSource `
        -repository $Repository `
        -channel $Channel `
        -catalog $catalogPath `
        -artifact $artifact `
        -signature $signature `
        -manifest-out $manifestFile `
        -source-commit $commit | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Release manifest generation failed.' }
    Remove-Item -LiteralPath $signature -Force -ErrorAction SilentlyContinue
    if ($Kind -eq 'workflow') {
        if (-not (Test-Path -LiteralPath $lock -PathType Leaf)) {
            throw "Workflow extension lock was not produced: $lock"
        }
        $releaseAssets += $lock
    }
    if ($allowGithub) {
        & gh release create $tag @releaseAssets --repo $Repository --title "$($manifest.id) v$version" --target $commit --generate-notes
        if ($LASTEXITCODE -ne 0) { throw "GitHub Release creation failed: $tag" }
    }
    else {
        Write-Host "已声明只发工作台：生成本地签名制品与发布清单，不创建 GitHub Release。"
    }
}

# 只发工作台时索引里没有可定位的 Release 资产，条目由 Agent 扩展工作区提交审核。
if (-not $allowGithub) {
    Write-Summary ([pscustomobject]@{
        repository = $Repository
        id = [string]$manifest.id
        version = $version
        kind = $Kind
        artifact = [IO.Path]::GetFileName($artifact)
        release_manifest = [IO.Path]::GetFileName($manifestFile)
        lock = if ($Kind -eq 'workflow') { [IO.Path]::GetFileName($lock) } else { $null }
        distribution_targets = $targets
        github_tag = $null
        next_step = '在 Agent 扩展工作区提交该候选版本，由组织审核后对内分发。'
    })
    return
}

$publishedAt = (gh release view $tag --repo $Repository --json publishedAt --jq .publishedAt).Trim()
if ($LASTEXITCODE -ne 0 -or $publishedAt -notmatch 'T') { throw "Unable to resolve publication time for $tag." }
$sourceTree = Resolve-SourceTree $commit $relativeSource

$catalogArguments = @(
    'run', './tools/cmd/himind-catalog-upsert',
    '-kind', $Kind,
    '-source', $relativeSource,
    '-release-manifest', $manifestFile,
    '-artifact', $artifact,
    '-repository', $Repository,
    '-catalog', '.himind/catalog.json',
    '-published-at', $publishedAt,
    '-source-tree', $sourceTree
)
if ($Kind -eq 'workflow') { $catalogArguments += @('-lock', $lock) }
& go @catalogArguments
if ($LASTEXITCODE -ne 0) { throw 'Public extension catalog update failed.' }

git add -- .himind/catalog.json
git diff --cached --quiet -- .himind/catalog.json
$catalogChanged = $LASTEXITCODE -ne 0
if ($catalogChanged) {
    git commit -m "catalog: publish $($manifest.id) v$version"
    if ($LASTEXITCODE -ne 0) { throw 'Catalog commit failed.' }
    if (-not $SkipPush) {
        git push origin HEAD:main
        if ($LASTEXITCODE -ne 0) { throw 'Catalog push failed.' }
    }
}

Write-Summary ([pscustomobject]@{
    repository = $Repository
    tag = $tag
    id = [string]$manifest.id
    version = $version
    kind = $Kind
    artifact = [IO.Path]::GetFileName($artifact)
    release_manifest = [IO.Path]::GetFileName($manifestFile)
    signed = $true
    reused_release = $releaseExists
    catalog = '.himind/catalog.json'
    dependencies = @($plan.dependencies)
    distribution_targets = $targets
    workbench_submission = if ($allowWorkbench) { 'pending' } else { $null }
    pushed = (-not $SkipPush)
})
