# Sentinel233 替代 Grafana 落地指南

## 目标定位

Sentinel233 不是一个 "Grafana clone"，而是把 Grafana 常见的监控工作流（数据采集、存储、查询、看板、告警、用户权限）收敛到一个可运维的服务里。  
目标是：**在不依赖 Grafana 主进程的情况下，完成同类监控任务的 80%+ 落地能力，并提供从 Grafana 迁移时的平滑路径。**

## 与 Grafana 的差异化实现方式

- 数据模型统一：面板、告警规则、采集目标、租户信息都走同一套后端元数据模型，不再需要面板 JSON 与外部配置双写。
- 一体化链路：同一套服务承载采集、PromQL 查询、告警判断、配置管理与前端呈现。
- 默认可部署单点：100+ 服务器上可共用一套 Server + 多个轻量 Agent。
- 原生可维护的 API：除兼容端点外提供 Sentinel 原生接口，支持后续自动化治理与配置下发。

## 功能对照（可替代程度）

| 需求 | Sentinel233 能力 | 说明 |
|---|---|---|
| 面板查看 | ✅ | 时序、表格、仪表盘聚合卡片 |
| PromQL 查询 | ✅ | 与 Prometheus 兼容核心查询链路，补齐 `labels/metadata/status/targets` 等 Grafana 常用探测端点 |
| 告警规则 | ✅ | 规则 CRUD + pending/firing 状态机 + webhook |
| 采集目标管理 | ✅ | UI/API 管理目标，支持健康态刷新 |
| 远程写入 | ✅ | `/api/v1/write`（Prometheus 兼容） |
| 多租户/权限 | ✅ | tenant + viewer/operator/admin |
| Grafana Dashboard 导入 | ✅ | Web UI 与 API 提供导入/导出能力，常见面板类型可映射，多 target 会保留并并行渲染 |
| Grafana datasource provisioning | ✅ | `/api/compat/import?source=grafana-datasources` 保存 datasource 映射，Prometheus datasource 指向 Sentinel `/api/v1` |
| Prometheus scrape config | ✅ | `/api/compat/import?source=prometheus-config` 可导入 `static_configs` 为 Sentinel scrape targets |
| Prometheus rule file | ✅ | `/api/compat/import?source=prometheus-rules` 可导入 alert rules，recording rules 保留为元数据 |
| Alertmanager webhook | ✅ | `/api/compat/alertmanager/webhook` 接收 Alertmanager payload，便于迁移期联调通知通道 |
| 兼容性预检 | ✅ | 导入前可查看需人工复核项（transformations、复杂 datasource、动态 service discovery 等） |
| ECharts 贴近渲染 | ✅ | 导入后的图表默认优先使用 ECharts 渲染器 |
| SQL 结果变换 | ✅ | 支持 `PromQL + SQL` 面板模式，适合 ECharts 聚合/排序/透视 |
| 数据源插件系统 | ⚠️ 部分 | 目前通过接入适配层和 Prometheus 生态兼容方式覆盖主流场景 |
| 社区生态插件 | ⚠️ 部分 | 目前以“内建能力 + 导入能力”为主，生态依赖下降 |

## 落地迁移路线

### 1. 接入层切换（零改造）

先保持现有 Exporter/Agent 不变：

1. 保持现有 Prometheus text scrape 与 remote_write 目标继续输出到 Sentinel233。
2. 配置 `/api/v1/write` 为兼容写入入口。
3. 将 Prometheus `scrape_configs` 中的 `static_configs` 通过 `/api/compat/import?source=prometheus-config` 导入为 Sentinel targets。
4. 在 Sentinel233 上验证关键指标采集率、写入成功率与查询延迟。

### 2. 看板迁移（可审计）

1. 从 Grafana 导出 dashboard JSON。
2. 在 Sentinel233 「仪表盘 - 从预设创建/导入」页面导入。
3. 如使用 Grafana datasource provisioning，先通过 `/api/compat/import?source=grafana-datasources` 导入 datasource 映射。
4. 将常用 dashboard 定位为「预设模板」，并统一保存在团队 Git 仓库中。
5. 通过 Dashboard API 与版本化文档追踪迁移清单。

### 3. 运维治理统一

1. 告警改在 Sentinel233 告警模块统一管理。
2. 将 Prometheus rule file 通过 `/api/compat/import?source=prometheus-rules` 导入为 Sentinel alert rules。
3. 使用一套租户+角色体系集中管理用户与鉴权。
4. 将 Prometheus + Grafana 逐步下线，仅保留 sentinel 入站链路。

## 建议的生产部署模板（最小可用）

1. 启动单台高可用优先级 server（先按 HA 扩展）
2. 按环境维度创建 tenant，并配置 operator 用户
3. 将现有 scrape 目标/alert rule 逐步迁移到 `api/targets` 与 `api/alert-rules`
4. 固化以下监控指标告警基线
   - `up == 0`（按 job + instance）
   - `process_resident_memory_bytes`（内存阈值）
   - `go_goroutines` 或服务自定义并发指标

## 你能从替代中收获的价值

