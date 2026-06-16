package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// indexSchemaVersion is bumped on incompatible store-layout changes
	// (v2: objectId:dataset:recordId primary keys). Mismatch = boot
	// error advising removal; no migration — the index is derived state.
	indexSchemaVersion = 2
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

	mu     sync.Mutex
	dim    int // 0 = unknown yet; learned lazily via EnsureDim
	colls  map[string]anystore.Collection
	hasVec map[string]bool // spaceId → vector index exists
}

// Hit is one search result row. Score semantics depend on the leg: BM25
// score (higher = better) for FTS, RRF score after fusion; the vector
// leg's raw cosine distance is folded before it reaches callers.
type Hit struct {
	Scope    string
	ObjectId string
	Dataset  string
	RecordId string
	Data     string
	Score    float64
}

// DocUpsert pairs an entry with its (optional) embedding. A nil Vector
// while the store has a vector index stores the doc as pending.
type DocUpsert struct {
	Entry  index.IndexEntry
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
		db:          db,
		path:        path,
		dim:         dim,
		markPending: embedderConfigured || dim > 0,
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
		return fmt.Errorf("indexer: index db schema v%d, this build needs v%d — remove %s to rebuild (re-indexes on next change, see docs/13-index.md)", got, indexSchemaVersion, filepath.Dir(s.path))
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
		return fmt.Errorf("indexer: index db was built with vector dim %d, configured %d — remove %s to rebuild from scratch", got, s.dim, filepath.Dir(s.path))
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
// embedding. A no-op when it matches the known dim; an error when the
// embedder's output contradicts what this DB was built with (model
// changed under a populated index).
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
		return fmt.Errorf("indexer: embedder returned dim %d but the index db was built with %d — fix the model or remove %s to rebuild", dim, s.dim, filepath.Dir(s.path))
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
	indexes := []anystore.IndexInfo{
		{Name: "fts", Kind: anystore.IndexKindFulltext, Fields: []string{"data"}},
		// sparse pending backs the embed loop.
		{Fields: []string{"pending"}, Sparse: true},
	}
	if err := coll.EnsureIndex(ctx, indexes...); err != nil {
		return nil, fmt.Errorf("indexer: ensure indexes for %s: %w", spaceId, err)
	}

	s.mu.Lock()
	s.colls[spaceId] = coll
	s.mu.Unlock()
	return coll, nil
}

// EnsureVectorIndex creates the space's IVF-SQ vector index once at
// least one embedded doc exists to train from (IVF builds its
// quantizers from existing documents — creating it empty is an error).
// Returns whether the index exists after the call. Idempotent and
// cheap once created (cached).
//
// IVF-SQ over HNSW: cold sync bulk-inserts whole spaces, where IVF
// inserts are cell-assign + code append instead of graph construction;
// RAM stays at centroids; recall beats IVF-PQ with no PQ training.
// CompactRatio bounds centroid drift (auto re-train as the space grows
// past the initial training set).
func (s *Store) EnsureVectorIndex(ctx context.Context, spaceId string) (bool, error) {
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
		Name: "vec",
		Kind: anystore.IndexKindVector,
		Vector: &anystore.VectorParams{
			Field:        "vector",
			Dim:          dim,
			Metric:       anystore.VectorCosine,
			Mode:         anystore.VectorModeIVFSQ,
			CompactRatio: 0.5,
		},
	})
	if err != nil {
		return false, fmt.Errorf("indexer: ensure vector index for %s: %w", spaceId, err)
	}
	s.mu.Lock()
	s.hasVec[spaceId] = true
	s.mu.Unlock()
	return true, nil
}

// docId is the per-collection primary key: objectId:dataset:recordId.
// The shape makes removal a primary-key prefix delete at every
// granularity — `objectId:` (object deleted), `objectId:dataset:`
// (type detached), exact id (record deleted) — and keeps ids unique
// even though recordIds repeat across objects (propIds do). Components
// are colon-free by construction: object ids are CIDs, dataset names
// are slugs, record ids are base58 change-derived ids / propIds /
// reserved literals.
func docId(objectId, dataset, recordId string) string {
	return objectId + ":" + dataset + ":" + recordId
}

// prefixUpper is the exclusive upper bound for a ':'-terminated id
// prefix: ids are compared bytewise and ';' is ':'+1, so
// [P, P[:len-1]+";") covers exactly the keys starting with P.
func prefixUpper(prefix string) string {
	return prefix[:len(prefix)-1] + ";"
}

