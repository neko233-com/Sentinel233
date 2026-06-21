package serverapp

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/neko233-com/Sentinel233/internal/alert"
	"github.com/neko233-com/Sentinel233/internal/api"
	"github.com/neko233-com/Sentinel233/internal/config"
	"github.com/neko233-com/Sentinel233/internal/promql"
	"github.com/neko233-com/Sentinel233/internal/scrape"
	"github.com/neko233-com/Sentinel233/internal/store"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
	"github.com/neko233-com/Sentinel233/internal/version"
)

func Run(args []string) {
	var (
		configPath string
		addr       string
		dataDir    string
		showVer    bool
	)

	fs := flag.NewFlagSet("sentinel233-server", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.StringVar(&addr, "addr", ":23390", "listen address")
	fs.StringVar(&dataDir, "data", "./data", "data directory")
	fs.BoolVar(&showVer, "version", false, "show version")
	fs.Parse(args)

	if showVer {
		fmt.Println(version.Full())
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	if addr != ":23390" {
		cfg.Server.Port = 0
		cfg.Server.Addr = addr
	}

	logger.Info("starting sentinel233-server", "version", version.Version, "addr", addr, "data", dataDir)

	st, err := store.Open(dataDir)
	if err != nil {
		logger.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := store.CreateDefaultTenant(st); err != nil {
		logger.Error("create default tenant failed", "err", err)
	}
	if err := store.CreateDefaultRoot(st); err != nil {
		logger.Error("create default root failed", "err", err)
	}
	if runtimeConfig, err := st.GetSetting(1, "runtime_config"); err == nil && runtimeConfig != "" {
		if err := json.Unmarshal([]byte(runtimeConfig), cfg); err != nil {
			logger.Warn("ignore invalid persisted runtime config", "err", err)
		} else {
			logger.Info("loaded persisted runtime config", "retention_days", cfg.Storage.RetentionDays)
		}
	}

	db, err := tsdb.OpenDB(tsdb.DBConfig{
		DataDir:       dataDir,
		Retention:     time.Duration(cfg.Storage.RetentionDays) * 24 * time.Hour,
		FlushInterval: time.Duration(cfg.Storage.FlushInterval) * time.Second,
	})
	if err != nil {
		logger.Error("open tsdb failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	engine := promql.NewEngine(db)
	scrapeMgr := scrape.NewManager(db, cfg.Scrape, logger)
	scrapeMgr.Start()
	defer scrapeMgr.Stop()

	alertMgr := alert.NewManager(db, engine, cfg.Alert, logger)
	alertMgr.Start()
	defer alertMgr.Stop()

	srv := api.NewServer(db, st, engine, scrapeMgr, alertMgr, cfg, logger)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		logger.Info("sentinel233-server ready", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutting down sentinel233-server")
}
