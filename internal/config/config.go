package config

import "github.com/anyproto/any-sync/app/logger"

type Config struct {
	// DataDir is the ROOT directory (default ~/.any). Each account
	// lives in <dataDir>/<accountId>/ (wallet.key, sdk/, index/,
	// server.pid); a wallet.key directly at the root is the legacy
	// flat layout and acts as the default account, keeping its data at
	// the root. config.yaml and the shared models/ cache stay at the
	// root.
	DataDir string `yaml:"dataDir"`
	// Account selects which account to boot when the root holds more
	// than one. Empty = the default (root wallet.key, or the sole
	// per-account dir).
	Account string  `yaml:"account"`
	Listen  Listen  `yaml:"listen"`
	Auth    Auth    `yaml:"auth"`
	Network Network `yaml:"network"`
	Storage Storage `yaml:"storage"`
	Sync    Sync    `yaml:"sync"`
	Index   Index   `yaml:"index"`
	Log     logger.Config `yaml:"log"`
}

type Listen struct {
	Addr string `yaml:"addr"`
}

type Auth struct {
	WalletPath string `yaml:"walletPath"`
	PasskeyEnv string `yaml:"passkeyEnv"`
}

type Network struct {
	NodeconfPath string `yaml:"nodeconfPath"`
	Nodeconf     string `yaml:"nodeconf"`
}

type Storage struct {
	Topology string `yaml:"topology"`
}

type Sync struct {
	DialTimeout     string `yaml:"dialTimeout"`
	ChangeBatchSize int    `yaml:"changeBatchSize"`
}

// Index configures the local search indexer (FTS + vector, see
// docs/13-index.md). FTS needs no external dependency; the vector side
// activates only when an embedder is configured.
type Index struct {
	// Enabled gates the whole indexer. Default true.
	Enabled bool `yaml:"enabled"`
	// Embedder selects the embedding provider: "local" (default —
	// in-process llama.cpp, no external service), "ollama", "openai"
	// (any OpenAI-compatible /embeddings API), or "none" (FTS-only).
	// Empty means unset and resolves to the default.
	Embedder string      `yaml:"embedder"`
	Ollama   IndexOllama `yaml:"ollama"`
	OpenAI   IndexOpenAI `yaml:"openai"`
	Local    IndexLocal  `yaml:"local"`
	Vector   IndexVector `yaml:"vector"`
	Search   IndexSearch `yaml:"search"`
}

// IndexSearch tunes hybrid search ranking (chunker-hybrid-search-report
// § 5). All zero values pick safe defaults that reproduce pre-tuning
// behavior, so an absent `index.search` block changes nothing.
type IndexSearch struct {
	// FtsWeight / VectorWeight scale each leg's reciprocal-rank-fusion
	// contribution in hybrid mode. Default 1 each (plain RRF). Lowering
	// VectorWeight trusts the lexical leg more — useful while the dense
	// leg is noisy (short chunks, weak embedder).
	FtsWeight    float64 `yaml:"ftsWeight"`
	VectorWeight float64 `yaml:"vectorWeight"`
	// MinVectorSim drops vector hits below this cosine similarity before
	// fusion. Default 0 keeps the legacy floor (similarity must be > 0);
	// raise it (e.g. 0.25–0.35, measured) to cut weak-but-positive
	// neighbours that only pollute fusion when nothing real matched.
	MinVectorSim float64 `yaml:"minVectorSim"`
	// StopWords toggles query-side stop-word stripping for the FTS leg
	// only (the vector leg always sees the full query). nil/absent = on
	// (a precision win on a bag-of-words OR engine); set false to keep
	// the raw query.
	StopWords *bool `yaml:"stopWords"`
}

type IndexOllama struct {
	Url   string `yaml:"url"`   // default http://localhost:11434
	Model string `yaml:"model"` // default embeddinggemma
}

type IndexOpenAI struct {
	BaseUrl string `yaml:"baseUrl"` // default https://api.openai.com/v1
	Model   string `yaml:"model"`
	ApiKey  string `yaml:"apiKey"` // never logged
}

// IndexLocal configures the built-in llama.cpp embedder. All fields are
// optional — `embedder: local` alone is the supported zero-config path:
// Qwen3-Embedding-0.6B Q8_0 is downloaded into <data-dir>/index/models
// on first run, llama.cpp shared libs are expected next to the binary
// (`make llamacpp`).
type IndexLocal struct {
	// ModelPath points at an existing GGUF file; when set, no download
	// is attempted (air-gapped operation).
	ModelPath string `yaml:"modelPath"`
	// ModelUrl overrides the download source for the default model path.
	ModelUrl string `yaml:"modelUrl"`
	// ModelSha256 is the expected checksum of the downloaded model.
	// Empty with a custom ModelUrl skips verification (logged once).
	ModelSha256 string `yaml:"modelSha256"`
	// LibDir holds the llama.cpp shared libraries; default is the
	// `llamacpp` directory next to the executable.
	LibDir string `yaml:"libDir"`
	// ContextSize bounds embedding input in tokens — longer texts are
	// truncated (docs/13-index.md § Known limits). Default 2048.
	ContextSize int `yaml:"contextSize"`
	// QueryPrefix is prepended to query (not document) texts. Empty
	// means: the default model's retrieval instruction for the default
	// model, nothing for a custom ModelPath/ModelUrl.
	QueryPrefix string `yaml:"queryPrefix"`
	// Dim truncates output vectors (Matryoshka) and renormalizes;
	// 0 keeps the model's full dimension (1024 for the default model).
	Dim int `yaml:"dim"`
}

type IndexVector struct {
	// Dim is the embedding dimension; 0 = probe the embedder at boot.
	Dim int `yaml:"dim"`
}

// Defaults returns a Config populated with v1 defaults. Paths here are
// unexpanded — Load resolves them against the process environment.
func Defaults() Config {
	return Config{
		DataDir: "~/.any",
		Listen:  Listen{Addr: "127.0.0.1:7001"},
		Auth:    Auth{PasskeyEnv: "ANY_WALLET_PASSKEY"},
		Storage: Storage{Topology: "shared"},
		Index:   Index{Enabled: true, Embedder: "local"},
		Log: logger.Config{
			DefaultLevel: "info",
			Format:       logger.ColorizedOutput,
		},
	}
}
