package indexer

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any/internal/index"
)

// cursorsCollection holds one row per space ({id: spaceId, seq}) plus
// the metaDocId row recording the vector configuration the DB was
// created with.
const (
	cursorsCollection = "cursors"
	metaDocId         = "_meta"
	// indexSchemaVersion is bumped on incompatible store-layout changes:
	// v2 = objectId:dataset:recordId primary keys; v3 = editor_blocks
	// indexed as coalesced windows (win_<anchor>) instead of one doc per
	// block; v4 = any-store alpha.15 FTS (postings format v2 — FTS v1
	// indexes have no on-disk back-compat, so the index must be rebuilt);
	// v5 = eviction keyed on ObjectChange.Deleted (rebuild drops docs of
	// deleted objects) + default-on property indexing under scope
	// "props" (rebuild backfills "name: value" entries for existing
	// rows) + type-definition rows excluded from the prop chunker and
	// short prop docs no longer embedded (rebuild purges stale
	// type-name docs and name vectors); v6 = long records split into
	// chunk docs (`base<U+001F>n` ids + `chunk` field — rebuild replaces
	// whole-record docs with chunked ones). Mismatch = boot error advising
	// removal; no migration — the index is derived state (re-indexes on
	// the next change).
	indexSchemaVersion = 6
)

// Store is the indexer-owned any-store database: one collection per
// space, each carrying a BM25 full-text index on `data` and (when dim >
// 0) an IVF-SQ cosine vector index on `vector`.
//
// Doc shape: {id: dataset+"/"+recordId, scope, objectId, dataset,
// recordId, data, applySeq, vector?, pending?}. `pending: 1` marks a doc
// whose text awaits embedding — the embed loop drains them; the field is
// removed once the vector lands.
type Store struct {
	db   anystore.DB
	path string
	// markPending: stamp text-bearing upserts as pending-embedding.
	// True whenever an embedder is configured — even while it's
	// unreachable or the dimension is still unknown, so an outage
	// freezes the vector pipeline without losing work.
	markPending bool

	// vectorMode selects the ANN index strategy (see vectorIndexParams);
	// "" resolves to the default. Set once at boot, before any search.
	vectorMode string
	// FTS BM25 tuning (any-store FulltextParams), set once at boot.
	// 0 = engine default. titleWeight is the BM25F boost for the `title`
	// field (editor heading / method sig / memory context) over `data`.
	ftsB, ftsK1, titleWeight float64

	mu     sync.Mutex
	dim    int // 0 = unknown yet; learned lazily via EnsureDim
	colls  map[string]anystore.Collection
	hasVec map[string]bool // spaceId → vector index exists
}

// SetVectorMode picks the ANN index strategy for indexes created after the
// call (existing indexes keep their mode until rebuilt). Empty = default.
func (s *Store) SetVectorMode(mode string) { s.vectorMode = mode }

// SetFTSParams sets the BM25 tuning for FTS indexes created after the call
// (b/k1 are index-creation params; titleWeight is read at query time).
func (s *Store) SetFTSParams(b, k1, titleWeight float64) {
	s.ftsB, s.ftsK1, s.titleWeight = b, k1, titleWeight
}

// ftsParams builds the any-store FulltextParams from the configured BM25
// tuning, or nil to use engine defaults (b=0.75, k1=1.2, no field boost).
func (s *Store) ftsParams() *anystore.FulltextParams {
	if s.ftsB == 0 && s.ftsK1 == 0 && s.titleWeight == 0 {
		return nil
	}
	p := &anystore.FulltextParams{B: s.ftsB, K1: s.ftsK1}
	if s.titleWeight > 0 {
		p.Weights = map[string]float64{"title": s.titleWeight}
	}
	return p
}

// Hit is one search result row. Score semantics depend on the leg: BM25
// score (higher = better) for FTS, RRF score after fusion; the vector
// leg's raw cosine distance is folded before it reaches callers.
type Hit struct {
	Scope    string
	ObjectId string
	Dataset  string
	RecordId string
	Chunk    int // 0-based chunk of the record this doc holds (chunk.go)
	Data     string
	Score    float64
}

// DocUpsert pairs an entry with its (optional) embedding. A nil Vector
// while the store has a vector index stores the doc as pending.
type DocUpsert struct {
	Entry  index.IndexEntry
	Chunk  int // which chunk of the record Entry.Data holds (expandEntry)
	Vector []float32
}

