package main

import (
	"flag"
	"fmt"
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
	listenAddr string
	serverURL  string
	interval   int
	showVer    bool
)

func main() {
	flag.StringVar(&listenAddr, "addr", ":23391", "agent metrics listen address")
	flag.StringVar(&serverURL, "server", "http://localhost:23390", "sentinel233 server URL")
	flag.IntVar(&interval, "interval", 15, "push interval in seconds")
	flag.BoolVar(&showVer, "version", false, "show version")
	flag.Parse()

	if showVer {
		fmt.Println(version.Full())
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("starting sentinel233-agent", "addr", listenAddr, "server", serverURL)

	http.HandleFunc("/metrics", handleMetrics)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	go func() {
		logger.Info("agent metrics endpoint ready", "addr", listenAddr)
		if err := http.ListenAndServe(listenAddr, nil); err != nil {
			logger.Error("agent error", "err", err)
			os.Exit(1)
		}
	}()

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
	sb.WriteString(fmt.Sprintf("process_cpu_seconds_total %f\n", time.Since(startTime).Seconds()))

	sb.WriteString("# HELP process_resident_memory_bytes Resident memory size in bytes.\n")
	sb.WriteString("# TYPE process_resident_memory_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("process_resident_memory_bytes %d\n", m.Sys))

	sb.WriteString("# HELP process_virtual_memory_bytes Virtual memory size in bytes.\n")
	sb.WriteString("# TYPE process_virtual_memory_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("process_virtual_memory_bytes %d\n", m.Sys))

	sb.WriteString("# HELP go_goroutines Number of goroutines that currently exist.\n")
	sb.WriteString("# TYPE go_goroutines gauge\n")
	sb.WriteString(fmt.Sprintf("go_goroutines %d\n", runtime.NumGoroutine()))

	sb.WriteString("# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.\n")
	sb.WriteString("# TYPE go_memstats_alloc_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("go_memstats_alloc_bytes %d\n", m.Alloc))

	sb.WriteString("# HELP go_memstats_total_alloc_bytes Total number of bytes allocated, even if freed.\n")
	sb.WriteString("# TYPE go_memstats_total_alloc_bytes counter\n")
	sb.WriteString(fmt.Sprintf("go_memstats_total_alloc_bytes %d\n", m.TotalAlloc))

	sb.WriteString("# HELP go_memstats_sys_bytes Number of bytes obtained from system.\n")
	sb.WriteString("# TYPE go_memstats_sys_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("go_memstats_sys_bytes %d\n", m.Sys))

	sb.WriteString("# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use.\n")
	sb.WriteString("# TYPE go_memstats_heap_alloc_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("go_memstats_heap_alloc_bytes %d\n", m.HeapAlloc))

	sb.WriteString("# HELP go_info Go version info.\n")
	sb.WriteString("# TYPE go_info gauge\n")
	sb.WriteString(fmt.Sprintf("go_info{version=\"%s\"} 1\n", runtime.Version()))

	sb.WriteString("# HELP up 1 if the target is up, 0 if down.\n")
	sb.WriteString("# TYPE up gauge\n")
	sb.WriteString("up 1\n")

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(sb.String()))
}
