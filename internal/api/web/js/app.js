let currentLang = localStorage.getItem('sentinel233_lang') || 'zh-CN';
let translations = {};
let charts = {};

const API = '';

async function loadI18n(lang) {
  try {
    const resp = await fetch(`${API}/api/i18n/${lang}`);
    translations = await resp.json();
    document.querySelectorAll('[data-i18n]').forEach(el => {
      const key = el.getAttribute('data-i18n');
      if (translations[key]) el.textContent = translations[key];
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
      const key = el.getAttribute('data-i18n-placeholder');
      if (translations[key]) el.placeholder = translations[key];
    });
  } catch (e) {
    console.warn('i18n load failed', e);
  }
}

function t(key) {
  return translations[key] || key;
}

async function api(path, opts = {}) {
  const resp = await fetch(`${API}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  return resp.json();
}

async function queryPromQL(expr, start, end, step) {
  if (start && end) {
    return api(`/api/v1/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=${step || 15}`);
  }
  return api(`/api/v1/query?query=${encodeURIComponent(expr)}`);
}

function timeRangeToSeconds(range) {
  const map = { '1h': 3600, '6h': 21600, '12h': 43200, '24h': 86400, '7d': 604800, '30d': 2592000 };
  return map[range] || 86400;
}

function formatNumber(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return n.toString();
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
  Object.values(charts).forEach(c => c.destroy());
  charts = {};
}

// Pages
const pages = {
  overview: renderOverview,
  explore: renderExplore,
  alerts: renderAlerts,
  targets: renderTargets,
  settings: renderSettings,
};

async function renderOverview() {
  destroyCharts();
  const container = document.getElementById('page-content');
  const stats = await api('/api/system/stats').catch(() => ({ data: { series: 0, samples: 0, targets: 0, activeAlerts: 0 } }));
  const d = stats.data || {};

  container.innerHTML = `
    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-label">${t('overview.total_series')}</div>
        <div class="stat-value">${formatNumber(d.series || 0)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">${t('overview.total_samples')}</div>
        <div class="stat-value">${formatNumber(d.samples || 0)}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">${t('overview.active_targets')}</div>
        <div class="stat-value">${d.targets || 0}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">${t('overview.active_alerts')}</div>
        <div class="stat-value" style="color:${(d.activeAlerts || 0) > 0 ? 'var(--danger)' : 'var(--success)'}">${d.activeAlerts || 0}</div>
      </div>
    </div>
    <div class="grid-2">
      <div class="card">
        <div class="card-header"><h3>${t('overview.series_growth')}</h3></div>
        <div class="card-body"><div class="chart-container"><canvas id="chart-series"></canvas></div></div>
      </div>
      <div class="card">
        <div class="card-header"><h3>CPU / ${t('overview.memory_usage')}</h3></div>
        <div class="card-body"><div class="chart-container"><canvas id="chart-resource"></canvas></div></div>
      </div>
    </div>
    <div class="card" style="margin-top:16px">
      <div class="card-header"><h3>${t('dashboard.title')}</h3><button class="btn btn-primary btn-sm" onclick="createDashboardDialog()">${t('dashboard.new')}</button></div>
      <div class="card-body" id="dashboard-list"></div>
    </div>
  `;

  // Charts
  const range = document.getElementById('time-range').value;
  const now = Math.floor(Date.now() / 1000);
  const start = now - timeRangeToSeconds(range);
  const step = Math.max(Math.floor(timeRangeToSeconds(range) / 200), 15);

  try {
    const seriesData = await queryPromQL('count({__name__=~".+"})', start, now, step);
    renderTimeSeriesChart('chart-series', seriesData, '#00d4ff');
  } catch(e) {
    renderEmptyChart('chart-series');
  }

  try {
    const cpuData = await queryPromQL('process_cpu_seconds_total', start, now, step);
    renderTimeSeriesChart('chart-resource', cpuData, '#22c55e');
  } catch(e) {
    renderEmptyChart('chart-resource');
  }

  loadDashboardList();
}

async function loadDashboardList() {
  const resp = await api('/api/dashboards').catch(() => ({ data: [] }));
  const list = document.getElementById('dashboard-list');
  if (!list) return;
  const dashboards = resp.data || [];
  if (dashboards.length === 0) {
    list.innerHTML = `<div class="empty-state"><h3>${t('dashboard.no_dashboards')}</h3></div>`;
    return;
  }
  list.innerHTML = `<table><thead><tr><th>ID</th><th>${t('dashboard.title')}</th><th>${t('targets.last_scrape')}</th><th></th></tr></thead><tbody>${dashboards.map(d => `
    <tr>
      <td>${d.id}</td>
      <td><a href="#" onclick="openDashboard(${d.id})">${d.title}</a></td>
      <td>${new Date(d.updated_at).toLocaleString()}</td>
      <td><button class="btn btn-danger btn-sm" onclick="deleteDashboard(${d.id})">${t('dashboard.delete')}</button></td>
    </tr>
  `).join('')}</tbody></table>`;
}

function createDashboardDialog() {
  showModal(t('dashboard.new'), `
    <div class="form-group"><label>${t('dashboard.title')}</label><input id="new-dash-title" placeholder="My Dashboard"></div>
    <div class="form-group"><label>${t('dashboard.title')}</label><textarea id="new-dash-desc" rows="3" placeholder="Description"></textarea></div>
    <button class="btn btn-primary" onclick="createDashboard()">${t('dashboard.save')}</button>
  `);
}

async function createDashboard() {
  const title = document.getElementById('new-dash-title').value;
  const desc = document.getElementById('new-dash-desc').value;
  await api('/api/dashboards', { method: 'POST', body: JSON.stringify({ title, description: desc, panels: '[]', layout: '{}', variables: '[]', tags: '[]' }) });
  closeModal();
  renderOverview();
}

async function deleteDashboard(id) {
  if (!confirm(t('dashboard.delete') + '?')) return;
  await api(`/api/dashboards/${id}`, { method: 'DELETE' });
  renderOverview();
}

async function openDashboard(id) {
  destroyCharts();
  const resp = await api(`/api/dashboards/${id}`);
  const d = resp.data;
  const container = document.getElementById('page-content');
  container.innerHTML = `
    <div class="card">
      <div class="card-header"><h3>${d.title}</h3>
        <div style="display:flex;gap:8px">
          <button class="btn btn-primary btn-sm" onclick="addPanelDialog(${id})">${t('dashboard.add_panel')}</button>
          <button class="btn btn-secondary btn-sm" onclick="renderOverview()">${t('common.refresh')}</button>
        </div>
      </div>
      <div class="card-body"><div class="dashboard-grid" id="panels-${id}"></div></div>
    </div>
  `;
  const panels = JSON.parse(d.panels || '[]');
  const grid = document.getElementById(`panels-${id}`);
  for (let i = 0; i < panels.length; i++) {
    const p = panels[i];
    const panelId = `panel-${id}-${i}`;
    grid.innerHTML += `<div class="panel"><div class="panel-header">${p.title || 'Panel'}</div><div class="panel-body"><div class="chart-container"><canvas id="${panelId}"></canvas></div></div></div>`;
    if (p.query) {
      try {
        const range = document.getElementById('time-range').value;
        const now = Math.floor(Date.now() / 1000);
        const start = now - timeRangeToSeconds(range);
        const data = await queryPromQL(p.query, start, now, 15);
        renderTimeSeriesChart(panelId, data, getRandomColor());
      } catch(e) { renderEmptyChart(panelId); }
    }
  }
}

function addPanelDialog(dashId) {
  showModal(t('dashboard.add_panel'), `
    <div class="form-group"><label>Title</label><input id="panel-title" placeholder="Panel Title"></div>
    <div class="form-group"><label>PromQL</label><input id="panel-query" placeholder="up" class="query-input"></div>
    <div class="form-group"><label>${t('panel.type.timeseries')}</label>
      <select id="panel-type"><option value="timeseries">${t('panel.type.timeseries')}</option><option value="gauge">${t('panel.type.gauge')}</option><option value="stat">${t('panel.type.stat')}</option></select>
    </div>
    <button class="btn btn-primary" onclick="addPanel(${dashId})">${t('dashboard.add_panel')}</button>
  `);
}

async function addPanel(dashId) {
  const title = document.getElementById('panel-title').value;
  const query = document.getElementById('panel-query').value;
  const type = document.getElementById('panel-type').value;
  const resp = await api(`/api/dashboards/${dashId}`);
  const d = resp.data;
  const panels = JSON.parse(d.panels || '[]');
  panels.push({ title, query, type });
  d.panels = JSON.stringify(panels);
  await api(`/api/dashboards/${dashId}`, { method: 'PUT', body: JSON.stringify(d) });
  closeModal();
  openDashboard(dashId);
}

async function renderExplore() {
  destroyCharts();
  const container = document.getElementById('page-content');
  container.innerHTML = `
    <div class="query-bar">
      <input type="text" class="query-input" id="explore-query" placeholder="${t('metrics.query_placeholder')}" value="up">
      <button class="btn btn-primary" onclick="runExploreQuery()">${t('metrics.execute')}</button>
    </div>
    <div class="card">
      <div class="card-header"><h3>${t('metrics.title')}</h3></div>
      <div class="card-body">
        <div class="chart-container"><canvas id="explore-chart"></canvas></div>
        <div id="explore-table" style="margin-top:16px"></div>
      </div>
    </div>
  `;
  document.getElementById('explore-query').addEventListener('keydown', e => { if (e.key === 'Enter') runExploreQuery(); });
}

async function runExploreQuery() {
  const expr = document.getElementById('explore-query').value;
  if (!expr) return;
  const range = document.getElementById('time-range').value;
  const now = Math.floor(Date.now() / 1000);
  const start = now - timeRangeToSeconds(range);
  const step = Math.max(Math.floor(timeRangeToSeconds(range) / 200), 15);

  try {
    const data = await queryPromQL(expr, start, now, step);
    if (charts['explore-chart']) charts['explore-chart'].destroy();
    renderTimeSeriesChart('explore-chart', data, '#00d4ff');
    renderExploreTable(data);
  } catch(e) {
    document.getElementById('explore-table').innerHTML = `<div class="badge badge-danger">${e.message || 'Query error'}</div>`;
  }
}

function renderExploreTable(data) {
  const table = document.getElementById('explore-table');
  if (!data.data || !data.data.result) { table.innerHTML = t('metrics.no_data'); return; }
  const results = data.data.result;
  if (results.length === 0) { table.innerHTML = t('metrics.no_data'); return; }

  let html = '<table><thead><tr><th>Metric</th><th>Value</th></tr></thead><tbody>';
  results.forEach(r => {
    const labels = r.metric ? Object.entries(r.metric).map(([k,v]) => `${k}="${v}"`).join(', ') : '';
    const value = r.value ? r.value[1] : (r.values ? r.values[r.values.length-1][1] : '-');
    html += `<tr><td style="font-family:monospace;font-size:13px">${labels || '{}'}</td><td>${value}</td></tr>`;
  });
  html += '</tbody></table>';
  table.innerHTML = html;
}

async function renderAlerts() {
  destroyCharts();
  const container = document.getElementById('page-content');
  const resp = await api('/api/alerts').catch(() => ({ data: [] }));
  const historyResp = await api('/api/alerts/history').catch(() => ({ data: [] }));
  const alerts = resp.data || [];
  const history = historyResp.data || [];

  container.innerHTML = `
    <div class="card">
      <div class="card-header"><h3>${t('alerts.active')} (${alerts.length})</h3></div>
      <div class="card-body">
        ${alerts.length === 0 ? `<div class="empty-state"><h3>${t('alerts.no_alerts')}</h3></div>` : alerts.map(a => `
          <div class="alert-row">
            <div class="alert-dot ${a.state}"></div>
            <div style="flex:1">
              <strong>${a.labels?.alertname || 'Unknown'}</strong>
              <span class="badge badge-${a.state === 'firing' ? 'danger' : a.state === 'pending' ? 'warning' : 'success'}">${t('alerts.state.' + a.state)}</span>
              <div style="color:var(--text-muted);font-size:13px;margin-top:4px">Value: ${a.value?.toFixed(4) || '-'}</div>
            </div>
          </div>
        `).join('')}
      </div>
    </div>
    <div class="card" style="margin-top:16px">
      <div class="card-header"><h3>${t('alerts.history')}</h3></div>
      <div class="card-body">
        ${history.length === 0 ? `<div class="empty-state">${t('alerts.no_alerts')}</div>` : `<table><thead><tr><th>Name</th><th>State</th><th>Time</th></tr></thead><tbody>${history.slice(-20).reverse().map(h => `
          <tr><td>${h.name}</td><td><span class="badge badge-${h.state === 'firing' ? 'danger' : 'success'}">${h.state}</span></td><td>${new Date(h.timestamp).toLocaleString()}</td></tr>
        `).join('')}</tbody></table>`}
      </div>
    </div>
  `;
}

async function renderTargets() {
  destroyCharts();
  const container = document.getElementById('page-content');
  const resp = await api('/api/targets').catch(() => ({ data: [] }));
  const targets = resp.data || [];

  container.innerHTML = `
    <div class="card">
      <div class="card-header"><h3>${t('targets.title')} (${targets.length})</h3>
        <button class="btn btn-primary btn-sm" onclick="addTargetDialog()">${t('targets.add')}</button>
      </div>
      <div class="card-body">
        ${targets.length === 0 ? `<div class="empty-state"><h3>${t('targets.add')}</h3></div>` : `<table><thead><tr><th>${t('targets.name')}</th><th>${t('targets.endpoint')}</th><th>${t('targets.labels')}</th><th>${t('targets.status')}</th><th>${t('targets.last_scrape')}</th><th></th></tr></thead><tbody>${targets.map(t => `
          <tr>
            <td><strong>${t.name}</strong></td>
            <td style="font-family:monospace;font-size:13px">${t.endpoint}</td>
            <td>${t.labels ? Object.entries(t.labels).map(([k,v]) => `<span class="badge badge-info">${k}=${v}</span>`).join(' ') : '-'}</td>
            <td><span class="badge badge-${t.healthy ? 'success' : 'danger'}">${t.healthy ? '${"targets.healthy"}' : '${"targets.unhealthy"}'}</span></td>
            <td>${t.lastScrape && t.lastScrape !== '0001-01-01T00:00:00Z' ? new Date(t.lastScrape).toLocaleString() : t('targets.never')}</td>
            <td><button class="btn btn-danger btn-sm" onclick="removeTarget('${t.endpoint}')">&times;</button></td>
          </tr>
        `).join('')}</tbody></table>`}
      </div>
    </div>
  `;
}

function addTargetDialog() {
  showModal(t('targets.add'), `
    <div class="form-group"><label>${t('targets.name')}</label><input id="target-name" placeholder="my-server"></div>
    <div class="form-group"><label>${t('targets.endpoint')}</label><input id="target-endpoint" placeholder="http://192.168.1.100:9090/metrics"></div>
    <div class="form-group"><label>${t('targets.labels')}</label><input id="target-labels" placeholder='{"env":"prod","region":"cn"}'></div>
    <button class="btn btn-primary" onclick="addTarget()">${t('targets.add')}</button>
  `);
}

async function addTarget() {
  const name = document.getElementById('target-name').value;
  const endpoint = document.getElementById('target-endpoint').value;
  let labels = {};
  try { labels = JSON.parse(document.getElementById('target-labels').value || '{}'); } catch(e) {}
  await api('/api/targets', { method: 'POST', body: JSON.stringify({ name, endpoint, labels }) });
  closeModal();
  renderTargets();
}

async function removeTarget(endpoint) {
  if (!confirm('Remove target?')) return;
  await api(`/api/targets?endpoint=${encodeURIComponent(endpoint)}`, { method: 'DELETE' });
  renderTargets();
}

function renderSettings() {
  destroyCharts();
  const container = document.getElementById('page-content');
  container.innerHTML = `
    <div class="card">
      <div class="card-header"><h3>${t('settings.title')}</h3></div>
      <div class="card-body">
        <div class="form-group"><label>${t('settings.language')}</label>
          <select id="settings-lang">
            <option value="zh-CN" ${currentLang==='zh-CN'?'selected':''}>中文</option>
            <option value="en-US" ${currentLang==='en-US'?'selected':''}>English</option>
            <option value="ja-JP" ${currentLang==='ja-JP'?'selected':''}>日本語</option>
          </select>
        </div>
        <div class="form-group"><label>${t('settings.retention')}</label><input type="number" id="settings-retention" value="15"></div>
        <div class="form-group"><label>${t('settings.scrape_interval')}</label><input type="number" id="settings-interval" value="15"></div>
        <button class="btn btn-primary" onclick="saveSettings()">${t('settings.save')}</button>
      </div>
    </div>
  `;
}

function saveSettings() {
  const lang = document.getElementById('settings-lang').value;
  currentLang = lang;
  localStorage.setItem('sentinel233_lang', lang);
  loadI18n(lang);
  alert(t('settings.saved'));
}

// Chart helpers
function renderTimeSeriesChart(canvasId, data, color) {
  const canvas = document.getElementById(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const results = data?.data?.result || [];

  const datasets = results.slice(0, 20).map((r, i) => {
    const label = r.metric ? Object.entries(r.metric).map(([k,v]) => `${k}="${v}"`).join(', ') : `series_${i}`;
    const points = (r.values || [r.value] || []).map(v => ({ x: new Date(v[0] * 1000), y: parseFloat(v[1]) }));
    const c = i === 0 ? color : getRandomColor();
    return { label: label.substring(0, 60), data: points, borderColor: c, backgroundColor: c + '20', borderWidth: 1.5, pointRadius: 0, fill: true, tension: 0.3 };
  });

  charts[canvasId] = new Chart(ctx, {
    type: 'line',
    data: { datasets },
    options: {
      responsive: true, maintainAspectRatio: false,
      animation: { duration: 500 },
      scales: {
        x: { type: 'time', time: { tooltipFormat: 'HH:mm:ss' }, grid: { color: '#2a3446' }, ticks: { color: '#64748b' } },
        y: { grid: { color: '#2a3446' }, ticks: { color: '#64748b' } }
      },
      plugins: { legend: { labels: { color: '#94a3b8', boxWidth: 12, font: { size: 11 } } } }
    }
  });
}

function renderEmptyChart(canvasId) {
  const canvas = document.getElementById(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#64748b';
  ctx.font = '14px sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(t('metrics.no_data'), canvas.width / 2, canvas.height / 2);
}

function getRandomColor() {
  const colors = ['#00d4ff', '#22c55e', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4', '#f97316'];
  return colors[Math.floor(Math.random() * colors.length)];
}

// Navigation
function navigate(page) {
  destroyCharts();
  document.querySelectorAll('.nav-item').forEach(el => el.classList.remove('active'));
  document.querySelector(`.nav-item[data-page="${page}"]`)?.classList.add('active');
  const titles = { overview: 'nav.dashboard', explore: 'nav.metrics', alerts: 'nav.alerts', targets: 'nav.targets', settings: 'nav.settings' };
  document.getElementById('page-title').setAttribute('data-i18n', titles[page] || page);
  document.getElementById('page-title').textContent = t(titles[page] || page);
  if (pages[page]) pages[page]();
}

// Init
document.addEventListener('DOMContentLoaded', async () => {
  await loadI18n(currentLang);
  document.getElementById('lang-select').value = currentLang;
  document.getElementById('lang-select').addEventListener('change', e => {
    currentLang = e.target.value;
    localStorage.setItem('sentinel233_lang', currentLang);
    loadI18n(currentLang);
    const hash = window.location.hash.slice(1) || 'overview';
    navigate(hash);
  });

  document.querySelectorAll('.nav-item').forEach(el => {
    el.addEventListener('click', e => {
      e.preventDefault();
      const page = el.dataset.page;
      window.location.hash = page;
    });
  });

  document.getElementById('btn-refresh').addEventListener('click', () => {
    const hash = window.location.hash.slice(1) || 'overview';
    navigate(hash);
  });

  document.getElementById('time-range').addEventListener('change', () => {
    const hash = window.location.hash.slice(1) || 'overview';
    navigate(hash);
  });

  window.addEventListener('hashchange', () => {
    navigate(window.location.hash.slice(1) || 'overview');
  });

  navigate(window.location.hash.slice(1) || 'overview');
});

// Make functions globally accessible
window.closeModal = closeModal;
window.createDashboardDialog = createDashboardDialog;
window.createDashboard = createDashboard;
window.deleteDashboard = deleteDashboard;
window.openDashboard = openDashboard;
window.addPanelDialog = addPanelDialog;
window.addPanel = addPanel;
window.runExploreQuery = runExploreQuery;
window.addTargetDialog = addTargetDialog;
window.addTarget = addTarget;
window.removeTarget = removeTarget;
window.saveSettings = saveSettings;
