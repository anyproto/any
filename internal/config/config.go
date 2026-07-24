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
	Account string        `yaml:"account"`
	Listen  Listen        `yaml:"listen"`
	Auth    Auth          `yaml:"auth"`
	Network Network       `yaml:"network"`
	Storage Storage       `yaml:"storage"`
	Sync    Sync          `yaml:"sync"`
	P2P     P2P           `yaml:"p2p"`
	Index   Index         `yaml:"index"`
	Files   Files         `yaml:"files"`
	Push    Push          `yaml:"push"`
	WebUI   WebUI         `yaml:"webUI"`
	Log     logger.Config `yaml:"log"`
	// DeferWarmup makes an account-selected boot bind the listener
	// after only the SDK's fast phase (store + tech space open — local
	// reads safe); the sync warmup (per-space headsync eager load,
	// profile republish, pending-join resume, …) continues in the
	// background, observable as `warming` on GET /v1/health and
	// GET /v1/auth. False — the default — keeps the fully synchronous
	// fail-fast boot; embedded/mobile boots force it on
	// (embedded.Start), where start latency gates the host app's first
	// screen. Store/wallet corruption still fails boot synchronously
	// either way.
	DeferWarmup bool `yaml:"deferWarmup"`
}

type Listen struct {
	Addr string `yaml:"addr"`
}

