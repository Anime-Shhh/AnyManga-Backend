# Run elevated after downloading a release and verifying SHA256SUMS.
# This script never restarts Windows, Tailscale, WSL, networking, SSH, or Codex.
[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$ReleaseDirectory,
 [Parameter(Mandatory=$true)][string]$DeploymentId,
 [string]$Root = 'C:\ProgramData\AnyManga'
)
$ErrorActionPreference = 'Stop'
$serviceName = 'AnyMangaBackend'
$exe = Join-Path $ReleaseDirectory 'anymanga-api-windows-amd64.exe'
if (!(Test-Path -LiteralPath $exe)) { throw 'The verified Windows release executable is missing.' }
$checksums = Join-Path $ReleaseDirectory 'SHA256SUMS'
$line = Get-Content -LiteralPath $checksums | Where-Object { $_ -match '  anymanga-api-windows-amd64.exe$' }
if (!$line -or (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant() -ne ($line -split '  ')[0]) { throw 'Executable checksum verification failed.' }
if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) { throw 'Service already exists; use the controlled update procedure.' }
$key = Join-Path $Root 'secrets\api-key.txt'
if (!(Test-Path -LiteralPath $key)) { throw 'Protected API key file must already exist.' }
$config = Join-Path $Root 'config.json'
@{
 address='127.0.0.1:8080'; apiKeyFile=$key; deploymentId=$DeploymentId;
 logDirectory=(Join-Path $Root 'logs'); metadataPerMinute=120; imagePerMinute=600; pdfPerMinute=2
} | ConvertTo-Json | Set-Content -LiteralPath $config -Encoding utf8NoBOM
& icacls.exe $config /inheritance:r /grant:r '*S-1-5-18:(F)' '*S-1-5-32-544:(F)' '*S-1-5-19:(R)' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Could not restrict config ACL.' }
& $exe --config $config --service install
if ($LASTEXITCODE -ne 0) { throw 'Service registration failed.' }
& $exe --service start
if ($LASTEXITCODE -ne 0) { throw 'Service start failed.' }
$healthy = $false
for ($attempt=0; $attempt -lt 30; $attempt++) {
 try { $response=Invoke-WebRequest 'http://127.0.0.1:8080/health' -TimeoutSec 3; if ($response.StatusCode -eq 204) { $healthy=$true; break } } catch {}
 Start-Sleep -Seconds 1
}
if (!$healthy) { throw 'Application service failed health verification. No public cutover should occur.' }
Get-Service -Name $serviceName | Select-Object Name,Status,StartType
