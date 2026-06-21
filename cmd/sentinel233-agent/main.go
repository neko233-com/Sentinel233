package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/neko233-com/Sentinel233/internal/version"
)

var (
	listenAddr  string
	serverURL   string
	agentID     string
	enrollToken string
	labelsText  string
	interval    int
	showVer     bool
)

const defaultEnrollmentToken = "sentinel233-agent"

func main() {
	flag.StringVar(&listenAddr, "addr", ":23391", "agent metrics listen address")
	flag.StringVar(&serverURL, "server", "http://localhost:23390", "sentinel233 server URL")
	flag.StringVar(&agentID, "id", "", "stable agent id; defaults to hostname")
	flag.StringVar(&enrollToken, "enroll-token", os.Getenv("SENTINEL233_AGENT_ENROLL_TOKEN"), "agent enrollment token")
	flag.StringVar(&labelsText, "labels", "", "comma-separated labels, for example env=prod,role=mysql")
	flag.IntVar(&interval, "interval", 15, "push interval in seconds")
	flag.BoolVar(&showVer, "version", false, "show version")
	flag.Parse()

	if showVer {
		fmt.Println(version.Full())
		return
	}
	if strings.TrimSpace(enrollToken) == "" {
		enrollToken = defaultEnrollmentToken
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("starting sentinel233-agent", "addr", listenAddr, "server", serverURL)
	hostname, _ := os.Hostname()
	if strings.TrimSpace(agentID) == "" {
		agentID = hostname
	}
	labels := parseLabels(labelsText)
	if hostname != "" && labels["hostname"] == "" {
		labels["hostname"] = hostname
	}

	http.HandleFunc("/metrics", handleMetrics)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	go func() {
		logger.Info("agent metrics endpoint ready", "addr", listenAddr)
		if err := http.ListenAndServe(listenAddr, nil); err != nil {
			logger.Error("agent error", "err", err)
			os.Exit(1)
		}
	}()

	go runControlPlane(logger, serverURL, agentID, hostname, listenAddr, enrollToken, labels, time.Duration(interval)*time.Second)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutting down agent")
}

var startTime = time.Now()

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var sb strings.Builder
	sb.WriteString("# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.\n")
	sb.WriteString("# TYPE process_cpu_seconds_total counter\n")
	fmt.Fprintf(&sb, "process_cpu_seconds_total %f\n", time.Since(startTime).Seconds())

	sb.WriteString("# HELP process_resident_memory_bytes Resident memory size in bytes.\n")
	sb.WriteString("# TYPE process_resident_memory_bytes gauge\n")
	fmt.Fprintf(&sb, "process_resident_memory_bytes %d\n", m.Sys)

	sb.WriteString("# HELP process_virtual_memory_bytes Virtual memory size in bytes.\n")
	sb.WriteString("# TYPE process_virtual_memory_bytes gauge\n")
	fmt.Fprintf(&sb, "process_virtual_memory_bytes %d\n", m.Sys)

	sb.WriteString("# HELP go_goroutines Number of goroutines that currently exist.\n")
	sb.WriteString("# TYPE go_goroutines gauge\n")
	fmt.Fprintf(&sb, "go_goroutines %d\n", runtime.NumGoroutine())

	sb.WriteString("# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.\n")
	sb.WriteString("# TYPE go_memstats_alloc_bytes gauge\n")
	fmt.Fprintf(&sb, "go_memstats_alloc_bytes %d\n", m.Alloc)

	sb.WriteString("# HELP go_memstats_total_alloc_bytes Total number of bytes allocated, even if freed.\n")
	sb.WriteString("# TYPE go_memstats_total_alloc_bytes counter\n")
	fmt.Fprintf(&sb, "go_memstats_total_alloc_bytes %d\n", m.TotalAlloc)

	sb.WriteString("# HELP go_memstats_sys_bytes Number of bytes obtained from system.\n")
	sb.WriteString("# TYPE go_memstats_sys_bytes gauge\n")
	fmt.Fprintf(&sb, "go_memstats_sys_bytes %d\n", m.Sys)

	sb.WriteString("# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use.\n")
	sb.WriteString("# TYPE go_memstats_heap_alloc_bytes gauge\n")
	fmt.Fprintf(&sb, "go_memstats_heap_alloc_bytes %d\n", m.HeapAlloc)

	sb.WriteString("# HELP go_info Go version info.\n")
	sb.WriteString("# TYPE go_info gauge\n")
	fmt.Fprintf(&sb, "go_info{version=\"%s\"} 1\n", runtime.Version())

	sb.WriteString("# HELP up 1 if the target is up, 0 if down.\n")
	sb.WriteString("# TYPE up gauge\n")
	sb.WriteString("up 1\n")

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(sb.String()))
}

