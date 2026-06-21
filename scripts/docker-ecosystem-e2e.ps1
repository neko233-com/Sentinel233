param(
  [string]$BaseUrl = "http://127.0.0.1:23390",
  [string]$ProjectName = "sentinel233-e2e",
  [switch]$UseLocalBinary,
  [switch]$KeepRunning
)

$ErrorActionPreference = "Stop"

function Invoke-JsonApi {
  param(
    [string]$Method,
    [string]$Url,
    [hashtable]$Headers = @{},
    [string]$Body = ""
  )
  $params = @{
    Method = $Method
    Uri = $Url
    Headers = $Headers
    TimeoutSec = 20
  }
  if ($Body -ne "") {
    $params.Body = $Body
    if (-not $Headers.ContainsKey("Content-Type")) {
      $params.ContentType = "application/json"
    }
  }
  return Invoke-RestMethod @params
}

function Wait-ForServer {
  param([string]$Url)
  $deadline = (Get-Date).AddSeconds(90)
  do {
    try {
      $health = Invoke-WebRequest -Uri "$Url/healthz" -UseBasicParsing -TimeoutSec 3
      if ($health.StatusCode -eq 200) { return }
    } catch {
      Start-Sleep -Seconds 2
    }
  } while ((Get-Date) -lt $deadline)
  throw "Sentinel233 did not become healthy at $Url"
}

function Assert-Success {
  param($Response, [string]$Step)
  if ($null -eq $Response -or ($Response.PSObject.Properties.Name -contains "status" -and $Response.status -ne "success")) {
    throw "$Step failed: $($Response | ConvertTo-Json -Depth 20)"
  }
}

function Send-RemoteWrite {
  param([string]$Url)
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("sentinel233-remote-write-{0}.go" -f ([Guid]::NewGuid().ToString("N")))
  @'
package main

import (
  "bytes"
  "encoding/binary"
  "math"
  "net/http"
  "os"
  "time"

  "github.com/golang/snappy"
)

const (
  wireVarint = 0
  wireFixed64 = 1
  wireBytes = 2
)

func main() {
  if len(os.Args) < 2 {
    panic("base url required")
  }
  now := time.Now().UnixMilli()
  payload := bytesField(1, timeSeries(
    []label{{"__name__", "http_requests_total"}, {"job", "api"}, {"instance", "docker-e2e:8080"}, {"route", "/checkout"}},
    []sample{{12, now - 30000}, {18, now}},
  ))
  req, err := http.NewRequest(http.MethodPost, os.Args[1]+"/api/v1/write", bytes.NewReader(snappy.Encode(nil, payload)))
  if err != nil { panic(err) }
  req.Header.Set("Content-Encoding", "snappy")
  req.Header.Set("Content-Type", "application/x-protobuf")
  resp, err := http.DefaultClient.Do(req)
  if err != nil { panic(err) }
  defer resp.Body.Close()
  if resp.StatusCode != http.StatusNoContent {
    panic(resp.Status)
  }
}

type label struct{ name, value string }
type sample struct{ value float64; ts int64 }

func timeSeries(labels []label, samples []sample) []byte {
  out := []byte{}
  for _, item := range labels {
    out = append(out, bytesField(1, labelBytes(item))...)
  }
  for _, item := range samples {
    out = append(out, bytesField(2, sampleBytes(item))...)
  }
  return out
}

func labelBytes(item label) []byte {
  out := []byte{}
  out = append(out, bytesField(1, []byte(item.name))...)
  out = append(out, bytesField(2, []byte(item.value))...)
  return out
}

func sampleBytes(item sample) []byte {
  out := []byte{}
  out = append(out, fixed64Field(1, math.Float64bits(item.value))...)
  out = append(out, varintField(2, uint64(item.ts))...)
  return out
}

func bytesField(field int, value []byte) []byte {
  out := []byte{}
  out = append(out, varint(uint64(field<<3|wireBytes))...)
  out = append(out, varint(uint64(len(value)))...)
  out = append(out, value...)
  return out
}

func fixed64Field(field int, value uint64) []byte {
  out := []byte{}
  out = append(out, varint(uint64(field<<3|wireFixed64))...)
  var buf [8]byte
  binary.LittleEndian.PutUint64(buf[:], value)
  out = append(out, buf[:]...)
  return out
}

func varintField(field int, value uint64) []byte {
  out := []byte{}
  out = append(out, varint(uint64(field<<3|wireVarint))...)
  out = append(out, varint(value)...)
  return out
}

func varint(value uint64) []byte {
  out := []byte{}
  for value >= 0x80 {
    out = append(out, byte(value)|0x80)
    value >>= 7
  }
  out = append(out, byte(value))
  return out
}
'@ | Set-Content -LiteralPath $tmp -Encoding UTF8
  try {
    go run $tmp $Url
  } finally {
    Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
  }
}

