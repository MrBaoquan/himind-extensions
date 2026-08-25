param(
    [Parameter(Mandatory = $true)][string]$ArtifactPath,
    [Parameter(Mandatory = $true)][string]$PrivateKeyPath,
    [Parameter(Mandatory = $true)][ValidatePattern('^[A-Za-z0-9._-]+$')][string]$KeyId,
    [Parameter(Mandatory = $true)][string]$OutputPath
)

$ErrorActionPreference = 'Stop'
$artifact = (Resolve-Path $ArtifactPath).Path
$privateKey = (Resolve-Path $PrivateKeyPath).Path
$opensslCommand = Get-Command openssl -ErrorAction SilentlyContinue
$openssl = if ($opensslCommand) { $opensslCommand.Source } else { Join-Path $env:ProgramFiles 'Git\usr\bin\openssl.exe' }
if (-not (Test-Path -LiteralPath $openssl -PathType Leaf)) { throw 'OpenSSL is required.' }
$signatureFile = Join-Path ([IO.Path]::GetTempPath()) "himind-extension-signature-$([guid]::NewGuid().ToString('N')).bin"
try {
    & $openssl dgst -sha256 -sigopt rsa_padding_mode:pss -sigopt rsa_pss_saltlen:-1 -sign $privateKey -out $signatureFile $artifact
    if ($LASTEXITCODE -ne 0) { throw 'Extension signing failed.' }
    $metadata = [ordered]@{
        file_name = [IO.Path]::GetFileName($artifact)
        file_size = (Get-Item -LiteralPath $artifact).Length
        sha256 = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
        signature = [Convert]::ToBase64String([IO.File]::ReadAllBytes($signatureFile))
        signature_key_id = $KeyId
        signature_algorithm = 'rsa-pss-sha256'
    }
    [IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputPath), ($metadata | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
}
finally {
    if (Test-Path -LiteralPath $signatureFile) { Remove-Item -LiteralPath $signatureFile -Force }
}
