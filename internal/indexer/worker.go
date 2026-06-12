package indexer

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// spaceWorker drives one space's two pipelines:
//   - advance loop (FTS path): change feed → chunkers → store, cursor-
//     driven. Near-instantaneous — never calls the embedder.
//   - embed loop (vector path): drains `pending` docs in batches. Runs
//     in parallel so embedder latency can't delay the cursor.
type spaceWorker struct {
	ix *Indexer
	sp space.Space

	// dirty coalesces change notifications (cap 1, non-blocking sends):
	// the subscribe callback runs synchronously on the SDK apply path
	// and must not block. Lost signals are harmless — advance is
	// cursor-driven, the next signal or boot align catches up.
	dirty chan struct{}
	// embedCh nudges the embed loop after an advance landed pending
	// docs; the PendingEvery ticker is the retry/catch-up path.
	embedCh chan struct{}

	ctx       context.Context
	cancel    context.CancelFunc
	cancelSub func()
}

func newSpaceWorker(ix *Indexer, sp space.Space) *spaceWorker {
	return &spaceWorker{
		ix:      ix,
		sp:      sp,
		dirty:   make(chan struct{}, 1),
		embedCh: make(chan struct{}, 1),
	}
}

func (w *spaceWorker) start() {
	w.ctx, w.cancel = context.WithCancel(w.ix.ctx)
	w.cancelSub = w.sp.Changes().Subscribe(func(space.ObjectChange) {
		select {
		case w.dirty <- struct{}{}:
		default:
		}
	})
	w.ix.wg.Add(1)
	go w.advanceLoop()
	if w.ix.HasEmbedder() {
		w.ix.wg.Add(1)
		go w.embedLoop()
	}
}

func (w *spaceWorker) stop() {
	if w.cancelSub != nil {
		w.cancelSub()
	}
	if w.cancel != nil {
		w.cancel()
	}
}

// advanceLoop: cold align at spawn, then debounced advances on dirty
// signals, with backoff-retry on failure (the cursor only moves past
// applied batches, so a retry resumes exactly where it failed).
func (w *spaceWorker) advanceLoop() {
	defer w.ix.wg.Done()
	w.runAdvance()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.dirty:
		}
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.ix.opts.Debounce):
		}
		// Coalesce signals that arrived during the debounce window.
		select {
		case <-w.dirty:
		default:
		}
		w.runAdvance()
	}
}

func (w *spaceWorker) runAdvance() {
	for {
		err := w.advance(w.ctx)
		if err == nil || w.ctx.Err() != nil {
			return
		}
		w.ix.lg.Warn("advance failed, retrying", zap.String("spaceId", w.sp.Id()), zap.Error(err))
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.ix.opts.RetryBackoff):
		}
	}
}

// embedLoop drains pending docs on nudges from advance and on the
// periodic tick (which also retries after embedder failures).
func (w *spaceWorker) embedLoop() {
	defer w.ix.wg.Done()
	ticker := time.NewTicker(w.ix.opts.PendingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.embedCh:
		case <-ticker.C:
		}
		if err := w.drainPending(w.ctx); err != nil && w.ctx.Err() == nil {
			w.ix.lg.Warn("embed pending failed, will retry on tick", zap.String("spaceId", w.sp.Id()), zap.Error(err))
		}
	}
}

// advance is the single cursor-driven operation: stream everything past
// the cursor through the chunkers and land it in the store, page by
// page. Never touches the embedder — text-bearing docs land as pending.
func (w *spaceWorker) advance(ctx context.Context) error {
	spaceId := w.sp.Id()
	cursor, err := w.ix.store.Cursor(ctx, spaceId)
	if err != nil {
		return err
	}
	for {
		changes, err := w.sp.Changes().ChangedSince(ctx, cursor, w.ix.opts.BatchLimit)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}

		// Dedup object ids; ChangedSince is ascending, so the last
		// element carries the page's max AddSeq.
		seen := map[string]bool{}
		var page pageOps
		for _, ch := range changes {
			if seen[ch.ObjectId] {
				continue
			}
			seen[ch.ObjectId] = true
			if err := w.collectObject(ctx, ch.ObjectId, cursor, &page); err != nil {
				return err
			}
		}

		if err := w.ix.store.Apply(ctx, spaceId, page.ups, page.dels, page.prefixDels); err != nil {
			return err
		}
		cursor = changes[len(changes)-1].AddSeq
		if err := w.ix.store.SetCursor(ctx, spaceId, cursor); err != nil {
			return err
		}
		if w.ix.HasEmbedder() && hasIndexableText(page.ups) {
			select {
			case w.embedCh <- struct{}{}:
			default:
			}
		}
		if len(changes) < w.ix.opts.BatchLimit {
			return nil
		}
	}
}

