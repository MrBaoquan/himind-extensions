param(
  [string]$IOStudioDll = "",
  [string]$MotionComposerExe = "",
  [string]$OutputDir = ""
)
$ErrorActionPreference = "Stop"
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$repoRoot = [IO.Path]::GetFullPath((Join-Path $root "..\.."))
if ([string]::IsNullOrWhiteSpace($MotionComposerExe)) { $MotionComposerExe = Join-Path $repoRoot "plugins\motion-composer\bin\motion-composer.exe" }
if ([string]::IsNullOrWhiteSpace($IOStudioDll)) { throw "请通过 -IOStudioDll 指定本机 IOStudio.dll" }
if ([string]::IsNullOrWhiteSpace($OutputDir)) { $OutputDir = Join-Path $repoRoot ".tmp\video-to-motion-e2e" }
$OutputDir = [IO.Path]::GetFullPath($OutputDir)
$specDir = Join-Path $OutputDir "specs"
$motionDir = Join-Path $OutputDir "motion"
New-Item -ItemType Directory -Force -Path $specDir,$motionDir | Out-Null

function Get-Sha256([string]$Path) {
  return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}
function Get-StringSha256([string]$Text) {
  $sha = [Security.Cryptography.SHA256]::Create()
  try { return ([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Text)))).Replace("-", "").ToLowerInvariant() } finally { $sha.Dispose() }
}
function Invoke-Rpc([string]$Method, [hashtable]$Params, [int]$Id) {
  $request = @{ jsonrpc = "2.0"; id = $Id; method = $Method; params = $Params } | ConvertTo-Json -Depth 20 -Compress
  $responseText = $request | & $MotionComposerExe
  if ($LASTEXITCODE -ne 0) { throw "motion-composer failed for $Method" }
  $response = $responseText | ConvertFrom-Json
  if ($null -ne $response.error) { throw "$Method failed: $($response.error.message)" }
  return $response.result
}

if (-not (Test-Path -LiteralPath $IOStudioDll)) { throw "IOStudio DLL not found: $IOStudioDll" }
if (-not (Test-Path -LiteralPath $MotionComposerExe)) { throw "motion-composer not found: $MotionComposerExe" }

$sourceVideo = Join-Path $OutputDir "source.mp4"
if (-not (Test-Path -LiteralPath $sourceVideo)) {
  & ffmpeg -y -v error -f lavfi -i "color=c=blue:s=320x180:d=8" -pix_fmt yuv420p $sourceVideo
  if ($LASTEXITCODE -ne 0) { throw "ffmpeg failed to create dry-run video" }
}
$probe = (& ffprobe -v error -show_entries format=duration,size -show_entries stream=width,height,r_frame_rate -of json $sourceVideo) | ConvertFrom-Json
$videoStream = $probe.streams | Where-Object { $_.width } | Select-Object -First 1
$num,$den = (($videoStream.r_frame_rate -split "/") + "1")[0..1]
$fps = [double]$num / [double]$den
$durationMs = [math]::Round(([double]$probe.format.duration) * 1000)
$sourceHash = Get-Sha256 $sourceVideo

$mediaEvidence = @{
  schema_version = 1
  source = @{ path = "source.mp4"; sha256 = $sourceHash; duration_ms = $durationMs; fps = $fps; width = [int]$videoStream.width; height = [int]$videoStream.height }
  proxy_path = $null
  projection = @{ mode = "flat" }
  assets = @{ contact_sheet = $null; keyframes = @() }
  signals = @{ scene = @{}; loudness = @{}; transcript = $null }
  candidates = @(@{ time_ms = 1000; kind = "drop"; evidence = "dry-run fixture cue"; confidence = 0.9 })
  notes = @("Synthetic 8s dry-run clip; field acceptance must use the real film.")
}
$mediaEvidence | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $OutputDir "media-evidence.json") -Encoding utf8

$behaviorPath = Join-Path $OutputDir "behavior-timeline.json"
$behavior = Get-Content -Encoding UTF8 -LiteralPath (Join-Path $root "examples\ydl-neutral0.behavior-timeline.json") -Raw | ConvertFrom-Json
$behavior.source.path = "source.mp4"
$behavior.source.sha256 = $sourceHash
$behavior | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $behaviorPath -Encoding utf8

$binding = Invoke-Rpc "motion.composer.platform.resolve" @{
  workspace_root = $root
  platform_profile_path = "profiles/mp-ydl-heave-v1.json"
  motion_intent_path = "examples/motion-intent.ydl.json"
  device_profile_path = "examples/device-profile.ydl.json"
} 1
if (-not $binding.ok -or $binding.overall_status -ne "exact") { throw "platform binding is not exact" }
$binding | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $OutputDir "platform-binding.json") -Encoding utf8

$variants = @("gentle","normal","intense")
$specPaths = @()
foreach ($variant in $variants) {
  $target = Join-Path $specDir "ydl-neutral0.$variant.spec.json"
  Copy-Item -LiteralPath (Join-Path $root "tests\e2e\ydl-neutral0.$variant.spec.json") -Destination $target -Force
  $specPaths += $target
}

