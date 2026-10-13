package config

import (
	"sync"

	"gopkg.in/yaml.v3"
)

type PluginConfig struct {
	Enabled         bool   `yaml:"enabled"`
	DefaultEndpoint string `yaml:"default_endpoint"` // "ide" or "cli"
	DefaultRegion   string `yaml:"default_region"`
	ClientVersion   string `yaml:"client_version"`
	OSVersion       string `yaml:"os_version"`
	NodeVersion     string `yaml:"node_version"`
}

var defaults = PluginConfig{
	Enabled:         true,
	DefaultEndpoint: "ide",
	DefaultRegion:   "us-east-1",
	ClientVersion:   "1.4.5",
	OSVersion:       "darwin#24.6.0",
	NodeVersion:     "22.22.0",
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
	cfgLock.Lock()
	currentConfig = cfg.withDefaults()
	cfgLock.Unlock()
	return nil
}

func (c PluginConfig) withDefaults() PluginConfig {
	if c.DefaultEndpoint == "" {
		c.DefaultEndpoint = defaults.DefaultEndpoint
	}
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
	return c
}
