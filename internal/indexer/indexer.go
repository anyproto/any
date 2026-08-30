package indexer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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
	// EmbedConcurrency is how many EmbedBatch chunks the embed loop embeds
	// in parallel per round. Default 1 (sequential — right for the local
	// child, which serves one frame at a time). Raise it for an online
	// API (openai/auto) where parallel requests are the throughput win.
	EmbedConcurrency int
	// Debounce delays an advance after a dirty signal so write bursts
	// coalesce into one page (and fuller embed batches). Default 250ms —
	// a no-op advance is sub-ms, so this dial trades only freshness.
	Debounce time.Duration
	// RetryBackoff is the wait after a failed advance. Default 5s.
	RetryBackoff time.Duration
	// PendingEvery is the embed loop's catch-up/retry tick (the nudge
	// channel covers the normal path). Default 1m.
	PendingEvery time.Duration
	// OnProcess, when set, receives indexing lifecycle updates — per
	// ProcessKind*, one started/progress*/terminal sequence per unit
	// of work (embed drain, advance pass, model download). The server
	// bridges these onto the process view (docs/22-processes.md);
	// nil = no reporting. Called from indexer goroutines — must not
	// block.
	OnProcess func(ProcessUpdate)
	// AnnounceAfter gates fts/embed reporting on elapsed work time: a
	// pass/drain announces only once it has been running this long, so
	// usual indexing (one message, one edit — done in well under a
	// second) never flashes through the view. Time, not a queue-size
	// threshold, because cost per doc varies ~50× with text length and
	// hardware (measured: local CPU embeds ~13 short chat docs/s but
	// ~2 long editor windows/s). Default 3s; negative = announce
	// immediately (tests). The model download always announces — it is
	// long by definition and its absence is the state worth showing.
	AnnounceAfter time.Duration

	// --- hybrid-search ranking knobs (chunker-hybrid-search-report § 5) ---

	// FtsWeight / VectorWeight scale each leg's RRF contribution in
	// hybrid mode. Default 1 each (plain RRF).
	FtsWeight    float64
	VectorWeight float64
	// FTSDefaultAnd makes the lexical leg require ALL query terms (AND)
	// instead of the default any-term (OR). Higher precision, lower recall
	// — off by default ($defaultOperator). Phrase/prefix and per-request
	// Require/Exclude still work regardless.
	FTSDefaultAnd bool
	// AdaptiveWeights scales the FTS leg's weight by its per-query
	// confidence (legConfidence) — auto-down-weighting BM25 when its
	// scores are flat/weak (e.g. paraphrastic corpora), so a weak lexical
	// leg can't drag hybrid below the dense leg. Vector weight is left
	// alone (cosine is uncalibrated). Off by default.
	AdaptiveWeights bool
	// ChunkRunes is the split target for long records (chunk.go): an
	// entry longer than this many runes is indexed as several chunk
	// docs. <= 0 = DefaultChunkRunes.
	ChunkRunes int
	// MinVectorSim drops vector hits below this cosine similarity before
	// fusion. Default 0 = the legacy "> 0" floor.
	MinVectorSim float64
	// StopWords strips a built-in stop list from the FTS-leg query (the
	// vector leg always gets the full query). Default off in the zero
	// Options; OpenIndexer turns it on unless config disables it.
	StopWords bool
	// QueryEmbedTimeout bounds the query embedding inside Search, for
	// every embedder: past it hybrid degrades to fts (vectorStatus
	// unavailable) and mode=vector fails as embedder-unavailable, so a
	// cold model load, a wedged child or a slow API never holds a
	// search — the local child already serves queries ahead of doc
	// frames, so in normal operation the budget is far from reached.
	// Default 5s.
	QueryEmbedTimeout time.Duration
}

// ProcessUpdate phases — each work unit reports started once, then
// progress (per landed batch / page / ~5s of download, plus a
// periodic heartbeat while an embed call runs long), then exactly one
// of done/failed/cancelled — cancelled when the owning context ends
// mid-work (space dropped, shutdown).
const (
	ProcessStarted   = "started"
	ProcessProgress  = "progress"
	ProcessDone      = "done"
	ProcessFailed    = "failed"
	ProcessCancelled = "cancelled"
)

// ProcessUpdate kinds — which pipeline the update reports on.
const (
	// ProcessKindEmbed — a per-space vector drain: Done/Total count
	// docs (Total from the pending count, re-read per round).
	ProcessKindEmbed = "embed"
	// ProcessKindFTS — a per-space chunk/advance pass: Done counts
	// processed changes, Total is unknown (the change feed has no
	// backlog count). Reported only past AnnounceAfter — routine
	// debounced advances stay silent.
	ProcessKindFTS = "fts"
	// ProcessKindModelDownload — the embedding-model fetch: Done/Total
	// are bytes (Total 0 until the server reports a length), Name the
	// model file name. Account-global, not per-space.
	ProcessKindModelDownload = "model_download"
)

