package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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

	// 4. Defaults that depend on the resolved layers, not just on
	//    Defaults(): the production push node rides the production
	//    network, so it can only be decided once the network is final.
	ApplyPushDefaults(&cfg)

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
	if v := os.Getenv("ANY_NETWORK_NODECONF_PATH"); v != "" {
		cfg.Network.NodeconfPath = v
	}
	if v := os.Getenv("ANY_LOG_LEVEL"); v != "" {
		cfg.Log.DefaultLevel = v
	}
	if v := os.Getenv("ANY_FILES_PUBLIC_READ_BASE_URL"); v != "" {
		cfg.Files.PublicReadBaseUrl = v
	}
	if v := os.Getenv("ANY_FILES_GC_INTERVAL"); v != "" {
		cfg.Files.GCInterval = v
	}
	if v := os.Getenv("ANY_PUSH_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Push.Enabled = &b
		}
	}
	if v := os.Getenv("ANY_PUSH_PEER_ID"); v != "" {
		cfg.Push.PeerId = v
	}
	if v := os.Getenv("ANY_PUSH_ADDRS"); v != "" {
		cfg.Push.Addrs = splitNonEmpty(v)
	}
	if v := os.Getenv("ANY_LOCAL_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Local.Enabled = b
		}
	}
	if v := os.Getenv("ANY_INDEX_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Index.Enabled = b
		}
	}
	if v := os.Getenv("ANY_INDEX_EMBEDDER"); v != "" {
		cfg.Index.Embedder = v
	}
	if v := os.Getenv("ANY_INDEX_EMBED_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Index.EmbedBatch = n
		}
	}
	if v := os.Getenv("ANY_INDEX_EMBED_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Index.EmbedConcurrency = n
		}
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
	if v := os.Getenv("ANY_INDEX_LOCAL_NICENESS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Index.Local.Niceness = &n
		}
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_REQUEST_TIMEOUT"); v != "" {
		cfg.Index.Local.RequestTimeout = v
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_GPU_LAYERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Index.Local.GpuLayers = &n
		}
	}
	if v := os.Getenv("ANY_INDEX_LOCAL_BATCH_DOCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Index.Local.BatchDocs = n
		}
	}
	if v := os.Getenv("ANY_INDEX_VECTOR_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Index.Vector.Dim = n
		}
	}
	if v := os.Getenv("ANY_INDEX_VECTOR_MODE"); v != "" {
		cfg.Index.Vector.Mode = v
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
	if v := os.Getenv("ANY_INDEX_SEARCH_BM25_B"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.Bm25B = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_BM25_K1"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.Bm25K1 = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_TITLE_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Index.Search.TitleWeight = f
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_ADAPTIVE_WEIGHTS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Index.Search.AdaptiveWeights = b
		}
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_DEFAULT_OPERATOR"); v != "" {
		cfg.Index.Search.DefaultOperator = v
	}
	if v := os.Getenv("ANY_INDEX_SEARCH_QUERY_EMBED_TIMEOUT"); v != "" {
		cfg.Index.Search.QueryEmbedTimeout = v
	}
}

// splitNonEmpty splits a comma-separated env value into trimmed,
// non-empty entries (ANY_PUSH_ADDRS).
func splitNonEmpty(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
