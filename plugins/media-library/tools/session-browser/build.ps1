param([string]$PluginRoot = (Join-Path $PSScriptRoot '../..'))
$ErrorActionPreference = 'Stop'
$destination = Join-Path ([IO.Path]::GetFullPath($PluginRoot)) 'bin/session-browser'
$intermediate = Join-Path ([IO.Path]::GetFullPath($PluginRoot)) 'tools/.cache/session-browser/'
dotnet build (Join-Path $PSScriptRoot 'MediaSessionBrowser.csproj') -c Release -o $destination "-p:BaseIntermediateOutputPath=$intermediate" --nologo
if ($LASTEXITCODE -ne 0) { throw '站点连接组件构建失败' }
# Preserve Microsoft's SDK license alongside its redistributable assemblies.
$license = Join-Path $env:USERPROFILE '.nuget/packages/microsoft.web.webview2/1.0.3537.50/LICENSE.txt'
if (Test-Path $license) { Copy-Item $license (Join-Path $destination 'WebView2-LICENSE.txt') -Force }
$notice = Join-Path $env:USERPROFILE '.nuget/packages/microsoft.web.webview2/1.0.3537.50/NOTICE.txt'
if (Test-Path $notice) { Copy-Item $notice (Join-Path $destination 'WebView2-NOTICE.txt') -Force }
