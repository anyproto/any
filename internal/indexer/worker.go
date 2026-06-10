package indexer

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

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
		var ups []DocUpsert
		var dels []string
		for _, ch := range changes {
			if seen[ch.ObjectId] {
				continue
			}
			seen[ch.ObjectId] = true
			if err := w.collectObject(ctx, ch.ObjectId, cursor, &ups, &dels); err != nil {
				return err
			}
		}

		if err := w.ix.store.Apply(ctx, spaceId, ups, dels); err != nil {
			return err
		}
		cursor = changes[len(changes)-1].AddSeq
		if err := w.ix.store.SetCursor(ctx, spaceId, cursor); err != nil {
			return err
		}
		if w.ix.HasEmbedder() && hasIndexableText(ups) {
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

// collectObject gathers one dirty object's entries. Object-deleted
// check first: the SDK drops a deleted object's datasets wholesale
// (per-record tombstones never stream), so the whole object purges.
func (w *spaceWorker) collectObject(ctx context.Context, objectId string, cursor uint64, ups *[]DocUpsert, dels *[]string) error {
	deleted, err := w.objectDeleted(ctx, objectId)
	if err != nil {
		return err
	}
	if deleted {
		return w.ix.store.PurgeObject(ctx, w.sp.Id(), objectId)
	}
	for _, ch := range w.ix.reg.All() {
		err := ch.ChunksSince(ctx, w.sp, objectId, cursor, func(e index.IndexEntry) error {
			if e.Data == "" {
				*dels = append(*dels, docId(e.Dataset, e.RecordId))
			} else {
				*ups = append(*ups, DocUpsert{Entry: e})
			}
			return nil
		})
		if err != nil {
			// The object may have been deleted between the check above
			// and the dataset read (tree gone). Re-check before failing.
			if del, derr := w.objectDeleted(ctx, objectId); derr == nil && del {
				return w.ix.store.PurgeObject(ctx, w.sp.Id(), objectId)
			}
			return err
		}
	}
	return nil
}

// objectDeleted reports whether the object's shared-objects row is a
// tombstone. A missing row is not deleted — some objects only ever see
// dataset writes.
func (w *spaceWorker) objectDeleted(ctx context.Context, objectId string) (bool, error) {
	row, err := w.sp.QueryObjects().
		Projection(space.ProjectionOpts{IncludeDeleted: true}).
		Filter(map[string]any{"id": objectId}).
		One(ctx)
	if errors.Is(err, space.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return index.IsDeleted(row), nil
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
			return err
		}
		if err := w.ix.store.SetVectors(ctx, spaceId, ids, vecs); err != nil {
			return err
		}
		if _, err := w.ix.store.EnsureVectorIndex(ctx, spaceId); err != nil {
			return err
		}
	}
}