// OpenStore opens (or creates) the index DB at path. dim is the
// configured vector dimension; 0 means "unknown — learn it from the
// first successful embedding" (EnsureDim). embedderConfigured turns on
// pending-marking even before the dimension is known. A dim change
// against an existing DB is a hard error — the index must be rebuilt.
func OpenStore(ctx context.Context, path string, dim int, embedderConfigured bool) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("indexer: create index dir: %w", err)
	}
	db, err := anystore.Open(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("indexer: open index db: %w", err)
	}
	s := newStore(db, path, dim, embedderConfigured)
	if err := s.checkMeta(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenStoreInMemory opens a throwaway in-memory store (tests).
func OpenStoreInMemory(ctx context.Context, dim int, embedderConfigured bool) (*Store, error) {
	db, err := anystore.Open(ctx, "", &anystore.Config{InMemory: true})
	if err != nil {
		return nil, err
	}
	s := newStore(db, "", dim, embedderConfigured)
	if err := s.checkMeta(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func newStore(db anystore.DB, path string, dim int, embedderConfigured bool) *Store {
	return &Store{
		db:   db,
		path: path,
		dim:  dim,
		// capVector gates the whole vector pipeline at build time: without
		// it, nothing is ever marked pending so the embed loop and vector
		// index stay dormant even if a dim is configured.
		markPending: capVector && (embedderConfigured || dim > 0),
		colls:       map[string]anystore.Collection{},
		hasVec:      map[string]bool{},
	}
}

// Dim returns the current vector dimension (0 = not yet known).
func (s *Store) Dim() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dim
}

// checkMeta reconciles the configured dimension with the one the DB was
// built with: adopt the persisted dim when none is configured, persist
// the configured one when the DB has none, error on a real mismatch
// (EnsureIndex would also catch it, but per-collection and later — this
// surfaces it once, at open, with a clear remedy).
//
// Both mismatch arms wrap ErrIndexRebuildRequired as a PREFIX. Prefix,
// not suffix, for two reasons: hosts read the head of a truncated
// message first, and the tails keep the substrings any-kotlin's
// AnyRuntimeImpl still matches on ("indexer:", "remove ", "to rebuild")
// so wiring the sentinel doesn't break Android before it adopts the
// code. Pinned by TestRebuildRequiredMessagesKeepAndroidSubstrings.
func (s *Store) checkMeta(ctx context.Context) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	doc, err := coll.FindId(ctx, metaDocId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return s.writeMetaDim(ctx, s.dim)
	}
	if err != nil {
		return err
	}
	if got := doc.Value().GetInt("schema"); got != indexSchemaVersion {
		return fmt.Errorf("%w: index db schema v%d, this build needs v%d — remove %s to rebuild (re-indexes on next change, see docs/13-index.md)", ErrIndexRebuildRequired, got, indexSchemaVersion, filepath.Dir(s.path))
	}
	got := doc.Value().GetInt("dim")
	switch {
	case got == s.dim:
		return nil
	case s.dim == 0:
		s.dim = got // adopt the dimension this DB was built with
		return nil
	case got == 0:
		return s.writeMetaDim(ctx, s.dim) // first run with a known dim
	default:
		return fmt.Errorf("%w: index db was built with vector dim %d, configured %d — remove %s to rebuild from scratch", ErrIndexRebuildRequired, got, s.dim, filepath.Dir(s.path))
	}
}

func (s *Store) writeMetaDim(ctx context.Context, dim int) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	arena := &anyenc.Arena{}
	meta := arena.NewObject()
	meta.Set("id", arena.NewString(metaDocId))
	meta.Set("schema", arena.NewNumberInt(indexSchemaVersion))
	meta.Set("dim", arena.NewNumberInt(dim))
	return coll.UpsertOne(ctx, meta)
}

