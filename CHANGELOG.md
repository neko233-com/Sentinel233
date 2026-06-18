# Changelog

All notable changes to Sentinel233 will be documented in this file.

## [Unreleased]

### Added
- Added first-class `/api/ecosystem/capabilities`, `/api/ecosystem/import`, and `/api/ecosystem/alertmanager/webhook` endpoints for stable Grafana/Prometheus/Alertmanager ecosystem integration.
- Added `scripts/docker-ecosystem-e2e.ps1` as the primary Docker ecosystem verification script.
- Added Prometheus API discovery and metadata endpoints including `/api/v1/labels`, `/api/v1/metadata`, `/api/v1/targets/metadata`, `/api/v1/status/tsdb`, `/api/v1/alertmanagers`, and `/api/v1/query_exemplars`.
- Expanded Docker ecosystem E2E validation to cover login, ecosystem imports, remote_write, PromQL, dashboard import/export, and Alertmanager webhook.
- Added an independent HTML-driven GitHub Pages documentation site under `site/`.

### Changed
- Reframed docs, UI copy, CI job names, and verification commands from temporary migration language to stable ecosystem integration language.
- Standardized `/api/v1/query_range` responses to Prometheus matrix shape and allowed GET/POST query forms.
- Preserved Grafana target objects during dashboard import/export and rendered multiple PromQL targets in dashboard panels.
- Expanded PromQL coverage for braced selectors, trailing aggregation grouping, `or/and/unless`, `time`, `timestamp`, `scalar`, `sort`, and `histogram_quantile`.
- Optimized CI and release packaging with Go cache, Docker E2E coverage, and matrixed release builds.

### Removed
- Removed old ecosystem aliases, old wrapper scripts, and old guide paths so new deployments use only the current ecosystem API and tooling.

## [v0.2.3] - 2026-06-15

### Added
- Added operational documentation for replacing Grafana in production with Sentinel233.
- Added migration guide (`docs/ecosystem-integration-guide.md`) covering capability matrix, rollout plan, and acceptance checklist.
- Added GitHub release playbook (`docs/github-release-guide.md`) and release notes (`docs/github-release-notes.md`) for `gh`-based publishing.
- Added dashboard migration rehearsal script (`scripts/dashboard-migrate.ps1`) for batch import, export archiving, and validation summaries.
- Clarified integration docs with an explicit Grafana migration workflow.
- Added SQL-transformed dashboard panels and ECharts renderer support for closer Grafana visual parity.

### Changed
- Enhanced Grafana dashboard import/export metadata so imported panels retain integration warnings, source PromQL, and renderer hints.
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
- Prometheus HTTP API:
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
