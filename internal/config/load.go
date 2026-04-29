package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Flags holds the subset of command-line flags that can override config
// file and environment values. An empty string means "not set".
type Flags struct {
	ConfigPath string
	DataDir    string
	Addr       string
	WalletPath string
	LogLevel   string
}

// Load resolves the effective config by layering defaults → config file →
// environment variables → flags. A missing config file is not an error.
func Load(flags Flags) (Config, error) {
	cfg := Defaults()

	// 1. File. Use explicit --config if given, otherwise probe search
	//    paths against the (possibly flag/env-overridden) data dir.
	effectiveDataDir := cfg.DataDir
	if d := os.Getenv("ANY_DATA_DIR"); d != "" {
		effectiveDataDir = d
	}
	if flags.DataDir != "" {
		effectiveDataDir = flags.DataDir
	}

	path := flags.ConfigPath
	if path == "" {
		for _, p := range ConfigSearchPaths(ExpandTilde(effectiveDataDir)) {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	// 2. Environment.
	applyEnv(&cfg)

	// 3. Flags — highest precedence.
	applyFlags(&cfg, flags)

	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("ANY_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("ANY_LISTEN_ADDR"); v != "" {
		cfg.Listen.Addr = v
	}
	if v := os.Getenv("ANY_WALLET_PATH"); v != "" {
		cfg.Auth.WalletPath = v
	}
	if v := os.Getenv("ANY_LOG_LEVEL"); v != "" {
		cfg.Log.DefaultLevel = v
	}
}

func applyFlags(cfg *Config, f Flags) {
	if f.DataDir != "" {
		cfg.DataDir = f.DataDir
	}
	if f.Addr != "" {
		cfg.Listen.Addr = f.Addr
	}
	if f.WalletPath != "" {
		cfg.Auth.WalletPath = f.WalletPath
	}
	if f.LogLevel != "" {
		cfg.Log.DefaultLevel = f.LogLevel
	}
}
