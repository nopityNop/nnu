package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Ping       PingConfig       `json:"ping"`
	Traceroute TracerouteConfig `json:"traceroute"`
}

type PingConfig struct {
	Count               int `json:"count"`
	TimeoutMs           int `json:"timeout_ms"`
	DelayMs             int `json:"delay_ms"`
	MaxConcurrent       int `json:"max_concurrent"`
	ConsecutiveTimeouts int `json:"consecutive_timeouts"`
}

type TracerouteConfig struct {
	MaxHops    int  `json:"max_hops"`
	IntervalMs int  `json:"interval_ms"`
	TimeoutMs  int  `json:"timeout_ms"`
	Probes     int  `json:"probes"`
	UseDNS     bool `json:"use_dns"`
}

func Default() Config {
	return Config{
		Ping: PingConfig{
			Count:               1,
			TimeoutMs:           1000,
			DelayMs:             100,
			MaxConcurrent:       10,
			ConsecutiveTimeouts: 3,
		},
		Traceroute: TracerouteConfig{
			MaxHops:    30,
			IntervalMs: 1000,
			TimeoutMs:  5000,
			Probes:     1,
			UseDNS:     true,
		},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Ping.Count < 1 {
		return fmt.Errorf("ping.count must be >= 1")
	}
	if c.Ping.TimeoutMs < 1 {
		return fmt.Errorf("ping.timeout_ms must be >= 1")
	}
	if c.Ping.DelayMs < 0 {
		return fmt.Errorf("ping.delay_ms must be >= 0")
	}
	if c.Ping.MaxConcurrent < 1 {
		return fmt.Errorf("ping.max_concurrent must be >= 1")
	}
	if c.Ping.ConsecutiveTimeouts < 1 {
		return fmt.Errorf("ping.consecutive_timeouts must be >= 1")
	}
	if c.Traceroute.MaxHops < 1 {
		return fmt.Errorf("traceroute.max_hops must be >= 1")
	}
	if c.Traceroute.IntervalMs < 1 {
		return fmt.Errorf("traceroute.interval_ms must be >= 1")
	}
	if c.Traceroute.TimeoutMs < 1 {
		return fmt.Errorf("traceroute.timeout_ms must be >= 1")
	}
	if c.Traceroute.Probes < 1 {
		return fmt.Errorf("traceroute.probes must be >= 1")
	}

	return nil
}

func (c Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
