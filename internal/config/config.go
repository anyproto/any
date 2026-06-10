package config

import "github.com/anyproto/any-sync/app/logger"

type Config struct {
	DataDir string  `yaml:"dataDir"`
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
// docs/11-index.md). FTS needs no external dependency; the vector side
// activates only when an embedder is configured.
type Index struct {
	// Enabled gates the whole indexer. Default true (FTS-only with no
	// embedder configured).
	Enabled bool `yaml:"enabled"`
	// Embedder selects the embedding provider: "" (none — FTS-only),
	// "ollama", or "openai" (any OpenAI-compatible /embeddings API).
	Embedder string      `yaml:"embedder"`
	Ollama   IndexOllama `yaml:"ollama"`
	OpenAI   IndexOpenAI `yaml:"openai"`
	Vector   IndexVector `yaml:"vector"`
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
		Index:   Index{Enabled: true},
		Log: logger.Config{
			DefaultLevel: "info",
			Format:       logger.ColorizedOutput,
		},
	}
}
