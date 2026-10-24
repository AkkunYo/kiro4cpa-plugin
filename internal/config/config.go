package config

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type PluginConfig struct {
	Enabled       bool   `yaml:"enabled"` // read by the CPA host itself
	DefaultRegion string `yaml:"default_region"`
	ClientVersion string `yaml:"client_version"`
	OSVersion     string `yaml:"os_version"`
	NodeVersion   string `yaml:"node_version"`
	// Per-credential pacing. Parsed by Durations; empty keeps the default.
	MinTokenInterval string `yaml:"min_token_interval"`
	MaxTokenInterval string `yaml:"max_token_interval"`
	SuspendCooldown  string `yaml:"suspend_cooldown"`
}

var defaults = PluginConfig{
	Enabled:          true,
	DefaultRegion:    "us-east-1",
	ClientVersion:    "1.4.5",
	OSVersion:        "darwin#24.6.0",
	NodeVersion:      "22.22.0",
	MinTokenInterval: "1s",
	MaxTokenInterval: "2s",
	SuspendCooldown:  "1h",
}

var (
	currentConfig = defaults
	cfgLock       sync.RWMutex
)

func Get() PluginConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return currentConfig
}

// UpdateFromYAML parses the plugin's own config block. CPA passes only
// plugins.configs.<plugin-id>, not the root config file.
func UpdateFromYAML(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	cfg := defaults
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	cfg = cfg.withDefaults()
	// A rejected block keeps the previous configuration; failing here surfaces
	// the typo in CPA's log instead of silently pacing with something else.
	if _, err := cfg.Durations(); err != nil {
		return err
	}
	cfgLock.Lock()
	currentConfig = cfg
	cfgLock.Unlock()
	return nil
}

func (c PluginConfig) withDefaults() PluginConfig {
	if c.DefaultRegion == "" {
		c.DefaultRegion = defaults.DefaultRegion
	}
	if c.ClientVersion == "" {
		c.ClientVersion = defaults.ClientVersion
	}
	if c.OSVersion == "" {
		c.OSVersion = defaults.OSVersion
	}
	if c.NodeVersion == "" {
		c.NodeVersion = defaults.NodeVersion
	}
	if c.MinTokenInterval == "" {
		c.MinTokenInterval = defaults.MinTokenInterval
	}
	if c.MaxTokenInterval == "" {
		c.MaxTokenInterval = defaults.MaxTokenInterval
	}
	if c.SuspendCooldown == "" {
		c.SuspendCooldown = defaults.SuspendCooldown
	}
	return c
}

// Pacing carries the parsed per-credential limits.
type Pacing struct {
	MinTokenInterval time.Duration
	MaxTokenInterval time.Duration
	SuspendCooldown  time.Duration
}

// Durations parses the pacing settings, reporting the first invalid value.
func (c PluginConfig) Durations() (Pacing, error) {
	minInterval, err := time.ParseDuration(c.MinTokenInterval)
	if err != nil {
		return Pacing{}, fmt.Errorf("invalid min_token_interval %q: %w", c.MinTokenInterval, err)
	}
	if minInterval <= 0 {
		return Pacing{}, fmt.Errorf("min_token_interval must be positive, got %q", c.MinTokenInterval)
	}
	maxInterval, err := time.ParseDuration(c.MaxTokenInterval)
	if err != nil {
		return Pacing{}, fmt.Errorf("invalid max_token_interval %q: %w", c.MaxTokenInterval, err)
	}
	if maxInterval < minInterval {
		return Pacing{}, fmt.Errorf("max_token_interval %q is shorter than min_token_interval %q", c.MaxTokenInterval, c.MinTokenInterval)
	}
	cooldown, err := time.ParseDuration(c.SuspendCooldown)
	if err != nil {
		return Pacing{}, fmt.Errorf("invalid suspend_cooldown %q: %w", c.SuspendCooldown, err)
	}
	if cooldown <= 0 {
		return Pacing{}, fmt.Errorf("suspend_cooldown must be positive, got %q", c.SuspendCooldown)
	}
	return Pacing{MinTokenInterval: minInterval, MaxTokenInterval: maxInterval, SuspendCooldown: cooldown}, nil
}
