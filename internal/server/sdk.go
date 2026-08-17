package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/auth"
	sdkconfig "github.com/anyproto/any-sync-sdk/config"
	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/internal/agentconfig"
	"github.com/anyproto/any/internal/agentlog"
	"github.com/anyproto/any/internal/agentmem"
	"github.com/anyproto/any/internal/agentsecrets"
	"github.com/anyproto/any/internal/agenttrigger"
	"github.com/anyproto/any/internal/chat"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/enricheddata"
	"github.com/anyproto/any/internal/enrichproposal"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/miniapp"
	"github.com/anyproto/any/internal/nav"
	"github.com/anyproto/any/internal/page"
	"github.com/anyproto/any/internal/program"
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

// OpenSDK boots the SDK against the wallet provider and the project
// config. Storage lives under <dataDir>/sdk so the SDK's any-store and
// any-sync state are isolated from other process state in the data dir.
//
// Topology defaults to Shared; only "shared" is supported in v1
// (per-space topology is on the SDK side but not yet exercised here).
func OpenSDK(ctx context.Context, cfg config.Config, dataDir string, provider auth.Provider) (*anysyncsdk.SDK, error) {
	nodeconfYAML, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		return nil, err
	}

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

	sdkCfg := sdkconfig.Config{
		Storage: sdkconfig.Storage{
			DataDir:  filepath.Join(dataDir, "sdk"),
			Topology: topology,
		},
		Network: sdkconfig.Network{NodeConfYAML: nodeconfYAML},
		Sync:    sdkconfig.Sync{ChangeBatchSize: cfg.Sync.ChangeBatchSize},
		P2P:     sdkconfig.P2P{Enabled: cfg.P2P.Enabled, Port: cfg.P2P.Port, ServiceName: cfg.P2P.ServiceName},
		// Hardcoded types this server adds on top of the SDK's
		// built-ins. Each entry registers its handler(s) with every
		// per-object Controller, so writes targeting the type's
		// dataset(s) flow through the type's validation logic.
		Types: []handler.Type{
			editor.NewType(),
			chat.NewType(),
			program.NewType(),
			miniapp.NewType(),
			agentlog.NewType(),       // agent_turns + agent_chunks on the chat object
			agentmem.NewType(),       // agent_memory_items on the per-space brain object
			agenttrigger.NewType(),   // agent_triggers + agent_trigger_runs (harness triggers)
			agentconfig.NewType(),    // agent_config on the per-space config object
			agentsecrets.NewType(),   // agent_secrets on the per-space secrets object
			enricheddata.NewType(),   // enriched_data collection attached to target objects
			enrichproposal.NewType(), // enrich_proposal_items — ephemeral review plan
			nav.NewType(),            // property-only: no dataset, just nav.* schema
			page.NewType(),           // marker-only: the shared "this object is a document" type
		},
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

// NewIndexRegistry builds the chunker registry — one chunker per
// indexed dataset, paralleling the Types list above. The indexer
// (internal/indexer) drives it.
//
// Indexed: editor blocks (coalesced windows), chat messages, agent MEMORY
// items, agent HISTORY (turns + chunks, scope "history"), enriched_data
// facts (sourced enrichment knowledge), and object properties (name /
// description under "basic"; user values default-on under "props",
// meta.index overriding — see internal/index/prop.go). Deliberately NOT indexed: program
// SOURCE and its docstrings (code, not knowledge — anybao ADR-010 §5;
// discovery is help()/describe() in the guest), miniapp content, and
// enrich_proposal items (ephemeral review scaffolding, deleted on
// apply) — none has a chunker.
// enrich_proposal objects are further excluded from the property chunker
// so their names never leak into search.
func NewIndexRegistry() *index.Registry {
	return index.NewRegistry(
		editor.NewChunker(),
		chat.NewChunker(),
		agentmem.NewChunker(),
		agentlog.NewTurnChunker(),
		agentlog.NewChunkChunker(),
		enricheddata.NewChunker(), // sourced enrichment facts ARE searchable knowledge
		// enrich_proposal excluded: ephemeral review scaffolding, not knowledge.
		index.NewPropChunker(enrichproposal.TypeId),
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
// onProcess (nil = off) receives embed-drain lifecycle updates for the
// process view — see deps.indexEmbedProcess.
func OpenIndexer(ctx context.Context, cfg config.Index, dataDir, modelsDir string, sdk *anysyncsdk.SDK, chunkers *index.Registry, onProcess func(indexer.ProcessUpdate)) (*indexer.Indexer, error) {
	emb, err := indexer.NewEmbedder(cfg, modelsDir, filepath.Join(dataDir, "index", "models"))
	if err != nil {
		return nil, err
	}
	st, err := indexer.OpenStore(ctx, filepath.Join(dataDir, "index", "index.db"), cfg.Vector.Dim, emb != nil)
	if err != nil {
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
	// throughput win), stay sequential for the local model (it serializes
	// internally, so parallelism only adds goroutines).
	embedConc := cfg.EmbedConcurrency
	if embedConc == 0 && (cfg.Embedder == "openai" || cfg.Embedder == "auto") {
		embedConc = 4
	}
	return indexer.New(sdk, chunkers, st, indexer.Options{
		Embedder:         emb,
		EmbedBatch:       cfg.EmbedBatch,
		EmbedConcurrency: embedConc,
		FtsWeight:        cfg.Search.FtsWeight,
		VectorWeight:     cfg.Search.VectorWeight,
		AdaptiveWeights:  cfg.Search.AdaptiveWeights,
		FTSDefaultAnd:    strings.EqualFold(cfg.Search.DefaultOperator, "and"),
		MinVectorSim:     cfg.Search.MinVectorSim,
		StopWords:        stopWords,
		OnProcess:        onProcess,
	}), nil
}