// EnsureDim records the dimension learned from the first successful
// embedding. A no-op when it matches the known dim; an
// ErrIndexRebuildRequired when the embedder's output contradicts what
// this DB was built with (model changed under a populated index) — the
// vectors already stored are unusable, so the db has to go.
//
// Unreachable on mobile today (vector is off in both binds), wrapped
// anyway so the host contract doesn't depend on that staying true.
func (s *Store) EnsureDim(ctx context.Context, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("indexer: EnsureDim: invalid dim %d", dim)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.dim == dim:
		return nil
	case s.dim != 0:
		return fmt.Errorf("%w: embedder returned dim %d but the index db was built with %d — fix the model or remove %s to rebuild", ErrIndexRebuildRequired, dim, s.dim, filepath.Dir(s.path))
	}
	if err := s.writeMetaDim(ctx, dim); err != nil {
		return err
	}
	s.dim = dim
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// spaceColl opens (or creates) the per-space collection and ensures its
// indexes. Cached — EnsureIndex is idempotent but not free.
func (s *Store) spaceColl(ctx context.Context, spaceId string) (anystore.Collection, error) {
	s.mu.Lock()
	if coll, ok := s.colls[spaceId]; ok {
		s.mu.Unlock()
		return coll, nil
	}
	s.mu.Unlock()

	coll, err := s.db.Collection(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	// The vector index is NOT ensured here: IVF trains its quantizers
	// from existing documents, so it can only be created on a populated
	// collection — see EnsureVectorIndex, called from the embed path.
	// No objectId index: structural deletes are primary-key prefix
	// ranges on the objectId:dataset:recordId id shape.
	//
	// Each leg's index is created only when its build tag compiled it in
	// (docs/13-index.md § build tags): the fulltext index under `fts`, the
	// sparse pending index (which backs the embed loop) under `vector`.
	var indexes []anystore.IndexInfo
	if capFTS {
		// BM25 over `data` (body). When titleWeight > 0, BM25F also covers
		// the boosted `title` field (heading / method sig / memory context);
		// otherwise the index stays single-field so default ranking is
		// unchanged. b/k1 (if set) apply either way.
		fts := anystore.IndexInfo{Name: "fts", Kind: anystore.IndexKindFulltext, Fields: []string{"data"}}
		if s.titleWeight > 0 {
			fts.Fields = []string{"data", "title"}
		}
		fts.Fulltext = s.ftsParams()
		indexes = append(indexes, fts)
	}
	if capVector {
		indexes = append(indexes, anystore.IndexInfo{Fields: []string{"pending"}, Sparse: true})
	}
	if len(indexes) > 0 {
		if err := coll.EnsureIndex(ctx, indexes...); err != nil {
			return nil, fmt.Errorf("indexer: ensure indexes for %s: %w", spaceId, err)
		}
	}

	s.mu.Lock()
	s.colls[spaceId] = coll
	s.mu.Unlock()
	return coll, nil
}

// vectorIndexParams resolves the ANN index strategy. The mode comes from
// ANY_INDEX_VECTOR_MODE (test/ops override) else the configured
// s.vectorMode else the default "ivfsq":
//   - "ivfsq" (DEFAULT) — IVF + scalar quant: cheap, near-flat incremental
//     ingest and physical deletes (churn-friendly), at ~3–4 recall@10
//     below exact. The right default for a local, continuously-written
//     index with bulk imports (docs/search/README.md § index mode).
//   - "btree"/"hnsw" — HNSW graph: recall ≈ exact, but incremental ingest
//     is serial and super-linear (~15–47× slower than IVF on the embed-
//     loop path, growing with N) and deletes tombstone + rebuild. Opt-in
//     for read-heavy / quality-max deployments.
//   - "hybrid" — HNSW + a RAM layer-0 cache (faster search, more RAM).
//   - "bruteforce"/"exact" — no index, exact scan; best recall but O(N)
//     per query (fine for small spaces).
func (s *Store) vectorIndexParams(dim int) *anystore.VectorParams {
	mode := os.Getenv("ANY_INDEX_VECTOR_MODE")
	if mode == "" {
		mode = s.vectorMode
	}
	p := &anystore.VectorParams{
		Field:        "vector",
		Dim:          dim,
		Metric:       anystore.VectorCosine,
		CompactRatio: 0.5,
	}
	switch mode {
	case "btree", "hnsw":
		p.Mode = anystore.VectorModeBTree
	case "hybrid":
		p.Mode = anystore.VectorModeHybrid
		p.HybridCacheVectors = true
	case "bruteforce", "exact":
		p.Mode = anystore.VectorModeBruteForce
		p.CompactRatio = 0 // ignored for brute force
	default: // "", "ivfsq"
		p.Mode = anystore.VectorModeIVFSQ
	}
	return p
}

// EnsureVectorIndex creates the space's vector index once at least one
// embedded doc exists (the default IVF-SQ mode trains quantizers from
// existing docs, so it can't be created empty; the others also wait so the
// first build sees real data). Returns whether the index exists after the
// call. Idempotent and cheap once created (cached). The strategy is chosen
// by vectorIndexParams — default IVF-SQ (cheap incremental ingest,
// churn-friendly); HNSW (btree) is opt-in for higher recall
// (docs/search/README.md § index mode).
func (s *Store) EnsureVectorIndex(ctx context.Context, spaceId string) (bool, error) {
	if !capVector {
		return false, nil
	}
	dim := s.Dim()
	if dim == 0 {
		return false, nil
	}
	s.mu.Lock()
	cached := s.hasVec[spaceId]
	s.mu.Unlock()
	if cached {
		return true, nil
	}

	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return false, err
	}
	for _, ix := range coll.GetIndexes() {
		if ix.Info().Kind == anystore.IndexKindVector {
			s.mu.Lock()
			s.hasVec[spaceId] = true
			s.mu.Unlock()
			return true, nil
		}
	}
	n, err := coll.Find(vectorPresent).Count(ctx)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil // nothing to train from yet
	}
	err = coll.EnsureIndex(ctx, anystore.IndexInfo{
		Name:   "vec",
		Kind:   anystore.IndexKindVector,
		Vector: s.vectorIndexParams(dim),
	})
	if err != nil {
		return false, fmt.Errorf("indexer: ensure vector index for %s: %w", spaceId, err)
	}
	s.mu.Lock()
	s.hasVec[spaceId] = true
	s.mu.Unlock()
	return true, nil
}

