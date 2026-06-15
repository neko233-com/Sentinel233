let currentLang = localStorage.getItem('sentinel233_lang') || 'zh-CN';
let translations = {};
let charts = {};
let session = JSON.parse(localStorage.getItem('sentinel233_session') || 'null');
let activeConfig = null;
let dashboardVariableState = {};
let activeDashboardPanels = [];
let activeDashboardId = null;

const API = '';

const pageMeta = {
  overview: ['总览', 'Prometheus 兼容采集、存储、查询和后台配置合一'],
  explore: ['指标探索', '用预设和 PromQL 快速定位指标，不必从空白输入框开始'],
  dashboards: ['仪表盘', '覆盖常用 Grafana 面板类型，并提供可直接落地的预设'],
  alerts: ['告警', '查看活跃告警、历史和规则入口'],
  targets: ['采集目标', '管理内置 agent 与外部 /metrics 目标'],
  integrations: ['接入中心', '接入 Go /metrics、MySQL、Linux node_exporter 和 Sentinel 高性能客户端'],
  config: ['配置中心', '存储、采集、agent 和告警在一个后台界面里统一应用'],
  docs: ['内置文档', '把常见监控方案、PromQL 和部署注意事项放在控制台内'],
};

const queryPresets = [
  { name: '实例存活', expr: 'up', note: '确认每个采集目标是否在线' },
  { name: '进程 CPU', expr: 'rate(process_cpu_seconds_total[5m])', note: '查看服务自身 CPU 使用趋势' },
  { name: '进程内存', expr: 'process_resident_memory_bytes', note: '排查常驻内存增长' },
  { name: '序列规模', expr: 'count({__name__=~".+"})', note: '估算当前 TSDB 基数压力' },
  { name: 'HTTP 请求速率', expr: 'sum(rate(http_requests_total[5m])) by (job)', note: '适合 Web 服务入口观测' },
  { name: '错误率', expr: 'sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))', note: '服务质量的第一眼指标' },
  { name: 'Grafana 宏速率', expr: 'sum(rate(http_requests_total[$__rate_interval])) by (job)', note: '兼容 Grafana $__rate_interval 宏' },
];

const dashboardPresets = [
  {
    title: '主机与进程基础盘',
    description: '适合 Sentinel233 自身、单机服务和轻量 VM。',
    panels: [
      { title: '实例存活', type: 'stat', query: 'up' },
      { title: 'CPU 使用', type: 'timeseries', query: 'rate(process_cpu_seconds_total[5m])' },
      { title: '常驻内存', type: 'timeseries', query: 'process_resident_memory_bytes' },
      { title: '序列数量', type: 'stat', query: 'count({__name__=~".+"})' },
    ],
  },
  {
    title: 'Web 服务 SLO',
    description: '请求量、错误率、延迟和目标健康的常用组合。',
    panels: [
      { title: '请求速率', type: 'timeseries', query: 'sum(rate(http_requests_total[5m])) by (job)' },
      { title: '错误率', type: 'timeseries', query: 'sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))' },
      { title: 'P95 延迟', type: 'timeseries', query: 'histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))' },
      { title: '目标在线', type: 'table', query: 'up' },
    ],
  },
  {
    title: '存储与基数巡检',
    description: '用于发现高基数、样本爆炸和保留期压力。',
    panels: [
      { title: '总序列', type: 'stat', query: 'count({__name__=~".+"})' },
      { title: '按指标分组', type: 'table', query: 'count by (__name__)({__name__=~".+"})' },
      { title: '样本写入', type: 'timeseries', query: 'rate(sentinel_samples_appended_total[5m])' },
      { title: '目标失败', type: 'table', query: 'up == 0' },
    ],
  },
  {
    title: 'Linux Node Exporter 总览',
    description: '覆盖 Linux CPU、内存、磁盘、网络和文件系统容量。',
    panels: [
      { title: '主机在线', type: 'stat', query: 'up{job=~"node|linux|node_exporter"}' },
      { title: 'CPU 使用率', type: 'timeseries', query: '100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[$__rate_interval])) * 100)', unit: 'percent' },
      { title: '内存使用率', type: 'gauge', query: '(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100', unit: 'percent' },
      { title: '磁盘空间使用率', type: 'bar', query: '100 - ((node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"} * 100) / node_filesystem_size_bytes{fstype!~"tmpfs|overlay"})', unit: 'percent' },
      { title: '网络接收', type: 'timeseries', query: 'rate(node_network_receive_bytes_total{device!~"lo"}[$__rate_interval])', unit: 'bytes' },
      { title: '网络发送', type: 'timeseries', query: 'rate(node_network_transmit_bytes_total{device!~"lo"}[$__rate_interval])', unit: 'bytes' },
    ],
  },
  {
    title: 'MySQL 运行总览',
    description: 'mysqld_exporter 常用连接、查询、慢查询、InnoDB 和线程指标。',
    panels: [
      { title: 'MySQL 在线', type: 'stat', query: 'mysql_up' },
      { title: '当前连接', type: 'gauge', query: 'mysql_global_status_threads_connected' },
      { title: 'QPS', type: 'timeseries', query: 'rate(mysql_global_status_queries[$__rate_interval])' },
      { title: '慢查询速率', type: 'timeseries', query: 'rate(mysql_global_status_slow_queries[$__rate_interval])' },
      { title: 'InnoDB Buffer Pool 命中率', type: 'gauge', query: '(1 - rate(mysql_global_status_innodb_buffer_pool_reads[$__rate_interval]) / rate(mysql_global_status_innodb_buffer_pool_read_requests[$__rate_interval])) * 100', unit: 'percent' },
      { title: '线程状态', type: 'table', query: 'mysql_global_status_threads_running' },
    ],
  },
  {
    title: 'Go Runtime 深度性能',
    description: 'Go 服务 runtime、GC、goroutine 和 HTTP 入口性能。',
    panels: [
      { title: 'Goroutines', type: 'stat', query: 'go_goroutines or sentinel_runtime_goroutines' },
      { title: 'Heap Alloc', type: 'timeseries', query: 'go_memstats_heap_alloc_bytes or sentinel_runtime_heap_alloc_bytes', unit: 'bytes' },
      { title: 'GC 暂停 P99', type: 'timeseries', query: 'histogram_quantile(0.99, sum(rate(go_gc_duration_seconds_bucket[$__rate_interval])) by (le))' },
      { title: 'HTTP 请求速率', type: 'timeseries', query: 'sum(rate(http_requests_total[$__rate_interval])) by (job)' },
      { title: 'HTTP P95 延迟', type: 'timeseries', query: 'histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[$__rate_interval])) by (le, job))' },
      { title: '原生 Sentinel 样本', type: 'table', query: '{source="sentinel_native"}' },
    ],
  },
  {
    title: 'JVM 服务运行总览',
    description: '兼容 Micrometer、Prometheus Java agent 和 Spring Boot Actuator。',
    panels: [
      { title: 'JVM 在线', type: 'stat', query: 'up{runtime=~"jvm|java"} or up{job=~".*jvm.*|.*java.*"}' },
      { title: 'Heap 使用率', type: 'gauge', query: 'sum(jvm_memory_used_bytes{area="heap"}) / sum(jvm_memory_max_bytes{area="heap"}) * 100', unit: 'percent' },
      { title: 'GC 暂停', type: 'timeseries', query: 'rate(jvm_gc_pause_seconds_sum[$__rate_interval]) / rate(jvm_gc_pause_seconds_count[$__rate_interval])' },
      { title: '线程数', type: 'timeseries', query: 'jvm_threads_live_threads' },
      { title: 'HTTP 请求速率', type: 'timeseries', query: 'sum(rate(http_server_requests_seconds_count[$__rate_interval])) by (job, status)' },
      { title: 'HTTP P95 延迟', type: 'timeseries', query: 'histogram_quantile(0.95, sum(rate(http_server_requests_seconds_bucket[$__rate_interval])) by (le, job))' },
    ],
  },
  {
    title: 'PostgreSQL 运行总览',
    description: 'postgres_exporter 常用连接、事务、缓存命中和复制延迟。',
    panels: [
      { title: 'PostgreSQL 在线', type: 'stat', query: 'pg_up' },
      { title: '连接数', type: 'gauge', query: 'sum(pg_stat_activity_count) by (datname)' },
      { title: '事务提交速率', type: 'timeseries', query: 'sum(rate(pg_stat_database_xact_commit[$__rate_interval])) by (datname)' },
      { title: '回滚速率', type: 'timeseries', query: 'sum(rate(pg_stat_database_xact_rollback[$__rate_interval])) by (datname)' },
      { title: '缓存命中率', type: 'gauge', query: 'sum(pg_stat_database_blks_hit) / (sum(pg_stat_database_blks_hit) + sum(pg_stat_database_blks_read)) * 100', unit: 'percent' },
      { title: '复制延迟', type: 'table', query: 'pg_replication_lag' },
    ],
  },
  {
    title: 'Redis 运行总览',
    description: 'redis_exporter 常用内存、连接、命令、keyspace 和持久化指标。',
    panels: [
      { title: 'Redis 在线', type: 'stat', query: 'redis_up' },
      { title: '内存使用', type: 'timeseries', query: 'redis_memory_used_bytes', unit: 'bytes' },
      { title: '连接客户端', type: 'gauge', query: 'redis_connected_clients' },
      { title: '命令速率', type: 'timeseries', query: 'rate(redis_commands_processed_total[$__rate_interval])' },
      { title: '命中率', type: 'gauge', query: 'rate(redis_keyspace_hits_total[$__rate_interval]) / (rate(redis_keyspace_hits_total[$__rate_interval]) + rate(redis_keyspace_misses_total[$__rate_interval])) * 100', unit: 'percent' },
      { title: '慢查询', type: 'timeseries', query: 'rate(redis_slowlog_length[$__rate_interval])' },
    ],
  },
  {
    title: 'Nginx 入口总览',
    description: 'nginx-prometheus-exporter 和 stub_status 入口的连接、请求与状态观测。',
    panels: [
      { title: 'Nginx 在线', type: 'stat', query: 'nginx_up' },
      { title: '活跃连接', type: 'gauge', query: 'nginx_connections_active' },
      { title: '请求速率', type: 'timeseries', query: 'rate(nginx_http_requests_total[$__rate_interval])' },
      { title: 'Reading/Writing/Waiting', type: 'table', query: 'nginx_connections_reading or nginx_connections_writing or nginx_connections_waiting' },
      { title: '接受连接速率', type: 'timeseries', query: 'rate(nginx_connections_accepted[$__rate_interval])' },
      { title: '处理连接速率', type: 'timeseries', query: 'rate(nginx_connections_handled[$__rate_interval])' },
    ],
  },
  {
    title: '容器与 cAdvisor 总览',
    description: 'Docker、containerd 和 cAdvisor 的容器 CPU、内存、网络与重启热点。',
    panels: [
      { title: '容器在线', type: 'stat', query: 'count(container_last_seen)' },
      { title: 'CPU 使用', type: 'timeseries', query: 'sum(rate(container_cpu_usage_seconds_total{container!=""}[$__rate_interval])) by (name, container)' },
      { title: '内存使用', type: 'timeseries', query: 'container_memory_working_set_bytes{container!=""}', unit: 'bytes' },
      { title: '网络接收', type: 'timeseries', query: 'sum(rate(container_network_receive_bytes_total[$__rate_interval])) by (name)', unit: 'bytes' },
      { title: '网络发送', type: 'timeseries', query: 'sum(rate(container_network_transmit_bytes_total[$__rate_interval])) by (name)', unit: 'bytes' },
      { title: '文件系统使用', type: 'bar', query: 'container_fs_usage_bytes{container!=""}', unit: 'bytes' },
    ],
  },
  {
    title: 'Kubernetes 集群总览',
    description: 'kube-state-metrics、kubelet/cAdvisor 和 API Server 的集群状态入口。',
    panels: [
      { title: '节点 Ready', type: 'stat', query: 'sum(kube_node_status_condition{condition="Ready",status="true"})' },
      { title: 'Pod 运行数', type: 'stat', query: 'sum(kube_pod_status_phase{phase="Running"})' },
      { title: 'Pod 重启', type: 'timeseries', query: 'sum(rate(kube_pod_container_status_restarts_total[$__rate_interval])) by (namespace, pod)' },
      { title: 'CPU Requests', type: 'timeseries', query: 'sum(kube_pod_container_resource_requests{resource="cpu"}) by (namespace)' },
      { title: 'Memory Requests', type: 'timeseries', query: 'sum(kube_pod_container_resource_requests{resource="memory"}) by (namespace)', unit: 'bytes' },
      { title: '异常 Pod', type: 'table', query: 'kube_pod_status_phase{phase=~"Failed|Pending|Unknown"}' },
    ],
  },
  {
    title: 'Blackbox 探测总览',
    description: 'HTTP/TCP/ICMP 外部可用性、证书、DNS 和延迟探测。',
    panels: [
      { title: '探测成功率', type: 'gauge', query: 'avg(probe_success) * 100', unit: 'percent' },
      { title: '探测延迟', type: 'timeseries', query: 'probe_duration_seconds' },
      { title: 'HTTP 状态码', type: 'table', query: 'probe_http_status_code' },
      { title: 'TLS 到期时间', type: 'table', query: '(probe_ssl_earliest_cert_expiry - time()) / 86400' },
      { title: 'DNS 查询耗时', type: 'timeseries', query: 'probe_dns_lookup_time_seconds' },
      { title: '失败目标', type: 'table', query: 'probe_success == 0' },
    ],
  },
];

