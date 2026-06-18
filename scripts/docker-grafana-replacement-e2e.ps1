param(
  [string]$BaseUrl = "http://127.0.0.1:23390",
  [string]$ProjectName = "sentinel233-e2e",
  [switch]$UseLocalBinary,
  [switch]$KeepRunning
)

$script = Join-Path $PSScriptRoot "docker-ecosystem-e2e.ps1"
& $script -BaseUrl $BaseUrl -ProjectName $ProjectName -UseLocalBinary:$UseLocalBinary -KeepRunning:$KeepRunning
exit $LASTEXITCODE
