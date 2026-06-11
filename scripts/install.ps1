<#
.SYNOPSIS
    Sentinel233 CLI / Agent one-click installer for Windows.
.DESCRIPTION
    Downloads the latest sentinel233-agent binary from GitHub Releases and installs it.
.EXAMPLE
    iwr -useb https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install.ps1 | iex
    iwr -useb .../install.ps1 | iex; Install-Sentinel233Agent -Version v0.1.0
#>
param([string]$Version = "latest")

$ErrorActionPreference = "Stop"

$Repo = "neko233-com/Sentinel233"
$Binary = "sentinel233-agent"

function Get-Arch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { Write-Error "Unsupported architecture: $arch"; exit 1 }
    }
}

function Get-LatestVersion {
    try {
        $resp = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        return $resp.tag_name
    } catch {
        return "v0.1.0"
    }
}

function Install-Sentinel233Agent {
    param([string]$Ver = $Version)

    $arch = Get-Arch
    if ($Ver -eq "latest") { $Ver = Get-LatestVersion }
    $verNum = $Ver -replace '^[vV]', ''

    $installDir = "$env:LOCALAPPDATA\sentinel233"
    if (!(Test-Path $installDir)) { New-Item -ItemType Directory -Path $installDir -Force | Out-Null }

    $archive = "$Binary-$verNum-windows-$arch.zip"
    $url = "https://github.com/$Repo/releases/download/$Ver/$archive"
    $tmpDir = Join-Path $env:TEMP "sentinel233-install-$(Get-Random)"

    Write-Host "Downloading $Binary $Ver for windows/$arch..."
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    try {
        Invoke-WebRequest -Uri $url -OutFile "$tmpDir\$archive" -UseBasicParsing
    } catch {
        Write-Host "Trying direct binary download..."
        $exeUrl = "https://github.com/$Repo/releases/download/$Ver/$Binary-windows-$arch.exe"
        Invoke-WebRequest -Uri $exeUrl -OutFile "$installDir\$Binary.exe" -UseBasicParsing
        Write-Host "Installed to $installDir\$Binary.exe"
        Write-Host ""
        Write-Host "Add to PATH: `$env:PATH += '$installDir'"
        Write-Host "Usage: $Binary -server http://your-server:23390"
        return
    }

    Expand-Archive -Path "$tmpDir\$archive" -DestinationPath $tmpDir -Force
    $exe = Get-ChildItem -Path $tmpDir -Filter "*.exe" -Recurse | Select-Object -First 1
    if ($exe) {
        Copy-Item $exe.FullName "$installDir\$Binary.exe" -Force
    }

    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue

    Write-Host ""
    Write-Host "$Binary $Ver installed to $installDir\$Binary.exe"
    Write-Host ""
    Write-Host "Usage:"
    Write-Host "  $Binary -server http://your-server:23390"
    Write-Host "  $Binary -addr :23391 -server http://your-server:23390"
    Write-Host ""
    Write-Host "Add to PATH: `$env:PATH += '$installDir'"
}

Install-Sentinel233Agent