// WebUI gates the embedded /ui debug harness (internal/server/web.go).
// Enabled by default so standalone `any run` is unchanged; app-embedded
// boots force it off (embedded.Start), so an in-process boot is headless
// — no /ui routes, no "web ui" advertising log line. The yaml key path
// `webUI.enabled` is a cross-repo contract: the any-swift subprocess host
// writes exactly this key into its config.yaml (IOS-116).
type WebUI struct {
	Enabled bool `yaml:"enabled"`
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

// Files tunes the SDK's file byte layer (files v2, docs/17-files.md).
// Zero values fall back to SDK defaults; an absent `files` block
// changes nothing.
type Files struct {
	// PublicReadBaseUrl overrides the network-advertised public read
	// base for durable file downloads ({base}/blob/{spaceId}/{rootCid}).
	// Normally left empty — the SDK resolves it once from the network's
	// fileV2 nodes and caches it. Set it for private deployments that
	// front the object store themselves.
	PublicReadBaseUrl string `yaml:"publicReadBaseUrl"`
	// GCInterval enables the SDK's periodic file-cache safety sweep at
	// the given cadence (duration string, e.g. "1h"). Empty/zero — the
	// default — means NO automatic sweep: reclamation is caller-driven
	// via POST /v1/files/cache/{free,sweep} and per-file offload.
	GCInterval string `yaml:"gcInterval"`
}

// Push configures the push-notification node (SYN-47). The node is a
// direct out-of-band peer — {peerId, addrs} from config, not from the
// nodeconf — that fans mobile push notifications out to the account's
// registered device tokens by topic. Threaded into the SDK's
// config.Push at OpenSDK; when inactive the SDK's PushAPI returns
// ErrPushNotConfigured and the /v1/push endpoints return 409
// push.disabled.
type Push struct {
	// Enabled is a tristate: nil (the default) means enabled only when
	// PeerId is set — configuring a push node is the opt-in. Explicit
	// false disables push even with a PeerId present; explicit true
	// without peer info still cannot run (Active() stays false).
	Enabled *bool `yaml:"enabled"`
	// PeerId is the push node's peer id (its device key identity).
	PeerId string `yaml:"peerId"`
	// Addrs are the node's dial addresses (same forms the nodeconf
	// uses, e.g. "quic://host:port" or "host:port").
	Addrs []string `yaml:"addrs"`
}

// IsEnabled resolves the tristate: an explicit value wins; nil means
// enabled iff a push node peer id is configured.
func (p Push) IsEnabled() bool {
	if p.Enabled != nil {
		return *p.Enabled
	}
	return p.PeerId != ""
}

// Active reports whether push should actually run: enabled AND the
// push node's dial info is complete. Gates both the SDK config
// threading (OpenSDK) and the internal/push service construction
// (bootEngine).
func (p Push) Active() bool {
	return p.IsEnabled() && p.PeerId != "" && len(p.Addrs) > 0
}

// P2P controls local-network discovery and sync (mDNS + QUIC between
// devices on the same LAN). Enabled by default; spaces shared with a
// discovered peer sync directly, including while sync nodes are
// unreachable. Surfaced in `any sync-status` (p2p / localPeers) and
// `any debug p2p`.
type P2P struct {
	// Enabled is an opt-out: absent/null = on.
	Enabled *bool `yaml:"enabled"`
	// Port fixes the QUIC listen port. 0 (default) = reuse the port
	// persisted from the previous run, or pick an ephemeral one.
	Port int `yaml:"port"`
	// ServiceName overrides the mDNS service type (default "_any._tcp").
	// Set it to isolate a deployment onto its own discovery namespace.
	ServiceName string `yaml:"serviceName"`
}

// Index configures the local search indexer (FTS + vector, see
// docs/13-index.md). FTS needs no external dependency; the vector side
// activates only when an embedder is configured.
type Index struct {
	// Enabled gates the whole indexer. Default true.
	Enabled bool `yaml:"enabled"`
	// Embedder selects the embedding provider: "auto" (DEFAULT — online
	// openai primary + local fallback, both the SAME model), "local"
	// (in-process llama.cpp only), "ollama", "openai" (any OpenAI-
	// compatible /embeddings API), or "none" (FTS-only). Empty resolves to
	// the default. The default "auto" primary uses the baked dev creds in
	// Defaults() (devEmbed*); override via the openai block / env.
	Embedder string `yaml:"embedder"`
	// EmbedBatch / EmbedConcurrency tune the embed loop. EmbedBatch is
	// docs per EmbedDocs call (0 = default 64). EmbedConcurrency is how
	// many batches embed in parallel (0 = default: 1 for local, a few for
	// online openai/auto — parallel requests are the online throughput
	// win; the local model serializes internally so concurrency is safe
	// but pointless). See docs/13-index.md.
	EmbedBatch       int         `yaml:"embedBatch"`
	EmbedConcurrency int         `yaml:"embedConcurrency"`
	Ollama           IndexOllama `yaml:"ollama"`
	OpenAI           IndexOpenAI `yaml:"openai"`
	Local            IndexLocal  `yaml:"local"`
	Vector           IndexVector `yaml:"vector"`
	Search           IndexSearch `yaml:"search"`
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
	// AdaptiveWeights down-weights the FTS leg per query by its score
	// concentration — so a weak lexical leg (e.g. paraphrastic queries
	// where BM25 is flat) can't drag hybrid below the dense leg. Off by
	// default. docs/search/README.md § per-corpus leg weighting.
	AdaptiveWeights bool `yaml:"adaptiveWeights"`
	// DefaultOperator controls how bare FTS terms combine: "or" (default,
	// any term) or "and" (all terms — higher precision, lower recall).
	DefaultOperator string `yaml:"defaultOperator"`
	// MinVectorSim drops vector hits below this cosine similarity before
	// fusion. Default 0 keeps the legacy floor (similarity must be > 0).
	// NOTE: measured against the default local model (Qwen3-Embedding-0.6B)
	// a static floor is a poor noise filter — gibberish queries score
	// ~0.6 cosine, on par with on-topic, so any cutoff that drops noise
	// also drops real hits (internal/indexer/live_probe_test.go). Leave
	// at 0 for that model; raise only for a better-calibrated embedder.
	MinVectorSim float64 `yaml:"minVectorSim"`
	// StopWords toggles query-side stop-word stripping for the FTS leg
	// only (the vector leg always sees the full query). nil/absent = on
	// (a precision win on a bag-of-words OR engine); set false to keep
	// the raw query.
	StopWords *bool `yaml:"stopWords"`
	// Bm25B / Bm25K1 tune the FTS index's BM25 (any-store FulltextParams).
	// 0 = the engine default (b=0.75, k1=1.2). Lower b reduces the bias
	// toward short docs (e.g. 0.4 for mixed-length notes). Set at index
	// creation — changing them needs a rebuild.
	Bm25B  float64 `yaml:"bm25B"`
	Bm25K1 float64 `yaml:"bm25K1"`
	// TitleWeight is the BM25F boost for the per-doc `title` field over
	// `body` (editor heading / program method signature / memory context).
	// 0 = the engine default (1.0 — no boost). Read at query time, so it
	// can change without a rebuild.
	TitleWeight float64 `yaml:"titleWeight"`
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
	// Threads sets the llama.cpp compute thread count (NThreads /
	// NThreadsBatch). 0 = default to runtime.NumCPU()-1 (leave one core
	// free). Going beyond the physical core count can regress on
	// hyperthreaded CPUs.
	Threads int `yaml:"threads"`
	// GpuLayers overrides llama.cpp's n_gpu_layers. Absent keeps the
	// llama.cpp default: offload every layer when a usable GPU backend
	// is present, CPU otherwise. 0 forces CPU-only inference even with
	// GPU libs installed — the opt-out for machines where the embedder
	// shouldn't take VRAM (docs/13-index.md § GPU offload).
	GpuLayers *int `yaml:"gpuLayers"`
	// BatchDocs caps how many documents pack into one llama_decode as
	// parallel sequences (also bounded by ContextSize tokens per
	// decode). Batching amortizes per-decode overhead — the main
	// embedding throughput lever, biggest on GPU backends. 0 = default
	// 16; 1 = one doc per decode (the pre-batching behavior).
	BatchDocs int `yaml:"batchDocs"`
}

type IndexVector struct {
	// Dim is the embedding dimension; 0 = probe the embedder at boot.
	Dim int `yaml:"dim"`
	// Mode selects the ANN index strategy: "" / "ivfsq" (default —
	// cheap near-flat ingest + physical deletes, ~3–4 recall@10 below
	// exact), "btree" / "hnsw" (recall ≈ exact but serial super-linear
	// ingest + tombstone-rebuild deletes), "hybrid" (HNSW + RAM cache),
	// or "bruteforce" (exact, O(N) per query). docs/search/README.md
	// § index mode.
	Mode string `yaml:"mode"`
}

// --- TEMPORARY pre-go-live embedding creds -------------------------------
//
// The default embedder is "auto": an online OpenAI-compatible primary
// (DeepInfra, serving the SAME model the local fallback runs) + the local
// model as fallback. These shared dev creds are baked in so teammates get
// fast online embedding with zero setup. ROTATE on DeepInfra and REMOVE
// this block before launch (move to real secret management). Changing the
// key is a one-line edit here.
const (
	devEmbedBaseURL = "https://api.deepinfra.com/v1/openai"
	devEmbedModel   = "Qwen/Qwen3-Embedding-0.6B" // must equal the local fallback model
	devEmbedAPIKey  = "ssb5zG4q85Eg2sxDvXbnMmFwsOtfKIxg"
)

// Defaults returns a Config populated with v1 defaults. Paths here are
// unexpanded — Load resolves them against the process environment.
func Defaults() Config {
	return Config{
		DataDir: "~/.any",
		Listen:  Listen{Addr: "127.0.0.1:7001"},
		Auth:    Auth{PasskeyEnv: "ANY_WALLET_PASSKEY"},
		Storage: Storage{Topology: "shared"},
		WebUI:   WebUI{Enabled: true},
		Index: Index{
			Enabled:  true,
			Embedder: "auto", // online primary + local fallback (same model)
			OpenAI:   IndexOpenAI{BaseUrl: devEmbedBaseURL, Model: devEmbedModel, ApiKey: devEmbedAPIKey},
		},
		Log: logger.Config{
			DefaultLevel: "info",
			Format:       logger.ColorizedOutput,
		},
	}
}
