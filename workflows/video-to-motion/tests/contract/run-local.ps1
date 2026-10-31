$ErrorActionPreference = "Stop"
$workflowRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$repoRoot = [IO.Path]::GetFullPath((Join-Path $workflowRoot "..\.."))
$package = Join-Path $repoRoot "dist\workflows\com.himind.workflow.video-to-motion-0.1.0.hmwf"
Push-Location $repoRoot
try {
  go test ./workflows/video-to-motion/tests/contract
  if ($LASTEXITCODE -ne 0) { throw "workflow contract tests failed" }

  go run .\tools\cmd\himind-plugin-validate-cli -path (Join-Path $repoRoot "plugins\motion-composer")
  if ($LASTEXITCODE -ne 0) { throw "motion-composer plugin validation failed" }

  Push-Location (Join-Path $repoRoot "plugins\motion-composer")
  try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "motion-composer tests failed" }
    go build -o bin\motion-composer.exe .
    if ($LASTEXITCODE -ne 0) { throw "motion-composer build failed" }
  } finally { Pop-Location }

  go run .\.tmp\workflow-package\main.go $workflowRoot $package
  if ($LASTEXITCODE -ne 0) { throw "workflow package failed" }
  go run .\.tmp\workflow-validate\main.go $workflowRoot
  if ($LASTEXITCODE -ne 0) { throw "workflow source validation failed" }
  go run .\.tmp\workflow-validate\main.go $package
  if ($LASTEXITCODE -ne 0) { throw "workflow package validation failed" }

  powershell -ExecutionPolicy Bypass -File (Join-Path $workflowRoot "tests\e2e\run-ydl-dry-run.ps1")
  if ($LASTEXITCODE -ne 0) { throw "YDL dry-run failed" }
} finally { Pop-Location }
Write-Host "video-to-motion local contracts passed"
