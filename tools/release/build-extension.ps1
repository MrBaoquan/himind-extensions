param(
    [Parameter(Mandatory = $true)][ValidateSet('plugin', 'skill')][string]$Kind,
    [Parameter(Mandatory = $true)][string]$ExtensionPath,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$source = (Resolve-Path (Join-Path $repoRoot $ExtensionPath)).Path
$outputRoot = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else {
    [IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory))
}
if (-not $source.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Extension path must stay inside the repository.' }
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null

$manifestName = if ($Kind -eq 'plugin') { 'plugin.json' } else { 'skill.json' }
$manifestPath = Join-Path $source $manifestName
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw "$manifestName was not found in $ExtensionPath" }
$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ([string]::IsNullOrWhiteSpace([string]$manifest.id) -or [string]::IsNullOrWhiteSpace([string]$manifest.version)) { throw "$manifestName must declare id and version." }

$safeName = ([string]$manifest.id) -replace '[^A-Za-z0-9._-]', '-'
$extension = if ($Kind -eq 'plugin') { '.hmpkg' } else { '.hmskill' }
$artifact = Join-Path $outputRoot "$safeName-$($manifest.version)$extension"
if ($Kind -eq 'skill') {
    Push-Location $repoRoot
    try { & go run ./tools/cmd/himind-skill-package -input $source -output $artifact }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'Skill packaging failed.' }
}
else {
    $staging = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-$([guid]::NewGuid().ToString('N'))"
    try {
        New-Item -ItemType Directory -Force -Path $staging | Out-Null
        $entry = [string]$manifest.entry
        if ([string]::IsNullOrWhiteSpace($entry) -or [IO.Path]::IsPathRooted($entry) -or $entry.Contains('..')) { throw 'Plugin entry is invalid.' }
        $binary = Join-Path $staging $entry
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $binary) | Out-Null
        Push-Location $repoRoot
        try { & go build -o $binary "./$($ExtensionPath.Replace('\', '/'))" }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin build failed.' }
        Copy-Item -LiteralPath $manifestPath -Destination (Join-Path $staging 'plugin.json') -Force
        $ui = Join-Path $source 'ui'
        if (Test-Path -LiteralPath $ui -PathType Container) { Copy-Item -LiteralPath $ui -Destination (Join-Path $staging 'ui') -Recurse -Force }
        Push-Location $repoRoot
        try { & go run ./tools/cmd/himind-plugin-package -path $staging -output $artifact }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'Plugin packaging failed.' }
    }
    finally {
        if ($staging -and (Test-Path -LiteralPath $staging)) { Remove-Item -LiteralPath $staging -Recurse -Force }
    }
}

$tag = "$Kind-$safeName-v$($manifest.version)"
$outputs = @(
    "artifact_path=$artifact",
    "artifact_name=$([IO.Path]::GetFileName($artifact))",
    "extension_id=$($manifest.id)",
    "version=$($manifest.version)",
    "release_tag=$tag"
)
if ($env:GITHUB_OUTPUT) { $outputs | Out-File -FilePath $env:GITHUB_OUTPUT -Encoding utf8 -Append }
else { $outputs | Write-Output }