func runControlPlane(logger *slog.Logger, server, id, hostname, listen, enrollment string, labels map[string]string, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	client := &http.Client{Timeout: 10 * time.Second}
	token := ""
	for {
		if token == "" {
			nextToken, err := registerAgent(client, server, id, hostname, listen, enrollment, labels)
			if err != nil {
				logger.Warn("agent registration failed", "err", err)
				time.Sleep(every)
				continue
			}
			token = nextToken
			logger.Info("agent registered", "id", id)
		}
		if err := heartbeatAgent(client, server, token, listen, labels); err != nil {
			logger.Warn("agent heartbeat failed", "err", err)
			token = ""
			time.Sleep(every)
			continue
		}
		if err := processTasks(client, server, token, listen, labels, logger); err != nil {
			logger.Warn("agent task poll failed", "err", err)
		}
		time.Sleep(every)
	}
}

func registerAgent(client *http.Client, server, id, hostname, listen, enrollment string, labels map[string]string) (string, error) {
	body := map[string]interface{}{
		"agent_id":         id,
		"name":             id,
		"hostname":         hostname,
		"version":          version.Version,
		"listen_addr":      listen,
		"enrollment_token": enrollment,
		"labels":           labels,
	}
	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := postJSON(client, server+"/api/agent/v1/register", "", body, &resp); err != nil {
		return "", err
	}
	if resp.Data.Token == "" {
		return "", fmt.Errorf("registration response did not include token")
	}
	return resp.Data.Token, nil
}

func heartbeatAgent(client *http.Client, server, token, listen string, labels map[string]string) error {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	body := map[string]interface{}{
		"version":     version.Version,
		"listen_addr": listen,
		"labels":      labels,
		"metrics": map[string]float64{
			"sentinel_agent_up":                     1,
			"sentinel_agent_goroutines":             float64(runtime.NumGoroutine()),
			"sentinel_agent_heap_alloc_bytes":       float64(m.HeapAlloc),
			"sentinel_agent_process_uptime_seconds": time.Since(startTime).Seconds(),
		},
	}
	return postJSON(client, server+"/api/agent/v1/heartbeat", token, body, nil)
}

func processTasks(client *http.Client, server, token, listen string, labels map[string]string, logger *slog.Logger) error {
	req, err := http.NewRequest(http.MethodGet, server+"/api/agent/v1/tasks", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("tasks status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data []agentTask `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	for _, task := range envelope.Data {
		logger.Info("agent task claimed", "id", task.ID, "type", task.Type)
		result, taskErr := executeTask(client, task, listen, labels)
		body := map[string]string{"result": result}
		if taskErr != nil {
			body["error"] = taskErr.Error()
		}
		if err := postJSON(client, fmt.Sprintf("%s/api/agent/v1/tasks/%d/complete", server, task.ID), token, body, nil); err != nil {
			return err
		}
	}
	return nil
}

type agentTask struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

func executeTask(client *http.Client, task agentTask, listen string, labels map[string]string) (string, error) {
	payload := map[string]string{}
	if strings.TrimSpace(task.Payload) != "" {
		_ = json.Unmarshal([]byte(task.Payload), &payload)
	}
	switch strings.TrimSpace(task.Type) {
	case "refresh_config":
		return jsonResult(map[string]interface{}{
			"version":     version.Version,
			"listen_addr": listen,
			"labels":      labels,
		})
	case "health_check":
		target := strings.TrimSpace(payload["url"])
		if target == "" {
			return "", fmt.Errorf("health_check requires payload.url")
		}
		return getURLSummary(client, target, false)
	case "scrape_once":
		target := strings.TrimSpace(payload["url"])
		if target == "" {
			return "", fmt.Errorf("scrape_once requires payload.url")
		}
		return getURLSummary(client, target, true)
	default:
		return "", fmt.Errorf("unsupported agent task type %q", task.Type)
	}
}

func getURLSummary(client *http.Client, target string, includeBody bool) (string, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result := map[string]interface{}{
		"url":         target,
		"status_code": resp.StatusCode,
		"content_len": len(data),
	}
	if includeBody {
		result["body"] = string(data)
	}
	out, err := jsonResult(result)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("GET %s returned status %d", target, resp.StatusCode)
	}
	return out, nil
}

func jsonResult(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func postJSON(client *http.Client, url, token string, payload interface{}, out interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

func parseLabels(text string) map[string]string {
	labels := make(map[string]string)
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 && strings.TrimSpace(pair[0]) != "" {
			labels[strings.TrimSpace(pair[0])] = strings.TrimSpace(pair[1])
		}
	}
	return labels
}
