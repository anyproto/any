package indexer

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// Options tunes the indexer. Zero values pick the defaults noted; the
// numeric defaults are measured (bench_test.go, docs/13-index.md §
// tuning), not guessed.
type Options struct {
	// Embedder is the vector pipeline's embedding client; nil = FTS-only.
	Embedder Embedder
	// BatchLimit is the ChangedSince page size, which also bounds one
	// Apply transaction. Default 256: FTS-insert throughput plateaus
	// there (~65k docs/s file-backed vs ~52k at 16) while one page
	// stays a few ms — larger pages add latency, not throughput.
	BatchLimit int
	// EmbedBatch is how many pending docs one EmbedDocs call carries.
	// Default 64: local Ollama (embeddinggemma) reaches ~97% of its max
	// throughput there (76.5 texts/s vs 78.6 at 128) at half the
	// per-call latency; vector insert (~30k vecs/s) never bottlenecks.
	EmbedBatch int
	// Debounce delays an advance after a dirty signal so write bursts
	// coalesce into one page (and fuller embed batches). Default 250ms —
	// a no-op advance is sub-ms, so this dial trades only freshness.
	Debounce time.Duration
	// RetryBackoff is the wait after a failed advance. Default 5s.
	RetryBackoff time.Duration
	// PendingEvery is the embed loop's catch-up/retry tick (the nudge
	// channel covers the normal path). Default 1m.
	PendingEvery time.Duration
}

func (o Options) withDefaults() Options {
	if o.BatchLimit <= 0 {
		o.BatchLimit = 256
	}
	if o.EmbedBatch <= 0 {
		o.EmbedBatch = 64
	}
	if o.Debounce <= 0 {
		o.Debounce = 250 * time.Millisecond
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = 5 * time.Second
	}
	if o.PendingEvery <= 0 {
		o.PendingEvery = time.Minute
	}
	return o
}

// Indexer is the phase-2 consumer of the chunker contract: per space it
// keeps a cursor over the SDK's change feed and mirrors chunker output
// into the local Store. FTS lands synchronously on the advance path;
// embedding runs on a parallel per-space loop so a slow embedder never
// delays the cursor (docs wait in `pending`).
type Indexer struct {
	sdk   *anysyncsdk.SDK
	reg   *index.Registry
	store *Store
	opts  Options
	lg    logger.CtxLogger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu             sync.Mutex
	workers        map[string]*spaceWorker
	cancelSpaceSub func()
}

// New constructs the indexer. Call Start to begin; Close to stop.
func New(sdk *anysyncsdk.SDK, reg *index.Registry, store *Store, opts Options) *Indexer {
	return &Indexer{
		sdk:     sdk,
		reg:     reg,
		store:   store,
		opts:    opts.withDefaults(),
		lg:      logger.NewNamed("indexer"),
		workers: map[string]*spaceWorker{},
	}
}

// HasEmbedder reports whether the vector pipeline is active.
func (ix *Indexer) HasEmbedder() bool { return ix.opts.Embedder != nil }

// Start lists current spaces, spawns a worker per indexable space, and
// subscribes to space-list changes for live discovery. Non-blocking.
// The passed ctx bounds all background work — cancel it (or call Close)
// to stop.
func (ix *Indexer) Start(ctx context.Context) {
	ix.ctx, ix.cancel = context.WithCancel(ctx)

	infos, err := ix.sdk.Spaces().List(ix.ctx)
	if err != nil {
		ix.lg.Warn("list spaces at boot", zap.Error(err))
	}
	for _, info := range infos {
		if indexableStatus(info.Status) {
			ix.spawnWorker(info.Id)
		}
	}

	ix.mu.Lock()
	ix.cancelSpaceSub = ix.sdk.Spaces().Subscribe(func(ev space.SpaceListEvent) {
		for _, info := range ev.Added {
			if indexableStatus(info.Status) {
				ix.spawnWorker(info.Id)
			}
		}
		for _, info := range ev.Updated {
			if indexableStatus(info.Status) {
				ix.spawnWorker(info.Id)
			} else if info.Status == space.StatusDeleted || info.Status == space.StatusRemoteDead {
				ix.dropSpace(info.Id)
			}
		}
		for _, id := range ev.Removed {
			ix.dropSpace(id)
		}
	})
	ix.mu.Unlock()
}

// indexableStatus filters which spaces get a worker: active spaces and
// unknown (not-yet-resolved) ones; joining/leaving/deleted are skipped —
// Updated events re-spawn once a space becomes active.
func indexableStatus(s space.Status) bool {
	return s == space.StatusActive || s == space.StatusUnknown
}

// Close stops every worker and closes the store. Idempotent.
func (ix *Indexer) Close() error {
	ix.mu.Lock()
	if ix.cancelSpaceSub != nil {
		ix.cancelSpaceSub()
		ix.cancelSpaceSub = nil
	}
	ix.mu.Unlock()
	if ix.cancel != nil {
		ix.cancel()
	}
	ix.wg.Wait()
	return ix.store.Close()
}

