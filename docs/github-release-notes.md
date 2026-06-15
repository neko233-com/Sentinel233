## Sentinel233 v0.2.2

### Highlights
- 完成 Sentinel233 与 Grafana 并行迁移场景的落地文档体系。
- 新增「替代 Grafana 落地指南」，定义完整迁移策略、能力对照与验收清单。
- 新增批量迁移 rehearsal 脚本，可导入一批 Grafana dashboards、回导归档并生成校验报告。
- Dashboard 前端支持 ECharts 渲染器与 `PromQL + SQL` 变换，便于实现更贴近 Grafana 的图表效果。
- 补充 `docs/github-release-guide.md`，给出基于 `gh` 的发布、校验、回滚流程。
- 明确将 Grafana 作为迁移源的导入/导出与兼容接入策略，便于实际生产裁剪。
- 新增 Dashboard API 落地链路：`POST /api/dashboards/import`、`GET /api/dashboards/{id}/export`，用于 API 方式迁移 Grafana 看板。

### API / 运维改进说明
- 强化 integrations 文档中的生产迁移路径说明（Scrape/remote_write 分层模型、角色与租户联动）。
- 导入后的面板保留 compatibility/source PromQL/renderer 元信息，便于 UI、API 和脚本统一处理。
- 归档了可直接用于发布会前对外沟通的版本说明文档。

### 使用建议
- 使用 `/api/v1/write` / `/api/sentinel/v1/*` 完成兼容接入。
- 先在并行环境验证关键指标面板与告警，再逐步下线 Grafana。
