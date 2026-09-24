[CmdletBinding()]
param(
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
    [string[]]$Only
)

# 全量重建分发：按扩展清单的顺序逐条发布。
#   顺序固定为 plugin → skill → workflow，因为工作流的依赖 pin 要求被依赖项
#   已经在市场索引里有规范发布；打乱顺序会把「依赖未发布」当成发布失败。
# 单条发布的事实全部来自 publish-extension.ps1，本脚本只负责排序、传参与汇总。

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
Set-Location $repoRoot

$config = Get-Content -LiteralPath (Join-Path $repoRoot 'extensions.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$kindOrder = @{ plugin = 0; skill = 1; workflow = 2 }
$ordered = @($config.extensions | Sort-Object { $kindOrder[[string]$_.type] })

if ($Only.Count -gt 0) {
    $wanted = @($Only | ForEach-Object { $_.Trim() })
    $ordered = @($ordered | Where-Object {
        $wanted -contains [string]$_.id -or $wanted -contains [string]$_.path
    })
    if ($ordered.Count -eq 0) { throw "Only 未匹配到任何扩展：$($Only -join ', ')" }
}

$common = @{
    Repository      = $Repository
    OutputDirectory = $OutputDirectory
    Channel         = $Channel
    AgentExecutable = $AgentExecutable
    AgentProfile    = $AgentProfile
}
foreach ($name in @('PrivateKeyPath', 'SigningKeyId')) {
    $value = Get-Variable -Name $name -ValueOnly
    if (-not [string]::IsNullOrWhiteSpace($value)) { $common[$name] = $value }
}
foreach ($name in @('SkipTests', 'SkipPush', 'AllowDirty', 'AllowVersionReuse')) {
    if ((Get-Variable -Name $name -ValueOnly)) { $common[$name] = $true }
}

$results = @()
$failures = @()
$publish = Join-Path $PSScriptRoot 'publish-extension.ps1'

foreach ($extension in $ordered) {
    $kind = [string]$extension.type
    $path = [string]$extension.path
    Write-Host ''
    Write-Host "=== $kind $path ==="
    try {
        $raw = & $publish -Kind $kind -ExtensionPath $path @common
        if ($LASTEXITCODE -ne 0) { throw "publish-extension.ps1 退出码 $LASTEXITCODE" }
        $result = ($raw -join "`n").Trim() | ConvertFrom-Json
        $results += $result
        Write-Host "OK  $($result.tag)"
    }
    catch {
        $failures += [pscustomobject]@{ kind = $kind; path = $path; error = $_.Exception.Message }
        Write-Host "FAIL $kind $path`n     $($_.Exception.Message)"
        break
    }
}

Write-Host ''
Write-Host '--- 发布汇总 ---'
foreach ($result in $results) {
    Write-Host ("{0,-8} {1,-52} {2}" -f $result.kind, $result.id, $result.version)
}

if ($failures.Count -gt 0) {
    Write-Host ''
    Write-Host '--- 失败 ---'
    $failures | Format-List | Out-String | Write-Host
    [pscustomobject]@{
        published = $results.Count
        failed    = $failures
        results   = $results
    } | ConvertTo-Json -Depth 10
    exit 1
}

Write-Host ''
Write-Host "已发布 $($results.Count) 个扩展。"
[pscustomobject]@{
    published = $results.Count
    results   = $results
} | ConvertTo-Json -Depth 10