// spawnWorker starts the per-space goroutines unless one is already
// running. Safe to call repeatedly (Updated events re-deliver).
func (ix *Indexer) spawnWorker(spaceId string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.workers[spaceId]; ok {
		return
	}
	if ix.ctx == nil || ix.ctx.Err() != nil {
		return
	}
	sp, err := ix.sdk.Spaces().Get(ix.ctx, spaceId)
	if err != nil {
		ix.lg.Warn("open space for indexing", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	w := newSpaceWorker(ix, sp)
	ix.workers[spaceId] = w
	w.start()
}

// dropSpace stops the space's worker and removes its index data.
func (ix *Indexer) dropSpace(spaceId string) {
	ix.mu.Lock()
	w := ix.workers[spaceId]
	delete(ix.workers, spaceId)
	ix.mu.Unlock()
	if w != nil {
		w.stop()
	}
	// Best-effort: the server may be shutting down concurrently.
	dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ix.store.DropSpace(dropCtx, spaceId); err != nil {
		ix.lg.Warn("drop space index", zap.String("spaceId", spaceId), zap.Error(err))
	}
}

// Sync synchronously advances every known space and fully drains the
// embed queue — the deterministic test hook (workers do the same work
// asynchronously in production).
func (ix *Indexer) Sync(ctx context.Context) error {
	ix.mu.Lock()
	workers := make([]*spaceWorker, 0, len(ix.workers))
	for _, w := range ix.workers {
		workers = append(workers, w)
	}
	ix.mu.Unlock()
	for _, w := range workers {
		if err := w.advance(ctx); err != nil {
			return err
		}
		if ix.HasEmbedder() {
			if err := w.drainPending(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncSpace is Sync for a single space (used by tests that target one
// space without started workers).
func (ix *Indexer) SyncSpace(ctx context.Context, sp space.Space) error {
	w := newSpaceWorker(ix, sp)
	if err := w.advance(ctx); err != nil {
		return err
	}
	if ix.HasEmbedder() {
		return w.drainPending(ctx)
	}
	return nil
}

// Search runs the requested mode over the space's local index. The
// caller validates mode/scopes; this layer only degrades hybrid→fts
// when the embedder is missing or the query embedding fails.
func (ix *Indexer) Search(ctx context.Context, spaceId string, req api.SearchRequest) (api.SearchResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	// Per-leg over-fetch so fusion has material to work with.
	fetch := limit * 3
	if fetch < 30 {
		fetch = 30
	}
	if fetch > 100 {
		fetch = 100
	}

	mode := req.Mode
	if mode == "" {
		mode = api.SearchModeHybrid
	}

	var ftsHits, vecHits []Hit
	var err error

	// vectorStatus tells the consumer whether semantic recall took part
	// and, if not, why — an agent can decide to retry, warn, or trust
	// lexical-only results accordingly.
	vectorStatus := api.VectorStatusSkipped
	if ix.opts.Embedder == nil {
		vectorStatus = api.VectorStatusDisabled
	}

	if mode == api.SearchModeFTS || mode == api.SearchModeHybrid {
		ftsHits, err = ix.store.SearchFTS(ctx, spaceId, req.Query, req.Scopes, fetch)
		if err != nil {
			return api.SearchResponse{}, err
		}
	}
	if mode == api.SearchModeVector || mode == api.SearchModeHybrid {
		if ix.opts.Embedder == nil {
			if mode == api.SearchModeVector {
				return api.SearchResponse{}, fmt.Errorf("indexer: no embedder configured")
			}
			mode = api.SearchModeFTS // hybrid degrades
		} else {
			qv, embErr := ix.opts.Embedder.EmbedQuery(ctx, req.Query)
			if embErr != nil {
				if mode == api.SearchModeVector {
					return api.SearchResponse{}, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, embErr)
				}
				ix.lg.Warn("hybrid search degrades to fts: query embedding failed", zap.Error(embErr))
				mode = api.SearchModeFTS
				vectorStatus = api.VectorStatusUnavailable
			} else {
				vecHits, err = ix.store.SearchVector(ctx, spaceId, qv, req.Scopes, fetch)
				if err != nil {
					return api.SearchResponse{}, err
				}
				vectorStatus = api.VectorStatusUsed
			}
		}
	}

	var hits []Hit
	switch mode {
	case api.SearchModeHybrid:
		hits = fuseRRF([][]Hit{ftsHits, vecHits}, limit)
	case api.SearchModeFTS:
		hits = ftsHits
	case api.SearchModeVector:
		hits = vecHits
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	// Single-leg results are already ranked; make the contract explicit.
	if mode != api.SearchModeHybrid {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	}

	out := api.SearchResponse{Hits: make([]api.SearchHit, 0, len(hits)), Mode: mode, VectorStatus: vectorStatus}
	for _, h := range hits {
		out.Hits = append(out.Hits, api.SearchHit{
			Scope:    h.Scope,
			ObjectId: h.ObjectId,
			Dataset:  h.Dataset,
			RecordId: h.RecordId,
			Data:     h.Data,
			Score:    h.Score,
		})
	}
	return out, nil
}
