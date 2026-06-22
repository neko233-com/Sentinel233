<#
.SYNOPSIS
    Sentinel233 Server one-click installer for Windows.
.DESCRIPTION
    Downloads the latest sentinel233 server binary from GitHub Releases and installs it.
.EXAMPLE
    iwr -useb https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install-server.ps1 | iex
#>
param(
    [string]$Version = "latest",
    [string]$DataDir = "$env:LOCALAPPDATA\sentinel233\data",
    [switch]$InstallService
)

$ErrorActionPreference = "Stop"

$Repo = "neko233-com/Sentinel233"
$Binary = "sentinel233-server"

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

function Install-Sentinel233Server {
    param([string]$Ver = $Version)

    $arch = Get-Arch
    if ($Ver -eq "latest") { $Ver = Get-LatestVersion }
    $verNum = $Ver -replace '^[vV]', ''

    $installDir = "$env:LOCALAPPDATA\sentinel233"
    if (!(Test-Path $installDir)) { New-Item -ItemType Directory -Path $installDir -Force | Out-Null }

    $archive = "$Binary-$verNum-windows-$arch.zip"
    $url = "https://github.com/$Repo/releases/download/$Ver/$archive"
    $tmpDir = Join-Path $env:TEMP "sentinel233-install-$(Get-Random)"

    Write-Host "Downloading $Binary server $Ver for windows/$arch..."
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    try {
        Invoke-WebRequest -Uri $url -OutFile "$tmpDir\$archive" -UseBasicParsing
    } catch {
        Write-Host "Trying direct binary download..."
        $exeUrl = "https://github.com/$Repo/releases/download/$Ver/$Binary-$verNum-windows-$arch.exe"
        Invoke-WebRequest -Uri $exeUrl -OutFile "$installDir\$Binary.exe" -UseBasicParsing
        Write-Host "Installed to $installDir\$Binary.exe"
        Install-ServiceIfRequested "$installDir\$Binary.exe"
        Print-Usage
        return
    }

    Expand-Archive -Path "$tmpDir\$archive" -DestinationPath $tmpDir -Force
    $exe = Get-ChildItem -Path $tmpDir -Filter "*.exe" -Recurse | Select-Object -First 1
    if ($exe) {
        Copy-Item $exe.FullName "$installDir\$Binary.exe" -Force
    }

    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue

    Write-Host ""
    Write-Host "$Binary server $Ver installed to $installDir\$Binary.exe"
    Install-ServiceIfRequested "$installDir\$Binary.exe"
    Print-Usage
}

function Install-ServiceIfRequested {
    param([string]$ExePath)
    if (!$InstallService) { return }
    if (!(Test-Path $DataDir)) { New-Item -ItemType Directory -Path $DataDir -Force | Out-Null }
    $svc = Get-Service -Name "sentinel233-server" -ErrorAction SilentlyContinue
    if ($svc) {
        Write-Host "Windows service sentinel233-server already exists."
        return
    }
    $binPath = "`"$ExePath`" -data `"$DataDir`""
    New-Service -Name "sentinel233-server" -BinaryPathName $binPath -DisplayName "Sentinel233 Server" -Description "Sentinel233 local TSDB and monitoring server" -StartupType Automatic | Out-Null
    Start-Service "sentinel233-server"
    Write-Host "Windows service sentinel233-server is running."
}

function Print-Usage {
    Write-Host ""
    Write-Host "Quick Start:"
    Write-Host "  $Binary.exe -data `"$DataDir`"       # Start server on :23390"
    Write-Host "  $Binary.exe -addr :8080 -data `"$DataDir`""
    Write-Host "  $Binary.exe -config sentinel233.yaml -data `"$DataDir`""
    Write-Host "  $Binary.exe -version                 # Show version"
    Write-Host ""
    Write-Host "Install Windows service:"
    Write-Host "  .\install-server.ps1 -InstallService"
    Write-Host ""
    Write-Host "Add to PATH: `$env:PATH += '$installDir'"
}

Install-Sentinel233Server
