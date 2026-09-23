package server

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/auth"
	sdkconfig "github.com/anyproto/any-sync-sdk/config"
	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/internal/bin"
	"github.com/anyproto/any/internal/chat"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/dataview"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/miniapp"
	"github.com/anyproto/any/internal/page"
)

// logConfigOnce gates Config.ApplyGlobal so the zap defaults are set
// at most once per process. ApplyGlobal mutates the
// any-sync/app/logger package globals (SetDefault, SetNamedLevels),
// which races against any background SDK goroutine still reading the
// logger after a prior boot — in the test suite, newTestDeps creates
// + tears down the SDK per test, and the streampool sendLoop's Debug
// calls from the previous boot trip the detector when the next boot
// re-applies the config.
//
// In production Run calls ApplyGlobal first; OpenSDK's call is a
// belt-and-braces no-op after the Once fires. In tests the first
// newTestDeps wins; subsequent ones leave the global alone.
var logConfigOnce sync.Once

// pinNodeconf resolves the nodeconf once at startup and pins the bytes
// inline on cfg, so every engine the process boots joins the network
// GET /v1/health reports, even if the configured file changes later.
// Returns the conf's networkId. Runs after config load, so the push
// defaults already saw whether the conf was the embedded one.
func pinNodeconf(cfg *config.Config) (string, error) {
	raw, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		return "", err
	}
	networkId, err := config.NodeconfNetworkId(raw)
	if err != nil {
		source := "embedded nodeconf"
		switch {
		case cfg.Network.Nodeconf != "":
			source = "network.nodeconf"
		case cfg.Network.NodeconfPath != "":
			source = config.ExpandTilde(cfg.Network.NodeconfPath)
		}
		return "", fmt.Errorf("%s: %w", source, err)
	}
	cfg.Network.Nodeconf = string(raw)
	return networkId, nil
}

// OpenSDK boots the SDK against the wallet provider and the project
// config, joining the network nodeconf names (resolved by the caller,
// which pins it). Storage lives under <dataDir>/sdk so the SDK's any-store and
// any-sync state are isolated from other process state in the data dir.
//
// Topology defaults to Shared; only "shared" is supported in v1
// (per-space topology is on the SDK side but not yet exercised here).
func OpenSDK(ctx context.Context, cfg config.Config, nodeconf []byte, dataDir string, provider auth.Provider) (*anysyncsdk.SDK, error) {
	topology := sdkconfig.StorageShared
	switch cfg.Storage.Topology {
	case "", "shared":
		topology = sdkconfig.StorageShared
	case "per-space":
		topology = sdkconfig.StoragePerSpace
	default:
		return nil, fmt.Errorf("unknown storage topology %q", cfg.Storage.Topology)
	}

	logConfigOnce.Do(cfg.Log.ApplyGlobal)

	localDiscovery := cfg.LocalDiscoveryEnabled()
	sdkCfg := sdkconfig.Config{
		Storage: sdkconfig.Storage{
			DataDir:  filepath.Join(dataDir, "sdk"),
			Topology: topology,
		},
		Network: sdkconfig.Network{NodeConfYAML: nodeconf},
		Sync:    sdkconfig.Sync{ChangeBatchSize: cfg.Sync.ChangeBatchSize},
		P2P: sdkconfig.P2P{
			Enabled:        cfg.P2P.Enabled,
			Port:           cfg.P2P.Port,
			ServiceName:    cfg.P2P.ServiceName,
			LocalDiscovery: &localDiscovery,
		},
		Types:       serverTypes(),
		Collections: serverCollections(),
		Modules:     serverModules(),
	}
	if cfg.Sync.DialTimeout != "" {
		d, err := time.ParseDuration(cfg.Sync.DialTimeout)
		if err != nil {
			return nil, fmt.Errorf("parse sync.dialTimeout %q: %w", cfg.Sync.DialTimeout, err)
		}
		sdkCfg.Sync.DialTimeout = d
	}
	// Files byte layer (files v2): public-read base override + the
	// opt-in periodic cache sweep. Zero GCInterval = no background GC
	// (reclamation stays caller-driven via /v1/files/cache/*).
	sdkCfg.Files.PublicReadBaseUrl = cfg.Files.PublicReadBaseUrl
	if cfg.Files.GCInterval != "" {
		d, err := time.ParseDuration(cfg.Files.GCInterval)
		if err != nil {
			return nil, fmt.Errorf("parse files.gcInterval %q: %w", cfg.Files.GCInterval, err)
		}
		sdkCfg.Files.GCInterval = d
	}
	// Push node (a direct out-of-band peer, not in the nodeconf). Only
	// threaded when active — the SDK treats an empty config.Push as
	// "not configured" and its PushAPI then returns
	// ErrPushNotConfigured, which the /v1/push handlers map to 409
	// push.disabled.
	if cfg.Push.Active() {
		sdkCfg.Push = sdkconfig.Push{PeerId: cfg.Push.PeerId, Addrs: cfg.Push.Addrs}
	}

	return anysyncsdk.Open(ctx, sdkCfg, provider)
}

