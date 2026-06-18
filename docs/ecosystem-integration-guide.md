# Sentinel233 Grafana/Prometheus 生态接入指南

Sentinel233 不是 Grafana clone，也不是临时兼容层。它把 Grafana dashboard、datasource provisioning、Prometheus scrape/rule files、remote_write、Prometheus HTTP API、Alertmanager webhook 当作一等生态输入，最终落成 Sentinel233 原生的 target、dashboard、rule、tenant 和 metadata。

## 稳定入口

| 入口 | 用途 |
|---|---|
| `/api/v1/write` | Prometheus remote_write 标准写入 |
| `/api/v1/query` / `/api/v1/query_range` | Grafana datasource 与 Prometheus HTTP API 查询 |
| `/api/v1/series` / `/api/v1/labels` / `/api/v1/label/{name}/values` | Grafana 变量、label browser、series metadata |
| `/api/ecosystem/capabilities` | 发现当前生态接入能力 |
| `/api/ecosystem/import?source=prometheus-config` | 接入 Prometheus scrape config |
| `/api/ecosystem/import?source=prometheus-rules` | 接入 Prometheus rule file |
| `/api/ecosystem/import?source=grafana-datasources` | 接入 Grafana datasource provisioning |
| `/api/ecosystem/import?source=grafana-dashboard` | 接入 Grafana dashboard JSON |
| `/api/ecosystem/alertmanager/webhook` | 接入 Alertmanager webhook payload |

## 接入路线

1. 数据面先接入：把 Prometheus Agent、Grafana Agent、Alloy 或现有 remote_write sender 指到 `/api/v1/write`。
2. 查询面接入：Grafana datasource 可直接以 Prometheus HTTP API 方式查询 Sentinel233 的 `/api/v1/*`。
3. 配置面接入：用 `/api/ecosystem/import` 导入 scrape config、rule file、datasource provisioning。
4. 面板接入：导入 Grafana dashboard JSON，保留 panels、targets、gridPos、fieldConfig、templating、datasource 和原始 Grafana metadata。
5. 治理面接管：将落成的 targets、rules、dashboards 纳入 Sentinel233 tenant/RBAC/metadata 管理。

## 能力契约

| 场景 | 当前能力 |
|---|---|
| Prometheus remote_write | 标准 snappy protobuf WriteRequest，保留原始 labels |
| Prometheus scrape config | `static_configs` 落成 Sentinel scrape targets；动态 SD 配置保留为 metadata |
| Prometheus rule file | alert rules 落成 Sentinel alert rules；recording rules 保留为 metadata |
| Grafana datasource provisioning | Prometheus datasource 映射到 Sentinel `/api/v1`，非 Prometheus datasource 保留生态 metadata |
| Grafana dashboard | 常见 panel 类型落成 Sentinel panel，多 target 并行渲染，导出仍可保持 Grafana JSON 形态 |
| Grafana variables | 支持 `label_values()` 所需 label endpoints，`query_range` 支持 Unix/RFC3339 时间和 duration step |
| Alertmanager webhook | payload 被稳定接收并留存，便于审计与通知链路验证 |

## 验收清单

- [ ] remote_write 或 scrape 数据进入 Sentinel233 TSDB
- [ ] Grafana datasource 使用 Sentinel233 `/api/v1/*` 查询成功
- [ ] `/api/ecosystem/capabilities` 返回 `primaryPrefix=/api/ecosystem`
- [ ] Prometheus scrape config、rule file、Grafana datasource provisioning 已通过 `/api/ecosystem/import` 接入
- [ ] Grafana dashboard JSON 导入后，变量、多 target、layout 与 fieldConfig 保留
- [ ] `/api/v1/label/{name}/values` 与 `/api/v1/query_range` 覆盖 Grafana 变量和图表刷新
- [ ] Alertmanager webhook 可接收并审计 payload
- [ ] Docker ecosystem E2E 和 GitHub CI 均通过

## 一键验证

```powershell
pwsh ./scripts/docker-ecosystem-e2e.ps1
```

在 Docker Hub 拉取受限的本机环境中：

```powershell
pwsh ./scripts/docker-ecosystem-e2e.ps1 -UseLocalBinary
```
