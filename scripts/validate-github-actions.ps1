param(
    [string]$WorkflowPath = ".github/workflows"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $WorkflowPath)) {
    Write-Host "No GitHub workflow directory found at $WorkflowPath"
    exit 0
}

$actionlint = Get-Command actionlint -ErrorAction SilentlyContinue
if (-not $actionlint) {
    Write-Warning "actionlint is not installed; skipping workflow lint. Install actionlint for full validation."
    exit 0
}

$workflowFiles = Get-ChildItem -LiteralPath $WorkflowPath -File -Include *.yml,*.yaml
if (-not $workflowFiles) {
    Write-Host "No workflow files found at $WorkflowPath"
    exit 0
}

& $actionlint.Source @($workflowFiles.FullName)