// extraCatalog is the test seam for the compiled-in catalog: types and
// modules a test binary registers next to the server's own, so
// registered-type parts, hidden built-ins and reserved modules can be
// exercised over HTTP without shipping a production entry for them.
// Set from a test file's init; empty in the server binary.
var extraCatalog struct {
	types       []handler.Type
	collections []handler.Collection
	modules     []handler.Module
}

// serverTypes is the hardcoded type set this server adds on top of the
// SDK's built-ins — the types that are not modules: the saved-view
// object and the plain page. The wiki tree is a catalog usecase, not a
// built-in. Each entry registers its handler(s) with every per-object
// Controller; a registered type reports builtIn with xKey = id, which
// is what reserves the id against user types.
func serverTypes() []handler.Type {
	out := []handler.Type{
		dataview.NewType(), // hidden: dataviews + views records on a dataview object
		page.NewType(),     // hidden: one part sharing the editor's canonical collection
	}
	return append(out, extraCatalog.types...)
}

// serverCollections is the hardcoded collection set: the hidden
// markers an object opts into next to its type — the sidebar entry
// and the bin.
func serverCollections() []handler.Collection {
	out := []handler.Collection{
		miniapp.NewCollection(), // hidden: the installed bundle an object runs
		bin.NewCollection(),     // hidden: move-to-bin stamps (handlers_properties.go)
	}
	return append(out, extraCatalog.collections...)
}

// serverModules is the dataset-module set: compiled-in behaviours a
// type declares at runtime inside its parts and the SDK instantiates per
// collection — the editor's block tree and the chat message stream.
// Shared by OpenSDK and the index registry.
func serverModules() []handler.Module {
	out := []handler.Module{
		editor.NewModule(),
		chat.NewModule(),
	}
	return append(out, extraCatalog.modules...)
}

// reservedModule reports whether a module name is registered as
// reserved to the server's own installs (handler.Module.Reserved).
func reservedModule(name string) bool {
	if name == "" {
		return false
	}
	for _, m := range serverModules() {
		if m.Name == name {
			return m.Reserved
		}
	}
	return false
}

// staticDatasetNames collects every compiled-in dataset name across
// serverTypes() and the modules' canonical collections — indexed or
// not — so the schema chunker never treats a compiled-in dataset as a
// runtime records one (belt-and-braces against definitions synced from
// a peer without this server's config).
func staticDatasetNames() []string {
	var out []string
	for _, t := range serverTypes() {
		for _, ds := range t.Datasets {
			out = append(out, ds.Name)
		}
	}
	for _, m := range serverModules() {
		if m.Canonical != "" {
			out = append(out, m.Canonical)
		}
	}
	return out
}

// NewIndexRegistry builds the chunker registry — one chunker per
// indexed dataset, paralleling the Types list above. The indexer
// (internal/indexer) drives it.
//
// Indexed: editor blocks (coalesced windows), chat messages, and object
// properties (name / description under "basic"; user values default-on
// under "props", meta.index overriding — see internal/index/prop.go).
// Deliberately NOT indexed: saved views (`dataviews` / `views` — navigation
// chrome, not knowledge) have no chunker. Agent data, enrichments,
// programs and mini apps are harness-declared runtime datasets: indexed
// via the schema chunker under their declared search scope, or not at
// all when the declaration carries no `search` mapping (program source
// and mini-app HTML are code, not knowledge — anybao ADR-010 §5).
func NewIndexRegistry() *index.Registry {
	return index.NewRegistry(
		editor.NewChunker(),
		chat.NewChunker(),
		index.NewPropChunker(),
		// Runtime-defined datasets with an x-search mapping, scope
		// "basic" (internal/index/schema.go).
		index.NewSchemaChunker(staticDatasetNames()...),
	)
}