const grafanaTypeMap = {
  graph: 'timeseries',
  timeseries: 'timeseries',
  stat: 'stat',
  gauge: 'gauge',
  table: 'table',
  barchart: 'bar',
  bargauge: 'bar',
  heatmap: 'heatmap',
  piechart: 'pie',
  histogram: 'bar',
};

const configPresets = {
  laptop: {
    label: '开发机轻量模式',
    patch: { storage: { retention_days: 7, flush_interval_seconds: 10, max_open_files: 256, compaction_every_seconds: 1800 }, scrape: { interval_seconds: 30, timeout_seconds: 8 } },
  },
  prod: {
    label: '生产默认模式',
    patch: { storage: { retention_days: 30, flush_interval_seconds: 5, max_open_files: 2048, compaction_every_seconds: 1800 }, scrape: { interval_seconds: 15, timeout_seconds: 10 } },
  },
  dense: {
    label: '高频采集模式',
    patch: { storage: { retention_days: 15, flush_interval_seconds: 3, max_open_files: 4096, compaction_every_seconds: 900 }, scrape: { interval_seconds: 5, timeout_seconds: 4 } },
  },
};

const integrationPresets = [
  {
    id: 'go-prometheus',
    title: 'Go 服务 /metrics',
    kind: 'Prometheus endpoint',
    endpoint: 'http://localhost:8080/metrics',
    labels: { job: 'go-service', runtime: 'go' },
    summary: '兼容 Prometheus Go client、gin/prometheus、echo middleware 等已有 /metrics。',
    snippet: `import "github.com/prometheus/client_golang/prometheus/promhttp"\n\nhttp.Handle("/metrics", promhttp.Handler())\nhttp.ListenAndServe(":8080", nil)`,
    dashboards: ['Go Runtime 深度性能', 'Web 服务 SLO', '主机与进程基础盘'],
  },
  {
    id: 'linux-node',
    title: 'Linux node_exporter',
    kind: 'Host exporter',
    endpoint: 'http://localhost:9100/metrics',
    labels: { job: 'node', os: 'linux' },
    summary: '接入 CPU、内存、磁盘、网络、文件系统等 Linux 主机指标。',
    snippet: `curl -LO https://github.com/prometheus/node_exporter/releases/latest/download/node_exporter-linux-amd64.tar.gz\nsudo ./node_exporter --web.listen-address=:9100`,
    dashboards: ['Linux Node Exporter 总览', '主机与进程基础盘', '存储与基数巡检'],
  },
  {
    id: 'mysql-exporter',
    title: 'MySQL / mysqld_exporter',
    kind: 'Database exporter',
    endpoint: 'http://localhost:9104/metrics',
    labels: { job: 'mysql', db: 'mysql' },
    summary: '通过 mysqld_exporter 接入连接数、慢查询、InnoDB、QPS/TPS 等数据库指标。',
    snippet: `export DATA_SOURCE_NAME='exporter:password@(127.0.0.1:3306)/'\n./mysqld_exporter --web.listen-address=:9104`,
    dashboards: ['MySQL 运行总览', '存储与基数巡检'],
  },
  {
    id: 'jvm-prometheus',
    title: 'JVM / Spring Boot /metrics',
    kind: 'Prometheus endpoint',
    endpoint: 'http://localhost:8080/actuator/prometheus',
    labels: { job: 'jvm-service', runtime: 'jvm' },
    summary: '兼容 Micrometer、Spring Boot Actuator 和 Prometheus Java agent，适合 Java/Kotlin 服务。',
    snippet: `management.endpoints.web.exposure.include=prometheus\nmanagement.endpoint.prometheus.enabled=true\n\n# scrape: http://localhost:8080/actuator/prometheus`,
    dashboards: ['JVM 服务运行总览', 'Web 服务 SLO'],
  },
  {
    id: 'postgres-exporter',
    title: 'PostgreSQL / postgres_exporter',
    kind: 'Database exporter',
    endpoint: 'http://localhost:9187/metrics',
    labels: { job: 'postgres', db: 'postgres' },
    summary: '接入 PostgreSQL 连接、事务、缓存命中、锁、复制和数据库体积指标。',
    snippet: `export DATA_SOURCE_NAME='postgresql://exporter:password@127.0.0.1:5432/postgres?sslmode=disable'\n./postgres_exporter --web.listen-address=:9187`,
    dashboards: ['PostgreSQL 运行总览', '存储与基数巡检'],
  },
  {
    id: 'redis-exporter',
    title: 'Redis / redis_exporter',
    kind: 'Cache exporter',
    endpoint: 'http://localhost:9121/metrics',
    labels: { job: 'redis', cache: 'redis' },
    summary: '接入 Redis 内存、连接、命令吞吐、keyspace 命中率、慢查询和持久化状态。',
    snippet: `export REDIS_ADDR='redis://127.0.0.1:6379'\n./redis_exporter --web.listen-address=:9121`,
    dashboards: ['Redis 运行总览', '存储与基数巡检'],
  },
  {
    id: 'nginx-exporter',
    title: 'Nginx / nginx-prometheus-exporter',
    kind: 'Edge exporter',
    endpoint: 'http://localhost:9113/metrics',
    labels: { job: 'nginx', edge: 'nginx' },
    summary: '接入 Nginx stub_status 连接、请求速率和入口健康指标。',
    snippet: `nginx-prometheus-exporter \\\n  --nginx.scrape-uri=http://127.0.0.1:8080/stub_status \\\n  --web.listen-address=:9113`,
    dashboards: ['Nginx 入口总览', 'Web 服务 SLO'],
  },
  {
    id: 'cadvisor',
    title: 'Docker / cAdvisor',
    kind: 'Container exporter',
    endpoint: 'http://localhost:8080/metrics',
    labels: { job: 'cadvisor', runtime: 'container' },
    summary: '接入 Docker/containerd 容器 CPU、内存、文件系统和网络指标。',
    snippet: `docker run -d --name=cadvisor -p 8080:8080 \\\n  --volume=/:/rootfs:ro --volume=/var/run:/var/run:ro \\\n  --volume=/sys:/sys:ro --volume=/var/lib/docker/:/var/lib/docker:ro \\\n  gcr.io/cadvisor/cadvisor:latest`,
    dashboards: ['容器与 cAdvisor 总览', '存储与基数巡检'],
  },
  {
    id: 'kubernetes',
    title: 'Kubernetes / kube-state-metrics',
    kind: 'Cluster metrics',
    endpoint: 'http://kube-state-metrics.kube-system.svc:8080/metrics',
    labels: { job: 'kube-state-metrics', platform: 'kubernetes' },
    summary: '接入 Kubernetes 节点、Pod、容器资源请求、重启和异常状态。',
    snippet: `helm repo add prometheus-community https://prometheus-community.github.io/helm-charts\nhelm install kube-state-metrics prometheus-community/kube-state-metrics -n kube-system\n\n# scrape kube-state-metrics plus kubelet/cAdvisor endpoints`,
    dashboards: ['Kubernetes 集群总览', '容器与 cAdvisor 总览'],
  },
  {
    id: 'blackbox-exporter',
    title: 'Blackbox exporter',
    kind: 'Probe exporter',
    endpoint: 'http://localhost:9115/probe?module=http_2xx&target=https://example.com',
    labels: { job: 'blackbox', probe: 'http' },
    summary: '接入 HTTP/TCP/ICMP 外部可用性、TLS 证书、DNS 和延迟探测。',
    snippet: `./blackbox_exporter --web.listen-address=:9115\n\n# scrape /probe?module=http_2xx&target=https://example.com`,
    dashboards: ['Blackbox 探测总览', 'Web 服务 SLO'],
  },
  {
    id: 'sentinel-lib-go',
    title: 'Sentinel233 Go Client Lib',
    kind: 'Native Sentinel client',
    endpoint: 'http://localhost:23390/api/sentinel/v1/write',
    labels: { job: 'sentinel-lib-go', client: 'sentinel233' },
    summary: '未来的原生高性能 client lib，目标是比 Prometheus client 暴露更丰富的运行时性能上下文。',
    snippet: `POST /api/sentinel/v1/write\n{\n  "resource":{"service.name":"api"},\n  "metrics":[{"name":"sentinel_runtime_goroutines","type":"gauge","samples":[{"value":42}]}]\n}\n\n# submodule: https://github.com/neko233-com/sentinel233-lib-go.git`,
    mode: 'native',
    dashboards: ['Go Runtime 深度性能', 'Web 服务 SLO'],
  },
];

async function loadI18n(lang) {
  try {
    const resp = await fetch(`${API}/api/i18n/${lang}`);
    translations = await resp.json();
    document.querySelectorAll('[data-i18n]').forEach(el => {
      const key = el.getAttribute('data-i18n');
      if (translations[key]) el.textContent = translations[key];
    });
  } catch (e) {
    console.warn('i18n load failed', e);
  }
}

function t(key) {
  return translations[key] || key;
}

async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
  if (session?.token) headers.Authorization = `Bearer ${session.token}`;
  const resp = await fetch(`${API}${path}`, { ...opts, headers });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok || data.status === 'error') {
    throw new Error(data.error || data.message || `HTTP ${resp.status}`);
  }
  return data;
}

function formatNumber(n) {
  const value = Number(n || 0);
  if (value >= 1e9) return `${(value / 1e9).toFixed(1)}B`;
  if (value >= 1e6) return `${(value / 1e6).toFixed(1)}M`;
  if (value >= 1e3) return `${(value / 1e3).toFixed(1)}K`;
  return Number.isFinite(value) ? value.toString() : '-';
}

function timeRangeToSeconds(range) {
  return ({ '1h': 3600, '6h': 21600, '12h': 43200, '24h': 86400, '7d': 604800, '30d': 2592000 })[range] || 86400;
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
}

function toast(message) {
  const old = document.querySelector('.toast');
  if (old) old.remove();
  const el = document.createElement('div');
  el.className = 'toast';
  el.textContent = message;
  document.body.appendChild(el);
  setTimeout(() => el.remove(), 3200);
}

function showModal(title, html) {
  document.getElementById('modal-title').textContent = title;
  document.getElementById('modal-body').innerHTML = html;
  document.getElementById('modal-overlay').classList.remove('hidden');
}

function closeModal() {
  document.getElementById('modal-overlay').classList.add('hidden');
}

function destroyCharts() {
  Object.values(charts).forEach(chart => {
    if (chart?.destroy) chart.destroy();
    if (chart?.dispose) chart.dispose();
  });
  charts = {};
}

async function queryPromQL(expr, start, end, step, variables = dashboardVariableState) {
  expr = applyGrafanaTemplate(expr, variables, { start, end, step });
  if (start && end) {
    return api(`/api/v1/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=${step || 15}`);
  }
  return api(`/api/v1/query?query=${encodeURIComponent(expr)}`);
}