function Start-DockerEnvironment {
  param([string]$Project, [switch]$LocalBinary)
  if (-not $LocalBinary) {
    docker compose -p $Project up -d --build
    return
  }

  $distDir = Join-Path (Get-Location) ".tmp-docker-e2e"
  New-Item -ItemType Directory -Force -Path $distDir | Out-Null
  $binary = Join-Path $distDir "sentinel233-server"
  $dockerfile = Join-Path $distDir "Dockerfile"
  $oldGoos = $env:GOOS
  $oldGoarch = $env:GOARCH
  $oldCgo = $env:CGO_ENABLED
  try {
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags="-s -w" -o $binary ./cmd/sentinel233-server
  } finally {
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:CGO_ENABLED = $oldCgo
  }
  @'
FROM alpine:3.22
COPY sentinel233-server /usr/local/bin/sentinel233-server
EXPOSE 23390
VOLUME /data
HEALTHCHECK --interval=10s --timeout=3s --retries=6 CMD wget -qO- http://127.0.0.1:23390/healthz || exit 1
ENTRYPOINT ["sentinel233-server"]
CMD ["-addr", ":23390", "-data", "/data"]
'@ | Set-Content -LiteralPath $dockerfile -Encoding ASCII
  docker build -f $dockerfile -t sentinel233-server:e2e-local $distDir
  docker run -d --name "${Project}-server-1" -p 23390:23390 -v "${Project}-data:/data" sentinel233-server:e2e-local | Out-Null
}

function Stop-DockerEnvironment {
  param([string]$Project, [switch]$LocalBinary)
  if ($LocalBinary) {
    docker rm -f "${Project}-server-1" 2>$null | Out-Null
    docker volume rm "${Project}-data" 2>$null | Out-Null
    Remove-Item -LiteralPath (Join-Path (Get-Location) ".tmp-docker-e2e") -Recurse -Force -ErrorAction SilentlyContinue
    return
  }
  docker compose -p $Project down -v
}

