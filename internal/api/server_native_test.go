package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/golang/snappy"

	"github.com/neko233-com/Sentinel233/internal/alert"
	"github.com/neko233-com/Sentinel233/internal/config"
	"github.com/neko233-com/Sentinel233/internal/promql"
	"github.com/neko233-com/Sentinel233/internal/scrape"
	"github.com/neko233-com/Sentinel233/internal/store"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
)

func newTestServer(t *testing.T) (*Server, *tsdb.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	db, err := tsdb.OpenDB(tsdb.DBConfig{DataDir: dir, Retention: time.Hour, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("open tsdb: %v", err)
	}

	st, err := store.Open(dir)
	if err != nil {
		db.Close()
		t.Fatalf("open store: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := promql.NewEngine(db)
	scrapeMgr := scrape.NewManager(db, cfg.Scrape, logger)
	alertMgr := alert.NewManager(db, engine, cfg.Alert, logger)
	server := NewServer(db, st, engine, scrapeMgr, alertMgr, cfg, logger)
	cleanup := func() {
		st.Close()
		db.Close()
	}
	return server, db, cleanup
}

func TestSentinelNativeWrite(t *testing.T) {
	server, db, cleanup := newTestServer(t)
	defer cleanup()

	body := []byte(`{
		"resource":{"service.name":"api","host.name":"devbox"},
		"metrics":[{
			"name":"sentinel_runtime_goroutines",
			"type":"gauge",
			"unit":"count",
			"labels":{"runtime":"go"},
			"samples":[{"timestamp":1710000000,"value":42,"labels":{"state":"running"}}]
		}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sentinel/v1/write", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("write status = %d, body = %s", rec.Code, rec.Body.String())
	}

	series := db.AllSeries()
	if len(series) != 1 {
		t.Fatalf("series count = %d, want 1", len(series))
	}
	labels := series[0].Labels
	if labels.Get("__name__") != "sentinel_runtime_goroutines" {
		t.Fatalf("__name__ label = %q", labels.Get("__name__"))
	}
	if labels.Get("source") != "sentinel_native" {
		t.Fatalf("source label = %q", labels.Get("source"))
	}
	if labels.Get("service.name") != "api" || labels.Get("runtime") != "go" || labels.Get("state") != "running" {
		t.Fatalf("labels not merged correctly: %v", labels)
	}
	samples := series[0].Samples()
	if len(samples) != 1 || samples[0].Value != 42 || samples[0].Timestamp != 1710000000000 {
		t.Fatalf("samples = %#v", samples)
	}
}

func TestPrometheusRemoteWriteSnappy(t *testing.T) {
	server, db, cleanup := newTestServer(t)
	defer cleanup()

	payload := encodeWriteRequest(
		[]remoteWriteTestLabel{
			{name: "__name__", value: "http_requests_total"},
			{name: "job", value: "api"},
			{name: "instance", value: "localhost:8080"},
		},
		[]remoteWriteTestSample{
			{value: 12.5, timestamp: 1710000000123},
			{value: 13.5, timestamp: 1710000001123},
		},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(snappy.Encode(nil, payload)))
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remote write status = %d, body = %s", rec.Code, rec.Body.String())
	}

	series := db.AllSeries()
	if len(series) != 1 {
		t.Fatalf("series count = %d, want 1", len(series))
	}
	labels := series[0].Labels
	if labels.Get("__name__") != "http_requests_total" || labels.Get("job") != "api" || labels.Get("instance") != "localhost:8080" {
		t.Fatalf("labels = %v", labels)
	}
	samples := series[0].Samples()
	if len(samples) != 2 {
		t.Fatalf("sample count = %d, want 2", len(samples))
	}
	if samples[0].Value != 12.5 || samples[0].Timestamp != 1710000000123 {
		t.Fatalf("first sample = %#v", samples[0])
	}
	if samples[1].Value != 13.5 || samples[1].Timestamp != 1710000001123 {
		t.Fatalf("second sample = %#v", samples[1])
	}
}

func TestSentinelCapabilities(t *testing.T) {
	server := NewServer(nil, nil, nil, nil, nil, config.DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/sentinel/v1/capabilities", nil)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAgentControlPlaneRegisterHeartbeatTasks(t *testing.T) {
	server, db, cleanup := newTestServer(t)
	defer cleanup()

	registerBody := []byte(`{"agent_id":"node-1","name":"node-1","hostname":"node-1","version":"test","listen_addr":":23391","labels":{"role":"linux"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agent/v1/register", bytes.NewReader(registerBody))
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("register without enrollment token status = %d, body = %s", rec.Code, rec.Body.String())
	}

	registerBody = []byte(`{"agent_id":"node-1","name":"node-1","hostname":"node-1","version":"test","listen_addr":":23391","enrollment_token":"sentinel233-agent","labels":{"role":"linux"}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/agent/v1/register", bytes.NewReader(registerBody))
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var registerResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &registerResp); err != nil {
		t.Fatal(err)
	}
	if registerResp.Data.Token == "" {
		t.Fatal("agent registration did not return token")
	}

	heartbeatBody := []byte(`{"version":"test2","listen_addr":":23391","labels":{"role":"linux"},"metrics":{"sentinel_agent_up":1}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/agent/v1/heartbeat", bytes.NewReader(heartbeatBody))
	req.Header.Set("Authorization", "Bearer "+registerResp.Data.Token)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(db.AllSeries()) == 0 {
		t.Fatal("heartbeat metrics were not written to TSDB")
	}

	token := testLoginToken(t, server)
	req = httptest.NewRequest(http.MethodPost, "/api/agents/node-1/tasks", bytes.NewReader([]byte(`{"type":"refresh_config","payload":{"reason":"test"}}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create task status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/agent/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+registerResp.Data.Token)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var tasksResp struct {
		Data []struct {
			ID     int64  `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tasksResp); err != nil {
		t.Fatal(err)
	}
	if len(tasksResp.Data) != 1 || tasksResp.Data[0].Type != "refresh_config" || tasksResp.Data[0].Status != "claimed" {
		t.Fatalf("tasks response = %#v", tasksResp.Data)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/agent/v1/tasks/"+strconv.FormatInt(tasksResp.Data[0].ID, 10)+"/complete", bytes.NewReader([]byte(`{"result":"ok"}`)))
	req.Header.Set("Authorization", "Bearer "+registerResp.Data.Token)
	rec = httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete task status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type remoteWriteTestLabel struct {
	name  string
	value string
}

type remoteWriteTestSample struct {
	value     float64
	timestamp int64
}

func encodeWriteRequest(labels []remoteWriteTestLabel, samples []remoteWriteTestSample) []byte {
	return protoBytesField(1, encodeTimeSeries(labels, samples))
}

func encodeTimeSeries(labels []remoteWriteTestLabel, samples []remoteWriteTestSample) []byte {
	var out []byte
	for _, label := range labels {
		out = append(out, protoBytesField(1, encodeLabel(label.name, label.value))...)
	}
	for _, sample := range samples {
		out = append(out, protoBytesField(2, encodeSample(sample.value, sample.timestamp))...)
	}
	return out
}

func encodeLabel(name, value string) []byte {
	var out []byte
	out = append(out, protoBytesField(1, []byte(name))...)
	out = append(out, protoBytesField(2, []byte(value))...)
	return out
}

func encodeSample(value float64, timestamp int64) []byte {
	var out []byte
	out = append(out, protoFixed64Field(1, math.Float64bits(value))...)
	out = append(out, protoVarintField(2, uint64(timestamp))...)
	return out
}

func protoBytesField(field int, value []byte) []byte {
	var out []byte
	out = append(out, protoVarint(uint64(field<<3|protoWireBytes))...)
	out = append(out, protoVarint(uint64(len(value)))...)
	out = append(out, value...)
	return out
}

func protoFixed64Field(field int, value uint64) []byte {
	var out []byte
	out = append(out, protoVarint(uint64(field<<3|protoWireFixed64))...)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], value)
	out = append(out, buf[:]...)
	return out
}

func protoVarintField(field int, value uint64) []byte {
	var out []byte
	out = append(out, protoVarint(uint64(field<<3|protoWireVarint))...)
	out = append(out, protoVarint(value)...)
	return out
}

func protoVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	out = append(out, byte(value))
	return out
}
