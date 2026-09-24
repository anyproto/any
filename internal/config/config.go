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
	// Mode declares who owns this server: "standalone" (default — the
	// user; keys on disk, account resolved from disk) or "managed" (a
	// spawning host; account key supplied per boot, never on disk).
	// See mode.go.
	Mode string `yaml:"mode"`
	// Account selects which account to boot when the root holds more
	// than one. Empty = the default (root wallet.key, or the sole
	// per-account dir). Standalone only.
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
	Local   Local         `yaml:"local"`
	Access  Access        `yaml:"access"`
	WebUI   WebUI         `yaml:"webUI"`
	Log     logger.Config `yaml:"log"`
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

// Access is the alpha invite-code service (any-invite). RedeemUrl is
// the base URL its POST /redeem lives under. Empty on the embedded
// production network means ProdRedeemUrl; empty on any other network
// disables POST /v1/account/access-code (409 access.disabled).
type Access struct {
	RedeemUrl string `yaml:"redeemUrl"`
}

// P2P controls the two direct layers — local-network discovery and
// sync (mDNS + QUIC between devices on the same LAN), and the
// internet-wide one under Global. Both enabled by default; spaces
// shared with a discovered peer sync directly, including while sync
// nodes are unreachable. Surfaced in `any sync-status` (p2p /
// localPeers / globalPeers) and `any debug p2p`.
type P2P struct {
	// Enabled is the LAN layer's opt-out: absent/null = on. An explicit
	// false also suppresses Global's PACKAGED default (see
	// GlobalP2P.isEnabled), so turning peer-to-peer off with one line
	// stays one line; relays named by hand are kept.
	Enabled *bool `yaml:"enabled"`
	// Port fixes the QUIC listen port. 0 (default) = reuse the port
	// persisted from the previous run, or pick an ephemeral one.
	Port int `yaml:"port"`
	// ServiceName overrides the mDNS service type (default "_any._tcp").
	// Set it to isolate a deployment onto its own discovery namespace.
	ServiceName string `yaml:"serviceName"`
	// LocalDiscovery switches mDNS announce and browse alone; the QUIC
	// listener stays up. Absent/null resolves per platform and mode
	// (LocalDiscoveryEnabled): off for a managed server on macOS, on
	// everywhere else. Flipped at runtime through PUT /v1/local-discovery.
	LocalDiscovery *bool `yaml:"localDiscovery"`
	// Global is the internet-wide layer, independent of the LAN one.
	Global GlobalP2P `yaml:"global"`
}

// GlobalEnabled resolves whether the internet-wide layer runs, given
// this P2P block as a whole. See GlobalP2P.isEnabled.
func (p P2P) GlobalEnabled() bool {
	return p.Global.isEnabled(p.Enabled != nil && !*p.Enabled)
}

// GlobalP2P controls the internet-wide device-to-device layer: iroh
// (QUIC over a relay, with hole punching to a direct path), peers
// discovered through each space's records and through the account's own
// pkarr record. Independent of the LAN layer above — either can be off
// while the other runs.
//
// On the embedded production network the relays and the pkarr relays
// default in (p2p_global_prod.go); every other network gets the layer
// only by naming its own.
type GlobalP2P struct {
	// Enabled is an opt-out on the production network, where the
	// defaults supply relays, and an opt-in anywhere else: absent
	// resolves to on only once relayUrls are known. False always wins.
	Enabled *bool `yaml:"enabled"`
	// RelayUrls are the home-relay candidates. The device probes them
	// and keeps a session to the nearest; a relay forwards encrypted
	// QUIC and helps the two sides find a direct path.
	RelayUrls []string `yaml:"relayUrls"`
	// PkarrRelayUrls hold the account's device-discovery record — how
	// this account's own devices find each other, and how a device
	// holding only the mnemonic finds them. Empty leaves the account
	// layer off; devices then know each other only through the records
	// of the spaces they share.
	PkarrRelayUrls []string `yaml:"pkarrRelayUrls"`
	// InsecureRelay admits http:// relay URLs — plaintext to the relay,
	// for a self-hosted or local relay without a certificate. The
	// end-to-end encryption between the two devices is unaffected;
	// what leaks is which endpoints are talking. Development only.
	InsecureRelay bool `yaml:"insecureRelay"`
	// InsecurePkarr admits http:// pkarr relay URLs. Development only.
	InsecurePkarr bool `yaml:"insecurePkarr"`

	// relaysDefaulted records that RelayUrls came from the packaged
	// production values rather than from this config. Only a packaged
	// default defers to an explicit `p2p.enabled: false`; relays an
	// operator wrote are honored. Not a yaml key — set by
	// ApplyGlobalP2PDefaults.
	relaysDefaulted bool `yaml:"-"`
	// Port fixes the UDP port of the iroh endpoint. 0 (default) = reuse
	// the port persisted from the previous run, or pick an ephemeral one;
	// with the LAN layer on, its port (configured or remembered) is never
	// reused.
	Port int `yaml:"port"`
	// MaxConnections caps the global connections this device keeps
	// open, chosen to cover the loaded spaces. 0 = the SDK default (4).
	MaxConnections int `yaml:"maxConnections"`
	// MaxInbound is the headroom above MaxConnections for connections
	// other devices opened. 0 = the SDK default (8).
	MaxInbound int `yaml:"maxInbound"`
}

