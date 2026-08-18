package indexer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// advance wraps advancePages with fts-process reporting
// (Options.OnProcess, nil = off), gated on elapsed work like the
// embed drain: a pass announces at the first page boundary past
// AnnounceAfter — routine debounced advances (one edit, a small sync
// burst; milliseconds) stay silent, a cold (re)index or large
// catch-up shows up. Checked at page boundaries only (pages are
// frequent); Total is unknown — the change feed has no backlog count.
func (w *spaceWorker) advance(ctx context.Context) error {
	if w.ix.opts.OnProcess == nil {
		return w.advancePages(ctx, func(int) {})
	}
	start := time.Now()
	var processed int64
	announced := false
	report := func(phase, msg string) {
		w.ix.opts.OnProcess(ProcessUpdate{Kind: ProcessKindFTS, SpaceId: w.sp.Id(),
			Phase: phase, Done: processed, Message: msg})
	}
	err := w.advancePages(ctx, func(n int) {
		processed += int64(n)
		if !announced {
			if time.Since(start) < w.ix.opts.AnnounceAfter {
				return
			}
			announced = true
			report(ProcessStarted, "")
		}
		report(ProcessProgress, "")
	})
	if announced {
		switch {
		case err == nil:
			report(ProcessDone, "")
		case ctx.Err() != nil:
			report(ProcessCancelled, "")
		default:
			report(ProcessFailed, err.Error())
		}
	}
	return err
}

// advancePages is the single cursor-driven operation: stream everything
// past the cursor through the chunkers and land it in the store, page
// by page. Never touches the embedder — text-bearing docs land as
// pending. progress is called after each landed page with the page's
// change count.
func (w *spaceWorker) advancePages(ctx context.Context, progress func(n int)) error {
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

		// Deleted is the only eviction signal for an object — its
		// projection is purged, nothing re-streams (space.ChangeIndexAPI).
		// Collected page-wide first so a same-page content change never
		// chunks an object past its eviction.
		deleted := map[string]struct{}{}
		for _, ch := range changes {
			if ch.Deleted {
				deleted[ch.ObjectId] = struct{}{}
			}
		}

		// Dedup object ids; ChangedSince is ascending, so the last
		// element carries the page's max ApplySeq.
		seen := map[string]bool{}
		var page pageOps
		for _, ch := range changes {
			if seen[ch.ObjectId] {
				continue
			}
			seen[ch.ObjectId] = true
			if _, del := deleted[ch.ObjectId]; del {
				page.prefixDels = append(page.prefixDels, ch.ObjectId+":")
				continue
			}
			if err := w.collectObject(ctx, ch.ObjectId, cursor, &page); err != nil {
				return err
			}
		}

		if err := w.ix.store.Apply(ctx, spaceId, page.ups, page.dels, page.prefixDels); err != nil {
			return err
		}
		cursor = changes[len(changes)-1].ApplySeq
		if err := w.ix.store.SetCursor(ctx, spaceId, cursor); err != nil {
			return err
		}
		progress(len(changes))
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
// one transaction, so eviction rides the same applySeq window as content.
type pageOps struct {
	ups        []DocUpsert
	dels       []string
	prefixDels []string
}

// collectObject gathers one live object's page ops, derived from the
// shared objects row inside the same ChangedSince window:
//   - gated chunkers whose type is not in any.types → dataset prefix
//     delete (covers DetachType; idempotent — one btree seek when
//     already empty);
//   - everything else → the chunkers' entries.
//
// Object deletion never reaches this path — advance evicts on
// ObjectChange.Deleted before chunking; the tombstoned-row check below
// only catches a delete racing the row read.
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
		var err error
		if rc, ok := ch.(index.Reconciler); ok {
			err = w.reconcile(ctx, rc, objectId, cursor, page)
		} else {
			err = w.streamChunks(ctx, ch, objectId, cursor, page)
		}
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

// reconcile runs a coalescing chunker (multi-record index unit, e.g.
// editor windows) and diffs its full doc set against what is stored, by
// content hash: vanished docs are deleted, new/changed docs upserted,
// unchanged docs left alone — so their vectors survive and are not
// re-embedded. This is what keeps an edit/append from re-embedding the
// whole object (chunker-hybrid-search-report § 9.5).
func (w *spaceWorker) reconcile(ctx context.Context, rc index.Reconciler, objectId string, cursor uint64, page *pageOps) error {
	entries, err := rc.Reconcile(ctx, w.sp, objectId, cursor)
	if err != nil {
		return err
	}
	stored, err := w.ix.store.DocHashes(ctx, w.sp.Id(), objectId+":"+rc.Dataset()+":")
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		id := docId(e.ObjectId, e.Dataset, e.RecordId)
		seen[id] = true
		if e.Data == "" {
			page.dels = append(page.dels, id)
			continue
		}
		if h, ok := stored[id]; ok && h == docHash(e.Data) {
			continue // unchanged — keep the stored doc and its vector
		}
		page.ups = append(page.ups, DocUpsert{Entry: e})
	}
	for id := range stored {
		if !seen[id] {
			page.dels = append(page.dels, id) // vanished
		}
	}
	return nil
}