// docId is the per-collection primary key of a record's first chunk:
// objectId:dataset:recordId (later chunks append a control-byte suffix,
// chunk.go). The shape makes removal a primary-key range delete at every
// granularity — `objectId:` (object deleted), `objectId:dataset:`
// (type detached), `[id, id+" ")` (record deleted) — and keeps ids unique
// even though recordIds repeat across objects (propIds do). Object ids
// and dataset names are colon-free by construction (CIDs; slug-checked
// names), so prefixes parse unambiguously. Record ids MAY contain
// colons (id:user datasets allow them) — harmless, the record id is
// the last segment.
func docId(objectId, dataset, recordId string) string {
	return objectId + ":" + dataset + ":" + recordId
}

// prefixUpper is the exclusive upper bound for a ':'-terminated id
// prefix: ids are compared bytewise and ';' is ':'+1, so
// [P, P[:len-1]+";") covers exactly the keys starting with P.
func prefixUpper(prefix string) string {
	return prefix[:len(prefix)-1] + ";"
}

// Cursor returns the last indexed ApplySeq for the space (0 = never)
// and the SDK generation that seq belongs to ("" on rows written before
// the generation was tracked, and on a never-indexed space).
func (s *Store) Cursor(ctx context.Context, spaceId string) (uint64, string, error) {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return 0, "", err
	}
	doc, err := coll.FindId(ctx, spaceId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	v := doc.Value()
	return uint64(v.GetInt("seq")), string(v.GetStringBytes("gen")), nil
}

// SetCursor persists the space cursor and the generation it belongs to.
//
// MERGES rather than replaces: an empty generation leaves a stamped one
// in place. A caller reaches the advance loop with no generation in hand
// whenever the boot-time read failed (alignIndex warns and returns) or
// never ran (SyncSpace builds a worker directly) — replacing the row
// there would erase the stamp, and a later SDK store rebuild would then
// pass both re-index triggers unnoticed: the cursor freezes the index on
// a renumbered applySeq axis with no error anywhere.
func (s *Store) SetCursor(ctx context.Context, spaceId string, seq uint64, generation string) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
		v.Set("seq", a.NewNumberInt(int(seq)))
		if generation != "" {
			v.Set("gen", a.NewString(generation))
		}
		return v, true, nil
	})
	_, err = coll.UpsertId(ctx, spaceId, mod)
	return err
}

