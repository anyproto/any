package indexer

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
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

	// generation is the SDK store epoch this worker's cursor belongs to,
	// resolved once at start (alignIndex) and persisted with every cursor
	// write.
	generation string

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

	// linksStamped: the cursor row already carries the link sink's
	// layout stamp (written once per worker after the first landed
	// page, or by the backfill).
	linksStamped bool
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
	// Before the loops: the alignment can drop this space's index, and
	// the embed loop caches the space collection handle — a drop racing
	// it would put a dropped handle back in the cache.
	w.alignIndex(w.ctx)
	w.ix.wg.Add(1)
	go w.advanceLoop()
	if w.ix.HasEmbedder() {
		w.ix.wg.Add(1)
		go w.embedLoop()
	}
}

// alignIndex decides, once per worker, whether the persisted index still
// describes the SDK store it was built from, and starts over when it
// does not.
//
// Two triggers, both from the SDK's ChangeIndexAPI contract:
//
//   - the per-space Generation changed — the SDK store was rebuilt from
//     scratch and the applySeq axis restarted at zero;
//   - the stored cursor sits past MaxApplySeq — an older sdk.db was
//     restored from backup under a cursor that ran ahead of it.
//
// Either way the cursor names a position the feed will never report
// again: ChangedSince returns nothing, forever, with no error, and the
// index silently stops updating. Dropping the space's docs and
// restarting at zero is the only recovery.
//
// Best-effort: a read that fails leaves the cursor alone (freezing the
// index over a transient error would be worse than the drift), and the
// next boot re-checks. The worker then advances with no epoch in hand —
// Store.SetCursor merges, so the stamp on record survives and the next
// boot can still detect a rebuild.
func (w *spaceWorker) alignIndex(ctx context.Context) {
	spaceId := w.sp.Id()
	cursor, storedGen, err := w.ix.store.Cursor(ctx, spaceId)
	if err != nil {
		w.ix.lg.Warn("read index cursor", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	w.generation = storedGen
	gen, err := w.sp.Changes().Generation(ctx)
	if err != nil {
		w.ix.lg.Warn("read space generation", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	if gen != "" {
		w.generation = gen
	}
	reason := ""
	switch {
	case gen != "" && storedGen != "" && storedGen != gen:
		reason = "sdk store was rebuilt"
	case cursor > 0:
		max, mErr := w.sp.Changes().MaxApplySeq(ctx)
		if mErr != nil {
			w.ix.lg.Warn("read max applySeq", zap.String("spaceId", spaceId), zap.Error(mErr))
			return
		}
		if cursor > max {
			reason = "cursor past the sdk store's axis"
		}
	}
	if reason == "" {
		if cursor > 0 && storedGen == "" && gen != "" {
			// A cursor from before generation tracking: stamp the epoch
			// now rather than at the next change, so the row describes
			// itself even on a space that never changes again.
			if err := w.ix.store.SetCursor(ctx, spaceId, cursor, gen); err != nil {
				w.ix.lg.Warn("stamp index generation", zap.String("spaceId", spaceId), zap.Error(err))
			}
		}
		return
	}
	w.ix.lg.Info("reindexing space", zap.String("spaceId", spaceId), zap.String("reason", reason),
		zap.Uint64("cursor", cursor), zap.String("was", storedGen), zap.String("now", gen))
	if err := w.ix.store.DropSpace(ctx, spaceId); err != nil {
		w.ix.lg.Warn("drop space index", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	if err := w.ix.store.SetCursor(ctx, spaceId, 0, w.generation); err != nil {
		w.ix.lg.Warn("reset index cursor", zap.String("spaceId", spaceId), zap.Error(err))
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
	// A db whose edges predate the link sink's layout is backfilled
	// first — here, off the boot path and outside the indexer lock, so
	// a first start after an upgrade neither stalls the server nor
	// blocks Close.
	w.backfillLinks(w.ctx)
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

// advance wraps advancePages with fts-process reporting through the
// shared procReporter: elapsed-work gate (AnnounceAfter), mid-page
// heartbeat (a single heavy page must not staleness-expire the row),
// one terminal. Total is unknown — the change feed has no backlog
// count.
func (w *spaceWorker) advance(ctx context.Context) error {
	rep := newProcReporter(w.ix.opts.OnProcess,
		ProcessUpdate{Kind: ProcessKindFTS, SpaceId: w.sp.Id()}, w.ix.opts.AnnounceAfter)
	var processed int64
	err := w.advancePages(ctx, rep.begin, func(n int) {
		processed += int64(n)
		rep.progress(processed, -1, "")
	})
	rep.finish(ctx, err)
	return err
}

// advancePages is the single cursor-driven operation: stream everything
// past the cursor through the chunkers and land it in the store, page
// by page. Never touches the embedder — text-bearing docs land as
// pending. onWork is called once when the first non-empty page is
// fetched (before chunking it); progress after each landed page with
// the page's change count.
func (w *spaceWorker) advancePages(ctx context.Context, onWork func(), progress func(n int)) error {
	spaceId := w.sp.Id()
	cursor, _, err := w.ix.store.Cursor(ctx, spaceId)
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
		if onWork != nil {
			onWork()
			onWork = nil
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

		touched, err := w.ix.store.ApplyPage(ctx, spaceId, page.ups, page.dels, page.prefixDels, page.textPrefixDels, &page.links)
		if err != nil {
			return err
		}
		if len(touched) > 0 && w.ix.opts.OnLinks != nil {
			w.ix.opts.OnLinks(spaceId, touched)
		}
		cursor = changes[len(changes)-1].ApplySeq
		if err := w.ix.store.SetCursor(ctx, spaceId, cursor, w.generation); err != nil {
			return err
		}
		if !w.linksStamped {
			// Edges written by an advance are on the current layout:
			// stamp once so a restart never backfills this space.
			if err := w.ix.store.StampLinksVersion(ctx, spaceId); err != nil {
				return err
			}
			w.linksStamped = true
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
	// textPrefixDels evict text docs only — a runtime dataset that
	// lost its search mapping but keeps link fields.
	textPrefixDels []string
	// links are the page's edge changes (links_store.go), already
	// diffed per object against the store (diffLinks); the structural
	// prefixDels above evict edges too.
	links LinkOps
}

// objectLinks accumulates one object's edges as its chunkers report
// them, with the scopes each report replaces: a streaming entry
// replaces its record's edges, a reconciled set a whole collection's.
// diffLinks turns it into exact page ops.
type objectLinks struct {
	edges  map[string]linkUp // doc id → edge
	scopes []string          // ':'-terminated prefixes whose stored edges are authoritative-replaced
}

type linkUp struct {
	entry index.LinkEntry
	seq   uint64
}

func (o *objectLinks) add(e index.LinkEntry, seq uint64) {
	if o.edges == nil {
		o.edges = map[string]linkUp{}
	}
	o.edges[linkDocId(e)] = linkUp{entry: e, seq: seq}
}

// collectObject gathers one live object's page ops, derived from the
// shared objects row inside the same ChangedSince window:
//   - gated chunkers whose type the object does not have → dataset
//     prefix delete (covers a retype; idempotent — one btree seek when
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
	if attached[index.MetaTypeLabel] || attached[index.MetaCollectionLabel] {
		// A definition object changed — a property was added, patched
		// or removed. The chunkers' catalog snapshots must see it before
		// the values written right after it (same page or the next)
		// are extracted, or those values wait for the snapshot's TTL
		// and a later write. Type objects precede their values in the
		// feed (the SDK parks a value whose definition has not applied).
		for _, ch := range w.ix.reg.All() {
			if inv, ok := ch.(index.CatalogInvalidator); ok {
				inv.Invalidate(w.sp.Id())
			}
		}
	}
	var links objectLinks
	for _, ch := range w.ix.reg.All() {
		if dyn, ok := ch.(index.DynamicChunker); ok {
			// Runtime datasets: per-dataset eviction names come from the
			// chunker (detached owning type, retired definitions); the
			// chunker self-gates streaming, so an evicted dataset is
			// never also upserted in this page. Same-tx prefix deletes
			// keep eviction applySeq-consistent, like the static gate.
			evict, err := dyn.EvictDatasets(ctx, w.sp, attached)
			if err != nil {
				return err
			}
			for _, ds := range evict {
				page.prefixDels = append(page.prefixDels, objectId+":"+ds+":")
			}
			if te, ok := ch.(index.TextEvictor); ok {
				// Datasets that keep their edges but lost their text
				// mapping: text docs go, edges follow the diff below.
				for _, ds := range te.EvictText(ctx, w.sp, attached) {
					page.textPrefixDels = append(page.textPrefixDels, objectId+":"+ds+":")
				}
			}
		} else if tid := ch.TypeId(); tid != "" && !attached[tid] {
			page.prefixDels = append(page.prefixDels, objectId+":"+ch.Dataset()+":")
			continue
		}
		var err error
		if mr, ok := ch.(index.MultiReconciler); ok && mr.Reconciles() {
			err = w.reconcileMulti(ctx, mr, objectId, cursor, page, &links)
		} else if rc, ok := ch.(index.Reconciler); ok {
			err = w.reconcile(ctx, rc, objectId, cursor, page, &links)
		} else {
			err = w.streamChunks(ctx, ch, objectId, cursor, page, &links)
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
	return w.diffLinks(ctx, objectId, cursor, &links, page)
}

// diffLinks turns an object's reported edges into exact page ops: the
// stored edge ids under the object are read once, every stored id
// inside a replaced scope that the records no longer produce is
// deleted, every produced id not stored is written, and only the
// liveness keys on either side are reported. Unchanged edges cost
// nothing — no rewrite, no signal. On the cold cursor nothing is
// stored, so the read is skipped.
func (w *spaceWorker) diffLinks(ctx context.Context, objectId string, cursor uint64, links *objectLinks, page *pageOps) error {
	if len(links.scopes) == 0 && len(links.edges) == 0 {
		return nil
	}
	var stored map[string]StoredLink
	if cursor > 0 {
		var err error
		if stored, err = w.ix.store.LinkIds(ctx, w.sp.Id(), objectId+":"); err != nil {
			return err
		}
	}
	touched := map[string]struct{}{}
	for id, st := range stored {
		if _, keep := links.edges[id]; keep {
			continue
		}
		inScope := false
		for _, p := range links.scopes {
			if strings.HasPrefix(id, p) {
				inScope = true
				break
			}
		}
		if !inScope {
			continue
		}
		page.links.Dels = append(page.links.Dels, id)
		touched[st.Key] = struct{}{}
	}
	for id, up := range links.edges {
		if _, have := stored[id]; have {
			continue
		}
		page.links.Ups = append(page.links.Ups, up.entry)
		page.links.Seqs = append(page.links.Seqs, up.seq)
		touched[linkTargetKey(up.entry.Target)] = struct{}{}
	}
	for k := range touched {
		page.links.Touched = append(page.links.Touched, k)
	}
	return nil
}

// reconcile runs a coalescing chunker (multi-record index unit, e.g.
// editor windows) and diffs its full doc set against what is stored, by
// content hash: vanished docs are deleted, new/changed docs upserted,
// unchanged docs left alone — so their vectors survive and are not
// re-embedded. This is what keeps an edit/append from re-embedding the
// whole object (chunker-hybrid-search-report § 9.5).
func (w *spaceWorker) reconcile(ctx context.Context, rc index.Reconciler, objectId string, cursor uint64, page *pageOps, links *objectLinks) error {
	entries, err := rc.Reconcile(ctx, w.sp, objectId, cursor)
	if err != nil {
		return err
	}
	stored, err := w.ix.store.DocHashes(ctx, w.sp.Id(), objectId+":"+rc.Dataset()+":")
	if err != nil {
		return err
	}
	w.plan(entries, stored, objectId, rc.Dataset(), page)
	planLinks(entries, objectId, rc.Dataset(), true, links)
	return nil
}

// reconcileMulti is reconcile for a module chunker spanning several
// collections: one stored-hash diff per active collection, keyed by the
// real collection name the entries carry.
func (w *spaceWorker) reconcileMulti(ctx context.Context, mr index.MultiReconciler, objectId string, cursor uint64, page *pageOps, links *objectLinks) error {
	sets, err := mr.ReconcileAll(ctx, w.sp, objectId, cursor)
	if err != nil {
		return err
	}
	for coll, entries := range sets {
		stored, err := w.ix.store.DocHashes(ctx, w.sp.Id(), objectId+":"+coll+":")
		if err != nil {
			return err
		}
		w.plan(entries, stored, objectId, coll, page)
		planLinks(entries, objectId, coll, true, links)
	}
	return nil
}

// planDocs turns chunker entries into page ops against the stored
// id→hash map covering every doc the entries could own: each entry
// expands to its chunk docs (expandEntry); a chunk whose stored hash
// matches is left alone (its vector survives), a changed or new one is
// upserted, and every stored id the entries no longer produce — a
// vanished doc, a record that now splits into fewer chunks, or an
// entry with empty Data — is deleted. Base ids of empty-Data entries
// are always deleted (the store's range delete covers chunks that
// stored may not list).
func planDocs(entries []index.IndexEntry, stored map[string]string, chunkRunes int, page *pageOps) (unindexable []string) {
	seen := make(map[string]bool, len(entries))
	var gone map[string]bool // bases whose range delete already covers their chunks
	for _, e := range entries {
		if !indexableId(e.RecordId) || !indexableId(e.Dataset) {
			unindexable = append(unindexable, e.Dataset+":"+e.RecordId)
			continue
		}
		base := docId(e.ObjectId, e.Dataset, e.RecordId)
		if e.Data == "" {
			page.dels = append(page.dels, base)
			if gone == nil {
				gone = map[string]bool{}
			}
			gone[base] = true
			continue
		}
		for _, up := range expandEntry(e, chunkRunes) {
			id := chunkDocId(base, up.Chunk)
			seen[id] = true
			if h, ok := stored[id]; ok && h == docHash(up.Entry.Data, up.Entry.Title) {
				continue // unchanged — keep the stored doc and its vector
			}
			page.ups = append(page.ups, up)
		}
	}
	for id := range stored {
		if seen[id] || gone[recordBase(id)] {
			continue
		}
		page.dels = append(page.dels, id) // vanished
	}
	return unindexable
}

// planLinks folds the entries' edges into the object's accumulator
// with the scope each report replaces: streaming shape (full=false),
// every entry's record — a record that now links nothing, or a
// tombstone (no Links), drops its edges; reconciled shape (full=true),
// the whole collection — even when the set is empty, which is exactly
// a collection whose last linked record was deleted. Entries whose ids
// carry a control byte are skipped like their text docs.
func planLinks(entries []index.IndexEntry, objectId, dataset string, full bool, links *objectLinks) {
	if full {
		links.scopes = append(links.scopes, objectId+":"+dataset+":")
	}
	for _, e := range entries {
		if !indexableId(e.RecordId) || !indexableId(e.Dataset) {
			continue
		}
		if !full {
			links.scopes = append(links.scopes, linkRecordPrefix(e.ObjectId, e.Dataset, e.RecordId))
		}
		for _, l := range e.Links {
			if !indexableId(l.RecordId) || !indexableId(l.Dataset) {
				continue
			}
			links.add(l, e.ApplySeq)
		}
	}
}

// indexableId rejects a doc-id component carrying a control byte. Chunk
// ids are the base id plus U+001F and a number, and a record's docs are
// the range [base, base+U+0020) — a component containing either byte
// would collide with a neighbour's chunk docs and make a record-level
// delete reach into it.
//
// It guards the DATASET as well as the record id: a name is only checked
// against a small deny-set (empty, `_` prefix, `.`/`/`/`:`, reserved
// names), so `a<U+001F>b` is a legal runtime dataset name and puts the
// separator one component earlier — same collision, same consequence.
// Record ids are auto CIDs or match a pattern that is client-supplied
// and validated only for compilation. Neither is checked for this, so
// the invariant is enforced here rather than assumed.
func indexableId(part string) bool {
	for i := 0; i < len(part); i++ {
		if part[i] < 0x20 {
			return false
		}
	}
	return true
}

// plan runs planDocs and reports what it had to skip, naming the first
// offender (quoted, so the control byte is visible) — a count alone
// leaves an operator with nothing to search for. One line per (object,
// dataset) page, and it repeats: the cause is a declared id pattern or
// dataset name that admits control bytes, so it persists until the
// declaration changes.
func (w *spaceWorker) plan(entries []index.IndexEntry, stored map[string]string, objectId, dataset string, page *pageOps) {
	if skipped := planDocs(entries, stored, w.ix.opts.ChunkRunes, page); len(skipped) > 0 {
		w.ix.lg.Warn("skipping records whose dataset or id carries a control byte",
			zap.String("spaceId", w.sp.Id()), zap.String("objectId", objectId),
			zap.String("dataset", dataset), zap.Int("count", len(skipped)),
			zap.String("first", strconv.Quote(skipped[0])))
	}
}

// streamChunks runs a per-record chunker. On the cold cursor (0) nothing
// is stored, so it applies entries blind. On an incremental advance it
// hash-checks each re-streamed record and skips re-embedding ones whose
// indexed text is unchanged — e.g. a memory item bumped only on
// accessCount, or a chat message that got a reaction.
func (w *spaceWorker) streamChunks(ctx context.Context, ch index.Chunker, objectId string, cursor uint64, page *pageOps, links *objectLinks) error {
	var entries []index.IndexEntry
	if err := ch.ChunksSince(ctx, w.sp, objectId, cursor, func(e index.IndexEntry) error {
		entries = append(entries, e)
		return nil
	}); err != nil {
		return err
	}
	_, whole := ch.(index.WholeCollectionLinks)
	planLinks(entries, objectId, ch.Dataset(), whole, links)
	if cursor == 0 {
		w.plan(entries, nil, objectId, ch.Dataset(), page)
		return nil
	}
	bases := make([]string, 0, len(entries))
	for _, e := range entries {
		// Same id guard planDocs applies: a record id carrying the chunk
		// separator produces a base byte-identical to a legitimate
		// record's chunk doc, which would pull that doc into `stored` and
		// get it deleted as vanished (planDocs never re-lists it).
		if e.Data != "" && indexableId(e.RecordId) && indexableId(e.Dataset) {
			bases = append(bases, docId(e.ObjectId, e.Dataset, e.RecordId))
		}
	}
	// Per-record chunk sets, so a re-streamed record with unchanged text
	// keeps its vectors and one that shrank drops its trailing chunks.
	stored, err := w.ix.store.DocHashesByRecords(ctx, w.sp.Id(), bases)
	if err != nil {
		return err
	}
	w.plan(entries, stored, objectId, ch.Dataset(), page)
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

// typeSet extracts the row's membership (its type, its collections,
// itself when it is a definition) into a set. Empty for nil rows.
func typeSet(row *anyenc.Value) map[string]bool { return index.Members(row) }

// drainPending wraps drainRounds with embed-process reporting through
// the shared procReporter: elapsed-work gate (a usual one-message
// drain finishes in well under a second and never appears), progress
// per landed batch, mid-batch gate ticks + heartbeat, one terminal.
// Total = docs embedded so far + the pending count, re-read at every
// round start, so late-arriving docs extend the bar instead of
// overflowing it; the round-start counters land before begin, so the
// started frame already carries them.
func (w *spaceWorker) drainPending(ctx context.Context) error {
	rep := newProcReporter(w.ix.opts.OnProcess,
		ProcessUpdate{Kind: ProcessKindEmbed, SpaceId: w.sp.Id()}, w.ix.opts.AnnounceAfter)
	var embedded int64
	err := w.drainRounds(ctx, func(landed, remaining int) {
		if landed > 0 {
			embedded += int64(landed)
			rep.progress(embedded, -1, "")
			return
		}
		if remaining >= 0 {
			rep.progress(embedded, embedded+int64(remaining), "")
		}
		rep.begin()
	})
	rep.finish(ctx, err)
	return err
}

// drainRounds embeds and lands pending docs until the queue is empty.
// Each round pulls up to EmbedConcurrency batches and embeds them
// concurrently: an online embedder parallelizes across HTTP requests
// (the throughput win), while the local child serves one frame at a
// time — so concurrency is safe regardless of backend. SetVectors /
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

		// First embedded vector teaches the store its dimension (no
		// boot-time probe — an embedder down at boot just starts here).
		for _, c := range chunks {
			if len(c.vecs) > 0 && len(c.vecs[0]) > 0 {
				if err := w.ix.store.EnsureDim(ctx, len(c.vecs[0])); err != nil {
					return err
				}
				break
			}
		}
		// Land what embedded — a failed chunk may still carry a leading
		// prefix (Embedder.EmbedDocs); the rest of its docs stay pending
		// (the ticker retries) and we surface the error after.
		var firstErr error
		landed := false
		for _, c := range chunks {
			if n := len(c.vecs); n > 0 {
				if err := w.ix.store.SetVectors(ctx, spaceId, c.ids[:n], c.vecs); err != nil {
					return err
				}
				landed = true
				progress(n, -1)
			}
			if c.err != nil && firstErr == nil {
				firstErr = c.err
			}
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

// backfillLinks rebuilds the space's edges from its records when they
// predate the link sink's layout (a db indexed before links existed,
// or a layout bump): the collection is dropped, every object the feed
// knows is re-extracted once through the chunkers and only its edges
// are kept, landed page by page — text docs are not touched, so
// nothing re-embeds — and the layout is stamped at the end. A failure
// is logged and retried on the next start (the stamp stays behind).
// Reported on the process view past AnnounceAfter like an advance.
func (w *spaceWorker) backfillLinks(ctx context.Context) {
	spaceId := w.sp.Id()
	needed, err := w.ix.store.LinksBackfillNeeded(ctx, spaceId)
	if err != nil {
		w.ix.lg.Warn("read links version", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	if !needed {
		// A never-indexed space (cursor 0) carries no stamp yet: its
		// first landed page writes it. Only an already-stamped row
		// spares the advance that write.
		v, verr := w.ix.store.LinksVersion(ctx, spaceId)
		w.linksStamped = verr == nil && v == linksSchemaVersion
		return
	}
	w.ix.lg.Info("backfilling links", zap.String("spaceId", spaceId))
	rep := newProcReporter(w.ix.opts.OnProcess,
		ProcessUpdate{Kind: ProcessKindLinksBackfill, SpaceId: spaceId}, w.ix.opts.AnnounceAfter)
	rep.begin()
	err = w.backfillLinksPages(ctx, rep)
	rep.finish(ctx, err)
	if err != nil {
		w.ix.lg.Warn("links backfill failed; retried on next start", zap.String("spaceId", spaceId), zap.Error(err))
		return
	}
	w.linksStamped = true
}

func (w *spaceWorker) backfillLinksPages(ctx context.Context, rep *procReporter) error {
	spaceId := w.sp.Id()
	if err := w.ix.store.ResetLinks(ctx, spaceId); err != nil {
		return err
	}
	seen := map[string]bool{}
	var from uint64
	var objects int64
	for {
		changes, err := w.sp.Changes().ChangedSince(ctx, from, w.ix.opts.BatchLimit)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			break
		}
		var ops LinkOps
		for _, ch := range changes {
			if ch.Deleted || seen[ch.ObjectId] {
				continue
			}
			seen[ch.ObjectId] = true
			// Cursor 0: the diff sees nothing stored and emits every
			// edge as an upsert; the collection was just dropped, so
			// that is exact.
			var page pageOps
			if err := w.collectObject(ctx, ch.ObjectId, 0, &page); err != nil {
				// One unreadable object must not hold the whole space
				// back: its edges arrive with its next change.
				w.ix.lg.Warn("links backfill: object skipped", zap.String("spaceId", spaceId),
					zap.String("objectId", ch.ObjectId), zap.Error(err))
				continue
			}
			ops.Ups = append(ops.Ups, page.links.Ups...)
			ops.Seqs = append(ops.Seqs, page.links.Seqs...)
			objects++
		}
		if err := w.ix.store.ApplyLinks(ctx, spaceId, &ops); err != nil {
			return err
		}
		rep.progress(objects, -1, "")
		from = changes[len(changes)-1].ApplySeq
		if len(changes) < w.ix.opts.BatchLimit {
			break
		}
	}
	return w.ix.store.StampLinksVersion(ctx, spaceId)
}