// streamChunks runs a per-record chunker. On the cold cursor (0) nothing
// is stored, so it applies entries blind. On an incremental advance it
// hash-checks each re-streamed record and skips re-embedding ones whose
// indexed text is unchanged — e.g. a memory item bumped only on
// accessCount, or a chat message that got a reaction.
func (w *spaceWorker) streamChunks(ctx context.Context, ch index.Chunker, objectId string, cursor uint64, page *pageOps) error {
	var entries []index.IndexEntry
	if err := ch.ChunksSince(ctx, w.sp, objectId, cursor, func(e index.IndexEntry) error {
		entries = append(entries, e)
		return nil
	}); err != nil {
		return err
	}
	if cursor == 0 {
		for _, e := range entries {
			if e.Data == "" {
				page.dels = append(page.dels, docId(e.ObjectId, e.Dataset, e.RecordId))
			} else {
				page.ups = append(page.ups, DocUpsert{Entry: e})
			}
		}
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Data != "" {
			ids = append(ids, docId(e.ObjectId, e.Dataset, e.RecordId))
		}
	}
	stored, err := w.ix.store.DocHashesByIds(ctx, w.sp.Id(), ids)
	if err != nil {
		return err
	}
	for _, e := range entries {
		id := docId(e.ObjectId, e.Dataset, e.RecordId)
		if e.Data == "" {
			page.dels = append(page.dels, id)
			continue
		}
		if h, ok := stored[id]; ok && h == docHash(e.Data) {
			continue // re-streamed but indexed text unchanged — keep vector
		}
		page.ups = append(page.ups, DocUpsert{Entry: e})
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

// drainPending wraps drainRounds with embed-process reporting
// (Options.OnProcess, nil = off), gated on elapsed work: a drain
// announces only once it has been running past AnnounceAfter — a
// usual one-message drain finishes in well under a second and never
// appears. Once announced: progress per landed batch, a periodic
// heartbeat while an embed call runs longer than the view's staleness
// budget, then exactly one terminal — done, failed, or cancelled when
// the worker context ends mid-drain (space dropped, shutdown) — so no
// ghost running row outlives the drain. Total = docs embedded so far
// + the pending count, re-read at every round start, so late-arriving
// docs extend the bar instead of overflowing it. The gate ticker also
// re-checks mid-batch, so one long EmbedDocs call announces ~on time.
func (w *spaceWorker) drainPending(ctx context.Context) error {
	if w.ix.opts.OnProcess == nil {
		return w.drainRounds(ctx, func(int, int) {})
	}
	var embedded, total atomic.Int64
	var (
		mu        sync.Mutex
		workStart time.Time
		announced bool
		lastFrame time.Time
	)
	emit := func(phase, msg string) {
		w.ix.opts.OnProcess(ProcessUpdate{Kind: ProcessKindEmbed, SpaceId: w.sp.Id(),
			Phase: phase, Done: embedded.Load(), Total: total.Load(), Message: msg})
	}
	// tryAnnounce emits the started frame once the gate opens; reports
	// whether the drain is announced. Callable from the drain goroutine
	// and the gate ticker.
	tryAnnounce := func() bool {
		mu.Lock()
		defer mu.Unlock()
		if announced {
			return true
		}
		if workStart.IsZero() || time.Since(workStart) < w.ix.opts.AnnounceAfter {
			return false
		}
		announced = true
		lastFrame = time.Now()
		emit(ProcessStarted, "")
		return true
	}
	stopGate := func() {}
	err := w.drainRounds(ctx, func(landed, remaining int) {
		if landed > 0 {
			// Gate check before counting, so a started frame emitted
			// here reports the pre-batch count.
			ann := tryAnnounce()
			embedded.Add(int64(landed))
			if ann {
				mu.Lock()
				lastFrame = time.Now()
				mu.Unlock()
				emit(ProcessProgress, "")
			}
			return
		}
		if remaining >= 0 {
			total.Store(embedded.Load() + int64(remaining))
		}
		mu.Lock()
		fresh := workStart.IsZero()
		if fresh {
			workStart = time.Now()
		}
		mu.Unlock()
		if !fresh {
			// Later rounds re-check the gate at the boundary — a drain
			// crossing the threshold between rounds announces here even
			// if every individual batch is quick.
			tryAnnounce()
			return
		}
		// First work this drain: run the gate/heartbeat ticker until the
		// drain ends. Pre-announce it re-checks the gate (bounding
		// announce latency under one long embed call); post-announce it
		// heartbeats when no batch has landed for a while.
		tryAnnounce() // immediate mode (negative AnnounceAfter) announces at work start
		gCtx, cancel := context.WithCancel(ctx)
		stopGate = cancel
		go func() {
			t := time.NewTicker(embedAnnounceTick)
			defer t.Stop()
			for {
				select {
				case <-gCtx.Done():
					return
				case <-t.C:
					if !tryAnnounce() {
						continue
					}
					mu.Lock()
					beat := time.Since(lastFrame) >= embedProcessHeartbeat
					if beat {
						lastFrame = time.Now()
					}
					mu.Unlock()
					if beat {
						emit(ProcessProgress, "")
					}
				}
			}
		}()
	})
	stopGate()
	mu.Lock()
	wasAnnounced := announced
	mu.Unlock()
	if wasAnnounced {
		switch {
		case err == nil:
			emit(ProcessDone, "")
		case ctx.Err() != nil:
			emit(ProcessCancelled, "")
		default:
			emit(ProcessFailed, err.Error())
		}
	}
	return err
}

// drainRounds embeds and lands pending docs until the queue is empty.
// Each round pulls up to EmbedConcurrency batches and embeds them
// concurrently: an online embedder parallelizes across HTTP requests
// (the throughput win), while the local model serializes internally on
// its mutex — so concurrency is safe regardless of backend. SetVectors /
// EnsureVectorIndex stay serial. The vector index is created lazily after
// the first batch lands. progress is called with (0, remaining) when a
// round found work (before embedding; remaining = pending count, -1
// when the count read failed) and with (landed, -1) after each batch.
func (w *spaceWorker) drainRounds(ctx context.Context, progress func(landed, remaining int)) error {
	spaceId := w.sp.Id()
	batch := w.ix.opts.EmbedBatch
	conc := w.ix.opts.EmbedConcurrency
	for {
		ids, texts, err := w.ix.store.Pending(ctx, spaceId, batch*conc)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		remaining, cerr := w.ix.store.PendingCount(ctx, spaceId)
		if cerr != nil {
			remaining = -1 // progress keeps the last known total
		}
		progress(0, remaining)

		// Split the page into batch-sized chunks, embed concurrently.
		type chunk struct {
			ids   []string
			texts []string
			vecs  [][]float32
			err   error
		}
		var chunks []*chunk
		for off := 0; off < len(ids); off += batch {
			end := min(off+batch, len(ids))
			chunks = append(chunks, &chunk{ids: ids[off:end], texts: texts[off:end]})
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, conc)
		for _, c := range chunks {
			wg.Add(1)
			sem <- struct{}{}
			go func(c *chunk) {
				defer wg.Done()
				defer func() { <-sem }()
				c.vecs, c.err = w.ix.opts.Embedder.EmbedDocs(ctx, c.texts)
			}(c)
		}
		wg.Wait()

		// First successful batch teaches the store its dimension (no
		// boot-time probe — an embedder down at boot just starts here).
		for _, c := range chunks {
			if c.err == nil && len(c.vecs) > 0 && len(c.vecs[0]) > 0 {
				if err := w.ix.store.EnsureDim(ctx, len(c.vecs[0])); err != nil {
					return err
				}
				break
			}
		}
		// Land the successful chunks; a failed chunk leaves its docs
		// pending (the ticker retries) and we surface the error after.
		var firstErr error
		landed := false
		for _, c := range chunks {
			if c.err != nil {
				if firstErr == nil {
					firstErr = c.err
				}
				continue
			}
			if err := w.ix.store.SetVectors(ctx, spaceId, c.ids, c.vecs); err != nil {
				return err
			}
			landed = true
			progress(len(c.ids), -1)
		}
		if landed {
			if _, err := w.ix.store.EnsureVectorIndex(ctx, spaceId); err != nil {
				return err
			}
		}
		if firstErr != nil {
			// Embedder down for some chunk: pipeline freezes (those docs
			// stay pending), FTS unaffected; the ticker retries.
			return firstErr
		}
		if len(ids) < batch*conc {
			return nil // last page
		}
	}
}