// Apply lands one advance page in a single write transaction:
// structural prefix deletes first (object deletions / type-detach
// evictions — ':'-terminated id prefixes), then doc deletions — each
// id removes that doc AND every chunk under it (the record range,
// chunk.go; a chunk id has no chunks of its own, so passing one deletes
// exactly it; missing ids are a no-op) — then upserts (full-doc replace
// — a re-written doc goes back to pending until re-embedded). Atomic
// with the page, so eviction can never race the cursor.
//
// A removal WINS over an upsert of the same doc in one page: collectObject
// appends an object-wide prefix delete mid-loop when it finds the object
// tombstoned, by which point earlier chunkers have already queued upserts
// for it. Writing those would resurrect docs of an object nothing will
// ever re-stream, so they are dropped rather than ordered around.
func (s *Store) Apply(ctx context.Context, spaceId string, ups []DocUpsert, dels []string, prefixDels []string) error {
	if len(ups) == 0 && len(dels) == 0 && len(prefixDels) == 0 {
		return nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return err
	}
	tx, err := coll.WriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck — no-op after Commit

	for _, p := range prefixDels {
		// Find joins the open tx via tx.Context(); the id range drives
		// the primary btree directly, and Delete cleans FTS + vector
		// entries per doc. Empty range = one seek, idempotent.
		idRange := query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, p)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(p))},
		}
		if _, err := coll.Find(idRange).Delete(tx.Context()); err != nil {
			return err
		}
	}

	for _, id := range dels {
		idRange := query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, id)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, recordUpper(id))},
		}
		if _, err := coll.Find(idRange).Delete(tx.Context()); err != nil {
			return err
		}
	}

	arena := &anyenc.Arena{}
	for _, up := range ups {
		e := up.Entry
		id := chunkDocId(docId(e.ObjectId, e.Dataset, e.RecordId), up.Chunk)
		if removed(id, dels, prefixDels) {
			continue
		}
		doc := arena.NewObject()
		doc.Set("id", arena.NewString(id))
		doc.Set("scope", arena.NewString(e.Scope))
		doc.Set("objectId", arena.NewString(e.ObjectId))
		doc.Set("dataset", arena.NewString(e.Dataset))
		doc.Set("recordId", arena.NewString(e.RecordId))
		if up.Chunk > 0 {
			doc.Set("chunk", arena.NewNumberInt(up.Chunk))
		}
		doc.Set("data", arena.NewString(e.Data))
		doc.Set("title", arena.NewString(e.Title)) // BM25F boosted field (may be "")
		doc.Set("hash", arena.NewString(docHash(e.Data)))
		doc.Set("applySeq", arena.NewNumberInt(int(e.ApplySeq)))
		switch {
		case up.Vector != nil:
			doc.Set("vector", arena.NewVectorF32(up.Vector))
		case s.markPending && shouldEmbed(e):
			// Awaiting embedding — marked even while the embedder is
			// down or its dimension unknown, so outages freeze the
			// vector pipeline without losing work. Empty-text docs have
			// nothing to embed and stay vector-less.
			doc.Set("pending", arena.NewNumberInt(1))
		}
		if err := coll.UpsertOne(tx.Context(), doc); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// removed reports whether a doc id falls inside one of the page's
// removals: a structural prefix (whole object, or one of its datasets) or
// a record range — the base doc plus its chunk suffixes, the same span
// the delete loop above covers. Both lists hold a handful of entries per
// page, so a linear scan beats building a set.
func removed(id string, dels, prefixDels []string) bool {
	for _, p := range prefixDels {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	for _, d := range dels {
		if id == d || strings.HasPrefix(id, d+chunkSep) {
			return true
		}
	}
	return false
}

// minPropEmbedBytes gates the vector leg for prop-dataset docs: entries
// shorter than this (bytes) are never marked pending and stay FTS-only.
// Short name-like strings embed into a compressed cosine band (~0.55-0.62
// against relevant and irrelevant queries alike — the minVectorSim
// finding, docs/search/README.md), so they fill vector top-N slots
// without discriminating; BM25 is the right retrieval for lexical
// labels. Scoped to the prop dataset — content datasets embed regardless
// of length. Byte count, not runes: non-ASCII text trips the gate
// earlier, erring toward embedding.
const minPropEmbedBytes = 64

// shouldEmbed reports whether an entry's text belongs in the vector
// leg: non-empty, not props scope (FTS-only by design — see
// index.ScopeProps), and — for prop-dataset docs — at least
// minPropEmbedBytes (so short names/descriptions stay lexical while
// long descriptions and scope-overridden values still embed).
func shouldEmbed(e index.IndexEntry) bool {
	if e.Data == "" || e.Scope == index.ScopeProps {
		return false
	}
	return e.Dataset != index.DatasetProp || len(e.Data) >= minPropEmbedBytes
}

// docHash is the content hash stored alongside each index doc. The
// indexer uses it to (a) diff a reconciled doc set against what is stored
// — deleting vanished docs, upserting changed ones, leaving unchanged
// ones (and their vectors) untouched — and (b) skip re-embedding a
// per-record doc that re-streamed without its indexed text changing
// (e.g. a memory item whose accessCount bumped, a chat message that got a
// reaction). 64-bit FNV-1a, hex-encoded so it round-trips through
// any-store as an exact string (no float-precision risk of a numeric).
func docHash(data string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(data))
	return strconv.FormatUint(h.Sum64(), 16)
}

// DocHashes returns id→hash for every stored doc under the id prefix
// (objectId:dataset:). The reconcile diff uses it to find vanished and
// changed docs without trusting the chunker to enumerate deletions.
func (s *Store) DocHashes(ctx context.Context, spaceId, idPrefix string) (map[string]string, error) {
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	idRange := query.And{
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, idPrefix)},
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(idPrefix))},
	}
	return collectHashes(ctx, coll, idRange)
}

