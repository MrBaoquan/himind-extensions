[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('plugin', 'skill')]
    [string]$Kind,
    [Parameter(Mandatory = $true)]
    [string]$ExtensionPath,
    [string]$Repository = 'MrBaoquan/himind-extensions',
    [string]$OutputDirectory = 'dist',
    [string]$PrivateKeyPath,
    [string]$SigningKeyId,
    [switch]$SkipTests,
    [switch]$SkipPush
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
Set-Location $repoRoot

function Resolve-Setting {
    param([string]$Value, [string]$EnvironmentName)
    if (-not [string]::IsNullOrWhiteSpace($Value)) { return $Value }
    return [Environment]::GetEnvironmentVariable($EnvironmentName, 'Process')
}

if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
    throw 'Repository must be a GitHub owner/repo.'
}
$PrivateKeyPath = Resolve-Setting $PrivateKeyPath 'HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH'
$SigningKeyId = Resolve-Setting $SigningKeyId 'HIMIND_EXTENSION_SIGNING_KEY_ID'
if ([string]::IsNullOrWhiteSpace($PrivateKeyPath) -or [string]::IsNullOrWhiteSpace($SigningKeyId)) {
    throw 'Extension publication requires HIMIND_EXTENSION_SIGNING_PRIVATE_KEY_PATH and HIMIND_EXTENSION_SIGNING_KEY_ID.'
}
if ($SigningKeyId -notmatch '^[A-Za-z0-9._-]+$') {
    throw 'SigningKeyId contains invalid characters.'
}
$PrivateKeyPath = [IO.Path]::GetFullPath($PrivateKeyPath)
if (-not (Test-Path -LiteralPath $PrivateKeyPath -PathType Leaf)) {
    throw "Private signing key not found: $PrivateKeyPath"
}

$source = (Resolve-Path (Join-Path $repoRoot $ExtensionPath)).Path
$relativeSource = $source.Substring($repoRoot.Length).TrimStart('\', '/').Replace('\', '/')
$expectedRoot = if ($Kind -eq 'plugin') { 'plugins/' } else { 'skills/' }
if (-not $relativeSource.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw "ExtensionPath must be inside $expectedRoot."
}
$manifestName = if ($Kind -eq 'plugin') { 'plugin.json' } else { 'skill.json' }
$manifestPath = Join-Path $source $manifestName
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw "$manifestName was not found in $ExtensionPath"
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ([string]::IsNullOrWhiteSpace([string]$manifest.id) -or
    [string]::IsNullOrWhiteSpace([string]$manifest.version) -or
    [string]::IsNullOrWhiteSpace([string]$manifest.release_notes)) {
    throw "$manifestName must declare id, version and release_notes."
}
$safeId = ([string]$manifest.id) -replace '[^A-Za-z0-9._-]', '-'
$version = [string]$manifest.version
if ($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9._-]+)?$') {
    throw "Invalid extension version: $version"
}
$tag = "$Kind-$safeId-v$version"

$gh = Get-Command gh -ErrorAction SilentlyContinue
if (-not $gh) { throw 'GitHub CLI (gh) is required.' }
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
$artifact = Join-Path $outputRoot "$safeId-$version$(if ($Kind -eq 'plugin') { '.hmpkg' } else { '.hmskill' })"
$signature = "$artifact.signature.json"

$existing = gh release view $tag --repo $Repository --json targetCommitish,publishedAt 2>$null
$releaseExists = $LASTEXITCODE -eq 0
$commit = (git rev-parse HEAD).Trim()
if ($releaseExists) {
    $release = ($existing -join "`n") | ConvertFrom-Json
    if ([string]$release.targetCommitish -ne $commit) {
        throw "Release $tag already exists for another source commit. Increase the extension version or use a dedicated repair procedure."
    }
    Write-Host "Reusing immutable Release $tag."
    $downloadRoot = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-existing-$([guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Force -Path $downloadRoot | Out-Null
    try {
        & gh release download $tag --repo $Repository --dir $downloadRoot --pattern ([IO.Path]::GetFileName($artifact)) --pattern ([IO.Path]::GetFileName($signature))
        if ($LASTEXITCODE -ne 0) { throw "Unable to download immutable Release $tag." }
        Copy-Item -LiteralPath (Join-Path $downloadRoot ([IO.Path]::GetFileName($artifact))) -Destination $artifact -Force
        Copy-Item -LiteralPath (Join-Path $downloadRoot ([IO.Path]::GetFileName($signature))) -Destination $signature -Force
    }
    finally {
        Remove-Item -LiteralPath $downloadRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
else {
    & (Join-Path $PSScriptRoot 'build-extension.ps1') -Kind $Kind -ExtensionPath $ExtensionPath -OutputDirectory $outputRoot
    if ($LASTEXITCODE -ne 0) { throw 'Extension packaging failed.' }
    & (Join-Path $PSScriptRoot 'sign-extension.ps1') -ArtifactPath $artifact -PrivateKeyPath $PrivateKeyPath -KeyId $SigningKeyId -OutputPath $signature
    if ($LASTEXITCODE -ne 0) { throw 'Extension signing failed.' }
    & gh release create $tag $artifact $signature --repo $Repository --title "$($manifest.id) v$version" --target $commit --generate-notes
    if ($LASTEXITCODE -ne 0) { throw "GitHub Release creation failed: $tag" }
}

$publishedAt = (gh release view $tag --repo $Repository --json publishedAt --jq .publishedAt).Trim()
if ($LASTEXITCODE -ne 0 -or $publishedAt -notmatch 'T') { throw "Unable to resolve publication time for $tag." }
& go run ./tools/cmd/himind-catalog-upsert `
    -kind $Kind `
    -source $ExtensionPath `
    -artifact $artifact `
    -signature $signature `
    -repository $Repository `
    -tag $tag `
    -generation "$commit-$tag" `
    -published-at $publishedAt `
    -catalog '.himind/catalog.json'
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

[pscustomobject]@{
    repository = $Repository
    tag = $tag
    id = [string]$manifest.id
    version = $version
    kind = $Kind
    artifact = [IO.Path]::GetFileName($artifact)
    signed = $true
    catalog = '.himind/catalog.json'
    pushed = (-not $SkipPush)
} | ConvertTo-Json
