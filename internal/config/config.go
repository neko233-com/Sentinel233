package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Scrape   ScrapeConfig   `yaml:"scrape"`
	Alert    AlertConfig    `yaml:"alert"`
	Agent    AgentConfig    `yaml:"agent"`
	I18n     I18nConfig     `yaml:"i18n"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
	Port int    `yaml:"port"`
}

type StorageConfig struct {
	DataDir         string `yaml:"data_dir"`
	RetentionDays   int    `yaml:"retention_days"`
	FlushInterval   int    `yaml:"flush_interval_seconds"`
	MaxOpenFiles    int    `yaml:"max_open_files"`
	CompactionEvery int    `yaml:"compaction_every_seconds"`
}

type ScrapeConfig struct {
	Interval     int              `yaml:"interval_seconds"`
	Timeout      int              `yaml:"timeout_seconds"`
	Targets      []ScrapeTarget   `yaml:"targets"`
}

type ScrapeTarget struct {
	Name     string            `yaml:"name"`
	Endpoint string            `yaml:"endpoint"`
	Labels   map[string]string `yaml:"labels"`
}

type AlertConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Rules    []AlertRule   `yaml:"rules"`
}

type AlertRule struct {
	Name      string `yaml:"name"`
	Expr      string `yaml:"expr"`
	Duration  string `yaml:"duration"`
	Severity  string `yaml:"severity"`
	NotifyURL string `yaml:"notify_url"`
}

type AgentConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	Labels     map[string]string `yaml:"labels"`
}

type I18nConfig struct {
	Default string `yaml:"default"`
	Supported []string `yaml:"supported"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Addr: "0.0.0.0",
			Port: 23390,
		},
		Storage: StorageConfig{
			DataDir:         "./data",
			RetentionDays:   15,
			FlushInterval:   10,
			MaxOpenFiles:    1024,
			CompactionEvery: 3600,
		},
		Scrape: ScrapeConfig{
			Interval: 15,
			Timeout:  10,
		},
		Alert: AlertConfig{
			Enabled: true,
		},
		Agent: AgentConfig{
			ListenAddr: "0.0.0.0:23391",
		},
		I18n: I18nConfig{
			Default:    "zh-CN",
			Supported:  []string{"zh-CN", "en-US", "ja-JP"},
		},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