// DocHashesByRecords returns id→hash for every chunk doc of the given
// record ids (base doc ids, chunk.go) — one primary-key range seek per
// record. The incremental stream path diffs a re-streamed record's new
// chunk set against it.
func (s *Store) DocHashesByRecords(ctx context.Context, spaceId string, bases []string) (map[string]string, error) {
	if len(bases) == 0 {
		return map[string]string{}, nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	// One query per record. An Or of the ranges looks cheaper but is not:
	// And keeps only its first contributing conjunct in IndexBounds and Or
	// does not tighten, so the combined filter yields NO primary-key
	// bounds and every call degrades to a full collection scan (measured
	// 416x slower over 64 records of a 20k-doc space).
	out := make(map[string]string, len(bases))
	for _, base := range bases {
		got, err := collectHashes(ctx, coll, query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, base)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, recordUpper(base))},
		})
		if err != nil {
			return nil, err
		}
		for id, h := range got {
			out[id] = h
		}
	}
	return out, nil
}

func collectHashes(ctx context.Context, coll anystore.Collection, filter query.Filter) (map[string]string, error) {
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := map[string]string{}
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		v := doc.Value()
		out[string(v.GetStringBytes("id"))] = string(v.GetStringBytes("hash"))
	}
	return out, iter.Err()
}

// DropSpace removes the space's collection and cursor (space deleted or
// left).
func (s *Store) DropSpace(ctx context.Context, spaceId string) error {
	s.mu.Lock()
	delete(s.colls, spaceId)
	delete(s.hasVec, spaceId)
	s.mu.Unlock()

	coll, err := s.db.Collection(ctx, spaceId)
	if err != nil {
		return err
	}
	if err := coll.Drop(ctx); err != nil {
		return err
	}
	cursors, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	if err := cursors.DeleteId(ctx, spaceId); err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
		return err
	}
	return nil
}

// Static filter pieces — query filters are immutable once built and
// safe for concurrent use, so the constant ones are built exactly once.
var (
	idPath        = []string{"id"}
	scopePath     = []string{"scope"}
	pendingEqOne  = query.Key{Path: []string{"pending"}, Filter: query.NewComp(query.CompOpEq, 1)}
	vectorPresent = query.Key{Path: []string{"vector"}, Filter: query.Exists{}}
)

// scopeKey builds the optional residual scope filter ($in over the
// scope field). Nil when no scopes are requested.
func scopeKey(scopes []string) query.Filter {
	if len(scopes) == 0 {
		return nil
	}
	arena := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(scopes))
	for i, sc := range scopes {
		vals[i] = arena.NewString(sc)
	}
	return query.Key{Path: scopePath, Filter: query.NewInValue(vals...)}
}

// SearchFTS runs the BM25 leg. Hits come back ranked by descending
// score; docs with empty data never match (nothing was indexed).
func (s *Store) SearchFTS(ctx context.Context, spaceId, q string, scopes []string, limit int) ([]Hit, error) {
	return s.SearchFTSQuery(ctx, spaceId, FTSQuery{Query: q}, scopes, limit)
}

// FTSQuery is the full-text query spec. Query is the `$search` string —
// phrases ("...") and prefixes (foo*) in it are honored by the engine.
// DefaultAnd makes bare terms required (AND) instead of OR. Require /
// Exclude are extra must / must-not terms ($require / $exclude); each may
// itself be a phrase or prefix.
type FTSQuery struct {
	Query      string
	DefaultAnd bool
	Require    []string
	Exclude    []string
}

// SearchFTSQuery runs the BM25(F) leg with full operator support and
// returns the first limit hits in rank order (0 = every match).
func (s *Store) SearchFTSQuery(ctx context.Context, spaceId string, fq FTSQuery, scopes []string, limit int) ([]Hit, error) {
	cur, err := s.openFTS(ctx, spaceId, fq, scopes)
	if err != nil {
		return nil, err
	}
	defer cur.Close()
	var out []Hit
	for limit <= 0 || len(out) < limit {
		h, ok, err := cur.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		out = append(out, h)
	}
	return out, nil
}

// ftsCursor streams the BM25(F) leg's hits in rank order. any-store
// accumulates and sorts every match before the first Next whatever the
// Limit — a Limit only wraps a LimitIter, and on this collection cannot
// change the plan (no bounded secondary index, no pk bounds) — and
// materializes rows only as they are pulled, so a caller reads exactly
// as deep as it needs and Close is O(1) at any point (BenchmarkCutoff,
// TestIteratorEarlyCloseNoLeak). The cursor holds a
// read tx — a reader slot, a page cache, a WAL read-mark — until Close:
// pull, then close; never park one across another store call. A nil
// iter is the FTS-compiled-out cursor: no fulltext index exists, so it
// yields nothing rather than erroring against a missing index.
type ftsCursor struct {
	iter anystore.Iterator
}