function applyGrafanaTemplate(expr, variables = {}, context = {}) {
  const rangeSeconds = Math.max(1, Math.floor((context.end || Date.now() / 1000) - (context.start || Date.now() / 1000 - 3600)));
  const intervalSeconds = Math.max(1, Number(context.step || Math.ceil(rangeSeconds / 240)));
  const builtins = {
    __interval: `${intervalSeconds}s`,
    __interval_ms: String(intervalSeconds * 1000),
    __rate_interval: `${Math.max(intervalSeconds * 4, 60)}s`,
    __range: `${rangeSeconds}s`,
    __range_s: String(rangeSeconds),
    __range_ms: String(rangeSeconds * 1000),
    __from: String(Math.floor((context.start || 0) * 1000)),
    __to: String(Math.floor((context.end || Date.now() / 1000) * 1000)),
  };
  const allValues = { ...builtins, ...variables };
  const formatValue = (name, format) => {
    const raw = allValues[name];
    const value = Array.isArray(raw) ? raw : [raw ?? ''];
    if (format === 'regex') return value.map(v => {
      const text = String(v);
      if (text === '.*' || text === '$__all') return '.*';
      return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }).join('|');
    if (format === 'csv') return value.join(',');
    if (format === 'pipe') return value.join('|');
    if (format === 'json') return JSON.stringify(Array.isArray(raw) ? raw : raw ?? '');
    return value.join('|');
  };
  return String(expr || '')
    .replace(/\$\{([A-Za-z_][\w]*)(?::([A-Za-z]+))?\}/g, (_, name, format) => formatValue(name, format))
    .replace(/\[\[([A-Za-z_][\w]*)\]\]/g, (_, name) => formatValue(name))
    .replace(/\$([A-Za-z_][\w]*)/g, (_, name) => formatValue(name));
}

function normalizeSeries(data) {
  const raw = data?.data?.result || [];
  const series = [];
  raw.forEach((item, index) => {
    if (item?.values || item?.value) {
      const values = item.values || [item.value];
      series.push({
        label: labelsToString(item.metric || {}, index),
        values: values.map(v => [Number(v[0]), Number(v[1])]),
      });
      return;
    }
    if (Array.isArray(item)) {
      item.forEach((row, rowIndex) => {
        if (Array.isArray(row) && row.length >= 2) {
          const metric = row[0] || {};
          const value = Array.isArray(row[1]) ? row[1] : [];
          series.push({
            label: labelsToString(metric, rowIndex),
            values: [[Number(value[0]), Number(value[1])]],
          });
        }
      });
    }
  });
  return series;
}

function labelsToString(metric, index) {
  const labels = Object.entries(metric || {}).map(([k, v]) => `${k}="${v}"`).join(', ');
  return labels || `series_${index + 1}`;
}

function latestRows(data) {
  return latestRowsFromSeries(normalizeSeries(data));
}

function latestRowsFromSeries(seriesList) {
  return (seriesList || []).map(series => {
    const latest = [...series.values].reverse().find(point => Number.isFinite(point[1]));
    return {
      label: series.label,
      value: latest ? latest[1] : null,
      time: latest ? latest[0] : null,
    };
  });
}

function normalizePanelDefinition(panel = {}, index = 0) {
  const queryType = panel.queryType || ((panel.sourceQuery && panel.sourceQuery !== panel.query) ? 'sql' : 'promql');
  const options = { ...(panel.options || {}) };
  if (typeof options.echarts === 'string') options.echarts = safeParseJSON(options.echarts, {});
  return {
    id: panel.id || index + 1,
    title: panel.title || `Panel ${index + 1}`,
    description: panel.description || '',
    type: panel.type || 'timeseries',
    queryType,
    query: panel.query || '',
    sourceQuery: panel.sourceQuery || (queryType === 'promql' ? (panel.query || '') : ''),
    datasource: panel.datasource || null,
    legend: panel.legend || '',
    unit: panel.unit || '',
    thresholds: Array.isArray(panel.thresholds) ? panel.thresholds : [],
    renderer: panel.renderer || (panel.grafana ? 'echarts' : 'auto'),
    options,
    fieldConfig: panel.fieldConfig || {},
    layout: panel.layout || { x: (index % 2) * 6, y: Math.floor(index / 2) * 8, w: 6, h: 8 },
    grafana: panel.grafana || null,
  };
}

function panelSourceQuery(panel) {
  if ((panel.queryType || 'promql') === 'sql') return panel.sourceQuery || '';
  return panel.query || panel.sourceQuery || '';
}

function resolvePanelRenderer(panel) {
  if (panel.renderer && panel.renderer !== 'auto') return panel.renderer;
  if (panel.type === 'pie' || panel.type === 'scatter' || panel.type === 'heatmap') return 'echarts';
  if (panel.grafana && panel.type !== 'table') return 'echarts';
  if ((panel.queryType || 'promql') === 'sql' && panel.type !== 'table') return 'echarts';
  return 'chartjs';
}

function promSeriesRows(data) {
  const raw = data?.data?.result || [];
  const rows = [];
  raw.forEach((item, index) => {
    const metric = item?.metric || {};
    const label = labelsToString(metric, index);
    const values = item?.values || (item?.value ? [item.value] : []);
    values.forEach(point => {
      const row = {
        series: label,
        label,
        time: Number(point[0]),
        value: Number(point[1]),
      };
      Object.entries(metric).forEach(([key, value]) => {
        row[key] = value;
      });
      rows.push(row);
    });
  });
  return rows;
}

function firstNumber(...values) {
  for (const value of values) {
    const num = Number(value);
    if (Number.isFinite(num)) return num;
  }
  return null;
}

function normalizeSQLRows(rows) {
  return (rows || []).map((row, index) => {
    const normalized = { ...(row || {}) };
    normalized.label = String(normalized.label ?? normalized.series ?? normalized.name ?? `row_${index + 1}`);
    normalized.value = firstNumber(normalized.value, normalized.y, normalized.metric, normalized.count);
    normalized.time = firstNumber(normalized.time, normalized.ts, normalized.timestamp);
    return normalized;
  });
}

function rowsToSeries(rows) {
  const grouped = new Map();
  (rows || []).forEach((row, index) => {
    const label = String(row.label ?? row.series ?? row.name ?? `series_${index + 1}`);
    const time = firstNumber(row.time, row.ts, row.timestamp, index + 1);
    const value = firstNumber(row.value, row.y, row.metric, row.count);
    if (!Number.isFinite(time) || !Number.isFinite(value)) return;
    if (!grouped.has(label)) grouped.set(label, { label, values: [] });
    grouped.get(label).values.push([time, value]);
  });
  return [...grouped.values()].map(series => ({
    ...series,
    values: series.values.sort((a, b) => a[0] - b[0]),
  }));
}

function runPanelSQLTransform(sql, rows) {
  const statement = (sql || '').trim();
  if (!window.alasql) throw new Error('SQL transform engine is not available');
  if (!statement) {
    return normalizeSQLRows(window.alasql('SELECT series AS label, MAX(value) AS value, MAX(time) AS time FROM ? GROUP BY series ORDER BY value DESC', [rows]));
  }
  const result = window.alasql(statement, [rows]);
  if (!Array.isArray(result)) throw new Error('SQL transform must return a row array');
  return normalizeSQLRows(result);
}

async function fetchPanelDataset(panel, start, end, step) {
  const sourceQuery = panelSourceQuery(panel);
  if (!sourceQuery) throw new Error((panel.queryType || 'promql') === 'sql' ? 'SQL 面板需要先填写源 PromQL 查询' : '此面板没有查询语句');
  const raw = await queryPromQL(sourceQuery, start, end, step);
  if ((panel.queryType || 'promql') !== 'sql') {
    const series = normalizeSeries(raw);
    return {
      mode: 'promql',
      raw,
      rows: latestRowsFromSeries(series),
      series,
    };
  }
  const sqlRows = runPanelSQLTransform(panel.query, promSeriesRows(raw));
  return {
    mode: 'sql',
    raw,
    rows: sqlRows,
    series: rowsToSeries(sqlRows),
  };
}

function mergeDeep(base, extra) {
  if (!extra || typeof extra !== 'object' || Array.isArray(extra)) return extra === undefined ? base : extra;
  const target = { ...(base || {}) };
  Object.entries(extra).forEach(([key, value]) => {
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      target[key] = mergeDeep(target[key], value);
    } else {
      target[key] = value;
    }
  });
  return target;
}

function buildPanelStatOption(dataset, panel) {
  const row = dataset.rows?.[0] || {};
  return {
    animationDuration: 240,
    tooltip: { show: false },
    xAxis: { show: false },
    yAxis: { show: false },
    series: [],
    graphic: [
      {
        type: 'text',
        left: 'center',
        top: '36%',
        style: {
          text: formatMetricValue(row.value, panel.unit),
          fill: '#f8fafc',
          font: '700 34px "Segoe UI", sans-serif',
        },
      },
      {
        type: 'text',
        left: 'center',
        top: '60%',
        style: {
          text: row.label || panel.legend || panel.title,
          fill: '#94a3b8',
          font: '500 13px "Segoe UI", sans-serif',
        },
      },
    ],
  };
}

function buildPanelGaugeOption(dataset, panel) {
  const row = dataset.rows?.[0] || {};
  const max = Number(panel.options?.max || panel.fieldConfig?.defaults?.max || 100);
  return {
    tooltip: { formatter: '{b}: {c}' },
    series: [{
      type: 'gauge',
      min: 0,
      max: Number.isFinite(max) && max > 0 ? max : 100,
      progress: { show: true, width: 14, itemStyle: { color: '#34d399' } },
      axisLine: { lineStyle: { width: 14, color: [[1, '#1f2937']] } },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { color: '#94a3b8' },
      detail: {
        valueAnimation: true,
        color: '#f8fafc',
        fontSize: 24,
        formatter: value => formatMetricValue(value, panel.unit),
      },
      data: [{ value: Number(row.value || 0), name: row.label || panel.title }],
    }],
  };
}

function buildPanelPieOption(dataset) {
  const rows = (dataset.rows || []).slice(0, 16);
  return {
    tooltip: { trigger: 'item' },
    legend: { bottom: 0, textStyle: { color: '#cbd5e1' } },
    series: [{
      type: 'pie',
      radius: ['38%', '70%'],
      itemStyle: { borderColor: '#081018', borderWidth: 2 },
      label: { color: '#e2e8f0' },
      data: rows.map(row => ({ name: row.label, value: Number(row.value || 0) })),
    }],
  };
}

function buildPanelBarOption(dataset, panel) {
  const rows = (dataset.rows || []).slice(0, 24);
  return {
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: rows.map(row => row.label),
      axisLabel: { color: '#94a3b8', interval: 0, rotate: rows.length > 8 ? 24 : 0 },
      axisLine: { lineStyle: { color: '#334155' } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#94a3b8' },
      splitLine: { lineStyle: { color: '#1f2937' } },
    },
    series: [{
      type: 'bar',
      name: panel.title,
      data: rows.map(row => Number(row.value || 0)),
      itemStyle: { color: '#34d399', borderRadius: [6, 6, 0, 0] },
    }],
  };
}

function buildPanelHeatmapOption(dataset) {
  const seriesList = (dataset.series || []).slice(0, 8);
  const xLabels = [];
  const xIndex = new Map();
  const yLabels = seriesList.map(item => item.label.slice(0, 48));
  const points = [];
  seriesList.forEach((item, y) => {
    item.values.slice(-48).forEach(([time, value]) => {
      const label = new Date(time * 1000).toLocaleTimeString();
      if (!xIndex.has(label)) {
        xIndex.set(label, xLabels.length);
        xLabels.push(label);
      }
      points.push([xIndex.get(label), y, Number(value || 0)]);
    });
  });
  return {
    tooltip: { position: 'top' },
    grid: { left: 84, right: 16, top: 24, bottom: 48 },
    xAxis: {
      type: 'category',
      data: xLabels,
      splitArea: { show: false },
      axisLabel: { color: '#94a3b8' },
    },
    yAxis: {
      type: 'category',
      data: yLabels,
      splitArea: { show: false },
      axisLabel: { color: '#94a3b8' },
    },
    visualMap: {
      min: Math.min(...points.map(point => point[2]), 0),
      max: Math.max(...points.map(point => point[2]), 1),
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      textStyle: { color: '#cbd5e1' },
      inRange: { color: ['#0f172a', '#2563eb', '#22c55e', '#f59e0b'] },
    },
    series: [{
      type: 'heatmap',
      data: points,
      emphasis: { itemStyle: { shadowBlur: 10, shadowColor: 'rgba(15,23,42,0.55)' } },
    }],
  };
}

