package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/neko233-com/Sentinel233/internal/store"
)

const maxEcosystemImportBodyBytes = 16 << 20

func (s *Server) handleEcosystemCapabilities(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"mode": "grafana-prometheus-ecosystem",
			"stability": map[string]interface{}{
				"contract":      "stable",
				"primaryPrefix": "/api/ecosystem",
				"localPrefix":   "/api/local/v1/ecosystem",
			},
			"formats": []map[string]interface{}{
				{"id": "grafana-dashboard", "contentTypes": []string{"application/json"}, "result": "dashboard"},
				{"id": "grafana-datasources", "contentTypes": []string{"application/json", "application/yaml"}, "result": "stored datasource mapping"},
				{"id": "prometheus-config", "contentTypes": []string{"application/yaml", "application/json"}, "result": "scrape targets"},
				{"id": "prometheus-rules", "contentTypes": []string{"application/yaml", "application/json"}, "result": "alert rules"},
				{"id": "alertmanager-webhook", "contentTypes": []string{"application/json"}, "result": "accepted alert payload"},
				{"id": "sentinel-dashboard", "contentTypes": []string{"application/json"}, "result": "dashboard"},
			},
			"channels": []map[string]string{
				{"id": "scrape", "endpoint": "/metrics", "direction": "pull"},
				{"id": "remote_write", "endpoint": "/api/v1/write", "direction": "push"},
				{"id": "prometheus_http_api", "endpoint": "/api/v1/*", "direction": "query"},
				{"id": "dashboard_import", "endpoint": "/api/dashboards/import", "direction": "control"},
				{"id": "ecosystem_import", "endpoint": "/api/ecosystem/import", "direction": "control"},
				{"id": "local_agent", "endpoint": "/api/local/v1/*", "direction": "loopback-control"},
				{"id": "alertmanager_webhook", "endpoint": "/api/ecosystem/alertmanager/webhook", "direction": "push"},
			},
			"grafanaPanelTypes": []string{"timeseries", "graph", "stat", "gauge", "table", "barchart", "bargauge", "heatmap", "piechart", "histogram", "xychart"},
			"queryModes":        []string{"promql", "promql+sql"},
		},
	})
}

func (s *Server) handleLocalAgentImportEcosystem(w http.ResponseWriter, r *http.Request) {
	s.handleEcosystemImport(w, r)
}

func (s *Server) handleAlertmanagerWebhookReceiver(w http.ResponseWriter, r *http.Request) {
	s.importAlertmanagerWebhook(w, r, s.getTenantID(r), "alertmanager-webhook")
}