// openFTS starts the lexical leg: the shoulds parsed from Query plus
// the required / excluded terms (each parsed so phrases / prefixes
// work), optionally restricted to scopes.
func (s *Store) openFTS(ctx context.Context, spaceId string, fq FTSQuery, scopes []string) (*ftsCursor, error) {
	if !capFTS {
		return &ftsCursor{}, nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	text := query.Text{Search: fq.Query, DefaultAnd: fq.DefaultAnd}
	if len(fq.Require) > 0 || len(fq.Exclude) > 0 {
		clauses := query.ParseTextSearch(fq.Query)
		clauses = appendClauses(clauses, fq.Require, query.TextMust)
		clauses = appendClauses(clauses, fq.Exclude, query.TextMustNot)
		text.Clauses = clauses
	}
	var filter query.Filter = text
	if sk := scopeKey(scopes); sk != nil {
		filter = query.And{filter, sk}
	}
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return nil, err
	}
	return &ftsCursor{iter: iter}, nil
}

// Next yields the next hit in rank order; ok is false once the leg is
// exhausted.
func (c *ftsCursor) Next() (Hit, bool, error) {
	if c.iter == nil {
		return Hit{}, false, nil
	}
	if !c.iter.Next() {
		return Hit{}, false, c.iter.Err()
	}
	doc, err := c.iter.Doc()
	if err != nil {
		return Hit{}, false, err
	}
	return hitFromDoc(doc.Value(), c.iter.Score()), true, nil
}

// Close releases the read tx. A no-op on the compiled-out cursor.
func (c *ftsCursor) Close() error {
	if c.iter == nil {
		return nil
	}
	return c.iter.Close()
}

// FilterTerms keeps only the hits that satisfy the Require / Exclude
// terms — the same $require / $exclude semantics SearchFTSQuery applies
// to the lexical leg, re-checked against the FTS index for hits that
// arrived some other way (the vector leg). One indexed query restricted
// to the hits' doc ids: any-store costs the pk restriction against the
// posting lists and probes the text index per candidate when that is
// cheaper (any-store v2.0.1 — before it, the $text predicate always
// drove and this cost the term's whole posting list). The analyzer
// decides "contains", so a phrase or prefix term behaves exactly as it
// does in the lexical leg. Nothing to enforce (no terms, no hits, FTS
// compiled out) returns hits unchanged.
//
// Negated clauses only tombstone docs a positive clause already scored,
// so an exclude-only filter is run inverted: match the excluded terms as
// shoulds and drop whatever comes back.
func (s *Store) FilterTerms(ctx context.Context, spaceId string, hits []Hit, require, exclude []string) ([]Hit, error) {
	if !capFTS || len(hits) == 0 || (len(require) == 0 && len(exclude) == 0) {
		return hits, nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	arena := &anyenc.Arena{}
	ids := make([]*anyenc.Value, len(hits))
	for i, h := range hits {
		ids[i] = arena.NewString(hitDocId(h))
	}
	var clauses []query.TextClause
	keepMatched := len(require) > 0
	if keepMatched {
		clauses = appendClauses(clauses, require, query.TextMust)
		clauses = appendClauses(clauses, exclude, query.TextMustNot)
	} else {
		clauses = appendClauses(clauses, exclude, query.TextShould)
	}
	filter := query.And{
		query.Text{Clauses: clauses},
		query.Key{Path: idPath, Filter: query.NewInValue(ids...)},
	}
	iter, err := coll.Find(filter).Limit(uint(len(hits))).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	matched := map[string]bool{}
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		matched[string(doc.Value().GetStringBytes("id"))] = true
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		if matched[hitDocId(h)] == keepMatched {
			out = append(out, h)
		}
	}
	return out, nil
}

// hitDocId is the primary key of the doc a hit came from — the record's
// base id plus its chunk suffix (chunk.go).
func hitDocId(h Hit) string {
	return chunkDocId(docId(h.ObjectId, h.Dataset, h.RecordId), h.Chunk)
}

// appendClauses parses each term (so phrase/prefix syntax is honored) and
// appends it with the given boolean role.
func appendClauses(dst []query.TextClause, terms []string, op query.TextOp) []query.TextClause {
	for _, t := range terms {
		for _, c := range query.ParseTextSearch(t) {
			c.Op = op
			dst = append(dst, c)
		}
	}
	return dst
}