// IsEnabled resolves the tristate: an explicit value wins, and an
// absent one turns the layer on exactly when relays are configured —
// so the production defaults enable it and a bare custom network does
// not. The SDK refuses to start the layer without a relay, since the
// published ticket would otherwise carry this device's IP addresses
// into every space's records.
//
// isEnabled resolves the tristate for this block. lanOptOut is
// P2P.Enabled read as an explicit false.
//
// The two layers are independent by design, and `p2p.enabled: false`
// with `p2p.global.enabled: true` is a valid setup. The one case the
// opt-out reaches is relays this operator never asked for: a config
// that says only "p2p: {enabled: false}" must not acquire an iroh
// endpoint, a relay session and a dialable ticket in every space's
// records from a PACKAGED default nested under the very key it set
// false. Relays written by hand are a deliberate configuration and
// are honored — turning the LAN layer off is a common thing to want
// on a noisy datacenter network, and it must not silently discard
// them. An explicit global value still wins in both directions.
func (g GlobalP2P) isEnabled(lanOptOut bool) bool {
	if g.Enabled != nil {
		return *g.Enabled
	}
	if len(g.RelayUrls) == 0 {
		return false
	}
	return !lanOptOut || !g.relaysDefaulted
}

// IsEnabled resolves the opt-out tristate: absent/null = on.
func (p P2P) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// Local configures the local store — device-local, non-CRDT any-store
// collections in the SDK's sdk.db under the "l_" tag, served at
// /v1/local
type Local struct {
	// Enabled gates the /v1/local routes. Default true. When false the
	// routes answer 409 local.disabled and no collection is created;
	// the file is the SDK's and stays open, so existing local
	// collections sit untouched on disk.
	Enabled bool `yaml:"enabled"`
}

// Index configures the local search indexer (FTS + vector, see
// docs/13-index.md). FTS needs no external dependency; the vector side
// activates only when an embedder is configured.
type Index struct {
	// Enabled gates the whole indexer. Default true.
	Enabled bool `yaml:"enabled"`
	// Embedder selects the embedding provider: "auto" (DEFAULT — the
	// online openai primary when index.openai.apiKey is set, with the
	// local model as fallback, both the SAME model; local only when no
	// key is configured), "local" (in-process llama.cpp only), "ollama",
	// "openai" (any OpenAI-compatible /embeddings API), or "none"
	// (FTS-only). Empty resolves to the default.
	Embedder string `yaml:"embedder"`
	// EmbedBatch / EmbedConcurrency tune the embed loop. EmbedBatch is
	// docs per EmbedDocs call (0 = default 64). EmbedConcurrency is how
	// many batches embed in parallel (0 = default: 1 for local, a few for
	// online openai/auto — parallel requests are the online throughput
	// win; the local child serves one frame at a time so concurrency is
	// safe but pointless). See docs/13-index.md.
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
	// 0 = no boost AND no title field in the index: the field set is
	// decided when the index is created, so going 0 → non-zero needs a
	// rebuild (remove <data-dir>/index). Changing between non-zero values
	// is read at query time and takes effect immediately.
	TitleWeight float64 `yaml:"titleWeight"`
	// QueryEmbedTimeout bounds the query embedding of one /search (Go
	// duration string). Past it hybrid answers lexical-only
	// (vectorStatus=unavailable) and mode=vector fails as
	// index.embedder_unavailable, so a cold model load, a wedged child or
	// a slow remote API degrades a search instead of holding it. Default
	// 5s; raise it for a remote embedder that is legitimately slower.
	QueryEmbedTimeout string `yaml:"queryEmbedTimeout"`
}

