package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/neko233-com/Sentinel233/internal/config"
	"github.com/neko233-com/Sentinel233/internal/promql"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

type State int

const (
	StateInactive State = iota
	StatePending
	StateFiring
)

func (s State) String() string {
	switch s {
	case StateInactive:
		return "inactive"
	case StatePending:
		return "pending"
	case StateFiring:
		return "firing"
	default:
		return "unknown"
	}
}

type Alert struct {
	Rule      config.AlertRule
	State     State
	Labels    map[string]string
	Value     float64
	ActiveAt  time.Time
	LastEval  time.Time
	Annotations map[string]string
}

type Manager struct {
	db      *tsdb.DB
	engine  *promql.Engine
	config  config.AlertConfig
	logger  *slog.Logger
	alerts  map[string]*Alert
	history []AlertEvent
	mu      sync.RWMutex
	stopCh  chan struct{}
	wg      sync.WaitGroup
	client  *http.Client
}

type AlertEvent struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
}

func NewManager(db *tsdb.DB, engine *promql.Engine, cfg config.AlertConfig, logger *slog.Logger) *Manager {
	return &Manager{
		db:     db,
		engine: engine,
		config: cfg,
		logger: logger,
		alerts: make(map[string]*Alert),
		stopCh: make(chan struct{}),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *Manager) Start() {
	if !m.config.Enabled {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.evalAll()
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

func (m *Manager) GetAlerts() []*Alert {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		result = append(result, a)
	}
	return result
}

func (m *Manager) GetHistory() []AlertEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]AlertEvent, len(m.history))
	copy(result, m.history)
	return result
}

func (m *Manager) evalAll() {
	for _, rule := range m.config.Rules {
		m.evalRule(rule)
	}
}

func (m *Manager) evalRule(rule config.AlertRule) {
	result, err := m.engine.EvalInstant(rule.Expr, time.Now())
	if err != nil {
		m.logger.Error("alert: eval failed", "rule", rule.Name, "err", err)
		return
	}

	key := rule.Name

	m.mu.Lock()
	defer m.mu.Unlock()

	if result.Type == promql.ValueInstantVector && len(result.Vector) > 0 {
		alert, exists := m.alerts[key]
		if !exists {
			alert = &Alert{
				Rule:  rule,
				State: StatePending,
				Labels: map[string]string{"alertname": rule.Name, "severity": rule.Severity},
			}
			m.alerts[key] = alert
		}

		if len(result.Vector) > 0 {
			alert.Value = result.Vector[0].Point.Value
		}

		dur, _ := parsePromDuration(rule.Duration)
		if alert.State == StatePending && time.Since(alert.ActiveAt) >= dur {
			alert.State = StateFiring
			alert.Annotations = map[string]string{
				"summary": fmt.Sprintf("Alert %s is firing, value=%.4f", rule.Name, alert.Value),
			}
			m.fireAlert(alert, rule)
		}
		if alert.ActiveAt.IsZero() {
			alert.ActiveAt = time.Now()
		}
		alert.LastEval = time.Now()
	} else {
		if alert, exists := m.alerts[key]; exists {
			m.history = append(m.history, AlertEvent{
				Name:      alert.Rule.Name,
				State:     "resolved",
				Timestamp: time.Now(),
				Labels:    alert.Labels,
			})
			delete(m.alerts, key)
		}
	}
}

func (m *Manager) fireAlert(alert *Alert, rule config.AlertRule) {
	m.logger.Warn("alert: FIRING", "name", rule.Name, "value", alert.Value, "severity", rule.Severity)

	m.history = append(m.history, AlertEvent{
		Name:      rule.Name,
		State:     "firing",
		Value:     alert.Value,
		Timestamp: time.Now(),
		Labels:    alert.Labels,
	})

	if rule.NotifyURL != "" {
		go m.sendNotification(rule.NotifyURL, alert)
	}
}

func (m *Manager) sendNotification(url string, alert *Alert) {
	payload := map[string]interface{}{
		"alertname":  alert.Rule.Name,
		"state":      alert.State.String(),
		"value":      alert.Value,
		"severity":   alert.Rule.Severity,
		"labels":     alert.Labels,
		"annotations": alert.Annotations,
		"activeAt":   alert.ActiveAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	resp, err := m.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		m.logger.Error("alert: notify failed", "url", url, "err", err)
		return
	}
	resp.Body.Close()
}

func parsePromDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var total time.Duration
	i := 0
	for i < len(s) {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		var val float64
		fmt.Sscanf(s[start:i], "%f", &val)
		unitStart := i
		for i < len(s) && (s[i] < '0' || s[i] > '9') {
			i++
		}
		unit := s[unitStart:i]
		switch unit {
		case "ms":
			total += time.Duration(val * float64(time.Millisecond))
		case "s":
			total += time.Duration(val * float64(time.Second))
		case "m":
			total += time.Duration(val * float64(time.Minute))
		case "h":
			total += time.Duration(val * float64(time.Hour))
		case "d":
			total += time.Duration(val * float64(24*time.Hour))
		default:
			return 0, fmt.Errorf("unknown unit: %s", unit)
		}
	}
	return total, nil
}