func hasIndexableText(ups []DocUpsert) bool {
	for _, up := range ups {
		if up.Vector == nil && up.Entry.Data != "" {
			return true
		}
	}
	return false
}

// pageOps accumulates one advance page: upserts, record-level deletes
// (full doc ids), and structural prefix deletes (':'-terminated id
// prefixes — whole object or whole objectId+dataset). All applied in
// one transaction, so eviction rides the same addSeq window as content.
type pageOps struct {
	ups        []DocUpsert
	dels       []string
	prefixDels []string
}

// collectObject gathers one dirty object's page ops, derived from the
// shared objects row inside the same ChangedSince window:
//   - tombstoned row → whole-object prefix delete (the SDK drops a
//     deleted object's datasets wholesale; per-record removals never
//     stream for them);
//   - gated chunkers whose type is not in any.types → dataset prefix
//     delete (covers DetachType; idempotent — one btree seek when
//     already empty);
//   - everything else → the chunkers' entries.
func (w *spaceWorker) collectObject(ctx context.Context, objectId string, cursor uint64, page *pageOps) error {
	row, err := w.objectRow(ctx, objectId)
	if err != nil {
		return err
	}
	if row != nil && index.IsDeleted(row) {
		page.prefixDels = append(page.prefixDels, objectId+":")
		return nil
	}
	attached := typeSet(row)
	for _, ch := range w.ix.reg.All() {
		if tid := ch.TypeId(); tid != "" && !attached[tid] {
			page.prefixDels = append(page.prefixDels, objectId+":"+ch.Dataset()+":")
			continue
		}
		err := ch.ChunksSince(ctx, w.sp, objectId, cursor, func(e index.IndexEntry) error {
			if e.Data == "" {
				page.dels = append(page.dels, docId(e.ObjectId, e.Dataset, e.RecordId))
			} else {
				page.ups = append(page.ups, DocUpsert{Entry: e})
			}
			return nil
		})
		if err != nil {
			// The object may have been deleted between the row read and
			// the dataset read (tree gone). Re-check before failing.
			if row2, derr := w.objectRow(ctx, objectId); derr == nil && row2 != nil && index.IsDeleted(row2) {
				page.prefixDels = append(page.prefixDels, objectId+":")
				return nil
			}
			return err
		}
	}
	return nil
}

// objectRow returns the object's shared-objects row, tombstones
// included, or nil when the object has no row (some objects only ever
// see dataset writes).
func (w *spaceWorker) objectRow(ctx context.Context, objectId string) (*anyenc.Value, error) {
	row, err := w.sp.QueryObjects().
		Projection(space.ProjectionOpts{IncludeDeleted: true}).
		Filter(query.Key{Path: idPath, Filter: query.NewComp(query.CompOpEq, objectId)}).
		One(ctx)
	if errors.Is(err, space.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// typeSet extracts any.types into a membership set. Empty for nil rows.
func typeSet(row *anyenc.Value) map[string]bool {
	if row == nil {
		return nil
	}
	types := row.GetArray("any", "types")
	if len(types) == 0 {
		return nil
	}
	out := make(map[string]bool, len(types))
	for _, v := range types {
		out[string(v.GetStringBytes())] = true
	}
	return out
}

// drainPending embeds and lands pending docs batch by batch until the
// queue is empty. The vector index is created lazily after the first
// batch (IVF trains from existing docs).
func (w *spaceWorker) drainPending(ctx context.Context) error {
	spaceId := w.sp.Id()
	for {
		ids, texts, err := w.ix.store.Pending(ctx, spaceId, w.ix.opts.EmbedBatch)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		vecs, err := w.ix.opts.Embedder.EmbedDocs(ctx, texts)
		if err != nil {
			// Embedder down: docs stay pending, the ticker retries —
			// the vector pipeline freezes, FTS is unaffected.
			return err
		}
		if len(vecs) > 0 && len(vecs[0]) > 0 {
			// First successful batch teaches the store its dimension
			// (no boot-time probe — an embedder that was down at boot
			// just starts working here once reachable).
			if err := w.ix.store.EnsureDim(ctx, len(vecs[0])); err != nil {
				return err
			}
		}
		if err := w.ix.store.SetVectors(ctx, spaceId, ids, vecs); err != nil {
			return err
		}
		if _, err := w.ix.store.EnsureVectorIndex(ctx, spaceId); err != nil {
			return err
		}
	}
}
