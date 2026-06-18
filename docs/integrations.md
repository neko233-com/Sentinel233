# Sentinel233 Server integrations

Sentinel233 Server treats Prometheus-compatible `/metrics` scraping as one ingestion path, not the only path.

## Supported ingestion presets

- Go service `/metrics`: any service exposing Prometheus/OpenMetrics text format.
- Linux node_exporter: host CPU, memory, disk, filesystem, and network metrics.
- MySQL mysqld_exporter: MySQL server health, InnoDB, query, connection, and replication metrics.
- PostgreSQL postgres_exporter: connections, transactions, cache hit ratio, replication, and database health.
- Redis redis_exporter: memory, clients, commands, hit ratio, slowlog, and persistence status.
- Nginx nginx-prometheus-exporter: stub_status connection and request metrics.
- Docker/cAdvisor: container CPU, memory, filesystem, and network metrics.
- Kubernetes kube-state-metrics: nodes, pods, restarts, resource requests, and abnormal states.
- Blackbox exporter: HTTP/TCP/ICMP probing, DNS, TLS certificate expiry, and external latency.
- Sentinel233 Go client lib: planned native high-performance client path for richer runtime telemetry.

## Prometheus remote write

Existing Prometheus-compatible agents can push samples directly to:

```text
POST /api/v1/write
```

The endpoint accepts the standard Prometheus remote write protobuf `WriteRequest` with snappy block compression. Sentinel233 preserves the original labels and stores the samples in the same TSDB used by scraped `/metrics` targets and native Sentinel clients.

Example Prometheus configuration:

```yaml
remote_write:
  - url: http://sentinel233-server:23390/api/v1/write
```

## Ecosystem import API

Automation tools can import Grafana, Prometheus, and Alertmanager ecosystem files through the stable ecosystem API:

```text
POST /api/ecosystem/import?source=<format>
```

Supported formats:

| Format | Result |
|---|---|
| `grafana-dashboard` | Creates a Sentinel dashboard and preserves Grafana target/layout/field metadata. |
| `grafana-datasources` | Stores Grafana datasource provisioning metadata; Prometheus datasources map to Sentinel `/api/v1`. |
| `prometheus-config` | Converts `scrape_configs[].static_configs` into Sentinel scrape targets and preserves remote_write/rule_files metadata. |
| `prometheus-rules` | Converts alerting rules into Sentinel alert rules and preserves recording rules as metadata. |
| `alertmanager-webhook` | Accepts an Alertmanager webhook payload for notification-channel verification and auditing. |

Loopback automation can use the same importer without a login token:

```text
POST /api/local/v1/ecosystem/import?source=prometheus-config
```

Alertmanager webhook receiver:

```text
POST /api/ecosystem/alertmanager/webhook
```

`/api/compat/*` remains available as a legacy alias for existing automation, but new integrations should target `/api/ecosystem/*`.

### Grafana/Prometheus 生态接入工作流（生产落地建议）

在接管 Grafana/Prometheus 生态的生产场景中，建议按三层接入分离：

- 第一层：统一抓取层（Scrape /remote_write）
  - 接入现网 Prometheus Exporter（node_exporter、mysqld_exporter 等）
  - 接入现有 Prometheus Agent、Grafana Agent、Alloy 的 `remote_write` 推送
  - 接入 Prometheus `scrape_configs` 静态目标导入
- 第二层：Sentinel 标准化存储与查询层（内置 TSDB + PromQL）
  - 所有数据以统一标签模型落表，支持跨源统一查询
  - 无需在多个组件中同步规则/目标元数据
- 第三层：可视化与告警层（Sentinel Dashboard / Alerts）
  - 使用内置 Dashboard API 与原生告警引擎，不再依赖外部 Grafana 运行时
  - 支持导入 Grafana dashboard/datasource provisioning 和 Prometheus rule file
  - 前端图表运行时支持 Chart.js 与 ECharts 双渲染器，可针对导入面板选择更贴近 Grafana 的呈现方式
  - 面板可在运行期使用 `PromQL + SQL` 做聚合和透视，便于在不引入外部 BI 引擎的前提下实现复杂 ECharts 展示

The key operational difference is that each dashboard, target, and alert rule is stored as native Sentinel metadata and directly linked to a tenant/operator identity, instead of being managed through separate binary components.

## Sentinel native write protocol

Native clients write JSON directly to:

```text
POST /api/sentinel/v1/write
```

Minimal payload:

```json
{
  "resource": {
    "service.name": "api",
    "host.name": "game-01"
  },
  "metrics": [
    {
      "name": "sentinel_runtime_goroutines",
      "type": "gauge",
      "unit": "count",
      "samples": [
        { "value": 42 }
      ]
    }
  ]
}
```

The server stores native samples in the same TSDB as scraped metrics and adds `source="sentinel_native"`. Timestamps use Unix milliseconds; Unix seconds are accepted and converted.

Capabilities are discoverable at:

```text
GET /api/sentinel/v1/capabilities
```

## Native Go client submodule

The target submodule is:

```bash
git submodule add https://github.com/neko233-com/sentinel233-lib-go.git libs/sentinel233-lib-go
```

At the time of this update, the remote repository is empty, so Git cannot checkout a submodule commit yet. Once `sentinel233-lib-go` has an initial commit, run the command above and commit `.gitmodules` plus the gitlink. The client should target `/api/sentinel/v1/write` rather than exposing Prometheus text as its primary path.

## Prometheus-compatible examples

Go HTTP server:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

http.Handle("/metrics", promhttp.Handler())
http.ListenAndServe(":8080", nil)
```

Linux node_exporter endpoint:

```text
http://<host>:9100/metrics
```

MySQL mysqld_exporter endpoint:

```text
http://<host>:9104/metrics
```

PostgreSQL postgres_exporter endpoint:

```text
http://<host>:9187/metrics
```

Redis redis_exporter endpoint:

```text
http://<host>:9121/metrics
```

Nginx exporter endpoint:

```text
http://<host>:9113/metrics
```

cAdvisor endpoint:

```text
http://<host>:8080/metrics
```

kube-state-metrics endpoint:

```text
http://kube-state-metrics.kube-system.svc:8080/metrics
```

Blackbox exporter probe endpoint:

```text
http://<host>:9115/probe?module=http_2xx&target=https://example.com
```
