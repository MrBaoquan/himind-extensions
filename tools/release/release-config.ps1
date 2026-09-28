# 发布链路共用的仓配置：发布到哪个仓、依赖所在的索引在哪。
#
# 这两件事由仓自己的 extensions.json 声明，而不是让每个调用方逐个记住传参。
# 打包与发布读同一份声明，才不会出现「打包过了、发布却因为依赖解析失败而中断」
# 这种半途情况。

# ConvertTo-OwnerRepo 把 git 远端写法（https / ssh）归一成 GitHub 的 owner/repo。
function ConvertTo-OwnerRepo {
    param([string]$Value)

    $text = ([string]$Value).Trim() -replace '\.git$', ''
    $text = $text -replace '^https://github\.com/', ''
    $text = $text -replace '^http://github\.com/', ''
    $text = $text -replace '^git@github\.com:', ''
    return $text.Trim('/')
}

# Read-ExtensionRepoConfig 读取本仓的 extensions.json。
function Read-ExtensionRepoConfig {
    param([Parameter(Mandatory = $true)][string]$RepoRoot)

    return (Get-Content -LiteralPath (Join-Path $RepoRoot 'extensions.json') -Raw -Encoding UTF8 | ConvertFrom-Json)
}

# Resolve-ReleaseRepository 决定这次发布写到哪个 GitHub 仓：显式参数优先，其次
# 取本仓 extensions.json 的 repository。脚本因此不必内置某个仓的名字。
function Resolve-ReleaseRepository {
    param(
        [Parameter(Mandatory = $true)]$Config,
        [string]$Override = ''
    )

    $repository = [string]$Override
    if ([string]::IsNullOrWhiteSpace($repository)) {
        $repository = ConvertTo-OwnerRepo ([string]$Config.repository)
    }
    if ($repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
        throw 'Repository must be a GitHub owner/repo. Pass -Repository, or declare repository in extensions.json.'
    }
    return $repository
}

# Resolve-DependencyCatalogs 汇总依赖仓索引的绝对路径：显式参数在前，其次是本仓
# extensions.json 的 dependency_catalogs。声明了却不存在就报错——少克隆一个仓
# 不该让必需依赖退化成「索引为空」那种更难读的报错。
function Resolve-DependencyCatalogs {
    param(
        [Parameter(Mandatory = $true)]$Config,
        [Parameter(Mandatory = $true)][string]$RepoRoot,
        [string[]]$Override = @()
    )

    $declared = @()
    foreach ($entry in @($Override)) {
        if (-not [string]::IsNullOrWhiteSpace([string]$entry)) { $declared += [string]$entry }
    }
    foreach ($entry in @($Config.dependency_catalogs)) {
        if (-not [string]::IsNullOrWhiteSpace([string]$entry)) { $declared += [string]$entry }
    }
    $resolved = @()
    foreach ($entry in $declared) {
        $file = [string]$entry
        if (-not [IO.Path]::IsPathRooted($file)) { $file = Join-Path $RepoRoot $file }
        $file = [IO.Path]::GetFullPath($file)
        if (-not (Test-Path -LiteralPath $file -PathType Leaf)) {
            throw "Dependency catalog not found: $file. Clone the dependency repository, or pass -DependencyCatalog."
        }
        $resolved += $file
    }
    return $resolved
}