function buildPanelScatterOption(dataset, panel) {
  const rows = (dataset.rows || []).slice(0, 200);
  return {
    tooltip: { trigger: 'item' },
    xAxis: {
      type: 'value',
      axisLabel: { color: '#94a3b8' },
      splitLine: { lineStyle: { color: '#1f2937' } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#94a3b8' },
      splitLine: { lineStyle: { color: '#1f2937' } },
    },
    series: [{
      type: 'scatter',
      name: panel.title,
      symbolSize: row => Math.max(8, Math.min(26, Number(row[2] || 10))),
      data: rows.map((row, index) => [
        firstNumber(row.x, row.time, index + 1) || 0,
        firstNumber(row.y, row.value) || 0,
        firstNumber(row.size, row.value, 10) || 10,
      ]),
      itemStyle: { color: '#38bdf8' },
    }],
  };
}

function buildPanelLineOption(dataset) {
  return {
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0, textStyle: { color: '#cbd5e1' } },
    grid: { left: 42, right: 16, top: 24, bottom: 52 },
    xAxis: {
      type: 'time',
      axisLabel: { color: '#94a3b8' },
      axisLine: { lineStyle: { color: '#334155' } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#94a3b8' },
      splitLine: { lineStyle: { color: '#1f2937' } },
    },
    series: (dataset.series || []).slice(0, 12).map((series, index) => ({
      type: 'line',
      name: series.label.slice(0, 72),
      showSymbol: false,
      smooth: 0.2,
      lineStyle: { width: index === 0 ? 2.4 : 1.6 },
      areaStyle: index === 0 ? { opacity: 0.08 } : undefined,
      data: series.values.map(([time, value]) => [time * 1000, value]),
    })),
  };
}

function buildEChartsOption(dataset, panel) {
  if (panel.type === 'stat') return buildPanelStatOption(dataset, panel);
  if (panel.type === 'gauge') return buildPanelGaugeOption(dataset, panel);
  if (panel.type === 'pie') return buildPanelPieOption(dataset);
  if (panel.type === 'bar' || panel.type === 'barchart') return buildPanelBarOption(dataset, panel);
  if (panel.type === 'heatmap') return buildPanelHeatmapOption(dataset);
  if (panel.type === 'scatter') return buildPanelScatterOption(dataset, panel);
  return buildPanelLineOption(dataset);
}

function renderEChartsPanel(host, dataset, panel) {
  if (!window.echarts) throw new Error('ECharts is not available');
  const domId = `${host.id}-echarts`;
  host.innerHTML = `<div id="${domId}" style="width:100%;height:100%"></div>`;
  const option = mergeDeep(buildEChartsOption(dataset, panel), panel.options?.echarts || {});
  const instance = window.echarts.init(document.getElementById(domId), null, { renderer: 'canvas' });
  instance.setOption(option, true);
  charts[domId] = instance;
}

function formatTableCell(value, key, unit) {
  if (value === null || value === undefined || value === '') return '-';
  if (/(^|_)(time|ts|timestamp)$/.test(key) && Number.isFinite(Number(value))) {
    const ts = Number(value);
    const ms = ts > 1e12 ? ts : ts * 1000;
    return new Date(ms).toLocaleString();
  }
  if (typeof value === 'number') {
    return key === 'value' || key === 'y' ? formatMetricValue(value, unit) : formatNumber(value);
  }
  if (typeof value === 'object') return escapeHtml(JSON.stringify(value));
  return escapeHtml(String(value));
}

function analyzeGrafanaDashboard(dashboard) {
  const panels = flattenGrafanaPanels(dashboard.panels || []);
  const report = {
    title: dashboard.title || 'Imported Grafana Dashboard',
    totalPanels: panels.length,
    fullySupported: 0,
    partiallySupported: 0,
    unsupported: 0,
    totalWarnings: 0,
    panels: [],
  };
  panels.forEach((panel, index) => {
    const warnings = [];
    const mappedType = grafanaTypeMap[panel.type] || panel.type || 'timeseries';
    if (!grafanaTypeMap[panel.type] && !['timeseries', 'stat', 'gauge', 'table', 'bar', 'pie', 'scatter', 'heatmap'].includes(mappedType)) {
      warnings.push(`面板类型 ${panel.type || 'unknown'} 需要人工确认`);
    }
    if ((panel.targets || []).length > 1) warnings.push('存在多个 targets，当前默认只直接渲染首个 target');
    if ((panel.transformations || []).length > 0) warnings.push('存在 transformations，当前保留原配置但不会逐条复刻 Grafana 行为');
    if (panel.datasource && typeof panel.datasource === 'object') warnings.push('datasource 为复杂对象，建议导入后复核变量与数据源映射');
    if (warnings.length === 0) report.fullySupported += 1;
    else if (mappedType) report.partiallySupported += 1;
    else report.unsupported += 1;
    report.totalWarnings += warnings.length;
    report.panels.push({
      id: panel.id || index + 1,
      title: panel.title || `Panel ${index + 1}`,
      grafanaType: panel.type || 'unknown',
      mappedType,
      warnings,
    });
  });
  return report;
}

function renderGrafanaCompatibilityReport(report) {
  if (!report) return '';
  return `
    <div class="doc-list">
      <div class="doc-item"><strong>兼容性总览</strong><p>面板 ${report.totalPanels} 个，完全兼容 ${report.fullySupported} 个，需人工复核 ${report.partiallySupported + report.unsupported} 个，告警 ${report.totalWarnings} 项。</p></div>
      ${report.panels.slice(0, 10).map(item => `<div class="doc-item"><strong>${escapeHtml(item.title)}</strong><p>${escapeHtml(item.grafanaType)} -> ${escapeHtml(item.mappedType)}</p><p>${item.warnings.length ? escapeHtml(item.warnings.join('；')) : '可直接落地'}</p></div>`).join('')}
    </div>
  `;
}

function formatMetricValue(value, unit = '') {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) return '-';
  const n = Number(value);
  if (unit === 'bytes') {
    if (n >= 1073741824) return `${(n / 1073741824).toFixed(2)} GiB`;
    if (n >= 1048576) return `${(n / 1048576).toFixed(2)} MiB`;
    if (n >= 1024) return `${(n / 1024).toFixed(1)} KiB`;
    return `${n.toFixed(0)} B`;
  }
  if (unit === 'percent' || unit === 'percentunit') return `${(unit === 'percentunit' ? n * 100 : n).toFixed(2)}%`;
  if (Math.abs(n) >= 1000) return formatNumber(n);
  if (Number.isInteger(n)) return String(n);
  return n.toFixed(Math.abs(n) < 1 ? 4 : 2);
}

function renderTimeSeriesChart(canvasId, data, color = '#34d399') {
  const canvas = document.getElementById(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const series = (Array.isArray(data) ? data : normalizeSeries(data)).slice(0, 18);
  const labels = [...new Set(series.flatMap(s => s.values.map(v => new Date(v[0] * 1000).toLocaleTimeString())))];
  const palette = ['#34d399', '#38bdf8', '#fbbf24', '#fb7185', '#a78bfa', '#f472b6', '#60a5fa'];
  charts[canvasId] = new Chart(ctx, {
    type: 'line',
    data: {
      labels,
      datasets: series.map((s, i) => ({
        label: s.label.slice(0, 72),
        data: s.values.map(v => Number.isFinite(v[1]) ? v[1] : 0),
        borderColor: i === 0 ? color : palette[i % palette.length],
        backgroundColor: `${i === 0 ? color : palette[i % palette.length]}22`,
        pointRadius: 0,
        borderWidth: 1.6,
        tension: 0.28,
        fill: true,
      })),
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 420 },
      scales: {
        x: { grid: { color: '#202838' }, ticks: { color: '#718096', maxTicksLimit: 7 } },
        y: { grid: { color: '#202838' }, ticks: { color: '#718096' } },
      },
      plugins: {
        legend: { labels: { color: '#aab6c8', boxWidth: 10, font: { size: 11 } } },
        tooltip: { intersect: false, mode: 'index' },
      },
    },
  });
}