// procHeartbeat paces the mid-work progress heartbeat: one embed call
// or chunker page on slow hardware can exceed the process view's
// staleness budget, and a stale row would flicker out mid-work.
// procAnnounceTick is how often a running work unit re-checks the
// AnnounceAfter gate, bounding announce latency past the threshold.
// Both live in procReporter (process_report.go), shared by every
// producer.
const (
	procHeartbeat    = 10 * time.Second
	procAnnounceTick = 500 * time.Millisecond
)

// ProcessUpdate is one Options.OnProcess report. Message carries the
// failure detail on ProcessFailed (log-grade — the server never puts
// it on the wire), empty otherwise.
type ProcessUpdate struct {
	Kind    string // ProcessKind*
	SpaceId string // per-space kinds; empty for model download
	Name    string // model file name (model download only)
	Phase   string
	Done    int64
	Total   int64 // 0 = unknown
	Message string
}

func (o Options) withDefaults() Options {
	if o.BatchLimit <= 0 {
		o.BatchLimit = 256
	}
	if o.EmbedBatch <= 0 {
		o.EmbedBatch = 64
	}
	if o.EmbedConcurrency <= 0 {
		o.EmbedConcurrency = 1
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
	if o.AnnounceAfter == 0 {
		o.AnnounceAfter = 3 * time.Second
	}
	if o.FtsWeight <= 0 {
		o.FtsWeight = 1
	}
	if o.VectorWeight <= 0 {
		o.VectorWeight = 1
	}
	if o.ChunkRunes <= 0 {
		o.ChunkRunes = DefaultChunkRunes
	}
	if o.QueryEmbedTimeout <= 0 {
		o.QueryEmbedTimeout = 5 * time.Second
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
	retries        map[string]context.CancelFunc // scheduled spawn retries, keyed by spaceId
	cancelSpaceSub func()

	// spacesAPI is a test seam overriding sdk.Spaces(); nil in production.
	spacesAPI space.Service
}

// spawn retry backoff: a Get failure at spawn is usually the first
// materialization racing the SDK's background boot pass — transient,
// but a quiescent space emits no further space-list events to
// re-trigger the spawn, so it must be retried, not dropped.
const (
	spawnRetryInitial = 2 * time.Second
	spawnRetryMax     = time.Minute
)

// spaces returns the space service — the SDK's, unless a test injected one.
func (ix *Indexer) spaces() space.Service {
	if ix.spacesAPI != nil {
		return ix.spacesAPI
	}
	return ix.sdk.Spaces()
}

// CompiledCaps reports which search legs were compiled into this binary
// via the `fts` / `vector` build tags (docs/13-index.md § build tags).
// vector is always false on gomobile builds. Both false means the search
// index, even when enabled at runtime, returns no results — callers log
// this so the state is observable rather than silently empty.
func CompiledCaps() (fts, vector bool) { return capFTS, capVector }

// New constructs the indexer. Call Start to begin; Close to stop.
func New(sdk *anysyncsdk.SDK, reg *index.Registry, store *Store, opts Options) *Indexer {
	return &Indexer{
		sdk:     sdk,
		reg:     reg,
		store:   store,
		opts:    opts.withDefaults(),
		lg:      logger.NewNamed("indexer"),
		workers: map[string]*spaceWorker{},
		retries: map[string]context.CancelFunc{},
	}
}

// HasEmbedder reports whether the vector pipeline is active.
func (ix *Indexer) HasEmbedder() bool { return ix.opts.Embedder != nil }

// EmbedHardware reports what the local embedder runs on — backends,
// devices and lib version as llama.cpp reports them — or false for an
// embedder that does not decode on this machine. Logged at child start;
// the accessor exists for hardware/error/speed statistics.
func (ix *Indexer) EmbedHardware() (Hardware, bool) {
	if h, ok := ix.opts.Embedder.(interface{ Hardware() Hardware }); ok {
		return h.Hardware(), true
	}
	return Hardware{}, false
}

// SetEmbedThreads changes the CPU budget the local embedder decodes
// with; 0 restores the default (NumCPU()-1). It takes effect when the
// embedder child next spawns, and is a no-op for embedders that don't
// decode on this machine (ollama / openai / none). This is the seam a
// settings surface plugs into — index.local.threads seeds the value.
func (ix *Indexer) SetEmbedThreads(n int) {
	if s, ok := ix.opts.Embedder.(interface{ SetThreads(int) }); ok {
		s.SetThreads(n)
	}
}

// Start lists current spaces, spawns a worker per indexable space, and
// subscribes to space-list changes for live discovery. Non-blocking.
// The passed ctx bounds all background work — cancel it (or call Close)
// to stop.
func (ix *Indexer) Start(ctx context.Context) {
	ix.ctx, ix.cancel = context.WithCancel(ctx)

	infos, err := ix.spaces().List(ix.ctx)
	if err != nil {
		ix.lg.Warn("list spaces at boot", zap.Error(err))
	}
	for _, info := range infos {
		if indexableStatus(info.Status) {
			ix.spawnWorker(info.Id)
		}
	}

	ix.mu.Lock()
	ix.cancelSpaceSub = ix.spaces().Subscribe(func(ev space.SpaceListEvent) {
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
	// Before wg.Wait, not after: an embed round in flight holds its
	// worker goroutine for as long as the embedder takes to answer, and
	// closing the embedder is what ends it (the local one kills its
	// child process). The round's docs stay pending. The local embedder
	// also owns a background download and a loaded model; the HTTP
	// embedders don't implement Closer.
	if c, ok := ix.opts.Embedder.(io.Closer); ok {
		_ = c.Close()
	}
	ix.wg.Wait()
	return ix.store.Close()
}

// spawnWorker starts the per-space goroutines unless one is already
// running. Safe to call repeatedly (Updated events re-deliver).
func (ix *Indexer) spawnWorker(spaceId string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.spawnWorkerLocked(spaceId, spawnRetryInitial)
}

// spawnWorkerLocked opens the space and starts its worker. A Get
// failure schedules a respawn after backoff (doubling, capped) —
// dropSpace and Close cancel it, and a fresh spawn request supersedes
// it (resetting the backoff).
func (ix *Indexer) spawnWorkerLocked(spaceId string, backoff time.Duration) {
	if _, ok := ix.workers[spaceId]; ok {
		ix.cancelRetryLocked(spaceId)
		return
	}
	if ix.ctx == nil || ix.ctx.Err() != nil {
		return
	}
	sp, err := ix.spaces().Get(ix.ctx, spaceId)
	if err != nil {
		ix.lg.Warn("open space for indexing",
			zap.String("spaceId", spaceId), zap.Duration("retryIn", backoff), zap.Error(err))
		ix.scheduleRetryLocked(spaceId, backoff)
		return
	}
	ix.cancelRetryLocked(spaceId)
	w := newSpaceWorker(ix, sp)
	ix.workers[spaceId] = w
	w.start()
}

// scheduleRetryLocked (re)arms the spawn retry for spaceId: wait
// backoff, then retry the spawn carrying the doubled backoff. At most
// one scheduled retry per space.
func (ix *Indexer) scheduleRetryLocked(spaceId string, backoff time.Duration) {
	ix.cancelRetryLocked(spaceId)
	ctx, cancel := context.WithCancel(ix.ctx)
	ix.retries[spaceId] = cancel
	ix.wg.Add(1)
	go func() {
		defer ix.wg.Done()
		t := time.NewTimer(backoff)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ix.mu.Lock()
		defer ix.mu.Unlock()
		// Cancelled or superseded while the timer fired / mu was contended.
		if ctx.Err() != nil {
			return
		}
		delete(ix.retries, spaceId)
		cancel() // release the timer ctx; the retry below re-arms its own
		ix.spawnWorkerLocked(spaceId, min(backoff*2, spawnRetryMax))
	}()
}

// cancelRetryLocked drops the space's scheduled spawn retry, if any.
func (ix *Indexer) cancelRetryLocked(spaceId string) {
	if cancel, ok := ix.retries[spaceId]; ok {
		cancel()
		delete(ix.retries, spaceId)
	}
}

// dropSpace stops the space's worker (or its pending spawn retry) and
// removes its index data.
func (ix *Indexer) dropSpace(spaceId string) {
	ix.mu.Lock()
	ix.cancelRetryLocked(spaceId)
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

// vectorLeg runs the ANN leg and enforces require / exclude on it:
// they are a contract on the hit, not on the leg, so vector hits are
// post-filtered against the FTS index and fusion can't re-admit a doc
// the lexical leg would have refused (SYN-187). any-store won't take
// $knn and $text in one query, so a selective term thins a fixed K —
// the leg widens K (×4, up to maxVectorFetch) until fetch survivors
// remain or the space runs out; past that the leg is genuinely
// starved.
func (ix *Indexer) vectorLeg(ctx context.Context, spaceId string, qv []float32, req api.SearchRequest, fetch int) ([]Hit, error) {
	k := fetch
	for {
		raw, err := ix.store.SearchVector(ctx, spaceId, qv, req.Scopes, k, ix.opts.MinVectorSim)
		if err != nil {
			return nil, err
		}
		if len(req.Require) == 0 && len(req.Exclude) == 0 {
			return raw, nil
		}
		kept, err := ix.store.FilterTerms(ctx, spaceId, raw, req.Require, req.Exclude)
		if err != nil {
			return nil, err
		}
		if len(kept) >= fetch || len(raw) < k || k >= maxVectorFetch {
			if len(kept) > fetch {
				kept = kept[:fetch]
			}
			return kept, nil
		}
		k = min(k*4, maxVectorFetch)
	}
}

// maxVectorFetch bounds the ANN over-fetch when require / exclude thin
// the vector leg.
const maxVectorFetch = 1000

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
		// Stop-word stripping is FTS-only: on a bag-of-words OR engine a
		// common word matches a huge fraction of docs and drags BM25
		// toward length/frequency noise. The vector leg keeps the full
		// query (below). chunker-hybrid-search-report § 5.6 / § 6.2.
		ftsQuery := req.Query
		// Don't strip inside quoted phrases — dropping a stop word would
		// break the phrase ("the big apple" → "big apple"). A query with a
		// quote bypasses stripping entirely (phrase searches are precise
		// already, so the stop-word noise argument doesn't apply).
		if ix.opts.StopWords && !strings.Contains(ftsQuery, `"`) {
			ftsQuery = stripStopWords(ftsQuery)
		}
		ftsHits, err = ix.store.SearchFTSQuery(ctx, spaceId, FTSQuery{
			Query:      ftsQuery,
			DefaultAnd: ix.opts.FTSDefaultAnd,
			Require:    req.Require,
			Exclude:    req.Exclude,
		}, req.Scopes, fetch)
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
			// Bounded: the degrade path below is only reachable if the
			// embed call returns.
			qctx, cancel := context.WithTimeout(ctx, ix.opts.QueryEmbedTimeout)
			qv, embErr := ix.opts.Embedder.EmbedQuery(qctx, req.Query)
			cancel()
			if embErr != nil {
				if err := ctx.Err(); err != nil {
					return api.SearchResponse{}, err // caller gone: nothing to degrade for
				}
				overBudget := errors.Is(embErr, context.DeadlineExceeded)
				if overBudget {
					embErr = fmt.Errorf("query embedding exceeded %s", ix.opts.QueryEmbedTimeout)
				}
				if mode == api.SearchModeVector {
					return api.SearchResponse{}, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, embErr)
				}
				if overBudget {
					// Expected while a model loads — one line per search,
					// not a warning per keystroke.
					ix.lg.Info("hybrid search degrades to fts: query embedding over budget", zap.Error(embErr))
				} else {
					ix.lg.Warn("hybrid search degrades to fts: query embedding failed", zap.Error(embErr))
				}
				mode = api.SearchModeFTS
				vectorStatus = api.VectorStatusUnavailable
			} else {
				vecHits, err = ix.vectorLeg(ctx, spaceId, qv, req, fetch)
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
		ftsW := ix.opts.FtsWeight
		if ix.opts.AdaptiveWeights {
			ftsW *= legConfidence(ftsHits) // down-weight a flat/weak BM25 leg
		}
		hits = fuseRRF([][]Hit{ftsHits, vecHits}, []float64{ftsW, ix.opts.VectorWeight}, limit)
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
		slices.SortStableFunc(hits, func(a, b Hit) int { return cmp.Compare(b.Score, a.Score) })
	}

	maxData := req.MaxData
	if maxData == 0 {
		maxData = api.DefaultSearchMaxData
	}
	terms := foldTerms(snippetTerms(req.Query, req.Require))
	out := api.SearchResponse{Hits: make([]api.SearchHit, 0, len(hits)), Mode: mode, VectorStatus: vectorStatus}
	for _, h := range hits {
		data, offset, total := snippet(h.Data, terms, maxData)
		out.Hits = append(out.Hits, api.SearchHit{
			Scope:      h.Scope,
			ObjectId:   h.ObjectId,
			Dataset:    h.Dataset,
			RecordId:   h.RecordId,
			Chunk:      h.Chunk,
			Data:       data,
			DataOffset: offset,
			DataTotal:  total,
			Score:      h.Score,
		})
	}
	return out, nil
}
