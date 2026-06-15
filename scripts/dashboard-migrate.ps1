param(
  [string]$BaseUrl = "http://127.0.0.1:23390",
  [string]$Tenant = "default",
  [string]$Username = "root",
  [string]$Password = "root",
  [string]$ImportDir = ".\dashboards",
  [string]$ArchiveDir = ".\artifacts\dashboard-exports",
  [string]$SummaryFile = ".\artifacts\dashboard-migration-report.json"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-Slug([string]$Value) {
  $safe = ($Value -replace "[^a-zA-Z0-9_-]+", "-").Trim("-")
  if ([string]::IsNullOrWhiteSpace($safe)) {
    return "dashboard"
  }
  return $safe.ToLowerInvariant()
}

function Get-FlattenPanels($Panels) {
  $result = @()
  foreach ($panel in @($Panels)) {
    if ($null -eq $panel) { continue }
    if ($panel.type -eq "row" -and $panel.panels) {
      $result += Get-FlattenPanels $panel.panels
      continue
    }
    $result += $panel
  }
  return $result
}

function Get-CompatibilityWarnings($DashboardData) {
  if (-not $DashboardData.layout) { return 0 }
  try {
    $layout = $DashboardData.layout | ConvertFrom-Json -Depth 100
    if ($layout.compatibility.totalWarnings -ne $null) {
      return [int]$layout.compatibility.totalWarnings
    }
  } catch {
  }
  return 0
}

function Invoke-JsonApi {
  param(
    [string]$Method,
    [string]$Url,
    [hashtable]$Headers,
    [string]$Body
  )
  if ([string]::IsNullOrWhiteSpace($Body)) {
    return Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers
  }
  return Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers -Body $Body -ContentType "application/json"
}

if (-not (Test-Path -LiteralPath $ImportDir)) {
  throw "Import directory not found: $ImportDir"
}

$archivePath = [System.IO.Path]::GetFullPath($ArchiveDir)
$summaryPath = [System.IO.Path]::GetFullPath($SummaryFile)
$summaryDir = Split-Path -Parent $summaryPath
New-Item -ItemType Directory -Force -Path $archivePath | Out-Null
New-Item -ItemType Directory -Force -Path $summaryDir | Out-Null

$loginBody = @{
  tenant = $Tenant
  username = $Username
  password = $Password
} | ConvertTo-Json

$login = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/login" -Headers @{} -Body $loginBody
if (-not $login.token) {
  throw "Login succeeded but token is missing."
}

$headers = @{
  Authorization = "Bearer $($login.token)"
}

$files = Get-ChildItem -LiteralPath $ImportDir -Filter *.json -File | Sort-Object Name
if ($files.Count -eq 0) {
  throw "No JSON dashboards found in $ImportDir"
}

$results = @()
foreach ($file in $files) {
  $raw = Get-Content -LiteralPath $file.FullName -Raw
  $source = $raw | ConvertFrom-Json -Depth 100
  $dashboard = if ($source.dashboard) { $source.dashboard } else { $source }
  $sourcePanels = @(Get-FlattenPanels $dashboard.panels)

  $imported = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/dashboards/import" -Headers $headers -Body $raw
  $dashboardData = $imported.data
  $dashboardId = [int64]$dashboardData.id
  $exported = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/dashboards/$dashboardId/export" -Headers $headers -Body ""

  $slug = Get-Slug $dashboardData.title
  $archiveFile = Join-Path $archivePath ("{0:D4}-{1}.grafana.json" -f $dashboardId, $slug)
  ($exported.data | ConvertTo-Json -Depth 100) | Set-Content -LiteralPath $archiveFile -Encoding UTF8

  $exportedPanels = @(Get-FlattenPanels $exported.data.panels)
  $warningCount = Get-CompatibilityWarnings $dashboardData
  $status = if ($sourcePanels.Count -eq $exportedPanels.Count) { "ok" } else { "review" }

  $results += [pscustomobject]@{
    file = $file.Name
    dashboard_id = $dashboardId
    title = $dashboardData.title
    source_panel_count = $sourcePanels.Count
    exported_panel_count = $exportedPanels.Count
    compatibility_warnings = $warningCount
    archive_file = $archiveFile
    status = $status
  }
}

$report = [pscustomobject]@{
  generated_at = (Get-Date).ToUniversalTime().ToString("o")
  base_url = $BaseUrl
  tenant = $Tenant
  dashboards = $results
}

($report | ConvertTo-Json -Depth 100) | Set-Content -LiteralPath $summaryPath -Encoding UTF8

Write-Host ""
Write-Host "Dashboard migration summary"
Write-Host "Base URL: $BaseUrl"
Write-Host "Tenant:   $Tenant"
Write-Host "Report:   $summaryPath"
Write-Host ""
$results | Format-Table file, dashboard_id, source_panel_count, exported_panel_count, compatibility_warnings, status -AutoSize
