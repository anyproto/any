package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Flags holds the subset of command-line flags that can override config
// file and environment values. An empty string means "not set".
type Flags struct {
	ConfigPath string
	DataDir    string
	Account    string
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
	if v := os.Getenv("ANY_ACCOUNT"); v != "" {
		cfg.Account = v
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
	if v := os.Getenv("ANY_INDEX_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Index.Enabled = b
		}
	}
	if v := os.Getenv("ANY_INDEX_EMBEDDER"); v != "" {
		cfg.Index.Embedder = v
	}
	if v := os.Getenv("ANY_INDEX_OLLAMA_URL"); v != "" {
		cfg.Index.Ollama.Url = v
	}
	if v := os.Getenv("ANY_INDEX_OLLAMA_MODEL"); v != "" {
		cfg.Index.Ollama.Model = v
	}
	if v := os.Getenv("ANY_INDEX_OPENAI_BASE_URL"); v != "" {
		cfg.Index.OpenAI.BaseUrl = v
	}
	if v := os.Getenv("ANY_INDEX_OPENAI_MODEL"); v != "" {
		cfg.Index.OpenAI.Model = v
	}
	if v := os.Getenv("ANY_INDEX_OPENAI_API_KEY"); v != "" {
		cfg.Index.OpenAI.ApiKey = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_MODEL_PATH"); v != "" {
		cfg.Index.Local.ModelPath = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_MODEL_URL"); v != "" {
		cfg.Index.Local.ModelUrl = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_MODEL_SHA256"); v != "" {
		cfg.Index.Local.ModelSha256 = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_LIB_DIR"); v != "" {
		cfg.Index.Local.LibDir = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_QUERY_PREFIX"); v != "" {
		cfg.Index.Local.QueryPrefix = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_CONTEXT_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Index.Local.ContextSize = n
		}
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Index.Local.Dim = n
		}
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_THREADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Index.Local.Threads = n
		}
	}
	if v := os.Getenv("ANY_INDEX_VECTOR_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Index.Vector.Dim = n
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_FTS_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.FtsWeight = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_VECTOR_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.VectorWeight = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_MIN_VECTOR_SIM"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.MinVectorSim = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_STOP_WORDS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Index.Search.StopWords = &b
		}
	}
}

func applyFlags(cfg *Config, f Flags) {
	if f.DataDir != "" {
		cfg.DataDir = f.DataDir
	}
	if f.Account != "" {
		cfg.Account = f.Account
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