// Cursor returns the last indexed ApplySeq for the space (0 = never).
func (s *Store) Cursor(ctx context.Context, spaceId string) (uint64, error) {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return 0, err
	}
	doc, err := coll.FindId(ctx, spaceId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return uint64(doc.Value().GetInt("seq")), nil
}

// SetCursor persists the space cursor.
func (s *Store) SetCursor(ctx context.Context, spaceId string, seq uint64) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	arena := &anyenc.Arena{}
	doc := arena.NewObject()
	doc.Set("id", arena.NewString(spaceId))
	doc.Set("seq", arena.NewNumberInt(int(seq)))
	return coll.UpsertOne(ctx, doc)
}

// Apply lands one advance page in a single write transaction:
// structural prefix deletes first (object deletions / type-detach
// evictions — ':'-terminated id prefixes), then record deletions
// (missing ids ignored), then upserts (full-doc replace — a re-written
// record goes back to pending until re-embedded). Atomic with the page,
// so eviction can never race the cursor.
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

	arena := &anyenc.Arena{}
	for _, up := range ups {
		e := up.Entry
		doc := arena.NewObject()
		doc.Set("id", arena.NewString(docId(e.ObjectId, e.Dataset, e.RecordId)))
		doc.Set("scope", arena.NewString(e.Scope))
		doc.Set("objectId", arena.NewString(e.ObjectId))
		doc.Set("dataset", arena.NewString(e.Dataset))
		doc.Set("recordId", arena.NewString(e.RecordId))
		doc.Set("data", arena.NewString(e.Data))
		doc.Set("applySeq", arena.NewNumberInt(int(e.ApplySeq)))
		switch {
		case up.Vector != nil:
			doc.Set("vector", arena.NewVectorF32(up.Vector))
		case s.markPending && e.Data != "":
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
	for _, id := range dels {
		if err := coll.DeleteId(tx.Context(), id); err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
			return err
		}
	}
	return tx.Commit()
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
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	var filter query.Filter = query.Text{Search: q}
	if sk := scopeKey(scopes); sk != nil {
		filter = query.And{filter, sk}
	}
	iter, err := coll.Find(filter).Limit(uint(limit)).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	return collectHits(iter, func(it anystore.Iterator) float64 { return it.Score() })
}

// SearchVector runs the ANN leg: nearest-first by cosine distance.
// Score is folded to similarity (1 - distance) so "higher = better"
// holds across legs. While the vector pipeline is frozen (dimension
// never learned, or no embedded docs in the space yet) it returns no
// hits rather than erroring — search degrades, never breaks.
func (s *Store) SearchVector(ctx context.Context, spaceId string, vec []float32, scopes []string, limit int) ([]Hit, error) {
	if s.Dim() == 0 {
		return nil, nil
	}
	ok, err := s.EnsureVectorIndex(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	arena := &anyenc.Arena{}
	arr := arena.NewArray()
	for i, f := range vec {
		arr.SetArrayItem(i, arena.NewNumberFloat64(float64(f)))
	}
	var filter query.Filter = query.Key{Path: []string{"vector"}, Filter: query.NewCompValue(query.CompOpEq, arr)}
	if sk := scopeKey(scopes); sk != nil {
		filter = query.And{filter, sk}
	}
	iter, err := coll.Find(filter).Limit(uint(limit)).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	hits, err := collectHits(iter, func(it anystore.Iterator) float64 { return 1 - float64(it.Distance()) })
	if err != nil {
		return nil, err
	}
	// Noise floor: ANN always returns the k nearest, however far. Drop
	// non-positive similarity (cosine distance >= 1 — orthogonal or
	// worse): such hits carry no signal and only pollute fusion when
	// nothing real matched.
	out := hits[:0]
	for _, h := range hits {
		if h.Score > 0 {
			out = append(out, h)
		}
	}
	return out, nil
}

func collectHits(iter anystore.Iterator, score func(anystore.Iterator) float64) ([]Hit, error) {
	var out []Hit
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		v := doc.Value()
		out = append(out, Hit{
			Scope:    string(v.GetStringBytes("scope")),
			ObjectId: string(v.GetStringBytes("objectId")),
			Dataset:  string(v.GetStringBytes("dataset")),
			RecordId: string(v.GetStringBytes("recordId")),
			Data:     string(v.GetStringBytes("data")),
			Score:    score(iter),
		})
	}
	return out, iter.Err()
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