$compare = Invoke-Rpc "motion.composer.compare" @{
  workspace_root = $repoRoot
  spec_paths = $specPaths
  behavior_timeline_path = $behaviorPath
  device_profile_path = "workflows/video-to-motion/examples/device-profile.ydl.json"
} 2
if (-not $compare.ok) { throw "motion-composer compare failed" }

$auditSet = @{
  schema_version = 1
  ok = $compare.ok
  same_cue_timing = $compare.same_cue_timing
  binding_status = $binding.overall_status
  audits = @{}
  compare = $compare
  blocking_issues = @()
  revision_summary = "dry-run fixtures passed without revision"
}
for ($i = 0; $i -lt $variants.Count; $i++) { $auditSet.audits[$variants[$i]] = $compare.reports[$i] }
$auditSet | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath (Join-Path $OutputDir "motion-audit-set.json") -Encoding utf8

$specSetVariants = @()
$deliveryVariants = @()
for ($i = 0; $i -lt $variants.Count; $i++) {
  $variant = $variants[$i]
  $specPath = $specPaths[$i]
  $motionPath = Join-Path $motionDir "ydl-neutral0.$variant.motion"
  & dotnet $IOStudioDll --motion-build $specPath --out $motionPath | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "IOStudio build failed: $variant" }
  & dotnet $IOStudioDll --motion $motionPath --validate --status-file (Join-Path $OutputDir "$variant.validate-status.json") | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "IOStudio validate failed: $variant" }
  & dotnet $IOStudioDll --motion $motionPath --dump-tracks --status-file (Join-Path $OutputDir "$variant.tracks-status.json") | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "IOStudio tracks failed: $variant" }
  $motion = Get-Content -Encoding UTF8 -LiteralPath $motionPath -Raw | ConvertFrom-Json
  $prov = $motion.PSObject.Properties['provenance'].Value
  $motionPlatformId = if ($null -eq $prov) { '' } else { [string]$prov.platform_profile_id }
  $motionPlatformHash = if ($null -eq $prov) { '' } else { [string]$prov.platform_profile_sha256 }
  $bindingPlatformHash = [string]$binding.platform_profile_sha256
  if ($motionPlatformId -ne "mp-ydl-heave-v1" -or $motionPlatformHash -ne $bindingPlatformHash) { throw "provenance mismatch: $variant motion=$motionPlatformId/$motionPlatformHash binding=$bindingPlatformHash" }
  $spec = Get-Content -Encoding UTF8 -LiteralPath $specPath -Raw | ConvertFrom-Json
  $cueTimingHash = Get-StringSha256 (($spec.cues | ConvertTo-Json -Depth 10 -Compress))
  $specHash = Get-Sha256 $specPath
  $motionHash = Get-Sha256 $motionPath
  $specSetVariants += @{ id = $variant; spec_path = "specs/$([IO.Path]::GetFileName($specPath))"; spec_sha256 = $specHash; cue_timing_hash = $cueTimingHash; peak_heave = $compare.reports[$i].summary.peak_heave; max_drop_rate = $compare.reports[$i].summary.max_down_rate_per_second }
  $deliveryVariants += @{ id = $variant; spec_sha256 = $specHash; motion_sha256 = $motionHash; motion_path = "motion/$([IO.Path]::GetFileName($motionPath))"; validation_ok = $true; tracks_ok = $true; provenance_platform_profile_id = $motionPlatformId; provenance_platform_profile_sha256 = $motionPlatformHash }
}

$specSet = @{ schema_version = 1; platform_profile_id = $binding.platform_profile_id; platform_profile_sha256 = $binding.platform_profile_sha256; duration_ms = 7000; variants = $specSetVariants; binding_status = $binding.overall_status; notes = @("YDL neutral=0 dry-run") }
$specSet | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $OutputDir "motion-spec-set.json") -Encoding utf8

$delivery = @{
  schema_version = 1
  status = "ready"
  source_sha256 = $sourceHash
  platform_profile_id = $binding.platform_profile_id
  platform_profile_sha256 = $binding.platform_profile_sha256
  device_profile_sha256 = Get-Sha256 (Join-Path $root "examples\device-profile.ydl.json")
  active_command = $null
  binding_status = $binding.overall_status
  variants = $deliveryVariants
  manual_acceptance = $null
  checksums_path = "checksums.sha256"
  notes = @("Synthetic dry-run only. Real film and device acceptance remain manual.")
}
$delivery | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $OutputDir "motion-delivery.json") -Encoding utf8

$checksumFiles = @("media-evidence.json","behavior-timeline.json","platform-binding.json","motion-spec-set.json","motion-audit-set.json","motion-delivery.json") + ($deliveryVariants | ForEach-Object { $_.motion_path })
$lines = foreach ($relative in $checksumFiles) { $path = Join-Path $OutputDir $relative; "$(Get-Sha256 $path)  $relative" }
$lines | Set-Content -LiteralPath (Join-Path $OutputDir "checksums.sha256") -Encoding ascii
Write-Host "YDL dry-run passed: $OutputDir"