try {
  Start-DockerEnvironment -Project $ProjectName -LocalBinary:$UseLocalBinary
  Wait-ForServer -Url $BaseUrl

  $login = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/login" -Body '{"username":"root","password":"root","tenant":"default"}'
  Assert-Success $login "login"
  $headers = @{ Authorization = "Bearer $($login.token)" }

  $datasources = @'
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prom
    type: prometheus
    url: http://prometheus:9090
    access: proxy
    isDefault: true
'@
  $capabilities = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/ecosystem/capabilities" -Headers $headers
  Assert-Success $capabilities "ecosystem capabilities"
  if ($capabilities.data.stability.primaryPrefix -ne "/api/ecosystem") {
    throw "ecosystem capabilities did not advertise stable primary prefix"
  }

  $ds = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/ecosystem/import?source=grafana-datasources" -Headers ($headers + @{ "Content-Type" = "application/yaml" }) -Body $datasources
  Assert-Success $ds "grafana datasource import"

  $promConfig = @'
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: node
    static_configs:
      - targets: ["localhost:9100"]
        labels:
          env: e2e
remote_write:
  - url: http://sentinel233-server:23390/api/v1/write
'@
  $targets = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/ecosystem/import?source=prometheus-config" -Headers ($headers + @{ "Content-Type" = "application/yaml" }) -Body $promConfig
  Assert-Success $targets "prometheus config import"

  $rulesYaml = @'
groups:
  - name: e2e
    rules:
      - alert: CheckoutTrafficMissing
        expr: http_requests_total == 0
        for: 1m
        labels:
          severity: warning
'@
  $rules = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/ecosystem/import?source=prometheus-rules" -Headers ($headers + @{ "Content-Type" = "application/yaml" }) -Body $rulesYaml
  Assert-Success $rules "prometheus rules import"

  Send-RemoteWrite -Url $BaseUrl

  $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
  $start = $now - 120
  $query = [uri]::EscapeDataString('http_requests_total{job="api"}')
  $range = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/query_range?query=$query&start=$start&end=$now&step=30"
  Assert-Success $range "query_range"
  if ($range.data.resultType -ne "matrix" -or @($range.data.result).Count -lt 1) {
    throw "query_range did not return matrix data: $($range | ConvertTo-Json -Depth 20)"
  }

  $rfcStart = [uri]::EscapeDataString(([DateTimeOffset]::FromUnixTimeSeconds($start)).UtcDateTime.ToString("o"))
  $rfcEnd = [uri]::EscapeDataString(([DateTimeOffset]::FromUnixTimeSeconds($now)).UtcDateTime.ToString("o"))
  $rangeDurationStep = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/query_range?query=$query&start=$rfcStart&end=$rfcEnd&step=1m"
  Assert-Success $rangeDurationStep "query_range duration step"

  $labels = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/labels?match[]=http_requests_total"
  Assert-Success $labels "labels"
  if (@($labels.data) -notcontains "job") { throw "labels endpoint did not expose job label" }

  $jobValues = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/label/job/values?match[]=http_requests_total"
  Assert-Success $jobValues "label values"
  if (@($jobValues.data) -notcontains "api") { throw "label values endpoint did not expose api job" }

  $metadata = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/metadata?metric=http_requests_total"
  Assert-Success $metadata "metadata"

  $dashboard = @'
{
  "title": "Grafana Replacement E2E",
  "uid": "sentinel-e2e",
  "schemaVersion": 39,
  "tags": ["e2e", "grafana-import"],
  "templating": {"list": [{"name": "job", "type": "query", "query": "label_values(job)", "current": {"value": "api"}}]},
  "panels": [
    {
      "id": 1,
      "type": "timeseries",
      "title": "HTTP Requests",
      "gridPos": {"x": 0, "y": 0, "w": 12, "h": 8},
      "datasource": {"type": "prometheus", "uid": "prom"},
      "targets": [
        {"refId": "A", "expr": "http_requests_total{job=\"$job\"}", "legendFormat": "{{instance}}"},
        {"refId": "B", "expr": "rate(http_requests_total{job=\"$job\"}[1m])", "legendFormat": "rate {{instance}}"}
      ],
      "fieldConfig": {"defaults": {"unit": "short"}},
      "options": {"legend": {"displayMode": "list"}}
    }
  ]
}
'@
  $imported = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/dashboards/import" -Headers $headers -Body $dashboard
  Assert-Success $imported "grafana dashboard import"
  $dashboardId = [int64]$imported.data.id
  if ($dashboardId -le 0) { throw "dashboard import did not return an id" }

  $exported = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/dashboards/$dashboardId/export" -Headers $headers
  Assert-Success $exported "grafana dashboard export"
  if (@($exported.data.panels[0].targets).Count -ne 2) {
    throw "dashboard export did not preserve both Grafana targets"
  }

  $alertPayload = '{"receiver":"sentinel233","status":"firing","alerts":[{"labels":{"alertname":"CheckoutTrafficMissing","severity":"warning"}}]}'
  $webhook = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/ecosystem/alertmanager/webhook" -Headers $headers -Body $alertPayload
  Assert-Success $webhook "alertmanager webhook"

  $rulesApi = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/rules"
  Assert-Success $rulesApi "prometheus rules api"

  $targetsApi = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/v1/targets"
  Assert-Success $targetsApi "prometheus targets api"

  $agentRegisterBody = @{
    agent_id = "docker-node-1"
    name = "docker-node-1"
    hostname = "docker-node-1"
    version = "e2e"
    listen_addr = ":23391"
    labels = @{ role = "linux"; env = "e2e" }
  } | ConvertTo-Json -Depth 10
  $agentRegister = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/agent/v1/register" -Body $agentRegisterBody
  Assert-Success $agentRegister "agent register"
  if (-not $agentRegister.data.token) {
    throw "agent registration did not return token"
  }
  $agentHeaders = @{ Authorization = "Bearer $($agentRegister.data.token)" }
  $heartbeatBody = @{
    version = "e2e"
    listen_addr = ":23391"
    labels = @{ role = "linux"; env = "e2e" }
    metrics = @{ sentinel_agent_up = 1; sentinel_agent_tasks_completed_total = 0 }
  } | ConvertTo-Json -Depth 10
  $heartbeat = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/agent/v1/heartbeat" -Headers $agentHeaders -Body $heartbeatBody
  Assert-Success $heartbeat "agent heartbeat"

  $taskBody = @{
    type = "refresh_config"
    payload = @{ reason = "docker-e2e" }
  } | ConvertTo-Json -Depth 10
  $task = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/agents/docker-node-1/tasks" -Headers $headers -Body $taskBody
  Assert-Success $task "agent task create"
  $claimedTasks = Invoke-JsonApi -Method "GET" -Url "$BaseUrl/api/agent/v1/tasks" -Headers $agentHeaders
  Assert-Success $claimedTasks "agent task claim"
  if (@($claimedTasks.data).Count -lt 1) {
    throw "agent did not claim any task"
  }
  $taskId = [int64]$claimedTasks.data[0].id
  $completeBody = @{ result = "ok" } | ConvertTo-Json
  $completed = Invoke-JsonApi -Method "POST" -Url "$BaseUrl/api/agent/v1/tasks/$taskId/complete" -Headers $agentHeaders -Body $completeBody
  Assert-Success $completed "agent task complete"

  Write-Host "Docker Grafana/Prometheus ecosystem E2E passed for $BaseUrl"
} finally {
  if (-not $KeepRunning) {
    Stop-DockerEnvironment -Project $ProjectName -LocalBinary:$UseLocalBinary
  }
}
