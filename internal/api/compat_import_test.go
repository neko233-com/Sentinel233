package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

func TestCompatImportPrometheusConfigCreatesTargets(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `
scrape_configs:
  - job_name: node
    static_configs:
      - targets: ["localhost:9100", "127.0.0.1:9200"]
        labels:
          env: prod
`
	req := httptest.NewRequest(http.MethodPost, "/api/local/v1/compat/import?source=prometheus-config", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	targets, err := server.store.ListScrapeTargets(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	if targets[0].Labels["job"] != "node" || targets[0].Labels["env"] != "prod" {
		t.Fatalf("labels not preserved: %#v", targets[0].Labels)
	}
	if targets[0].Endpoint != "http://localhost:9100/metrics" {
		t.Fatalf("endpoint = %q", targets[0].Endpoint)
	}
}

func TestCompatImportPrometheusRulesCreatesAlertRules(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `
groups:
  - name: api
    rules:
      - alert: InstanceDown
        expr: up == 0
        for: 5m
        labels:
          severity: critical
      - record: job:http_requests:rate5m
        expr: sum(rate(http_requests_total[5m])) by (job)
`
	req := httptest.NewRequest(http.MethodPost, "/api/local/v1/compat/import?source=prometheus-rules", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rules, err := server.store.ListAlertRules(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(rules))
	}
	if rules[0].Name != "InstanceDown" || rules[0].Severity != "critical" || rules[0].Duration != "5m" {
		t.Fatalf("rule not converted: %#v", rules[0])
	}
	if _, err := server.store.GetSetting(1, "prometheus_recording_rules_import"); err != nil {
		t.Fatalf("recording rules metadata was not preserved: %v", err)
	}
}

func TestCompatImportGrafanaDatasourcesStoresMapping(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prom
    type: prometheus
    url: http://prometheus:9090
    access: proxy
    isDefault: true
`
	req := httptest.NewRequest(http.MethodPost, "/api/local/v1/compat/import?source=grafana-datasources", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	raw, err := server.store.GetSetting(1, "grafana_datasources")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "/api/v1") {
		t.Fatalf("datasource mapping does not point to Sentinel Prometheus API: %s", raw)
	}
}

func TestEcosystemAPIIsPrimaryStableImportPath(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/ecosystem/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+testLoginToken(t, server))
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"primaryPrefix":"/api/ecosystem"`) {
		t.Fatalf("capabilities did not advertise stable ecosystem prefix: %s", rec.Body.String())
	}

	body := `{"receiver":"sentinel","status":"firing","alerts":[{"labels":{"alertname":"InstanceDown"}}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/ecosystem/alertmanager/webhook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testLoginToken(t, server))
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ecosystem alertmanager status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLocalEcosystemImportIsStableLoopbackPath(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `
scrape_configs:
  - job_name: local-node
    static_configs:
      - targets: ["localhost:9100"]
`
	req := httptest.NewRequest(http.MethodPost, "/api/local/v1/ecosystem/import?source=prometheus-config", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local ecosystem import status = %d, body = %s", rec.Code, rec.Body.String())
	}
	targets, err := server.store.ListScrapeTargets(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "local-node/localhost:9100" {
		t.Fatalf("targets not imported through local ecosystem path: %#v", targets)
	}
}

func TestAlertmanagerWebhookReceiverAcceptsPublicPayload(t *testing.T) {
	server, _, cleanup := newTestServer(t)
	defer cleanup()

	body := []byte(`{"receiver":"sentinel","status":"firing","alerts":[{"labels":{"alertname":"InstanceDown"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/compat/alertmanager/webhook", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	raw, err := server.store.GetSetting(1, "last_alertmanager_webhook")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "InstanceDown") {
		t.Fatalf("webhook payload was not preserved: %s", raw)
	}
}

func TestPrometheusCompatEndpointsReturnStandardShapes(t *testing.T) {
	server, db, cleanup := newTestServer(t)
	defer cleanup()

	now := time.Now()
	labels := tsdb.Labels{{Name: "__name__", Value: "up"}, {Name: "job", Value: "api"}, {Name: "instance", Value: "localhost:9090"}}
	if err := db.Append(labels, now.Add(-time.Minute).UnixMilli(), 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Append(labels, now.UnixMilli(), 1); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=up&start="+strconvFormat(now.Add(-time.Minute).Unix())+"&end="+strconvFormat(now.Unix())+"&step=30", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("query_range status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rangeResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rangeResp); err != nil {
		t.Fatal(err)
	}
	data := rangeResp["data"].(map[string]interface{})
	if data["resultType"] != "matrix" {
		t.Fatalf("resultType = %#v", data["resultType"])
	}
	result := data["result"].([]interface{})
	if len(result) != 1 {
		t.Fatalf("matrix series = %d, want 1", len(result))
	}
	if _, ok := result[0].(map[string]interface{})["values"]; !ok {
		t.Fatalf("matrix result lacks values: %#v", result[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/labels?match[]=up", nil)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "instance") {
		t.Fatalf("labels response = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/metadata?metric=up", nil)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"up"`) {
		t.Fatalf("metadata response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPrometheusCompatAcceptsGrafanaStyleFormAndDurations(t *testing.T) {
	server, db, cleanup := newTestServer(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []struct {
		job string
		ts  time.Time
	}{
		{job: "zeta", ts: now.Add(-2 * time.Minute)},
		{job: "api", ts: now.Add(-time.Minute)},
		{job: "api", ts: now},
	} {
		labels := tsdb.Labels{{Name: "__name__", Value: "http_requests_total"}, {Name: "job", Value: item.job}, {Name: "instance", Value: item.job + ":9090"}}
		if err := db.Append(labels, item.ts.UnixMilli(), 1); err != nil {
			t.Fatal(err)
		}
	}

	body := strings.NewReader("query=http_requests_total&start=" + now.Add(-3*time.Minute).Format(time.RFC3339Nano) + "&end=" + now.Format(time.RFC3339Nano) + "&step=1m")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query_range", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("query_range form status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/label/job/values?match[]=http_requests_total", nil)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("label values status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var labelResp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &labelResp); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(labelResp.Data, ","), "api,zeta"; got != want {
		t.Fatalf("label values = %q, want %q", got, want)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader("query=http_requests_total&time="+strconv.FormatInt(now.UnixMilli(), 10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("query millisecond form status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func strconvFormat(v int64) string {
	return strconv.FormatInt(v, 10)
}

func testLoginToken(t *testing.T, server *Server) string {
	t.Helper()
	token := "test-token"
	server.tokens[token] = tokenInfo{TenantID: 1, Username: "root", Role: "admin"}
	return token
}
