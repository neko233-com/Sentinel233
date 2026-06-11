package scrape

import "testing"

func TestParseOpenMetrics(t *testing.T) {
	data := `# HELP http_requests_total The total number of HTTP requests.
# TYPE http_requests_total counter
http_requests_total{method="GET",handler="/api",code="200"} 1027 1395066363000
http_requests_total{method="POST",handler="/api",code="200"} 3 1395066363000
# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.
# TYPE process_cpu_seconds_total gauge
process_cpu_seconds_total 4.20712846
up 1
`
	metrics, err := ParseOpenMetrics(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 4 {
		t.Fatalf("expected 4 metrics, got %d", len(metrics))
	}

	// Check first metric
	if metrics[0].Name != "http_requests_total" {
		t.Fatalf("expected http_requests_total, got %s", metrics[0].Name)
	}
	if metrics[0].Value != 1027 {
		t.Fatalf("expected 1027, got %f", metrics[0].Value)
	}

	// Check labels
	found := false
	for _, l := range metrics[0].Labels {
		if l.Name == "method" && l.Value == "GET" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected method=GET label")
	}

	// Check up metric
	if metrics[3].Name != "up" || metrics[3].Value != 1 {
		t.Fatalf("expected up=1, got %s=%f", metrics[3].Name, metrics[3].Value)
	}
}

func TestParseMetricLine(t *testing.T) {
	tests := []struct {
		line     string
		name     string
		value    float64
		labelCnt int
	}{
		{"up 1", "up", 1, 0},
		{`cpu{host="a"} 50.5`, "cpu", 50.5, 1},
		{`req_total{method="GET",code="200"} 100`, "req_total", 100, 2},
	}
	for _, tt := range tests {
		m, err := parseMetricLine(tt.line)
		if err != nil {
			t.Errorf("parseMetricLine(%q) error: %v", tt.line, err)
			continue
		}
		if m.Name != tt.name {
			t.Errorf("expected name %s, got %s", tt.name, m.Name)
		}
		if m.Value != tt.value {
			t.Errorf("expected value %f, got %f", tt.value, m.Value)
		}
		if len(m.Labels) != tt.labelCnt {
			t.Errorf("expected %d labels, got %d", tt.labelCnt, len(m.Labels))
		}
	}
}

func TestParseLabelString(t *testing.T) {
	labels := parseLabelString(`method="GET",code="200"`)
	if len(labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(labels))
	}
	if labels[0].Name != "method" || labels[0].Value != "GET" {
		t.Fatalf("expected method=GET, got %s=%s", labels[0].Name, labels[0].Value)
	}
}
