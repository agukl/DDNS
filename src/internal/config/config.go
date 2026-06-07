package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ddns/internal/provider"
)

type Config struct {
	IntervalSeconds int                       `json:"interval_seconds"`
	StateFile       string                    `json:"state_file"`
	IPProbe         IPProbeConfig             `json:"ip_probe"`
	Records         []provider.TargetRecord   `json:"records"`
	Providers       map[string]ProviderConfig `json:"providers"`
	Logging         LoggingConfig             `json:"logging"`
	BaseDir         string                    `json:"-"`
}

type IPProbeConfig struct {
	Type           string   `json:"type"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Endpoints      []string `json:"endpoints"`
}

type ProviderConfig struct {
	APITokenEnv        string `json:"api_token_env"`
	SecretID           string `json:"secret_id"`
	SecretIDEnv        string `json:"secret_id_env"`
	SecretKey          string `json:"secret_key"`
	SecretKeyEnv       string `json:"secret_key_env"`
	AccessKeyID        string `json:"access_key_id"`
	AccessKeyIDEnv     string `json:"access_key_id_env"`
	AccessKeySecret    string `json:"access_key_secret"`
	AccessKeySecretEnv string `json:"access_key_secret_env"`
}

type LoggingConfig struct {
	Level    string `json:"level"`
	File     string `json:"file"`
	MaxLines int    `json:"max_lines"`
	MaxFiles int    `json:"max_files"`
}

var defaultEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://checkip.amazonaws.com",
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	abs, err := filepath.Abs(path)
	if err == nil {
		cfg.BaseDir = filepath.Dir(abs)
	}
	cfg.setDefaults()
	return cfg, nil
}

func (c *Config) setDefaults() {
	if c.IntervalSeconds <= 0 {
		c.IntervalSeconds = 300
	}
	if c.StateFile == "" {
		c.StateFile = ".ddns-state.json"
	}
	if c.IPProbe.Type == "" {
		c.IPProbe.Type = "ipv4"
	}
	c.IPProbe.Type = strings.ToLower(strings.TrimSpace(c.IPProbe.Type))
	if c.IPProbe.TimeoutSeconds <= 0 {
		c.IPProbe.TimeoutSeconds = 5
	}
	if len(c.IPProbe.Endpoints) == 0 {
		c.IPProbe.Endpoints = append([]string(nil), defaultEndpoints...)
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "INFO"
	}
	c.Logging.Level = strings.ToUpper(strings.TrimSpace(c.Logging.Level))
	if c.Logging.MaxLines <= 0 {
		c.Logging.MaxLines = 2000
	}
	if c.Logging.MaxFiles <= 0 {
		c.Logging.MaxFiles = 5
	}

	for i := range c.Records {
		c.Records[i].Provider = strings.ToLower(strings.TrimSpace(c.Records[i].Provider))
		c.Records[i].Zone = strings.TrimSpace(c.Records[i].Zone)
		c.Records[i].Name = strings.TrimSpace(c.Records[i].Name)
		c.Records[i].Type = strings.ToUpper(strings.TrimSpace(c.Records[i].Type))
		if c.Records[i].Type == "" {
			c.Records[i].Type = "A"
		}
		if c.Records[i].TTL <= 0 {
			c.Records[i].TTL = 300
		}
		c.Records[i].RecordLine = strings.TrimSpace(c.Records[i].RecordLine)
		c.Records[i].RecordLineID = strings.TrimSpace(c.Records[i].RecordLineID)
	}

	if c.Providers != nil {
		normalized := make(map[string]ProviderConfig, len(c.Providers))
		for name, cfg := range c.Providers {
			normalized[strings.ToLower(strings.TrimSpace(name))] = cfg
		}
		c.Providers = normalized
	}
}

func (c Config) ValidateRecords() error {
	if len(c.Records) == 0 {
		return fmt.Errorf("records must contain at least one A record")
	}
	for i, record := range c.Records {
		if record.Provider == "" {
			return fmt.Errorf("records[%d].provider is required", i)
		}
		if record.Zone == "" {
			return fmt.Errorf("records[%d].zone is required", i)
		}
		if record.Name == "" {
			return fmt.Errorf("records[%d].name is required", i)
		}
		if record.Type != "A" {
			return fmt.Errorf("records[%d].type %q is not supported yet; only A is supported", i, record.Type)
		}
	}
	return nil
}

func (c Config) ResolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) || c.BaseDir == "" {
		return path
	}
	return filepath.Join(c.BaseDir, path)
}
