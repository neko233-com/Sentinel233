# Changelog

All notable changes to Sentinel233 will be documented in this file.

## [v0.2.2] - 2026-06-15

### Added
- Added operational documentation for replacing Grafana in production with Sentinel233.
- Added migration guide (`docs/grafana-replacement-guide.md`) covering capability matrix, rollout plan, and acceptance checklist.
- Added GitHub release playbook (`docs/github-release-guide.md`) and release notes (`docs/github-release-notes.md`) for `gh`-based publishing.
- Added dashboard migration rehearsal script (`scripts/dashboard-migrate.ps1`) for batch import, export archiving, and validation summaries.
- Clarified integration docs with an explicit Grafana migration workflow.
- Added SQL-transformed dashboard panels and ECharts renderer support for closer Grafana visual parity.

### Changed
- Enhanced Grafana dashboard import/export metadata so imported panels retain compatibility warnings, source PromQL, and renderer hints.
- Improved dashboard authoring UX with clearer query mode, renderer, and panel configuration semantics.

### Docs
- Updated README documentation index to include new operational and release documents.

## [v0.1.0] - 2026-06-11

### Added
- TSDB storage engine with WAL (Write-Ahead Log) for crash recovery
- Full PromQL query engine with support for:
  - Instant vectors and range vectors
  - Binary operators (+, -, *, /, ^, %, ==, !=, >, <, >=, <=)
  - Aggregations (sum, avg, min, max, count, stddev, stdvar, topk, bottomk, group)
  - Functions (rate, irate, increase, avg_over_time, min_over_time, max_over_time, sum_over_time, count_over_time, last_over_time, abs, ceil, floor, round, sqrt, ln, log2, log10, exp, clamp_min, clamp_max, delta, deriv, resets, changes, absent, vector)
  - Label matchers (=, !=, =~, !~)
- Prometheus-compatible HTTP API:
  - `/api/v1/query` - instant query
  - `/api/v1/query_range` - range query
  - `/api/v1/series` - series metadata
  - `/api/v1/label/{name}/values` - label values
  - `/api/v1/targets` - scrape targets
  - `/api/v1/alerts` - active alerts
  - `/api/v1/status/config` - configuration
  - `/api/v1/status/buildinfo` - build information
  - `/api/v1/status/runtime` - runtime stats
- Scrape manager with OpenMetrics parser
- Alert manager with rule evaluation and webhook notifications
- Dashboard management (CRUD) with SQLite metadata store
- Grafana-style dark theme Web UI:
  - Overview dashboard with stats cards and charts
  - PromQL Explore page with interactive query
  - Alert management page
  - Target management page
  - Settings page
  - Chart.js time-series visualization
  - Gauge, stat, and timeseries panel types
- i18n support (zh-CN, en-US, ja-JP) with embedded locale files
- One-click install scripts:
  - `scripts/install.ps1` - Agent installer for Windows
  - `scripts/install-server.ps1` - Server installer for Windows
  - `scripts/install.sh` - Agent installer for Linux/macOS
  - `scripts/install-server.sh` - Server installer for Linux/macOS
- Docker support with Dockerfile and docker-compose.yml
- GitHub Actions CI/CD pipeline
- Comprehensive test suite for all packages
- Built-in metrics agent (`sentinel233-agent`)