type IndexOllama struct {
	Url   string `yaml:"url"`   // default http://localhost:11434
	Model string `yaml:"model"` // default embeddinggemma
}

type IndexOpenAI struct {
	BaseUrl string `yaml:"baseUrl"` // no default; required for "openai", and for "auto" once apiKey is set
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
	// NThreadsBatch) — the CPU budget the embedder child decodes with.
	// 0 = default to runtime.NumCPU()-1 (leave one core free). Going
	// beyond the physical core count can regress on hyperthreaded CPUs;
	// lowering it is how you keep background indexing off the user's
	// cores. Changeable at runtime (Indexer.SetEmbedThreads) — it takes
	// effect on the child's next spawn.
	Threads int `yaml:"threads"`
	// Niceness lowers the embedder child's scheduling priority so a
	// re-index yields to interactive work: 0..19 on Unix (nice), a
	// below-normal/idle priority class on Windows. Absent = 10; 0 keeps
	// the server's own priority. Raising priority is not supported.
	Niceness *int `yaml:"niceness"`
	// RequestTimeout bounds one frame to the child process — one decode
	// group of up to BatchDocs texts (Go duration string). Default 3m.
	// It exists to unwedge a hung GPU: a lost device stops answering
	// rather than failing, so without a bound the embed loop blocks
	// forever. Generous by design — it polices a wedge, not slow
	// hardware.
	RequestTimeout string `yaml:"requestTimeout"`
	// GpuLayers overrides llama.cpp's n_gpu_layers. Absent keeps the
	// llama.cpp default: offload every layer when a usable GPU backend
	// is present, CPU otherwise. 0 forces CPU-only inference even with
	// GPU libs installed — the opt-out for machines where the embedder
	// shouldn't take VRAM (docs/13-index.md § GPU offload).
	GpuLayers *int `yaml:"gpuLayers"`
	// BatchDocs caps how many documents pack into one llama_decode as
	// parallel sequences. The unified KV cache partitions ContextSize
	// across them, so N caps each text at ContextSize/N tokens — a wider
	// batch trades input length for a per-decode saving that measures as
	// nothing on a realistic record mix (docs/13-index.md). 0 = default
	// 1 (one doc per decode, the whole ContextSize available to it).
	BatchDocs int `yaml:"batchDocs"`
}

type IndexVector struct {
	// Dim is the embedding dimension; 0 = learn it from the first
	// successful embed batch.
	Dim int `yaml:"dim"`
	// Mode selects the ANN index strategy: "" / "ivfsq" (default —
	// cheap near-flat ingest + physical deletes, ~3–4 recall@10 below
	// exact), "btree" / "hnsw" (recall ≈ exact but serial super-linear
	// ingest + tombstone-rebuild deletes), "hybrid" (HNSW + RAM cache),
	// or "bruteforce" (exact, O(N) per query). docs/search/README.md
	// § index mode.
	Mode string `yaml:"mode"`
}

// defaultEmbedModel is the online primary's model under "auto": the same
// model the local fallback runs, spelled the way most OpenAI-compatible
// providers list it. Providers differ in naming, so index.openai.model is
// overridable — but it must still name the SAME model, or the online and
// local vectors land in one index as two incompatible spaces. No provider
// host and no key ship with the binary: index.openai.baseUrl and
// index.openai.apiKey turn the online primary on; without them "auto" is
// the local model alone.
const defaultEmbedModel = "Qwen/Qwen3-Embedding-0.6B"

// Defaults returns a Config populated with v1 defaults. Paths here are
// unexpanded — Load resolves them against the process environment.
func Defaults() Config {
	return Config{
		DataDir: "~/.any",
		Mode:    ModeStandalone,
		Listen:  Listen{Addr: "127.0.0.1:7001"},
		Auth:    Auth{PasskeyEnv: "ANY_WALLET_PASSKEY"},
		Storage: Storage{Topology: "shared"},
		WebUI:   WebUI{Enabled: true},
		Local:   Local{Enabled: true},
		Index: Index{
			Enabled:  true,
			Embedder: "auto", // online primary + local fallback (same model)
			OpenAI:   IndexOpenAI{Model: defaultEmbedModel},
		},
		Log: logger.Config{
			DefaultLevel: "info",
			Format:       logger.ColorizedOutput,
		},
	}
}
