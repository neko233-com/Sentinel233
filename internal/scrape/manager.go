package scrape

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neko233-com/Sentinel233/internal/config"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

type Manager struct {
	db      *tsdb.DB
	config  config.ScrapeConfig
	client  *http.Client
	logger  *slog.Logger
	mu      sync.RWMutex
	targets []*Target
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

type Target struct {
	Name       string
	Endpoint   string
	Labels     map[string]string
	LastScrape time.Time
	LastError  error
	Healthy    bool
}

type ScrapeResult struct {
	Labels  tsdb.Labels
	Samples []tsdb.Sample
}

func NewManager(db *tsdb.DB, cfg config.ScrapeConfig, logger *slog.Logger) *Manager {
	m := &Manager{
		db:     db,
		config: cfg,
		client: &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second},
		logger: logger,
		stopCh: make(chan struct{}),
	}
	for _, t := range cfg.Targets {
		m.targets = append(m.targets, &Target{
			Name:     t.Name,
			Endpoint: t.Endpoint,
			Labels:   t.Labels,
			Healthy:  true,
		})
	}
	return m
}

func (m *Manager) Start() {
	interval := time.Duration(m.config.Interval) * time.Second
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.scrapeAll()
			case <-m.stopCh:
				return
			}
		}
	}()
}

func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *Manager) AddTarget(name, endpoint string, labels map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = append(m.targets, &Target{
		Name:     name,
		Endpoint: endpoint,
		Labels:   labels,
		Healthy:  true,
	})
}

func (m *Manager) ApplyConfig(cfg config.ScrapeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = cfg
	m.client = &http.Client{Timeout: time.Duration(cfg.Timeout) * time.Second}
	m.targets = m.targets[:0]
	for _, t := range cfg.Targets {
		m.targets = append(m.targets, &Target{
			Name:     t.Name,
			Endpoint: t.Endpoint,
			Labels:   t.Labels,
			Healthy:  true,
		})
	}
}

func (m *Manager) RemoveTarget(endpoint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.targets {
		if t.Endpoint == endpoint {
			m.targets = append(m.targets[:i], m.targets[i+1:]...)
			return
		}
	}
}

func (m *Manager) GetTargets() []*Target {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Target, len(m.targets))
	copy(result, m.targets)
	return result
}

func (m *Manager) scrapeAll() {
	m.mu.RLock()
	targets := make([]*Target, len(m.targets))
	copy(targets, m.targets)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t *Target) {
			defer wg.Done()
			m.scrapeTarget(t)
		}(t)
	}
	wg.Wait()
}

func (m *Manager) scrapeTarget(t *Target) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.config.Timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", t.Endpoint, nil)
	if err != nil {
		t.LastError = err
		t.Healthy = false
		m.logger.Warn("scrape: create request failed", "target", t.Name, "err", err)
		return
	}

	resp, err := m.client.Do(req)
	if err != nil {
		t.LastError = err
		t.Healthy = false
		m.logger.Warn("scrape: request failed", "target", t.Name, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.LastError = fmt.Errorf("HTTP %d", resp.StatusCode)
		t.Healthy = false
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.LastError = err
		t.Healthy = false
		return
	}

	metrics, err := ParseOpenMetrics(string(body))
	if err != nil {
		t.LastError = err
		t.Healthy = false
		m.logger.Warn("scrape: parse failed", "target", t.Name, "err", err)
		return
	}

	now := time.Now().UnixMilli()
	for _, metric := range metrics {
		labels := make(tsdb.Labels, 0, len(t.Labels)+len(metric.Labels)+1)
		for k, v := range t.Labels {
			labels = append(labels, tsdb.Label{Name: k, Value: v})
		}
		labels = append(labels, tsdb.Label{Name: "__name__", Value: metric.Name})
		labels = append(labels, metric.Labels...)

		if err := m.db.Append(labels, now, metric.Value); err != nil {
			m.logger.Error("scrape: append failed", "target", t.Name, "err", err)
		}
	}

	t.LastScrape = time.Now()
	t.LastError = nil
	t.Healthy = true
}

type MetricSample struct {
	Name   string
	Labels tsdb.Labels
	Value  float64
}

func ParseOpenMetrics(data string) ([]MetricSample, error) {
	var metrics []MetricSample
	lines := strings.Split(data, "\n")
	helpCache := make(map[string]string)
	typeCache := make(map[string]string)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "# HELP ") {
				parts := strings.SplitN(strings.TrimPrefix(line, "# HELP "), " ", 2)
				if len(parts) == 2 {
					helpCache[parts[0]] = parts[1]
				}
			} else if strings.HasPrefix(line, "# TYPE ") {
				parts := strings.SplitN(strings.TrimPrefix(line, "# TYPE "), " ", 2)
				if len(parts) == 2 {
					typeCache[parts[0]] = parts[1]
				}
			}
			continue
		}
		_ = helpCache
		_ = typeCache

		m, err := parseMetricLine(line)
		if err != nil {
			continue
		}
		metrics = append(metrics, *m)
	}
	return metrics, nil
}

func parseMetricLine(line string) (*MetricSample, error) {
	// parse: metric_name{label="value"} float_value timestamp
	nameEnd := strings.IndexAny(line, "{ ")
	if nameEnd == -1 {
		return nil, fmt.Errorf("invalid metric line: %s", line)
	}
	name := line[:nameEnd]
	rest := line[nameEnd:]

	var labels tsdb.Labels
	if rest[0] == '{' {
		labelEnd := strings.Index(rest, "}")
		if labelEnd == -1 {
			return nil, fmt.Errorf("unterminated labels")
		}
		labelStr := rest[1:labelEnd]
		labels = parseLabelString(labelStr)
		rest = rest[labelEnd+1:]
	}

	rest = strings.TrimSpace(rest)
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no value")
	}

	val, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid value: %s", parts[0])
	}

	return &MetricSample{
		Name:   name,
		Labels: labels,
		Value:  val,
	}, nil
}

func parseLabelString(s string) tsdb.Labels {
	if s == "" {
		return nil
	}
	var labels tsdb.Labels
	parts := splitLabels(s)
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), "\"")
		labels = append(labels, tsdb.Label{Name: key, Value: val})
	}
	return labels
}

func splitLabels(s string) []string {
	var parts []string
	current := strings.Builder{}
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			current.WriteByte(ch)
		} else if ch == ',' && !inQuote {
			parts = append(parts, current.String())
			current.Reset()
		} else {
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
