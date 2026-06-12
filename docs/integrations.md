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