function renderEmptyChart(canvasId, message = '暂无数据') {
  const canvas = document.getElementById(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  ctx.fillStyle = '#718096';
  ctx.font = '14px sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(message, canvas.width / 2, canvas.height / 2);
}

function pageShell(title, subtitle) {
  document.getElementById('page-title').textContent = title;
  document.getElementById('page-subtitle').textContent = subtitle;
}

async function renderOverview() {
  destroyCharts();
  pageShell(...pageMeta.overview);
  const container = document.getElementById('page-content');
  const [statsResp, targetsResp, buildResp] = await Promise.all([
    api('/api/system/stats').catch(() => ({ data: {} })),
    api('/api/v1/targets').catch(() => ({ data: [] })),
    api('/api/v1/status/buildinfo').catch(() => ({ data: {} })),
  ]);
  const stats = statsResp.data || {};
  const targets = targetsResp.data || [];
  const healthyTargets = targets.filter(x => x.healthy).length;

  container.innerHTML = `
    <div class="workspace">
      <section class="hero band">
        <div class="hero-copy">
          <h2>一个控制台完成采集、存储、查询、告警和配置。</h2>
          <p>不用在 Grafana、Prometheus agent 和配置文件之间来回跳。Sentinel233 把常用监控路径收进一个更明确的操作台。</p>
          <div class="hero-actions">
            <button class="btn btn-primary" onclick="navigate('config')">一键配置采集与存储</button>
            <button class="btn btn-secondary" onclick="navigate('dashboards')">从预设创建仪表盘</button>
            <button class="btn btn-secondary" onclick="navigate('docs')">查看内置文档</button>
          </div>
        </div>
        <div class="hero-rail">
          <div class="rail-item"><span>运行版本</span><strong>${escapeHtml(buildResp.data?.version || 'dev')}</strong></div>
          <div class="rail-item"><span>采集健康</span><strong>${healthyTargets}/${targets.length || 0}</strong></div>
          <div class="rail-item"><span>数据保留</span><strong id="overview-retention">读取中</strong></div>
          <div class="rail-item"><span>默认账号</span><strong>root / root</strong></div>
        </div>
      </section>

      <section class="grid-4">
        ${metricCard('指标序列', formatNumber(stats.series), '当前 TSDB 中的 series 数')}
        ${metricCard('采样点', formatNumber(stats.samples), '累计写入样本')}
        ${metricCard('采集目标', formatNumber(stats.targets), `${healthyTargets} 个目标健康`)}
        ${metricCard('活跃告警', formatNumber(stats.activeAlerts), stats.activeAlerts > 0 ? '需要处理' : '当前稳定', stats.activeAlerts > 0 ? 'bad' : '')}
      </section>

      <section class="split">
        <div class="band">
          <div class="section-title"><div><h2>核心趋势</h2><p>默认展示序列规模和进程资源。</p></div><button class="btn btn-sm" onclick="navigate('explore')">继续探索</button></div>
          <div class="grid-2">
            <div class="panel"><div class="section-title" style="padding:14px;margin:0"><h3>序列增长</h3></div><div class="chart-container"><canvas id="chart-series"></canvas></div></div>
            <div class="panel"><div class="section-title" style="padding:14px;margin:0"><h3>进程 CPU</h3></div><div class="chart-container"><canvas id="chart-cpu"></canvas></div></div>
          </div>
        </div>
        <div class="band">
          <div class="section-title"><div><h2>常用入口</h2><p>从明确任务开始，少点猜。</p></div></div>
          <div class="preset-grid">
            ${queryPresets.slice(0, 4).map(p => `<button class="preset" onclick="openPresetQuery('${escapeHtml(p.expr)}')"><strong>${p.name}</strong><span>${p.note}</span></button>`).join('')}
          </div>
        </div>
      </section>
    </div>
  `;

  loadConfigSummary();
  drawOverviewCharts();
}

function metricCard(label, value, note, status = '') {
  return `<div class="metric"><label><span class="status-dot ${status}"></span>${label}</label><strong>${value}</strong><span>${note}</span></div>`;
}

async function loadConfigSummary() {
  const el = document.getElementById('overview-retention');
  if (!el) return;
  try {
    const resp = await api(session?.token ? '/api/admin/config' : '/api/v1/status/config');
    const cfg = resp.data?.config || resp.data || {};
    el.textContent = `${cfg.storage?.retention_days || cfg.Storage?.RetentionDays || 15} 天`;
  } catch {
    el.textContent = '15 天';
  }
}

async function drawOverviewCharts() {
  const range = document.getElementById('time-range').value;
  const now = Math.floor(Date.now() / 1000);
  const start = now - timeRangeToSeconds(range);
  const step = Math.max(Math.floor(timeRangeToSeconds(range) / 160), 15);
  try { renderTimeSeriesChart('chart-series', await queryPromQL('count({__name__=~".+"})', start, now, step)); } catch { renderEmptyChart('chart-series'); }
  try { renderTimeSeriesChart('chart-cpu', await queryPromQL('rate(process_cpu_seconds_total[5m])', start, now, step), '#38bdf8'); } catch { renderEmptyChart('chart-cpu'); }
}

function openPresetQuery(expr) {
  window.location.hash = 'explore';
  setTimeout(() => {
    const input = document.getElementById('explore-query');
    if (input) {
      input.value = expr;
      runExploreQuery();
    }
  }, 80);
}

async function renderExplore() {
  destroyCharts();
  pageShell(...pageMeta.explore);
  document.getElementById('page-content').innerHTML = `
    <div class="workspace">
      <section class="band">
        <div class="query-bar">
          <input type="text" class="query-input" id="explore-query" placeholder="${t('metrics.query_placeholder')}" value="up">
          <button class="btn btn-primary" onclick="runExploreQuery()">执行查询</button>
          <button class="btn btn-secondary" onclick="copyQueryLink()">复制链接</button>
        </div>
        <div class="preset-grid">${queryPresets.map(p => `<button class="preset" onclick="setExploreQuery('${escapeHtml(p.expr)}')"><strong>${p.name}</strong><span>${p.expr}</span></button>`).join('')}</div>
      </section>
      <section class="band">
        <div class="section-title"><div><h2>查询结果</h2><p id="query-note">按当前时间范围展示折线和最新值。</p></div></div>
        <div class="chart-container"><canvas id="explore-chart"></canvas></div>
        <div id="explore-table" style="margin-top:14px"></div>
      </section>
    </div>
  `;
  document.getElementById('explore-query').addEventListener('keydown', e => { if (e.key === 'Enter') runExploreQuery(); });
  runExploreQuery();
}

function setExploreQuery(expr) {
  document.getElementById('explore-query').value = expr;
  runExploreQuery();
}

async function runExploreQuery() {
  const expr = document.getElementById('explore-query')?.value.trim();
  if (!expr) return;
  const range = document.getElementById('time-range').value;
  const now = Math.floor(Date.now() / 1000);
  const start = now - timeRangeToSeconds(range);
  const step = Math.max(Math.floor(timeRangeToSeconds(range) / 220), 15);
  try {
    const data = await queryPromQL(expr, start, now, step, {});
    if (charts['explore-chart']) charts['explore-chart'].destroy();
    renderTimeSeriesChart('explore-chart', data);
    renderExploreTable(data);
    document.getElementById('query-note').textContent = `PromQL: ${expr}`;
  } catch (e) {
    renderEmptyChart('explore-chart', e.message);
    document.getElementById('explore-table').innerHTML = `<span class="badge badge-danger">${escapeHtml(e.message)}</span>`;
  }
}

function renderExploreTable(data) {
  const rows = normalizeSeries(data).map(s => {
    const last = s.values[s.values.length - 1] || [];
    return `<tr><td class="code">${escapeHtml(s.label)}</td><td>${escapeHtml(last[1] ?? '-')}</td><td>${last[0] ? new Date(last[0] * 1000).toLocaleString() : '-'}</td></tr>`;
  }).join('');
  document.getElementById('explore-table').innerHTML = rows
    ? `<div class="table-wrap"><table><thead><tr><th>Series</th><th>Latest</th><th>Time</th></tr></thead><tbody>${rows}</tbody></table></div>`
    : '<div class="empty-state">没有返回数据</div>';
}

function copyQueryLink() {
  const expr = document.getElementById('explore-query')?.value || '';
  navigator.clipboard?.writeText(`${location.origin}${location.pathname}#explore?query=${encodeURIComponent(expr)}`);
  toast('查询链接已复制');
}

function requireWriteSession() {
  if (session?.token) return true;
  toast('请先登录后再保存配置或仪表盘');
  loginDialog();
  return false;
}

async function renderDashboards() {
  destroyCharts();
  pageShell(...pageMeta.dashboards);
  const resp = await api('/api/dashboards').catch(() => ({ data: [] }));
  const dashboards = resp.data || [];
  document.getElementById('page-content').innerHTML = `
    <div class="workspace">
      <section class="band">
        <div class="section-title">
          <div><h2>预设仪表盘</h2><p>先从可用模板开始，也可以直接导入 Grafana JSON。</p></div>
          <div>
            <button class="btn btn-secondary" onclick="importGrafanaDialog()">导入 Grafana JSON</button>
            <button class="btn btn-primary" onclick="createDashboardDialog()">新建空白盘</button>
          </div>
        </div>
        <div class="preset-grid">
          ${dashboardPresets.map((p, i) => `<button class="preset" onclick="createDashboardFromPreset(${i})"><strong>${p.title}</strong><span>${p.description}</span></button>`).join('')}
        </div>
      </section>
      <section class="band">
        <div class="section-title"><div><h2>已有仪表盘</h2><p>${dashboards.length} 个工作区面板。</p></div></div>
        ${dashboards.length ? `<div class="table-wrap"><table><thead><tr><th>名称</th><th>说明</th><th>更新时间</th><th></th></tr></thead><tbody>${dashboards.map(d => `
          <tr><td><a href="#" onclick="openDashboard(${d.id});return false;"><strong>${escapeHtml(d.title)}</strong></a></td><td>${escapeHtml(d.description || '-')}</td><td>${new Date(d.updated_at).toLocaleString()}</td><td><button class="btn btn-danger btn-sm" onclick="deleteDashboard(${d.id})">删除</button></td></tr>
        `).join('')}</tbody></table></div>` : '<div class="empty-state">暂无仪表盘，建议从预设创建。</div>'}
      </section>
    </div>
  `;
}

function createDashboardDialog() {
  showModal('新建仪表盘', `
    <div class="form-group"><label>标题</label><input id="new-dash-title" placeholder="服务运行总览"></div>
    <div class="form-group"><label>说明</label><textarea id="new-dash-desc" placeholder="这个仪表盘的用途"></textarea></div>
    <button class="btn btn-primary" onclick="createDashboard()">保存</button>
  `);
}

async function createDashboard() {
  if (!requireWriteSession()) return;
  const title = document.getElementById('new-dash-title').value || 'Untitled Dashboard';
  const desc = document.getElementById('new-dash-desc').value || '';
  await api('/api/dashboards', { method: 'POST', body: JSON.stringify({ title, description: desc, panels: '[]', layout: '{}', variables: '[]', tags: '[]' }) });
  closeModal();
  toast('仪表盘已创建');
  renderDashboards();
}

async function createDashboardFromPreset(index) {
  if (!requireWriteSession()) return;
  const preset = dashboardPresets[index];
  await createDashboardPreset(preset, ['preset']);
  toast('预设仪表盘已创建');
  renderDashboards();
}

async function createDashboardPreset(preset, tags = ['preset']) {
  if (!preset) return;
  await api('/api/dashboards', {
    method: 'POST',
    body: JSON.stringify({
      title: preset.title,
      description: preset.description,
      panels: JSON.stringify((preset.panels || []).map((panel, index) => normalizePanelDefinition(panel, index))),
      layout: '{}',
      variables: '[]',
      tags: JSON.stringify(tags),
    }),
  });
}

function importGrafanaDialog() {
  showModal('导入 Grafana Dashboard JSON', `
    <div class="form-group">
      <label>Grafana JSON</label>
      <textarea id="grafana-json" class="json-editor code" placeholder='{"title":"Node Exporter","panels":[...]}'></textarea>
    </div>
    <p class="hint">支持 Grafana 的 panels、targets、gridPos、fieldConfig、templating。导入后会转换成更易读的 Sentinel233 面板定义，并优先走 ECharts 贴近 Grafana 观感。</p>
    <div id="grafana-compatibility"></div>
    <div style="display:flex;gap:10px;flex-wrap:wrap">
      <button class="btn btn-secondary" onclick="previewGrafanaCompatibility()">先校验兼容性</button>
      <button class="btn btn-primary" onclick="importGrafanaDashboard()">导入</button>
    </div>
  `);
}

function previewGrafanaCompatibility() {
  try {
    const raw = JSON.parse(document.getElementById('grafana-json').value);
    const report = analyzeGrafanaDashboard(raw.dashboard || raw);
    document.getElementById('grafana-compatibility').innerHTML = renderGrafanaCompatibilityReport(report);
  } catch (e) {
    toast(`校验失败：${e.message}`);
  }
}

async function importGrafanaDashboard() {
  if (!requireWriteSession()) return;
  try {
    const raw = JSON.parse(document.getElementById('grafana-json').value);
    const converted = convertGrafanaDashboard(raw.dashboard || raw);
    await api('/api/dashboards', {
      method: 'POST',
      body: JSON.stringify({
        title: converted.title,
        description: converted.description,
        panels: JSON.stringify(converted.panels),
        layout: JSON.stringify(converted.layout),
        variables: JSON.stringify(converted.variables),
        tags: JSON.stringify(converted.tags),
      }),
    });
    closeModal();
    toast(`已导入 ${converted.panels.length} 个 Grafana 面板，兼容告警 ${converted.layout.compatibility?.totalWarnings || 0} 项`);
    renderDashboards();
  } catch (e) {
    toast(`导入失败：${e.message}`);
  }
}

function convertGrafanaDashboard(dashboard) {
  const compatibility = analyzeGrafanaDashboard(dashboard);
  const compatibilityByID = new Map(compatibility.panels.map(item => [item.id, item]));
  const panels = flattenGrafanaPanels(dashboard.panels || []).map(panel => {
    const target = (panel.targets || []).find(tg => tg.expr || tg.query) || {};
    const type = grafanaTypeMap[panel.type] || panel.type || 'timeseries';
    return normalizePanelDefinition({
      title: panel.title || 'Grafana Panel',
      type,
      query: target.expr || target.query || '',
      sourceQuery: target.expr || target.query || '',
      queryType: 'promql',
      datasource: target.datasource || panel.datasource || null,
      legend: target.legendFormat || target.alias || '',
      unit: panel.fieldConfig?.defaults?.unit || '',
      thresholds: panel.fieldConfig?.defaults?.thresholds?.steps || [],
      renderer: type === 'table' ? 'table' : 'echarts',
      options: panel.options || {},
      fieldConfig: panel.fieldConfig || {},
      layout: panel.gridPos || {},
      grafana: {
        id: panel.id,
        type: panel.type,
        targets: panel.targets || [],
        transformations: panel.transformations || [],
        compatibility: compatibilityByID.get(panel.id || 0) || null,
      },
    });
  });
  return {
    title: dashboard.title || 'Imported Grafana Dashboard',
    description: dashboard.description || `Imported from Grafana uid ${dashboard.uid || '-'}`,
    panels,
    layout: { schemaVersion: dashboard.schemaVersion, uid: dashboard.uid, time: dashboard.time || null, compatibility },
    variables: (dashboard.templating?.list || []).map(v => ({
      name: v.name,
      label: v.label || v.name,
      type: v.type,
      query: v.query,
      current: v.current,
      options: v.options || [],
      includeAll: Boolean(v.includeAll),
      multi: Boolean(v.multi),
    })),
    tags: dashboard.tags || ['grafana-import'],
  };
}

function flattenGrafanaPanels(panels) {
  return panels.flatMap(panel => panel.type === 'row' && Array.isArray(panel.panels) ? flattenGrafanaPanels(panel.panels) : [panel]);
}

async function exportGrafanaDashboard(id) {
  const resp = await api(`/api/dashboards/${id}`);
  const dash = resp.data;
  const exported = convertToGrafanaDashboard(dash);
  const blob = new Blob([JSON.stringify(exported, null, 2)], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `${dash.title.replace(/[^a-z0-9_-]+/gi, '-') || 'sentinel233-dashboard'}.grafana.json`;
  link.click();
  URL.revokeObjectURL(link.href);
}

function convertToGrafanaDashboard(dash) {
  let panels = [];
  let variables = [];
  let layout = {};
  try { panels = JSON.parse(dash.panels || '[]'); } catch { panels = []; }
  try { variables = JSON.parse(dash.variables || '[]'); } catch { variables = []; }
  try { layout = JSON.parse(dash.layout || '{}'); } catch { layout = {}; }
  panels = panels.map((panel, index) => normalizePanelDefinition(panel, index));
  return {
    title: dash.title,
    uid: layout.uid || `sentinel-${dash.id}`,
    schemaVersion: layout.schemaVersion || 39,
    tags: safeParseJSON(dash.tags, []),
    timezone: 'browser',
    time: layout.time || { from: 'now-24h', to: 'now' },
    templating: { list: variables },
    panels: panels.map((panel, index) => ({
      id: panel.grafana?.id || index + 1,
      title: panel.title,
      type: panel.grafana?.type || reverseGrafanaType(panel.type),
      gridPos: panel.layout || { x: (index % 2) * 12, y: Math.floor(index / 2) * 8, w: 12, h: 8 },
      datasource: panel.datasource || null,
      fieldConfig: panel.fieldConfig || { defaults: { unit: panel.unit || '' }, overrides: [] },
      options: panel.options || {},
      targets: panel.grafana?.targets?.length ? panel.grafana.targets : [{ refId: 'A', expr: panelSourceQuery(panel), legendFormat: panel.legend || '' }],
    })),
  };
}

function reverseGrafanaType(type) {
  return ({ timeseries: 'timeseries', stat: 'stat', gauge: 'gauge', table: 'table', bar: 'barchart', pie: 'piechart', heatmap: 'heatmap', scatter: 'xychart' })[type] || 'timeseries';
}

function safeParseJSON(value, fallback) {
  try { return JSON.parse(value || ''); } catch { return fallback; }
}

async function deleteDashboard(id) {
  if (!confirm('确认删除这个仪表盘？')) return;
  await api(`/api/dashboards/${id}`, { method: 'DELETE' });
  renderDashboards();
}

async function openDashboard(id) {
  destroyCharts();
  const resp = await api(`/api/dashboards/${id}`);
  const dash = resp.data;
  let panels = [];
  let variables = [];
  try { panels = JSON.parse(dash.panels || '[]'); } catch { panels = []; }
  try { variables = JSON.parse(dash.variables || '[]'); } catch { variables = []; }
  panels = panels.map((panel, index) => normalizePanelDefinition(panel, index));
  variables = await hydrateDashboardVariables(variables);
  activeDashboardPanels = panels;
  activeDashboardId = id;
  dashboardVariableState = buildDashboardVariableState(variables);
  pageShell(dash.title, dash.description || '仪表盘详情');
  document.getElementById('page-content').innerHTML = `
    <div class="workspace">
      <section class="band">
        <div class="section-title">
          <div><h2>${escapeHtml(dash.title)}</h2><p>${escapeHtml(dash.description || '自定义面板')}</p></div>
          <div>
            <button class="btn btn-secondary btn-sm" onclick="navigate('dashboards')">返回</button>
            <button class="btn btn-secondary btn-sm" onclick="exportGrafanaDashboard(${id})">导出 Grafana JSON</button>
            <button class="btn btn-primary btn-sm" onclick="addPanelDialog(${id})">添加面板</button>
          </div>
        </div>
        ${renderVariableControls(variables)}
        <div class="dashboard-layout" id="dashboard-panels">${panels.map((p, i) => panelFrame(p, `dash-${id}-${i}`)).join('') || '<div class="empty-state">还没有面板。</div>'}</div>
      </section>
    </div>
  `;
  panels.forEach((p, i) => drawPanel(p, `dash-${id}-${i}`));
}

function buildDashboardVariableState(variables) {
  const state = {};
  variables.forEach(variable => {
    const current = variable.current || {};
    let value = current.value ?? current.text;
    if (value === undefined && variable.options?.length) {
      const selected = variable.options.filter(option => option.selected);
      value = (selected.length ? selected : [variable.options[0]]).map(option => option.value ?? option.text);
    }
    if (Array.isArray(value) && value.length === 1) value = value[0];
    if (value === '$__all') value = '.*';
    if (value === undefined || value === null || value === '') value = variable.includeAll ? '.*' : '';
    state[variable.name] = value;
  });
  return state;
}

async function hydrateDashboardVariables(variables) {
  const hydrated = [];
  for (const variable of variables) {
    if (variable.type === 'query' && (!variable.options || variable.options.length === 0)) {
      const labelName = parseGrafanaLabelValuesQuery(variable.query);
      if (labelName) {
        try {
          const resp = await api(`/api/v1/label/${encodeURIComponent(labelName)}/values`);
          const values = resp.data || [];
          hydrated.push({ ...variable, options: values.map(value => ({ text: value, value })) });
          continue;
        } catch {
          hydrated.push(variable);
          continue;
        }
      }
    }
    hydrated.push(variable);
  }
  return hydrated;
}

function parseGrafanaLabelValuesQuery(query) {
  const text = String(query || '').trim();
  const twoArg = text.match(/^label_values\((.+),\s*([A-Za-z_:][\w:]*)\)$/);
  if (twoArg) return twoArg[2];
  const oneArg = text.match(/^label_values\(([A-Za-z_:][\w:]*)\)$/);
  if (oneArg) return oneArg[1];
  return '';
}

function renderVariableControls(variables) {
  if (!variables.length) return '';
  return `
    <div class="variable-bar">
      ${variables.map(variable => {
        const options = variableOptions(variable);
        const current = dashboardVariableState[variable.name];
        if (options.length) {
          return `<label><span>${escapeHtml(variable.label || variable.name)}</span><select data-var="${escapeHtml(variable.name)}" onchange="setDashboardVariable(this.dataset.var,this.value)">${options.map(option => `<option value="${escapeHtml(option.value)}" ${String(option.value) === String(current) ? 'selected' : ''}>${escapeHtml(option.text)}</option>`).join('')}</select></label>`;
        }
        return `<label><span>${escapeHtml(variable.label || variable.name)}</span><input data-var="${escapeHtml(variable.name)}" value="${escapeHtml(current)}" onchange="setDashboardVariable(this.dataset.var,this.value)"></label>`;
      }).join('')}
    </div>
  `;
}

function variableOptions(variable) {
  const options = (variable.options || []).filter(option => option.value !== undefined || option.text !== undefined).map(option => ({
    text: option.text ?? option.value,
    value: option.value ?? option.text,
  }));
  if (variable.includeAll && !options.some(option => option.value === '$__all')) {
    return [{ text: 'All', value: '.*' }, ...options];
  }
  if (variable.type === 'custom' && typeof variable.query === 'string' && !options.length) {
    return variable.query.split(',').map(item => item.trim()).filter(Boolean).map(item => ({ text: item, value: item }));
  }
  return options;
}

function setDashboardVariable(name, value) {
  dashboardVariableState[name] = value;
  activeDashboardPanels.forEach((panel, index) => {
    drawPanel(panel, `dash-${activeDashboardId}-${index}`);
  });
}

function panelFrame(panel, canvasId) {
  const width = Math.min(12, Math.max(3, Number(panel.layout?.w || 6)));
  const queryText = (panel.queryType || 'promql') === 'sql'
    ? `PromQL: ${panelSourceQuery(panel) || '-'} | SQL: ${panel.query || '-'}`
    : (panel.query || panel.description || '');
  const badge = [panel.type || 'timeseries', panel.queryType || 'promql', resolvePanelRenderer(panel)].join(' / ');
  return `<div class="panel dashboard-panel" style="grid-column:span ${width}"><div class="section-title" style="padding:14px;margin:0"><div><h3>${escapeHtml(panel.title || 'Panel')}</h3><p>${escapeHtml(queryText)}</p></div><span class="badge">${escapeHtml(badge)}</span></div><div class="panel-viz" id="${canvasId}"></div></div>`;
}

async function drawPanel(panel, canvasId) {
  if (!panelSourceQuery(panel)) {
    const emptyHost = document.getElementById(canvasId);
    if (emptyHost) emptyHost.innerHTML = '<div class="empty-state">此面板还没有可执行的数据查询</div>';
    return;
  }
  const range = document.getElementById('time-range').value;
  const now = Math.floor(Date.now() / 1000);
  const start = now - timeRangeToSeconds(range);
  const step = Math.max(Math.floor((now - start) / 240), 15);
  const host = document.getElementById(canvasId);
  if (!host) return;
  Object.keys(charts).filter(key => key.startsWith(canvasId)).forEach(key => {
    if (charts[key]?.destroy) charts[key].destroy();
    if (charts[key]?.dispose) charts[key].dispose();
    delete charts[key];
  });
  try {
    const dataset = await fetchPanelDataset(panel, start, now, step);
    const renderer = resolvePanelRenderer(panel);
    if (renderer === 'echarts' && panel.type !== 'table') return renderEChartsPanel(host, dataset, panel);
    if (panel.type === 'stat') return renderStatPanel(host, dataset, panel);
    if (panel.type === 'gauge') return renderGaugePanel(host, dataset, panel);
    if (panel.type === 'table') return renderTablePanel(host, dataset, panel);
    if (panel.type === 'bar' || panel.type === 'barchart') return renderBarPanel(host, dataset, panel);
    host.innerHTML = `<canvas id="${canvasId}-canvas"></canvas>`;
    return renderTimeSeriesChart(`${canvasId}-canvas`, dataset.series);
  } catch (e) {
    host.innerHTML = `<div class="empty-state">${escapeHtml(e.message)}</div>`;
  }
}

function renderStatPanel(host, dataset, panel) {
  const rows = dataset.rows || [];
  const primary = rows[0];
  host.innerHTML = `
    <div class="stat-display">
      <strong>${formatMetricValue(primary?.value, panel.unit)}</strong>
      <span>${escapeHtml(primary?.label || panel.legend || panel.query)}</span>
    </div>
  `;
}

function renderGaugePanel(host, dataset, panel) {
  const rows = dataset.rows || [];
  const value = Number(rows[0]?.value || 0);
  const max = Number(panel.options?.max || panel.fieldConfig?.defaults?.max || 100);
  const pct = Math.max(0, Math.min(100, max ? (value / max) * 100 : value));
  host.innerHTML = `
    <div class="gauge-display" style="--pct:${pct}">
      <div class="gauge-ring"><strong>${formatMetricValue(value, panel.unit)}</strong><span>${Math.round(pct)}%</span></div>
      <p>${escapeHtml(rows[0]?.label || panel.query)}</p>
    </div>
  `;
}

function renderTablePanel(host, dataset, panel) {
  const rows = dataset.rows || [];
  if (!rows.length) {
    host.innerHTML = '<div class="empty-state">暂无数据</div>';
    return;
  }
  const columns = [...new Set(rows.flatMap(row => Object.keys(row)).filter(key => !['__raw'].includes(key)))].slice(0, 8);
  host.innerHTML = `
    <div class="table-wrap panel-table"><table><thead><tr>${columns.map(key => `<th>${escapeHtml(key)}</th>`).join('')}</tr></thead><tbody>
      ${rows.slice(0, 120).map(row => `<tr>${columns.map(key => `<td${key === 'label' || key === 'series' ? ' class="code"' : ''}>${formatTableCell(row[key], key, panel.unit)}</td>`).join('')}</tr>`).join('')}
    </tbody></table></div>
  `;
}

function renderBarPanel(host, dataset, panel) {
  const rows = (dataset.rows || []).slice(0, 24);
  host.innerHTML = `<canvas id="${host.id}-bar"></canvas>`;
  charts[`${host.id}-bar`] = new Chart(document.getElementById(`${host.id}-bar`).getContext('2d'), {
    type: 'bar',
    data: {
      labels: rows.map(row => String(row.label || row.series || '-').slice(0, 32)),
      datasets: [{ label: panel.title, data: rows.map(row => row.value || 0), backgroundColor: '#34d39999', borderColor: '#34d399', borderWidth: 1 }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      scales: {
        x: { grid: { color: '#202838' }, ticks: { color: '#718096' } },
        y: { grid: { color: '#202838' }, ticks: { color: '#718096' } },
      },
      plugins: { legend: { labels: { color: '#aab6c8' } } },
    },
  });
}

function addPanelDialog(dashId) {
  showModal('添加面板', `
    <div class="form-grid">
      <div class="form-group"><label>标题</label><input id="panel-title" placeholder="CPU 使用"></div>
      <div class="form-group"><label>类型</label><select id="panel-type"><option value="timeseries">时间序列</option><option value="stat">统计值</option><option value="table">表格</option><option value="gauge">仪表盘</option><option value="bar">柱状图</option><option value="pie">饼图</option><option value="scatter">散点图</option><option value="heatmap">热力图</option></select></div>
      <div class="form-group"><label>查询模式</label><select id="panel-query-type"><option value="promql">PromQL 直出</option><option value="sql">PromQL + SQL 变换</option></select></div>
      <div class="form-group"><label>渲染器</label><select id="panel-renderer"><option value="auto">自动选择</option><option value="chartjs">Chart.js</option><option value="echarts">ECharts</option><option value="table">表格优先</option></select></div>
    </div>
    <div class="form-group"><label>源 PromQL</label><input id="panel-source-query" class="query-input" placeholder="rate(process_cpu_seconds_total[5m])"></div>
    <div class="form-group"><label>SQL 变换（可选）</label><textarea id="panel-sql" class="code" placeholder="SELECT series AS label, MAX(value) AS value, MAX(time) AS time FROM ? GROUP BY series ORDER BY value DESC"></textarea></div>
    <div class="form-grid">
      <div class="form-group"><label>图例名（可选）</label><input id="panel-legend" placeholder="CPU"></div>
      <div class="form-group"><label>单位（可选）</label><input id="panel-unit" placeholder="percent / bytes / ms"></div>
    </div>
    <div class="form-group"><label>ECharts 额外配置 JSON（可选）</label><textarea id="panel-echarts-options" class="code" placeholder='{"tooltip":{"backgroundColor":"#111827"}}'></textarea></div>
    <p class="hint"><code>PromQL 直出</code> 适合常规监控面板；<code>PromQL + SQL 变换</code> 会先取回时序点，再用 SQL（<code>FROM ?</code>）做聚合、透视或排序，最后交给 ECharts / Table 渲染。</p>
    <button class="btn btn-primary" onclick="addPanel(${dashId})">添加</button>
  `);
}

async function addPanel(dashId) {
  const resp = await api(`/api/dashboards/${dashId}`);
  const dash = resp.data;
  const panels = JSON.parse(dash.panels || '[]');
  const queryType = document.getElementById('panel-query-type').value;
  const sourceQuery = document.getElementById('panel-source-query').value || 'up';
  const echartsText = document.getElementById('panel-echarts-options').value.trim();
  let echartsOptions = {};
  if (echartsText) {
    try {
      echartsOptions = JSON.parse(echartsText);
    } catch (e) {
      toast(`ECharts 配置 JSON 无法解析：${e.message}`);
      return;
    }
  }
  panels.push(normalizePanelDefinition({
    title: document.getElementById('panel-title').value || 'Panel',
    type: document.getElementById('panel-type').value,
    queryType,
    query: queryType === 'sql'
      ? (document.getElementById('panel-sql').value || 'SELECT series AS label, MAX(value) AS value, MAX(time) AS time FROM ? GROUP BY series ORDER BY value DESC')
      : sourceQuery,
    sourceQuery,
    legend: document.getElementById('panel-legend').value || '',
    unit: document.getElementById('panel-unit').value || '',
    renderer: document.getElementById('panel-renderer').value,
    options: { echarts: echartsOptions },
  }, panels.length));
  dash.panels = JSON.stringify(panels);
  await api(`/api/dashboards/${dashId}`, { method: 'PUT', body: JSON.stringify(dash) });
  closeModal();
  openDashboard(dashId);
}

async function renderAlerts() {
  destroyCharts();
  pageShell(...pageMeta.alerts);
  const [activeResp, historyResp, rulesResp] = await Promise.all([
    api('/api/alerts').catch(() => ({ data: [] })),
    api('/api/alerts/history').catch(() => ({ data: [] })),
    api('/api/alert-rules').catch(() => ({ data: [] })),
  ]);
  const active = activeResp.data || [];
  const history = historyResp.data || [];
  const rules = rulesResp.data || [];
  document.getElementById('page-content').innerHTML = `
    <div class="workspace split">
      <section class="band">
        <div class="section-title"><div><h2>活跃告警</h2><p>${active.length} 条当前状态。</p></div></div>
        ${active.length ? active.map(a => `<div class="doc-item"><strong>${escapeHtml(a.labels?.alertname || 'Alert')}</strong><p><span class="badge badge-danger">${escapeHtml(a.state)}</span> value=${escapeHtml(a.value ?? '-')}</p></div>`).join('') : '<div class="empty-state">暂无活跃告警。</div>'}
      </section>
      <section class="band">
        <div class="section-title"><div><h2>规则与历史</h2><p>${rules.length} 条规则，${history.length} 条历史。</p></div></div>
        <div class="doc-list">
          ${rules.slice(0, 8).map(r => `<div class="doc-item"><strong>${escapeHtml(r.name)}</strong><p class="code">${escapeHtml(r.expr)} · ${escapeHtml(r.severity)}</p></div>`).join('') || '<div class="empty-state">暂无规则。</div>'}
        </div>
      </section>
    </div>
  `;
}

async function renderTargets() {
  destroyCharts();
  pageShell(...pageMeta.targets);
  const resp = await api('/api/targets').catch(() => ({ data: [] }));
  const targets = resp.data || [];
  document.getElementById('page-content').innerHTML = `
    <div class="workspace">
      <section class="band">
        <div class="section-title"><div><h2>采集目标</h2><p>Sentinel233 直接管理目标，保存后立即加入采集。</p></div><div><button class="btn btn-secondary" onclick="navigate('integrations')">接入中心</button> <button class="btn btn-primary" onclick="addTargetDialog()">添加目标</button></div></div>
        ${targets.length ? `<div class="table-wrap"><table><thead><tr><th>名称</th><th>地址</th><th>标签</th><th>状态</th><th></th></tr></thead><tbody>${targets.map(targetRow).join('')}</tbody></table></div>` : '<div class="empty-state">暂无采集目标。</div>'}
      </section>
    </div>
  `;
}

function targetRow(tg) {
  const labels = Object.entries(tg.labels || {}).map(([k, v]) => `<span class="badge badge-info">${escapeHtml(k)}=${escapeHtml(v)}</span>`).join(' ');
  return `<tr><td><strong>${escapeHtml(tg.name)}</strong></td><td class="code">${escapeHtml(tg.endpoint)}</td><td>${labels || '-'}</td><td><span class="badge ${tg.healthy ? 'badge-success' : 'badge-danger'}">${tg.healthy ? '健康' : '异常'}</span></td><td><button class="btn btn-danger btn-sm" onclick="removeTarget(${tg.id})">删除</button></td></tr>`;
}

function addTargetDialog(presetId = '') {
  const preset = integrationPresets.find(item => item.id === presetId) || {};
  if (preset.mode === 'native') return nativeClientDialog(presetId);
  showModal('添加采集目标', `
    <div class="form-grid">
      <div class="form-group"><label>名称</label><input id="target-name" placeholder="api-server" value="${escapeHtml(preset.labels?.job || '')}"></div>
      <div class="form-group"><label>Endpoint</label><input id="target-endpoint" placeholder="http://localhost:9100/metrics" value="${escapeHtml(preset.endpoint || '')}"></div>
    </div>
    <div class="form-group"><label>标签 JSON</label><textarea id="target-labels" class="code">${escapeHtml(JSON.stringify(preset.labels || { job: 'node', env: 'prod' }, null, 2))}</textarea></div>
    <button class="btn btn-primary" onclick="addTarget()">保存并开始采集</button>
  `);
}

async function addTarget() {
  let labels = {};
  try { labels = JSON.parse(document.getElementById('target-labels').value || '{}'); } catch { return toast('标签 JSON 格式错误'); }
  await api('/api/targets', { method: 'POST', body: JSON.stringify({ name: document.getElementById('target-name').value, endpoint: document.getElementById('target-endpoint').value, labels }) });
  closeModal();
  renderTargets();
}

async function removeTarget(id) {
  if (!confirm('确认删除这个采集目标？')) return;
  await api(`/api/targets/${id}`, { method: 'DELETE' });
  renderTargets();
}

function renderIntegrations() {
  destroyCharts();
  pageShell(...pageMeta.integrations);
  document.getElementById('page-content').innerHTML = `
    <div class="workspace">
      <section class="band">
        <div class="section-title"><div><h2>接入目录</h2><p>Prometheus endpoint 是一种接入方式；Sentinel233 也预留原生高性能 client lib。</p></div></div>
        <div class="integration-grid">
          ${integrationPresets.map(item => `
            <article class="integration">
              <div class="integration-head">
                <div><strong>${escapeHtml(item.title)}</strong><span>${escapeHtml(item.kind)}</span></div>
                <div class="integration-actions">
                  ${item.mode === 'native'
                    ? `<button class="btn btn-secondary btn-sm" onclick="nativeClientDialog('${item.id}')">查看协议</button>`
                    : `<button class="btn btn-primary btn-sm" onclick="addTargetDialog('${item.id}')">添加目标</button>`}
                  <button class="btn btn-secondary btn-sm" onclick="createIntegrationDashboards('${item.id}')">生成仪表盘</button>
                </div>
              </div>
              <p>${escapeHtml(item.summary)}</p>
              <div class="integration-meta"><span class="badge badge-info">${escapeHtml(item.endpoint)}</span>${Object.entries(item.labels).map(([k, v]) => `<span class="badge">${escapeHtml(k)}=${escapeHtml(v)}</span>`).join('')}</div>
              <pre class="snippet"><code>${escapeHtml(item.snippet)}</code></pre>
              <div class="integration-dashboards">${item.dashboards.map(name => `<span>${escapeHtml(name)}</span>`).join('')}</div>
            </article>
          `).join('')}
        </div>
      </section>
    </div>
  `;
}

async function createIntegrationDashboards(presetId) {
  if (!requireWriteSession()) return;
  const item = integrationPresets.find(entry => entry.id === presetId);
  if (!item) return;
  const presets = item.dashboards
    .map(name => dashboardPresets.find(preset => preset.title === name))
    .filter(Boolean);
  if (!presets.length) return toast('这个接入项还没有仪表盘预设');
  for (const preset of presets) {
    await createDashboardPreset(preset, ['preset', 'integration', presetId]);
  }
  toast(`已生成 ${presets.length} 个仪表盘`);
  navigate('dashboards');
}

function nativeClientDialog(presetId = 'sentinel-lib-go') {
  const item = integrationPresets.find(entry => entry.id === presetId) || integrationPresets.find(entry => entry.id === 'sentinel-lib-go');
  showModal('Sentinel 原生写入协议', `
    <div class="doc-list">
      <div class="doc-item"><strong>接入地址</strong><p class="code">${escapeHtml(item.endpoint)}</p></div>
      <div class="doc-item"><strong>设计目标</strong><p>原生 client 直接写入结构化样本，不需要先暴露 /metrics 再由 server 拉取，适合高频 runtime、trace-adjacent 性能点和更低延迟采样。</p></div>
      <div class="doc-item"><strong>请求示例</strong><pre class="snippet"><code>${escapeHtml(item.snippet)}</code></pre></div>
      <div class="doc-item"><strong>Git Submodule</strong><p class="code">git submodule add https://github.com/neko233-com/sentinel233-lib-go.git libs/sentinel233-lib-go</p></div>
    </div>
    <button class="btn btn-primary" onclick="createIntegrationDashboards('${item.id}')">生成 Go Runtime 仪表盘</button>
  `);
}

async function renderConfig() {
  destroyCharts();
  pageShell(...pageMeta.config);
  document.getElementById('page-content').innerHTML = '<div class="empty-state">正在读取配置...</div>';
  try {
    const resp = await api(session?.token ? '/api/admin/config' : '/api/v1/status/config');
    activeConfig = resp.data?.config || resp.data || {};
  } catch (e) {
    document.getElementById('page-content').innerHTML = `<div class="band"><div class="section-title"><h2>需要管理员登录</h2></div><p>${escapeHtml(e.message)}</p><button class="btn btn-primary" onclick="loginDialog()">登录</button></div>`;
    return;
  }
  renderConfigEditor();
}

function renderConfigEditor() {
  const cfg = normalizeConfig(activeConfig);
  document.getElementById('page-content').innerHTML = `
    <div class="workspace split">
      <section class="band">
        <div class="section-title"><div><h2>后台配置</h2><p>表单和 JSON 共用同一份配置。</p></div><button class="btn btn-primary" onclick="saveConfigFromForm()">应用配置</button></div>
        <div class="form-grid">
          <div class="form-group"><label>数据目录</label><input id="cfg-data-dir" value="${escapeHtml(cfg.storage.data_dir)}"></div>
          <div class="form-group"><label>保留天数</label><input id="cfg-retention" type="number" value="${cfg.storage.retention_days}"></div>
          <div class="form-group"><label>Flush 间隔(秒)</label><input id="cfg-flush" type="number" value="${cfg.storage.flush_interval_seconds}"></div>
          <div class="form-group"><label>Compaction 间隔(秒)</label><input id="cfg-compact" type="number" value="${cfg.storage.compaction_every_seconds}"></div>
          <div class="form-group"><label>采集间隔(秒)</label><input id="cfg-scrape-interval" type="number" value="${cfg.scrape.interval_seconds}"></div>
          <div class="form-group"><label>采集超时(秒)</label><input id="cfg-scrape-timeout" type="number" value="${cfg.scrape.timeout_seconds}"></div>
          <div class="form-group"><label>Agent 监听</label><input id="cfg-agent-listen" value="${escapeHtml(cfg.agent.listen_addr)}"></div>
          <div class="form-group"><label>告警开关</label><select id="cfg-alert-enabled"><option value="true" ${cfg.alert.enabled ? 'selected' : ''}>开启</option><option value="false" ${!cfg.alert.enabled ? 'selected' : ''}>关闭</option></select></div>
        </div>
        <div class="form-group"><label>采集目标 JSON</label><textarea id="cfg-targets" class="code">${escapeHtml(JSON.stringify(cfg.scrape.targets || [], null, 2))}</textarea></div>
      </section>
      <section class="band">
        <div class="section-title"><div><h2>一键方案</h2><p>根据部署规模快速写入推荐值。</p></div></div>
        <div class="preset-grid">
          ${Object.entries(configPresets).map(([key, value]) => `<button class="preset" onclick="applyConfigPreset('${key}')"><strong>${value.label}</strong><span>${JSON.stringify(value.patch.storage)}</span></button>`).join('')}
        </div>
        <div class="section-title" style="margin-top:18px"><div><h2>完整 JSON</h2><p>适合复制、审阅和批量修改。</p></div><button class="btn btn-sm" onclick="saveConfigFromJson()">应用 JSON</button></div>
        <textarea id="cfg-json" class="json-editor code">${escapeHtml(JSON.stringify(cfg, null, 2))}</textarea>
      </section>
    </div>
  `;
}

function normalizeConfig(cfg) {
  if (cfg.storage) return cfg;
  return {
    server: { addr: cfg.Server?.Addr || '0.0.0.0', port: cfg.Server?.Port || 23390 },
    storage: {
      data_dir: cfg.Storage?.DataDir || './data',
      retention_days: cfg.Storage?.RetentionDays || 15,
      flush_interval_seconds: cfg.Storage?.FlushInterval || 10,
      max_open_files: cfg.Storage?.MaxOpenFiles || 1024,
      compaction_every_seconds: cfg.Storage?.CompactionEvery || 3600,
    },
    scrape: {
      interval_seconds: cfg.Scrape?.Interval || 15,
      timeout_seconds: cfg.Scrape?.Timeout || 10,
      targets: cfg.Scrape?.Targets || [],
    },
    alert: { enabled: cfg.Alert?.Enabled ?? true, rules: cfg.Alert?.Rules || [] },
    agent: { listen_addr: cfg.Agent?.ListenAddr || '0.0.0.0:23391', labels: cfg.Agent?.Labels || {} },
    i18n: { default: cfg.I18n?.Default || 'zh-CN', supported: cfg.I18n?.Supported || ['zh-CN', 'en-US', 'ja-JP'] },
  };
}

function collectConfigFromForm() {
  const cfg = JSON.parse(document.getElementById('cfg-json').value);
  cfg.storage.data_dir = document.getElementById('cfg-data-dir').value;
  cfg.storage.retention_days = Number(document.getElementById('cfg-retention').value);
  cfg.storage.flush_interval_seconds = Number(document.getElementById('cfg-flush').value);
  cfg.storage.compaction_every_seconds = Number(document.getElementById('cfg-compact').value);
  cfg.scrape.interval_seconds = Number(document.getElementById('cfg-scrape-interval').value);
  cfg.scrape.timeout_seconds = Number(document.getElementById('cfg-scrape-timeout').value);
  cfg.scrape.targets = JSON.parse(document.getElementById('cfg-targets').value || '[]');
  cfg.agent.listen_addr = document.getElementById('cfg-agent-listen').value;
  cfg.alert.enabled = document.getElementById('cfg-alert-enabled').value === 'true';
  return cfg;
}

function applyConfigPreset(key) {
  const cfg = collectConfigFromForm();
  const patch = configPresets[key].patch;
  cfg.storage = { ...cfg.storage, ...patch.storage };
  cfg.scrape = { ...cfg.scrape, ...patch.scrape };
  activeConfig = cfg;
  renderConfigEditor();
  toast(`${configPresets[key].label} 已填入`);
}

async function saveConfigFromForm() {
  try { await saveConfig(collectConfigFromForm()); } catch (e) { toast(e.message); }
}

async function saveConfigFromJson() {
  try { await saveConfig(JSON.parse(document.getElementById('cfg-json').value)); } catch (e) { toast(e.message); }
}

async function saveConfig(cfg) {
  if (!session?.token) return loginDialog();
  const resp = await api('/api/admin/config', { method: 'PUT', body: JSON.stringify(cfg) });
  activeConfig = resp.data.config;
  toast(resp.data.restartNeeded ? '配置已应用；存储引擎参数重启后完全生效' : '配置已应用');
  renderConfigEditor();
}

function renderDocs() {
  destroyCharts();
  pageShell(...pageMeta.docs);
  document.getElementById('page-content').innerHTML = `
    <div class="workspace split">
      <section class="band">
        <div class="section-title"><div><h2>快速上手</h2><p>把 Grafana 里常见的空白决策变成可选项。</p></div></div>
        <div class="doc-list">
          <div class="doc-item"><strong>1. 添加采集目标</strong><p>进入“采集目标”，填入任意 Prometheus /metrics 地址。内置 agent 默认监听 23391。</p></div>
          <div class="doc-item"><strong>2. 套用配置方案</strong><p>进入“配置中心”，选择开发机、生产默认或高频采集方案，再应用配置。</p></div>
          <div class="doc-item"><strong>3. 从接入中心开始</strong><p>内置 Go/JVM、Linux node_exporter、MySQL、PostgreSQL、Redis、Nginx、Docker/cAdvisor、Kubernetes、Blackbox 和 Sentinel233 Go client lib 的接入模板。</p></div>
          <div class="doc-item"><strong>4. 创建预设仪表盘</strong><p>“仪表盘”页提供主机、Web SLO、存储巡检三类模板，比从空白面板开始更快。</p></div>
          <div class="doc-item"><strong>5. 导入 Grafana JSON</strong><p>支持 panels、targets、gridPos、fieldConfig 和 templating，常见 timeseries/stat/gauge/table/bar 面板会自动映射。</p></div>
          <div class="doc-item"><strong>6. 使用 Grafana 变量</strong><p>支持 $job、\${instance}、[[env]]、\${job:regex}，并内置 $__interval、$__rate_interval、$__range、$__from、$__to。</p></div>
          <div class="doc-item"><strong>7. 接入 Remote Write</strong><p>现有 Prometheus agent、Grafana Agent 或 Alloy 可以直接写入 /api/v1/write；支持 snappy protobuf WriteRequest，并保留原始 labels。</p></div>
          <div class="doc-item"><strong>8. 用 PromQL 深挖</strong><p>“指标探索”内置常用表达式，适合直接复制到面板或告警规则。</p></div>
        </div>
      </section>
      <section class="band">
        <div class="section-title"><div><h2>PromQL 参考</h2><p>常用表达式。</p></div></div>
        <div class="doc-list">
          ${queryPresets.map(p => `<div class="doc-item"><strong>${p.name}</strong><p class="code">${escapeHtml(p.expr)}</p><p>${p.note}</p></div>`).join('')}
        </div>
      </section>
    </div>
  `;
}

function loginDialog() {
  showModal('登录 Sentinel233', `
    <div class="form-grid">
      <div class="form-group"><label for="login-tenant">租户</label><input id="login-tenant" value="default"></div>
      <div class="form-group"><label for="login-user">用户名</label><input id="login-user" value="root"></div>
    </div>
    <div class="form-group"><label for="login-pass">密码</label><input id="login-pass" type="password" value="root"></div>
    <button class="btn btn-primary" onclick="login()">登录</button>
  `);
}

async function login() {
  const resp = await api('/api/login', {
    method: 'POST',
    body: JSON.stringify({
      tenant: document.getElementById('login-tenant').value,
      username: document.getElementById('login-user').value,
      password: document.getElementById('login-pass').value,
    }),
  });
  session = resp;
  localStorage.setItem('sentinel233_session', JSON.stringify(session));
  closeModal();
  updateSessionUI();
  toast('已登录');
  navigate(window.location.hash.slice(1) || 'overview');
}

function logout() {
  session = null;
  localStorage.removeItem('sentinel233_session');
  updateSessionUI();
  toast('已退出');
}

function updateSessionUI() {
  const chip = document.getElementById('session-chip');
  const btn = document.getElementById('btn-login');
  if (session?.token) {
    chip.textContent = `${session.username} · ${session.role}`;
    btn.textContent = '退出';
    btn.onclick = logout;
  } else {
    chip.textContent = '未登录';
    btn.textContent = '登录';
    btn.onclick = loginDialog;
  }
}

const pages = {
  overview: renderOverview,
  explore: renderExplore,
  dashboards: renderDashboards,
  alerts: renderAlerts,
  targets: renderTargets,
  integrations: renderIntegrations,
  config: renderConfig,
  docs: renderDocs,
};

function navigate(page) {
  const cleanPage = (page || 'overview').split('?')[0];
  destroyCharts();
  document.querySelectorAll('.nav-item').forEach(el => el.classList.toggle('active', el.dataset.page === cleanPage));
  if (pages[cleanPage]) pages[cleanPage]();
  if (window.location.hash.slice(1) !== cleanPage) window.location.hash = cleanPage;
}

document.addEventListener('DOMContentLoaded', async () => {
  await loadI18n(currentLang);
  document.getElementById('lang-select').value = currentLang;
  document.getElementById('lang-select').addEventListener('change', e => {
    currentLang = e.target.value;
    localStorage.setItem('sentinel233_lang', currentLang);
    loadI18n(currentLang);
  });
  document.querySelectorAll('.nav-item').forEach(el => el.addEventListener('click', e => {
    e.preventDefault();
    navigate(el.dataset.page);
  }));
  document.getElementById('btn-refresh').addEventListener('click', () => navigate(window.location.hash.slice(1) || 'overview'));
  document.getElementById('time-range').addEventListener('change', () => navigate(window.location.hash.slice(1) || 'overview'));
  window.addEventListener('hashchange', () => navigate(window.location.hash.slice(1) || 'overview'));
  updateSessionUI();
  navigate(window.location.hash.slice(1) || 'overview');
});

window.closeModal = closeModal;
window.navigate = navigate;
window.openPresetQuery = openPresetQuery;
window.setExploreQuery = setExploreQuery;
window.runExploreQuery = runExploreQuery;
window.copyQueryLink = copyQueryLink;
window.createDashboardDialog = createDashboardDialog;
window.createDashboard = createDashboard;
window.createDashboardFromPreset = createDashboardFromPreset;
window.importGrafanaDialog = importGrafanaDialog;
window.importGrafanaDashboard = importGrafanaDashboard;
window.exportGrafanaDashboard = exportGrafanaDashboard;
window.deleteDashboard = deleteDashboard;
window.openDashboard = openDashboard;
window.addPanelDialog = addPanelDialog;
window.addPanel = addPanel;
window.addTargetDialog = addTargetDialog;
window.addTarget = addTarget;
window.removeTarget = removeTarget;
window.createIntegrationDashboards = createIntegrationDashboards;
window.nativeClientDialog = nativeClientDialog;
window.applyConfigPreset = applyConfigPreset;
window.saveConfigFromForm = saveConfigFromForm;
window.saveConfigFromJson = saveConfigFromJson;
window.loginDialog = loginDialog;
window.login = login;