// SearchVector runs the ANN leg: nearest-first by cosine distance.
// Score is folded to similarity (1 - distance) so "higher = better"
// holds across legs. While the vector pipeline is frozen (dimension
// never learned, or no embedded docs in the space yet) it returns no
// hits rather than erroring — search degrades, never breaks.
//
// minSim is the cosine-similarity floor: hits at or below it are dropped.
// The effective floor is max(minSim, smallest-positive) — a similarity
// must always be > 0 (cosine distance < 1) to carry any signal.
func (s *Store) SearchVector(ctx context.Context, spaceId string, vec []float32, scopes []string, limit int, minSim float64) ([]Hit, error) {
	hits, _, err := s.searchVector(ctx, spaceId, vec, scopes, limit, minSim)
	return hits, err
}

// searchVector is SearchVector plus n, the number of rows the index
// returned BEFORE the similarity floor: n < limit means the ANN's reach
// is spent — the probed IVF cells (~4√N docs at nprobe 16), or under a
// scope residual the beam any-store sizes from K — and len(hits) < n
// means the floor trimmed the far tail. A caller widening K reads both
// (vectorStop).
func (s *Store) searchVector(ctx context.Context, spaceId string, vec []float32, scopes []string, limit int, minSim float64) (hits []Hit, n int, err error) {
	if !capVector || s.Dim() == 0 {
		return nil, 0, nil
	}
	ok, err := s.EnsureVectorIndex(ctx, spaceId)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, 0, err
	}
	// $k bounds the result set; the scope filter is applied before the
	// cut-to-k, so every returned hit is in scope.
	var filter query.Filter = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(vec, limit)}
	if sk := scopeKey(scopes); sk != nil {
		filter = query.And{filter, sk}
	}
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer iter.Close()
	hits, err = collectHits(iter, func(it anystore.Iterator) float64 { return 1 - float64(it.Distance()) })
	if err != nil {
		return nil, 0, err
	}
	n = len(hits)
	// Noise floor: ANN always returns the k nearest, however far. Drop
	// non-positive similarity (cosine distance >= 1 — orthogonal or
	// worse): such hits carry no signal and only pollute fusion when
	// nothing real matched. minSim raises the floor above 0 when
	// configured (chunker-hybrid-search-report § 5.3).
	floor := max(0.0, minSim)
	out := hits[:0]
	for _, h := range hits {
		if h.Score > floor {
			out = append(out, h)
		}
	}
	return out, n, nil
}

func collectHits(iter anystore.Iterator, score func(anystore.Iterator) float64) ([]Hit, error) {
	var out []Hit
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		out = append(out, hitFromDoc(doc.Value(), score(iter)))
	}
	return out, iter.Err()
}

// hitFromDoc copies a row's fields out of the iterator-owned value.
func hitFromDoc(v *anyenc.Value, score float64) Hit {
	return Hit{
		Scope:    string(v.GetStringBytes("scope")),
		ObjectId: string(v.GetStringBytes("objectId")),
		Dataset:  string(v.GetStringBytes("dataset")),
		RecordId: string(v.GetStringBytes("recordId")),
		Chunk:    v.GetInt("chunk"),
		Data:     string(v.GetStringBytes("data")),
		Score:    score,
	}
}

// Pending returns up to limit docs awaiting embedding (only docs with
// non-empty text ever carry the pending mark — see Apply).
func (s *Store) Pending(ctx context.Context, spaceId string, limit int) (ids []string, texts []string, err error) {
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, nil, err
	}
	iter, err := coll.Find(pendingEqOne).Limit(uint(limit)).Iter(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer iter.Close()
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, nil, err
		}
		v := doc.Value()
		ids = append(ids, string(v.GetStringBytes("id")))
		texts = append(texts, string(v.GetStringBytes("data")))
	}
	return ids, texts, iter.Err()
}

// PendingCount reports how many docs still await embedding — the
// denominator for embed-progress reporting (rides the sparse pending
// index, so it stays cheap).
func (s *Store) PendingCount(ctx context.Context, spaceId string) (int, error) {
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return 0, err
	}
	return coll.Find(pendingEqOne).Count(ctx)
}

// SetVectors lands one embed batch in a single write transaction:
// $set vector + clear pending, update-only (a doc deleted since Pending
// is skipped, not resurrected).
func (s *Store) SetVectors(ctx context.Context, spaceId string, ids []string, vecs [][]float32) error {
	if len(ids) != len(vecs) {
		return fmt.Errorf("indexer: SetVectors: %d ids, %d vectors", len(ids), len(vecs))
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return err
	}
	tx, err := coll.WriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for i, id := range ids {
		vec := vecs[i]
		mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
			v.Set("vector", a.NewVectorF32(vec))
			v.Del("pending")
			return v, true, nil
		})
		if _, err := coll.UpdateId(tx.Context(), id, mod); err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
			return err
		}
	}
	return tx.Commit()
}
