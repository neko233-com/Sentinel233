# Sentinel233 Server

[![CI](https://github.com/neko233-com/Sentinel233/actions/workflows/ci.yml/badge.svg)](https://github.com/neko233-com/Sentinel233/actions/workflows/ci.yml)
[![Lint](https://github.com/neko233-com/Sentinel233/actions/workflows/lint.yml/badge.svg)](https://github.com/neko233-com/Sentinel233/actions/workflows/lint.yml)

**存储 + 监控一体化监控面板服务器**。`sentinel233-server` = Prometheus Agent + Grafana + 原生 Sentinel client ingestion，100 台游戏服务器只需一台 Sentinel233 Server，极大降低监控运维成本。

## 功能概览

| 能力 | 说明 |
|------|------|
| **自研 TSDB** | WAL 崩溃恢复 + 内存存储 + 自动数据保留清理 |
| **完整 PromQL** | 瞬时/范围向量、二元运算、聚合(sum/avg/min/max/count/stddev/topk...)、30+ 内置函数(rate/increase/delta/abs/ceil/floor/round/sqrt/log...)、标签匹配(=, !=, =~, !~) |
| **Prometheus 兼容 API** | `/api/v1/query`、`/api/v1/query_range`、`/api/v1/series`、`/api/v1/label/{name}/values` |
| **Scrape 采集** | 拉模式采集 + OpenMetrics 解析 + 动态目标管理 |
| **常用集成预设** | Go、JVM、MySQL、PostgreSQL、Redis、Nginx、Linux node_exporter、Docker/cAdvisor、Kubernetes、Blackbox |
| **告警引擎** | 规则评估 + pending→firing 状态机 + Webhook 通知 |
| **独立实现的可落地监控系统** | 一体化的采集、存储、查询、告警、面板与权限体系（无外部依赖）；UI 视觉上借鉴 Grafana 的操作便捷性，但实现链路与数据模型与其不同 |
| **Dashboard 管理** | 创建/编辑/删除仪表盘、Grafana 导入导出、兼容性预检、动态添加面板 |
| **可视化运行时** | Chart.js + ECharts 双渲染器，支持更贴近 Grafana 的图表效果 |
| **数据变换** | 面板支持 `PromQL` 直出或 `PromQL + SQL` 变换，用于 ECharts 绘制与聚合透视 |
| **i18n 国际化** | 中文 / English / 日本語，默认支持 3 语言 |
| **轻量 Agent** | 内置 Go runtime 指标采集 (CPU/内存/goroutine)，一键部署 |
| **SQLite 元数据** | Dashboard、用户、设置持久化，纯 Go 无 CGO 依赖 |
| **多租户** | 租户隔离 (Dashboard/用户/告警规则/采集目标)，RBAC (viewer/operator/admin)，默认租户 default |

## 快速开始

### 一键安装（服务端）

```bash
curl -fsSL https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install-server.sh | bash
```

```powershell
iwr -useb https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install-server.ps1 | iex
```

### 一键安装（Agent）

```bash
curl -fsSL https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install.sh | bash
```

```powershell
iwr -useb https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install.ps1 | iex
```

### 指定版本

```bash
curl -fsSL .../install-server.sh | bash -s -- v0.1.0
```

### go install

```bash
go install github.com/neko233-com/Sentinel233/cmd/sentinel233-server@latest
go install github.com/neko233-com/Sentinel233/cmd/sentinel233-agent@latest
```

## 启动

```bash
# 启动服务端（默认端口 23390）
sentinel233-server

# 指定配置文件
sentinel233-server -config sentinel233.yaml

# 自定义端口和数据目录
sentinel233-server -addr :8080 -data /var/lib/sentinel233

# 启动 Agent（每台游戏服务器部署一个）
sentinel233-agent -server http://your-server:23390
```

访问 `http://localhost:23390` 打开监控面板。

**默认账号**：`root` / `root`

## 配置

配置文件 `sentinel233.yaml`：

```yaml
server:
  addr: "0.0.0.0"
  port: 23390

storage:
  data_dir: "./data"
  retention_days: 15

scrape:
  interval_seconds: 15
  timeout_seconds: 10
  targets:
    - name: "game-server-1"
      endpoint: "http://192.168.1.100:23391/metrics"
      labels:
        instance: "192.168.1.100"
        job: "game"

alert:
  enabled: true
  rules:
    - name: "InstanceDown"
      expr: "up == 0"
      duration: "1m"
      severity: "critical"
      notify_url: "https://your-webhook.url"

local_api:
  enabled: true
  tenant_id: 1

i18n:
  default: "zh-CN"
  supported: ["zh-CN", "en-US", "ja-JP"]
```

## PromQL 示例

```
# 基础查询
up
http_requests_total{method="GET"}

# 聚合
sum(http_requests_total) by (job)
avg(process_cpu_seconds_total)

# 函数
rate(http_requests_total[5m])
increase(http_requests_total[1h])
avg_over_time(cpu_usage[5m])

# 二元运算
http_requests_total / 60
node_memory_total - node_memory_free

# 告警表达式
up == 0
process_resident_memory_bytes > 1073741824
```

## 工作流

| 步骤 | 说明 |
|------|------|
| 部署服务端 | 一键安装 Sentinel233 服务器 |
| 部署 Agent | 每台游戏服务器部署 `sentinel233-agent`，指向服务端 |
| 配置采集 | 在服务端添加 Agent 为采集目标 |
| 创建面板 | 在 Web UI 创建 Dashboard，添加 PromQL 面板 |
| 配置告警 | 设置告警规则和 Webhook 通知地址 |

## 目标架构

```
┌─────────────────────────────────────────────────────┐
│                  Sentinel233 Server                  │
│              (一台强服务器，端口 23390)               │
│                                                      │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────┐  │
│  │  TSDB    │ │  PromQL  │ │  Alert   │ │  Web   │  │
│  │  Engine  │ │  Engine  │ │  Manager │ │   UI   │  │
│  └──────────┘ └──────────┘ └──────────┘ └────────┘  │
│       ▲                                              │
└───────┼──────────────────────────────────────────────┘
        │ Scrape (pull)
   ┌────┴────┬─────────┬──────────┐
   ▼         ▼         ▼          ▼
┌──────┐ ┌──────┐ ┌──────┐ ┌──────────┐
│Agent │ │Agent │ │Agent │ │ Prometheus│
│:23391│ │:23391│ │:23391│ │ 兼容节点  │
│  S1  │ │  S2  │ │  S3  │ │  S4~S100 │
└──────┘ └──────┘ └──────┘ └──────────┘
  Game     Game     Game      Game
  Server   Server   Server    Servers
```

## Docker 部署

```bash
docker compose up -d
```

```bash
docker build -t sentinel233-server .
docker run -d -p 23390:23390 -p 23391:23391 -v sentinel233-data:/data sentinel233-server
```

## API 参考

### Prometheus 兼容 API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/v1/query` | GET | 瞬时查询 |
| `/api/v1/query_range` | GET | 范围查询 |
| `/api/v1/series` | GET | 序列列表 |
| `/api/v1/label/{name}/values` | GET | 标签值 |
| `/api/v1/targets` | GET | 采集目标 |
| `/api/v1/alerts` | GET | 活跃告警 |
| `/api/v1/status/config` | GET | 配置信息 |
| `/api/v1/status/buildinfo` | GET | 构建信息 |
| `/api/v1/status/runtime` | GET | 运行时信息 |
| `/api/v1/write` | POST | Prometheus Remote Write，支持 snappy block 压缩 protobuf WriteRequest |

`/api/v1/write` 会保留 Prometheus 侧原始 labels 并写入内置 TSDB，适合把现有 Prometheus agent、Grafana Agent、Alloy 或其他 remote_write sender 指向 Sentinel233 Server。

### Sentinel 原生写入 API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/sentinel/v1/capabilities` | GET | 原生 client 能力描述 |
| `/api/sentinel/v1/write` | POST | Sentinel 原生 JSON 样本写入 |

### Dashboard API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/dashboards` | GET | 列表 |
| `/api/dashboards` | POST | 创建 |
| `/api/dashboards/import` | POST | 导入 Grafana JSON 或标准面板 JSON |
| `/api/dashboards/{id}` | GET | 详情 |
| `/api/dashboards/{id}/export` | GET | 导出 Grafana JSON |
| `/api/dashboards/{id}` | PUT | 更新 |
| `/api/dashboards/{id}` | DELETE | 删除 |

### Local Agent API

仅允许本机 `127.0.0.1` / `::1` 访问，目的是让本地 agent、自动化脚本或 Codex 运行时直接操控 dashboard，不需要人工登录拿 token。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/local/v1/capabilities` | GET | 返回本机 agent 能力描述 |
| `/api/local/v1/dashboards` | GET | 列出 dashboard |
| `/api/local/v1/dashboards` | POST | 直接创建 dashboard |
| `/api/local/v1/dashboards/import` | POST | 直接导入 Grafana 或 Sentinel dashboard JSON |
| `/api/local/v1/dashboards/{id}` | GET | 获取 dashboard |
| `/api/local/v1/dashboards/{id}` | PUT | 更新 dashboard |
| `/api/local/v1/dashboards/{id}/panels` | POST | 追加单个 panel |

示例：本机 agent 直接创建一个带 SQL 变换的 ECharts 面板

```bash
curl -X POST http://127.0.0.1:23390/api/local/v1/dashboards \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Agent Generated Dashboard",
    "description": "Created by local agent",
    "panels": [{
      "title": "CPU TopN",
      "type": "bar",
      "queryType": "sql",
      "sourceQuery": "rate(process_cpu_seconds_total[5m])",
      "query": "SELECT series AS label, MAX(value) AS value, MAX(time) AS time FROM ? GROUP BY series ORDER BY value DESC LIMIT 10",
      "renderer": "echarts"
    }],
    "layout": {},
    "variables": [],
    "tags": ["local-agent"]
  }'
```

示例：给现有 dashboard 直接追加 panel

```bash
curl -X POST http://127.0.0.1:23390/api/local/v1/dashboards/1/panels \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Memory Usage",
    "type": "timeseries",
    "queryType": "promql",
    "query": "process_resident_memory_bytes",
    "renderer": "echarts",
    "unit": "bytes"
  }'
```

### 系统 API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/tenants` | GET | 租户列表 |
| `/api/tenants` | POST | 创建租户 (admin) |
| `/api/tenants/{id}` | GET/PUT/DELETE | 租户管理 |
| `/api/users` | GET | 用户列表 (admin) |
| `/api/users` | POST | 创建用户 (admin) |
| `/api/users/{username}/role` | PUT | 修改角色 (admin) |
| `/api/users/{username}` | DELETE | 删除用户 (admin) |
| `/api/targets` | GET/POST/DELETE | 采集目标管理 |
| `/api/alert-rules` | GET/POST/PUT/DELETE | 告警规则管理 |
| `/api/alerts` | GET | 告警列表 |
| `/api/alerts/history` | GET | 告警历史 |
| `/api/system/stats` | GET | 系统统计 |
| `/api/login` | POST | 登录 (支持 tenant 字段) |
| `/api/i18n/{lang}` | GET | 国际化翻译 |
| `/healthz` | GET | 健康检查 |
| `/readyz` | GET | 就绪检查 |

## 本地脚本

| 脚本 | 说明 |
|------|------|
| `scripts/install.ps1` | Windows Agent 安装 |
| `scripts/install.sh` | Linux/macOS Agent 安装 |
| `scripts/install-server.ps1` | Windows Server 安装 |
| `scripts/install-server.sh` | Linux/macOS Server 安装 |
| `scripts/dashboard-migrate.ps1` | 批量导入 Grafana dashboard、导出归档并生成校验报告 |

## 开发

```bash
make test           # 运行测试
make test-race      # 竞态检测测试
make build          # 构建二进制
make run-server     # 启动开发服务器
make run-agent      # 启动 Agent
make lint           # golangci-lint
make smoke          # 构建 + 冒烟测试
make docker-build   # Docker 构建
make docker-run     # Docker 启动
```

## 命令行参考

### sentinel233-server (服务端)

```
sentinel233-server [flags]
  -addr string      监听地址 (默认 ":23390")
  -config string    配置文件路径
  -data string      数据目录 (默认 "./data")
  -version          显示版本
```

### sentinel233-agent (采集 Agent)

```
sentinel233-agent [flags]
  -addr string      指标暴露地址 (默认 ":23391")
  -server string    Sentinel233 服务端地址 (默认 "http://localhost:23390")
  -interval int     推送间隔秒数 (默认 15)
  -version          显示版本
```

## 文档

| 文档 | 说明 |
|------|------|
| [CHANGELOG.md](CHANGELOG.md) | 版本变更记录 |
| [configs/sentinel233.yaml.example](configs/sentinel233.yaml.example) | 配置文件示例 |
| [docs/integrations.md](docs/integrations.md) | 接入与采集方式说明 |
| [docs/grafana-replacement-guide.md](docs/grafana-replacement-guide.md) | 从 Grafana 迁移到 Sentinel233 的落地指南 |
| [docs/github-release-guide.md](docs/github-release-guide.md) | 使用 `gh` 进行发布与文档发布流程 |
| [docs/github-release-notes.md](docs/github-release-notes.md) | 本次发布说明与可直接发布的 release notes |

## Grafana 迁移与 SQL/ECharts 面板

- 导入 Grafana JSON 前，Web UI 会先给出兼容性摘要，提示多 target、transformations、复杂 datasource 等需人工复核项。
- 导入后的面板默认保留原始 Grafana 元信息，并优先用 `ECharts` 贴近原可视化观感。
- 新增面板时可选择：
  - `PromQL 直出`：适合普通监控曲线、表格、Gauge、Stat。
  - `PromQL + SQL 变换`：先拉 PromQL 样本点，再用 SQL（`FROM ?`）聚合、排序、透视，最后交给 `ECharts` 或表格渲染。
- 如果要让本机 agent 零人工快速做图，优先使用 `Local Agent API`，它天然跳过登录 token，但只接受 loopback 请求。
- 生产批量迁移演练可使用：

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

## GitHub Actions

| 工作流 | 触发 | 说明 |
|--------|------|------|
| `ci.yml` | push/PR to main | 三平台测试 + vet + build + ShellCheck |
| `release.yml` | tag `v*` | 多平台构建 + GitHub Release |

## License

[MIT](LICENSE)

Go 1.26 · MIT