// OpenIndexer builds the search indexer: embedder (per config), local
// index store under <dataDir>/index, and the service over the chunker
// registry. modelsDir is the shared (cross-account) model cache; a
// model already downloaded to the legacy <dataDir>/index/models keeps
// being used from there. There is no boot-time embedder probe: an
// unreachable embedder never blocks the boot or the FTS pipeline —
// text-bearing docs queue as pending and the embed loop picks them up
// once the embedder responds (the dimension is learned from the first
// successful batch unless index.vector.dim pins it). A misconfigured
// embedder (bad name, missing model) is still a hard error.
// onProcess (nil = off) receives indexing lifecycle updates (fts /
// embed / model download) for the process view — see
// deps.indexerProcess.
func OpenIndexer(ctx context.Context, cfg config.Index, dataDir, modelsDir string, sdk *anysyncsdk.SDK, chunkers *index.Registry, onProcess func(indexer.ProcessUpdate), onLinks func(spaceId string, targets []string)) (*indexer.Indexer, error) {
	var queryEmbedTimeout time.Duration
	if cfg.Search.QueryEmbedTimeout != "" {
		d, err := time.ParseDuration(cfg.Search.QueryEmbedTimeout)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("index.search.queryEmbedTimeout: want a positive duration, got %q", cfg.Search.QueryEmbedTimeout)
		}
		queryEmbedTimeout = d
	}
	emb, err := indexer.NewEmbedder(cfg, modelsDir, filepath.Join(dataDir, "index", "models"), onProcess)
	if err != nil {
		return nil, err
	}
	st, err := indexer.OpenStore(ctx, filepath.Join(dataDir, "index", "index.db"), cfg.Vector.Dim, emb != nil)
	if err != nil {
		// The local embedder already runs its model download.
		if c, ok := emb.(io.Closer); ok {
			_ = c.Close()
		}
		return nil, err
	}
	st.SetVectorMode(cfg.Vector.Mode) // ANN strategy; "" = IVF-SQ default
	st.SetFTSParams(cfg.Search.Bm25B, cfg.Search.Bm25K1, cfg.Search.TitleWeight)
	// Stop-word stripping defaults on (a precision win on the bag-of-words
	// FTS engine); config may disable it. Weights/floor default to
	// pre-tuning behavior via Options.withDefaults.
	stopWords := true
	if cfg.Search.StopWords != nil {
		stopWords = *cfg.Search.StopWords
	}
	// Default embed concurrency: parallelize for an online API (the
	// throughput win), stay sequential for the local child (it serves one
	// frame at a time, so parallelism only adds goroutines).
	embedConc := cfg.EmbedConcurrency
	onlinePrimary := cfg.Embedder == "openai" || (cfg.Embedder == "auto" && cfg.OpenAI.ApiKey != "")
	if embedConc == 0 && onlinePrimary {
		embedConc = 4
	}
	ix := indexer.New(sdk, chunkers, st, indexer.Options{
		Embedder:          emb,
		EmbedBatch:        cfg.EmbedBatch,
		EmbedConcurrency:  embedConc,
		QueryEmbedTimeout: queryEmbedTimeout,
		FtsWeight:         cfg.Search.FtsWeight,
		VectorWeight:      cfg.Search.VectorWeight,
		AdaptiveWeights:   cfg.Search.AdaptiveWeights,
		FTSDefaultAnd:     strings.EqualFold(cfg.Search.DefaultOperator, "and"),
		MinVectorSim:      cfg.Search.MinVectorSim,
		StopWords:         stopWords,
		OnProcess:         onProcess,
		OnLinks:           onLinks,
	})
	// The chunk target comes back from the indexer that resolved it, so
	// the db's pin can never describe boundaries the chunker isn't using.
	// A db built on a different one is refused here (rebuild required).
	if err := st.PinChunkRunes(ctx, ix.ChunkRunes()); err != nil {
		// Closes the store and the embedder: the error tells the user to
		// remove the index dir, which Windows refuses while it is open.
		_ = ix.Close()
		return nil, err
	}
	return ix, nil
}
