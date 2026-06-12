package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server" json:"server"`
	Storage StorageConfig `yaml:"storage" json:"storage"`
	Scrape  ScrapeConfig  `yaml:"scrape" json:"scrape"`
	Alert   AlertConfig   `yaml:"alert" json:"alert"`
	Agent   AgentConfig   `yaml:"agent" json:"agent"`
	I18n    I18nConfig    `yaml:"i18n" json:"i18n"`
}

type ServerConfig struct {
	Addr string `yaml:"addr" json:"addr"`
	Port int    `yaml:"port" json:"port"`
}

type StorageConfig struct {
	DataDir         string `yaml:"data_dir" json:"data_dir"`
	RetentionDays   int    `yaml:"retention_days" json:"retention_days"`
	FlushInterval   int    `yaml:"flush_interval_seconds" json:"flush_interval_seconds"`
	MaxOpenFiles    int    `yaml:"max_open_files" json:"max_open_files"`
	CompactionEvery int    `yaml:"compaction_every_seconds" json:"compaction_every_seconds"`
}

type ScrapeConfig struct {
	Interval int            `yaml:"interval_seconds" json:"interval_seconds"`
	Timeout  int            `yaml:"timeout_seconds" json:"timeout_seconds"`
	Targets  []ScrapeTarget `yaml:"targets" json:"targets"`
}

type ScrapeTarget struct {
	Name     string            `yaml:"name" json:"name"`
	Endpoint string            `yaml:"endpoint" json:"endpoint"`
	Labels   map[string]string `yaml:"labels" json:"labels"`
}

type AlertConfig struct {
	Enabled bool        `yaml:"enabled" json:"enabled"`
	Rules   []AlertRule `yaml:"rules" json:"rules"`
}

type AlertRule struct {
	Name      string `yaml:"name" json:"name"`
	Expr      string `yaml:"expr" json:"expr"`
	Duration  string `yaml:"duration" json:"duration"`
	Severity  string `yaml:"severity" json:"severity"`
	NotifyURL string `yaml:"notify_url" json:"notify_url"`
}

type AgentConfig struct {
	ListenAddr string            `yaml:"listen_addr" json:"listen_addr"`
	Labels     map[string]string `yaml:"labels" json:"labels"`
}

type I18nConfig struct {
	Default   string   `yaml:"default" json:"default"`
	Supported []string `yaml:"supported" json:"supported"`
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
			Default:   "zh-CN",
			Supported: []string{"zh-CN", "en-US", "ja-JP"},
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