- 部署面更小：减少一套外部 UI 运行时。
- 成本更可控：配置、告警、看板在同一系统里可追踪。
- 可迁移更快：从现有 Grafana 迁移采用“导入+兼容查询”双路径，先并行运行再逐步下线。

## 已知边界与路线（避免误用）

- 部分高级 Grafana 插件生态（例如高度定制的 panel 插件）需要在 Sentinel233 中先转化为“业务看板模板”。
- Prometheus 动态 service discovery（例如 Kubernetes/Consul/file_sd/http_sd）建议迁移期继续由 Prometheus Agent、Grafana Agent 或 Alloy 负责发现，并通过 `remote_write` 推送到 Sentinel；Sentinel 当前直接落地 `static_configs`。
- 复杂告警分组策略建议先在测试环境验证后再全量迁移。
- 仪表盘导入兼容的是常见面板类型，新增面板类型可通过模板化方式逐步补齐。

## 迁移验收清单

- [ ] `remote_write` 或 `scrape` 接入全部成功
- [ ] Prometheus `scrape_configs`、rule file、Grafana datasource provisioning 已通过 `/api/compat/import` 迁移并留档
- [ ] 关键 10 条告警规则通过告警通知链路验证
- [ ] 5-10 个核心 Dashboard 可导入并可视化
- [ ] 所有角色权限可在租户级验证
- [ ] 替代 Grafana 的运维手册更新到 Git 仓库

## Dashboard 迁移 API 示例

以下示例以 `http://<sentinel-host>:23390` 为例，先登录拿到 token 后可直接进行迁移自动化：

### 1) 登录拿 Token

```bash
curl -X POST http://127.0.0.1:23390/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"root","password":"root","tenant":"default"}'
```

返回字段中的 `token` 作为后续请求 `Authorization: Bearer <token>`。

### 2) 导入 Grafana dashboard JSON

```bash
curl -X POST http://127.0.0.1:23390/api/dashboards/import \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  --data-binary @grafana-dashboard.json
```

支持两类 JSON：

- Grafana 导出原始格式（`source` 可为 `grafana` 或自动识别）
- Sentinel 原生 dashboard JSON（`panels/layout/variables/tags` 为 JSON 字段）

### 3) 导出 Sentinel Dashboard 为可导入 JSON

```bash
curl -X GET "http://127.0.0.1:23390/api/dashboards/1/export?token=<token>"
```

也可结合 jq 转换为文件保存：

```bash
curl -sS -H "Authorization: Bearer <token>" \
  "http://127.0.0.1:23390/api/dashboards/1/export" \
  | jq -r '.data' > sentinel-export.json
```

### 3.1) 批量迁移演练脚本

仓库内置了一个可直接用于生产 rehearsal 的批量迁移脚本：

```powershell
pwsh ./scripts/dashboard-migrate.ps1 `
  -BaseUrl "http://127.0.0.1:23390" `
  -Tenant "default" `
  -Username "root" `
  -Password "root" `
  -ImportDir ".\\grafana-dashboards" `
  -ArchiveDir ".\\artifacts\\dashboard-exports" `
  -SummaryFile ".\\artifacts\\dashboard-migration-report.json"
```

脚本会依次完成：

1. 登录并获取 token
2. 批量导入目录下所有 Grafana JSON
3. 将导入后的 Dashboard 再导出为归档 JSON
4. 生成包含面板数量校验和兼容告警数的摘要报告

适合用于：

- 发布前批量 rehearsal
- 与原 Grafana 看板对照检查
- 留存迁移归档以便回滚或审计

### 4) 生产切换建议（并行窗口）

1. 在生产前先保留 Grafana 只读期：Sentinel 与 Grafana 同时运行 1-2 周。  
2. 将关键告警、关键看板全部导入到 Sentinel 并对照告警触发与查询延迟。  
3. 验收通过后逐步停用 Grafana 的 Dashboard 与查询入口，仅保留 Sentinel 写入链路。  
4. 将变更动作纳入版本化脚本（Git 仓库 + gh release 附件 + 变更记录）。

该文档适用于：希望用单体式方式接管监控运维，而不是再维护一套“Grafana + 数据面多组件”架构的团队。

## 本机 Agent 直控模式

如果你的目标是“让本机 agent 或自动化运行时直接做图”，Sentinel233 现在原生预留了 loopback-only HTTP 控制面：

- 路径前缀：`/api/local/v1/*`
- 默认启用，但只允许 `127.0.0.1` / `::1`
- 不需要人工登录或 token
- 适合：
  - 本地 agent 自动创建 dashboard
  - Codex/脚本批量追加 panel
  - 动态生成 `PromQL + SQL + ECharts` 面板

推荐配置：

```yaml
local_api:
  enabled: true
  tenant_id: 1
```

推荐调用顺序：

1. `GET /api/local/v1/capabilities`
2. `POST /api/local/v1/dashboards` 或 `POST /api/local/v1/dashboards/import`
3. `POST /api/local/v1/dashboards/{id}/panels`
4. `GET /api/local/v1/dashboards/{id}` 获取最终面板结构