func (s *Server) handleEcosystemImport(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEcosystemImportBodyBytes))
	if err != nil {
		s.jsonError(w, "ecosystem import body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		s.jsonError(w, "empty ecosystem import payload", http.StatusBadRequest)
		return
	}

	format, content, err := unwrapEcosystemImportPayload(r, body)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.importEcosystemContent(tenantID, format, content)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func (s *Server) importAlertmanagerWebhook(w http.ResponseWriter, r *http.Request, tenantID int64, format string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEcosystemImportBodyBytes))
	if err != nil {
		s.jsonError(w, "alertmanager webhook body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := s.importEcosystemContent(tenantID, format, body)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func unwrapEcosystemImportPayload(r *http.Request, body []byte) (string, []byte, error) {
	format := firstNonEmptyString(
		r.URL.Query().Get("source"),
		r.URL.Query().Get("format"),
		r.Header.Get("X-Sentinel-Source"),
		r.Header.Get("X-Sentinel-Format"),
	)
	content := bytes.TrimSpace(body)

	var wrapper struct {
		Source  string          `json:"source"`
		Format  string          `json:"format"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(body, &wrapper) == nil && len(wrapper.Content) > 0 {
		format = firstNonEmptyString(format, wrapper.Source, wrapper.Format)
		var text string
		if err := json.Unmarshal(wrapper.Content, &text); err == nil {
			content = []byte(text)
		} else {
			content = wrapper.Content
		}
	}

	if strings.TrimSpace(format) == "" {
		format = detectEcosystemFormat(content)
	}
	if strings.TrimSpace(format) == "" {
		return "", nil, fmt.Errorf("ecosystem import source is required when the format cannot be detected")
	}
	return normalizeEcosystemFormat(format), bytes.TrimSpace(content), nil
}

func (s *Server) importEcosystemContent(tenantID int64, format string, content []byte) (map[string]interface{}, error) {
	format = normalizeEcosystemFormat(format)
	switch format {
	case "grafana-dashboard":
		payload, err := rawJSONMap(content)
		if err != nil {
			return nil, err
		}
		if nested, ok := payload["dashboard"]; ok {
			var nestedMap map[string]json.RawMessage
			if err := json.Unmarshal(nested, &nestedMap); err == nil {
				payload = nestedMap
			}
		}
		dash, err := convertGrafanaPayloadToDashboard(payload)
		if err != nil {
			return nil, err
		}
		dash.TenantID = tenantID
		normalizeDashboardRecord(dash)
		if err := s.store.CreateDashboard(dash); err != nil {
			return nil, err
		}
		return map[string]interface{}{"format": format, "dashboard": dash}, nil
	case "sentinel-dashboard":
		payload, err := rawJSONMap(content)
		if err != nil {
			return nil, err
		}
		dash, err := convertDashboardPayload(payload)
		if err != nil {
			return nil, err
		}
		dash.TenantID = tenantID
		normalizeDashboardRecord(dash)
		if err := s.store.CreateDashboard(dash); err != nil {
			return nil, err
		}
		return map[string]interface{}{"format": format, "dashboard": dash}, nil
	case "prometheus-config":
		return s.importPrometheusConfig(tenantID, content)
	case "prometheus-rules":
		return s.importPrometheusRules(tenantID, content)
	case "grafana-datasources":
		return s.importGrafanaDatasources(tenantID, content)
	case "alertmanager-webhook":
		return s.importAlertmanagerPayload(tenantID, content)
	default:
		return nil, fmt.Errorf("unsupported ecosystem import source %q", format)
	}
}

func normalizeEcosystemFormat(format string) string {
	key := strings.ToLower(strings.TrimSpace(format))
	key = strings.ReplaceAll(key, "_", "-")
	switch key {
	case "grafana", "grafana-json", "grafana-dashboard-json", "dashboard":
		return "grafana-dashboard"
	case "sentinel", "sentinel-json", "sentinel-dashboard-json":
		return "sentinel-dashboard"
	case "prometheus", "prometheus-yaml", "prometheus-scrape", "prometheus-scrape-config":
		return "prometheus-config"
	case "prometheus-rule", "prometheus-rule-files", "rules", "rule":
		return "prometheus-rules"
	case "grafana-datasource", "grafana-datasource-provisioning", "datasource", "datasources":
		return "grafana-datasources"
	case "alertmanager", "alertmanager-webhook-payload", "webhook":
		return "alertmanager-webhook"
	default:
		return key
	}
}

func detectEcosystemFormat(content []byte) string {
	if payload, err := rawJSONMap(content); err == nil {
		if _, ok := payload["dashboard"]; ok {
			return "grafana-dashboard"
		}
		if looksLikeGrafanaDashboard(payload) {
			return "grafana-dashboard"
		}
		if _, ok := payload["scrape_configs"]; ok {
			return "prometheus-config"
		}
		if _, ok := payload["groups"]; ok {
			return "prometheus-rules"
		}
		if _, ok := payload["datasources"]; ok {
			return "grafana-datasources"
		}
		if _, ok := payload["alerts"]; ok {
			return "alertmanager-webhook"
		}
	}
	if doc, err := yamlDocument(content); err == nil {
		if _, ok := doc["scrape_configs"]; ok {
			return "prometheus-config"
		}
		if _, ok := doc["groups"]; ok {
			return "prometheus-rules"
		}
		if _, ok := doc["datasources"]; ok {
			return "grafana-datasources"
		}
	}
	return ""
}

type prometheusConfigImport struct {
	Global struct {
		ScrapeInterval string `json:"scrape_interval" yaml:"scrape_interval"`
		ScrapeTimeout  string `json:"scrape_timeout" yaml:"scrape_timeout"`
	} `json:"global" yaml:"global"`
	RuleFiles     []string                 `json:"rule_files" yaml:"rule_files"`
	RemoteWrite   []map[string]interface{} `json:"remote_write" yaml:"remote_write"`
	ScrapeConfigs []prometheusScrapeConfig `json:"scrape_configs" yaml:"scrape_configs"`
}

type prometheusScrapeConfig struct {
	JobName                    string                   `json:"job_name" yaml:"job_name"`
	Scheme                     string                   `json:"scheme" yaml:"scheme"`
	MetricsPath                string                   `json:"metrics_path" yaml:"metrics_path"`
	ScrapeInterval             string                   `json:"scrape_interval" yaml:"scrape_interval"`
	ScrapeTimeout              string                   `json:"scrape_timeout" yaml:"scrape_timeout"`
	Params                     map[string][]string      `json:"params" yaml:"params"`
	StaticConfigs              []prometheusStaticConfig `json:"static_configs" yaml:"static_configs"`
	FileSDConfigs              []interface{}            `json:"file_sd_configs" yaml:"file_sd_configs"`
	HTTPSDConfigs              []interface{}            `json:"http_sd_configs" yaml:"http_sd_configs"`
	KubernetesSDConfigs        []interface{}            `json:"kubernetes_sd_configs" yaml:"kubernetes_sd_configs"`
	ConsulSDConfigs            []interface{}            `json:"consul_sd_configs" yaml:"consul_sd_configs"`
	DNSSDConfigs               []interface{}            `json:"dns_sd_configs" yaml:"dns_sd_configs"`
	DockerSDConfigs            []interface{}            `json:"docker_sd_configs" yaml:"docker_sd_configs"`
	EC2SDConfigs               []interface{}            `json:"ec2_sd_configs" yaml:"ec2_sd_configs"`
	AzureSDConfigs             []interface{}            `json:"azure_sd_configs" yaml:"azure_sd_configs"`
	GCESDConfigs               []interface{}            `json:"gce_sd_configs" yaml:"gce_sd_configs"`
	RelabelConfigs             []interface{}            `json:"relabel_configs" yaml:"relabel_configs"`
	MetricRelabelConfigs       []interface{}            `json:"metric_relabel_configs" yaml:"metric_relabel_configs"`
	Authorization              map[string]interface{}   `json:"authorization" yaml:"authorization"`
	BasicAuth                  map[string]interface{}   `json:"basic_auth" yaml:"basic_auth"`
	TLSConfig                  map[string]interface{}   `json:"tls_config" yaml:"tls_config"`
	BearerToken                string                   `json:"bearer_token" yaml:"bearer_token"`
	BearerTokenFile            string                   `json:"bearer_token_file" yaml:"bearer_token_file"`
	HonorLabels                bool                     `json:"honor_labels" yaml:"honor_labels"`
	HonorTimestamps            bool                     `json:"honor_timestamps" yaml:"honor_timestamps"`
	NativeHistogramBucketLimit int                      `json:"native_histogram_bucket_limit" yaml:"native_histogram_bucket_limit"`
}

type prometheusStaticConfig struct {
	Targets []string          `json:"targets" yaml:"targets"`
	Labels  map[string]string `json:"labels" yaml:"labels"`
}

func (s *Server) importPrometheusConfig(tenantID int64, content []byte) (map[string]interface{}, error) {
	var cfg prometheusConfigImport
	if err := decodeYAMLOrJSON(content, &cfg); err != nil {
		return nil, err
	}
	var imported []map[string]interface{}
	var warnings []string
	for _, scrapeCfg := range cfg.ScrapeConfigs {
		if strings.TrimSpace(scrapeCfg.JobName) == "" {
			warnings = append(warnings, "scrape_config without job_name skipped")
			continue
		}
		if len(scrapeCfg.RelabelConfigs) > 0 || len(scrapeCfg.MetricRelabelConfigs) > 0 {
			warnings = append(warnings, fmt.Sprintf("job %q relabel configs are preserved in import metadata but not executed by the built-in scraper", scrapeCfg.JobName))
		}
		if hasServiceDiscovery(scrapeCfg) {
			warnings = append(warnings, fmt.Sprintf("job %q uses dynamic service discovery; import static_configs now and keep external discovery through Prometheus/Agent remote_write", scrapeCfg.JobName))
		}
		for _, staticCfg := range scrapeCfg.StaticConfigs {
			for _, target := range staticCfg.Targets {
				endpoint := buildPrometheusEndpoint(scrapeCfg, target)
				if endpoint == "" {
					continue
				}
				labels := map[string]string{"job": scrapeCfg.JobName}
				for k, v := range staticCfg.Labels {
					labels[k] = v
				}
				name := scrapeCfg.JobName
				if instance := strings.TrimSpace(target); instance != "" {
					labels["instance"] = instance
					name = fmt.Sprintf("%s/%s", scrapeCfg.JobName, instance)
				}
				item := &store.ScrapeTarget{TenantID: tenantID, Name: name, Endpoint: endpoint, Labels: labels, Enabled: true}
				if err := s.store.CreateScrapeTarget(item); err != nil {
					return nil, err
				}
				if s.scrape != nil {
					s.scrape.AddTarget(item.Name, item.Endpoint, item.Labels)
				}
				imported = append(imported, map[string]interface{}{"id": item.ID, "name": item.Name, "endpoint": item.Endpoint, "labels": item.Labels})
			}
		}
	}
	meta := map[string]interface{}{
		"global":      cfg.Global,
		"ruleFiles":   cfg.RuleFiles,
		"remoteWrite": cfg.RemoteWrite,
		"warnings":    warnings,
	}
	if err := s.store.SetSetting(tenantID, "prometheus_config_import", mustJSON(meta)); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"format":          "prometheus-config",
		"importedTargets": imported,
		"remoteWrite":     cfg.RemoteWrite,
		"ruleFiles":       cfg.RuleFiles,
		"warnings":        warnings,
	}, nil
}

type prometheusRulesImport struct {
	Groups []struct {
		Name     string `json:"name" yaml:"name"`
		Interval string `json:"interval" yaml:"interval"`
		Rules    []struct {
			Alert       string            `json:"alert" yaml:"alert"`
			Record      string            `json:"record" yaml:"record"`
			Expr        string            `json:"expr" yaml:"expr"`
			For         string            `json:"for" yaml:"for"`
			Labels      map[string]string `json:"labels" yaml:"labels"`
			Annotations map[string]string `json:"annotations" yaml:"annotations"`
		} `json:"rules" yaml:"rules"`
	} `json:"groups" yaml:"groups"`
}

func (s *Server) importPrometheusRules(tenantID int64, content []byte) (map[string]interface{}, error) {
	var rulesFile prometheusRulesImport
	if err := decodeYAMLOrJSON(content, &rulesFile); err != nil {
		return nil, err
	}
	var imported []map[string]interface{}
	var preserved []map[string]interface{}
	for _, group := range rulesFile.Groups {
		for _, rule := range group.Rules {
			if strings.TrimSpace(rule.Alert) == "" {
				if rule.Record != "" {
					preserved = append(preserved, map[string]interface{}{"group": group.Name, "record": rule.Record, "expr": rule.Expr})
				}
				continue
			}
			severity := firstNonEmptyString(rule.Labels["severity"], "warning")
			notifyURL := firstNonEmptyString(rule.Annotations["notify_url"], rule.Annotations["runbook_url"])
			item := &store.AlertRule{
				TenantID:  tenantID,
				Name:      rule.Alert,
				Expr:      rule.Expr,
				Duration:  firstNonEmptyString(rule.For, "0s"),
				Severity:  severity,
				NotifyURL: notifyURL,
				Enabled:   true,
			}
			if err := s.store.CreateAlertRule(item); err != nil {
				return nil, err
			}
			imported = append(imported, map[string]interface{}{"id": item.ID, "group": group.Name, "name": item.Name, "expr": item.Expr, "duration": item.Duration, "severity": item.Severity})
		}
	}
	if len(preserved) > 0 {
		if err := s.store.SetSetting(tenantID, "prometheus_recording_rules_import", mustJSON(preserved)); err != nil {
			return nil, err
		}
	}
	return map[string]interface{}{
		"format":                  "prometheus-rules",
		"importedAlertRules":      imported,
		"preservedRecordingRules": preserved,
	}, nil
}

type grafanaDatasourceImport struct {
	APIVersion  int `json:"apiVersion" yaml:"apiVersion"`
	Datasources []struct {
		Name           string                 `json:"name" yaml:"name"`
		UID            string                 `json:"uid" yaml:"uid"`
		Type           string                 `json:"type" yaml:"type"`
		URL            string                 `json:"url" yaml:"url"`
		Access         string                 `json:"access" yaml:"access"`
		IsDefault      bool                   `json:"isDefault" yaml:"isDefault"`
		BasicAuth      bool                   `json:"basicAuth" yaml:"basicAuth"`
		JsonData       map[string]interface{} `json:"jsonData" yaml:"jsonData"`
		SecureJsonData map[string]interface{} `json:"secureJsonData" yaml:"secureJsonData"`
		Editable       bool                   `json:"editable" yaml:"editable"`
	} `json:"datasources" yaml:"datasources"`
}

func (s *Server) importGrafanaDatasources(tenantID int64, content []byte) (map[string]interface{}, error) {
	var cfg grafanaDatasourceImport
	if err := decodeYAMLOrJSON(content, &cfg); err != nil {
		return nil, err
	}
	imported := make([]map[string]interface{}, 0, len(cfg.Datasources))
	var warnings []string
	for _, ds := range cfg.Datasources {
		entry := map[string]interface{}{
			"name":      ds.Name,
			"uid":       ds.UID,
			"type":      ds.Type,
			"url":       ds.URL,
			"access":    ds.Access,
			"isDefault": ds.IsDefault,
			"jsonData":  ds.JsonData,
		}
		if strings.EqualFold(ds.Type, "prometheus") {
			entry["sentinelEndpoint"] = "/api/v1"
			entry["integrationMode"] = "prometheus-http-api"
		} else if ds.Type != "" {
			warnings = append(warnings, fmt.Sprintf("Grafana datasource %q type %q is preserved as ecosystem metadata; route it through Prometheus/OpenMetrics exporters or native Sentinel ingestion", ds.Name, ds.Type))
		}
		if len(ds.SecureJsonData) > 0 || ds.BasicAuth {
			warnings = append(warnings, fmt.Sprintf("Grafana datasource %q contains auth material; Sentinel stores only integration metadata and expects secrets to stay in deployment config", ds.Name))
		}
		imported = append(imported, entry)
	}
	if err := s.store.SetSetting(tenantID, "grafana_datasources", mustJSON(imported)); err != nil {
		return nil, err
	}
	return map[string]interface{}{"format": "grafana-datasources", "datasources": imported, "warnings": warnings}, nil
}

type alertmanagerWebhookPayload struct {
	Receiver          string                   `json:"receiver"`
	Status            string                   `json:"status"`
	GroupLabels       map[string]string        `json:"groupLabels"`
	CommonLabels      map[string]string        `json:"commonLabels"`
	CommonAnnotations map[string]string        `json:"commonAnnotations"`
	ExternalURL       string                   `json:"externalURL"`
	Version           string                   `json:"version"`
	GroupKey          string                   `json:"groupKey"`
	TruncatedAlerts   int                      `json:"truncatedAlerts"`
	Alerts            []map[string]interface{} `json:"alerts"`
}

func (s *Server) importAlertmanagerPayload(tenantID int64, content []byte) (map[string]interface{}, error) {
	var payload alertmanagerWebhookPayload
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("invalid alertmanager webhook payload: %w", err)
	}
	if err := s.store.SetSetting(tenantID, "last_alertmanager_webhook", mustJSON(payload)); err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"format":          "alertmanager-webhook",
		"receiver":        payload.Receiver,
		"status":          payload.Status,
		"acceptedAlerts":  len(payload.Alerts),
		"truncatedAlerts": payload.TruncatedAlerts,
		"groupKey":        payload.GroupKey,
	}, nil
}

func hasServiceDiscovery(cfg prometheusScrapeConfig) bool {
	return len(cfg.FileSDConfigs)+len(cfg.HTTPSDConfigs)+len(cfg.KubernetesSDConfigs)+len(cfg.ConsulSDConfigs)+len(cfg.DNSSDConfigs)+len(cfg.DockerSDConfigs)+len(cfg.EC2SDConfigs)+len(cfg.AzureSDConfigs)+len(cfg.GCESDConfigs) > 0
}

func buildPrometheusEndpoint(cfg prometheusScrapeConfig, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	endpoint := target
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		scheme := firstNonEmptyString(cfg.Scheme, "http")
		path := firstNonEmptyString(cfg.MetricsPath, "/metrics")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		endpoint = fmt.Sprintf("%s://%s%s", scheme, endpoint, path)
	}
	if len(cfg.Params) == 0 {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	values := parsed.Query()
	for key, list := range cfg.Params {
		for _, value := range list {
			values.Add(key, value)
		}
	}
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func rawJSONMap(content []byte) (map[string]json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func yamlDocument(content []byte) (map[string]interface{}, error) {
	var payload map[string]interface{}
	if err := yaml.Unmarshal(content, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeYAMLOrJSON(content []byte, target interface{}) error {
	if json.Valid(content) {
		return json.Unmarshal(content, target)
	}
	return yaml.Unmarshal(content, target)
}

func mustJSON(value interface{}) string {
	data, _ := json.Marshal(value)
	return string(data)
}
