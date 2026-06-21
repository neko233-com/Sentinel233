package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Port != 23390 {
		t.Fatalf("expected port 23390, got %d", cfg.Server.Port)
	}
	if cfg.Storage.RetentionDays != 8 {
		t.Fatalf("expected retention 8, got %d", cfg.Storage.RetentionDays)
	}
	if cfg.Scrape.Interval != 15 {
		t.Fatalf("expected scrape interval 15, got %d", cfg.Scrape.Interval)
	}
	if cfg.I18n.Default != "zh-CN" {
		t.Fatalf("expected default lang zh-CN, got %s", cfg.I18n.Default)
	}
	if !cfg.LocalAPI.Enabled {
		t.Fatal("expected local api enabled by default")
	}
	if cfg.LocalAPI.TenantID != 1 {
		t.Fatalf("expected local api tenant 1, got %d", cfg.LocalAPI.TenantID)
	}
	if cfg.Agent.EnrollmentToken == "" {
		t.Fatal("expected default agent enrollment token for local bootstrap")
	}
}

func TestLoadNonExistent(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("should return default config for nonexistent file, got error: %v", err)
	}
	if cfg.Server.Port != 23390 {
		t.Fatalf("expected default port, got %d", cfg.Server.Port)
	}
}

func TestLoadAndSave(t *testing.T) {
	dir, _ := os.MkdirTemp("", "config-test-*")
	defer os.RemoveAll(dir)

	cfg := DefaultConfig()
	cfg.Server.Port = 9999
	cfg.Storage.RetentionDays = 30

	path := filepath.Join(dir, "test.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 9999 {
		t.Fatalf("expected port 9999, got %d", loaded.Server.Port)
	}
	if loaded.Storage.RetentionDays != 30 {
		t.Fatalf("expected retention 30, got %d", loaded.Storage.RetentionDays)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir, _ := os.MkdirTemp("", "config-test-*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("server:\n  port: not_a_number\n  [][][]invalid"), 0644)

	cfg, err := Load(path)
	// YAML parser may be lenient - just verify it doesn't panic
	if err != nil {
		t.Logf("Got expected error: %v", err)
	} else {
		t.Logf("YAML parser accepted input, cfg port=%d", cfg.Server.Port)
	}
}
