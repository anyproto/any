package indexer

// Store-level harness for the search `filter`: a condition on
// the hit's HOST OBJECT row, which lives in the SDK's objects
// collection (sdk.db) while the hits live in the indexer's own index.db.
// Two strategies, measured head to head:
//
//	MODE 1 (pre-resolve) — run the filter on the objects collection,
//	collect the matching object ids, run the leg with an `objectId $in
//	[ids]` residual. Variants: without / with a secondary index on
//	`objectId` in the index store, and a primary-key variant ($or of
//	`objectId:` id ranges).
//	MODE 2 (post-filter) — run the leg unrestricted, pull rows in
//	batches, resolve each batch's distinct object ids with ONE objects
//	query (`id $in [batch] AND filter`), cache the verdicts, keep
//	pulling until `limit` distinct matching records are covered or the
//	scan cap is spent.
//
// Everything is synthetic — two any-store DBs, no SDK, no embedder
// (vectors are written straight into the store). Gated on
// ANY_FILTER_BENCH=1 so `go test ./...` never pays for it.
//
// Run: ANY_FILTER_BENCH=1 go test -run TestFilterModesBench \
//		-v -count=1 -timeout 60m ./internal/indexer/
//
// Env: ANY_FILTER_BENCH_SIZES (total chunks per corpus, default
// "20000,100000"), ANY_FILTER_BENCH_SHAPES (chunks per object, default
// "1,20"), ANY_FILTER_BENCH_RUNS (timed runs per cell, default 5),
// ANY_FILTER_BENCH_OUT (report dir, default <tmp>/any-filter-bench).

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/index"
)

const (
	fbSpace = "sp"
	fbDim   = 256
	// fbScanCap bounds either mode's read of the index store — the
	// "bounded scan" a request-time implementation would carry.
	fbScanCap = 5000
	// fbVerdictBatch is mode 2's row batch: how many index rows are
	// pulled between objects-store verdict queries.
	fbVerdictBatch = 64
	// fbMaxKnnK bounds the vector leg's widening (×4 per round).
	fbMaxKnnK = 4096
	// fbPkRangeMax caps the $or-of-id-ranges mode-1 variant: past it the
	// disjunction is pathological (and any-store drops bounds for an $or
	// wider than 10000 branches anyway).
	fbPkRangeMax = 512
	// fbCapN is the capped-count probe's threshold ("more than N?").
	fbCapN = 200
	// fbPropType / fbPropId are the owner/property namespace every object
	// carries a value under — the `<ownerId>.<propId>` path shape, with no
	// index on it (the SDK indexes only any.type, any.collections and
	// modifiedAt).
	fbPropType = "typ_props"
	fbPropId   = "prp_choice"
	// fbBin is the built-in bin collection, on 10% of objects.
	fbBin = "bin"
	// fbVocab is the Zipfian vocabulary size; fbSentence is how many of
	// its head words the natural-sentence query uses.
	fbVocab    = 2000
	fbSentence = 5
	// fbRareDocs is the planted document frequency of `rareterm`.
	fbRareDocs = 30
)

// fbTypes is the skewed type distribution: one dominant type, three at
// 10%, five at 1%, one at 0.1%, the rest filler.
var fbTypes = []struct {
	id string
	p  float64
}{
	{"typ_note", 0.50},
	{"typ_task", 0.10}, {"typ_page", 0.10}, {"typ_person", 0.10},
	{"typ_a", 0.01}, {"typ_b", 0.01}, {"typ_c", 0.01}, {"typ_d", 0.01}, {"typ_e", 0.01},
	{"typ_rare", 0.001},
	{"typ_x", 0.0745}, {"typ_y", 0.0745},
}

// --- corpus -----------------------------------------------------------

// fbObject is one host-object row: its type, its collections, its
// recency stamp and its single property value.
type fbObject struct {
	id          string
	typeId      string
	collections []string
	modAt       time.Time
	propVal     string
}

// fbFixture is one built corpus: the index store, the objects
// collection and the facts the queries need. Synthetic corpora come
// from fbBuild; a real one (fbOpenReal) fills the same fields from a
// server's two databases, which is why space / queries / filters are
// fields rather than constants.
type fbFixture struct {
	chunks, perObject int
	label             string // real mode's row label; "" = synthetic naming
	space             string // index-store collection = the space id
	st                *Store
	objColl           anystore.Collection
	objects           []fbObject
	queries           []fbQuery
	filters           []fbFilter
	queryVec          []float32            // sampled document vector (fallback)
	vectors           map[string][]float32 // real query embeddings, by query name
	docCount          int
	recentCut         time.Time
}

func (f *fbFixture) name() string {
	if f.label != "" {
		return f.label
	}
	return fmt.Sprintf("%dk/%dc", f.chunks/1000, f.perObject)
}

// vectorFor is the query's embedding: the real one when a query
// embedder produced it, the sampled document vector for the no-text
// pseudo-query, nil when the vector legs can't run for this query.
func (f *fbFixture) vectorFor(q fbQuery) []float32 {
	if v := f.vectors[q.name]; v != nil {
		return v
	}
	if q.vector {
		return f.queryVec
	}
	return nil
}

// fbWords builds the Zipfian vocabulary: fbVocab synthetic words, head
// words drawn far more often than the tail.
func fbWords() []string {
	w := make([]string, fbVocab)
	for i := range w {
		w[i] = fmt.Sprintf("w%04d", i)
	}
	return w
}

// fbText renders one chunk's text: 350–550 Zipfian words plus the
// planted marker terms this doc carries. The phrase flag plants
// "midterm rareterm" adjacently so a phrase query has adjacency to
// match.
func fbText(rng *rand.Rand, zipf *rand.Zipf, words []string, common, mid, rare, phrase bool) string {
	n := 350 + rng.Intn(200)
	var sb strings.Builder
	sb.Grow(n * 7)
	plant := func(term string) {
		sb.WriteString(term)
		sb.WriteByte(' ')
	}
	commonAt, midAt, rareAt := rng.Intn(n), rng.Intn(n), rng.Intn(n)
	for i := 0; i < n; i++ {
		if common && i == commonAt {
			plant("commonterm")
		}
		if phrase && i == rareAt {
			plant("midterm rareterm")
		} else {
			if mid && i == midAt {
				plant("midterm")
			}
			if rare && i == rareAt {
				plant("rareterm")
			}
		}
		sb.WriteString(words[zipf.Uint64()])
		sb.WriteByte(' ')
	}
	return sb.String()
}

// fbVec returns a unit vector: near the planted cluster centre when
// near, uniformly random otherwise.
func fbVec(rng *rand.Rand, centre []float32, near bool) []float32 {
	v := make([]float32, fbDim)
	for i := range v {
		v[i] = rng.Float32()*2 - 1
	}
	if near {
		for i := range v {
			v[i] = centre[i] + 0.35*v[i]
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// fbBuild generates one corpus: objects into the stand-in objects DB
// (mirroring the SDK's two indexes) and chunk docs into the index store,
// each with a vector.
func fbBuild(tb testing.TB, dir string, chunks, perObject int) *fbFixture {
	tb.Helper()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(int64(chunks*31 + perObject)))
	zipf := rand.NewZipf(rng, 1.15, 1, uint64(fbVocab-1))
	words := fbWords()

	objCount := chunks / perObject
	if objCount == 0 {
		objCount = 1
	}
	epoch := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	span := 2 * 365 * 24 * time.Hour
	// The recency cut keeps the newest 10%.
	recentCut := epoch.Add(time.Duration(float64(span) * 0.9))

	objs := make([]fbObject, objCount)
	for i := range objs {
		o := fbObject{id: fmt.Sprintf("obj%07d", i)}
		r, acc := rng.Float64(), 0.0
		o.typeId = fbTypes[len(fbTypes)-1].id
		for _, t := range fbTypes {
			if acc += t.p; r < acc {
				o.typeId = t.id
				break
			}
		}
		if rng.Float64() < 0.10 {
			o.collections = []string{fbBin}
		}
		o.modAt = epoch.Add(time.Duration(rng.Float64() * float64(span)))
		o.propVal = fmt.Sprintf("val%02d", rng.Intn(20))
		objs[i] = o
	}

	objDB, err := anystore.Open(ctx, filepath.Join(dir, "objects.db"), nil)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = objDB.Close() })
	objColl, err := objDB.Collection(ctx, "objects")
	if err != nil {
		tb.Fatal(err)
	}
	// The SDK's standing read-side indexes on the shared objects
	// collection (any-sync-sdk internal/spaceobjects/store.go
	// SharedObjects): dense on any.type, sparse on any.collections, dense
	// on modifiedAt. Nothing indexes property values.
	if err = objColl.EnsureIndex(ctx,
		anystore.IndexInfo{Fields: []string{"any.type"}},
		anystore.IndexInfo{Fields: []string{"any.collections"}, Sparse: true},
		anystore.IndexInfo{Fields: []string{"modifiedAt"}},
	); err != nil {
		tb.Fatal(err)
	}

	arena := &anyenc.Arena{}
	for off := 0; off < len(objs); off += 1024 {
		end := min(off+1024, len(objs))
		tx, err := objColl.WriteTx(ctx)
		if err != nil {
			tb.Fatal(err)
		}
		for _, o := range objs[off:end] {
			doc := arena.NewObject()
			doc.Set("id", arena.NewString(o.id))
			any := arena.NewObject()
			any.Set("type", arena.NewString(o.typeId))
			if len(o.collections) > 0 {
				colls := arena.NewArray()
				for i, c := range o.collections {
					colls.SetArrayItem(i, arena.NewString(c))
				}
				any.Set("collections", colls)
			}
			any.Set("name", arena.NewString("object "+o.id))
			doc.Set("any", any)
			doc.Set("modifiedAt", arena.NewDateTime(o.modAt))
			props := arena.NewObject()
			props.Set(fbPropId, arena.NewString(o.propVal))
			doc.Set(fbPropType, props)
			if err := objColl.UpsertOne(tx.Context(), doc); err != nil {
				_ = tx.Rollback()
				tb.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			tb.Fatal(err)
		}
		arena.Reset()
	}

	st, err := OpenStore(ctx, filepath.Join(dir, "index.db"), fbDim, true)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = st.Close() })

	dataset := "chat_messages"
	scope := "chat"
	if perObject > 1 {
		dataset, scope = "editor_blocks", "basic"
	}
	// rareterm lands on fbRareDocs docs spread evenly across the corpus;
	// half of them carry it adjacent to midterm (the phrase query).
	rareEvery := max(chunks/fbRareDocs, 1)
	centre := fbVec(rand.New(rand.NewSource(99)), nil, false)

	const page = 512
	docIds := make([]string, 0, page)
	ups := make([]DocUpsert, 0, page)
	vecs := make([][]float32, 0, page)
	flush := func() {
		if len(ups) == 0 {
			return
		}
		if err := st.Apply(ctx, fbSpace, ups, nil, nil); err != nil {
			tb.Fatal(err)
		}
		if err := st.SetVectors(ctx, fbSpace, docIds, vecs); err != nil {
			tb.Fatal(err)
		}
		if ok, err := st.EnsureVectorIndex(ctx, fbSpace); err != nil {
			tb.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
		}
		ups, vecs, docIds = ups[:0], vecs[:0], docIds[:0]
	}
	for i := 0; i < chunks; i++ {
		obj := objs[i%len(objs)]
		rare := i%rareEvery == 0
		phrase := rare && (i/rareEvery)%2 == 0
		near := rng.Float64() < 0.10
		e := index.IndexEntry{
			Scope:    scope,
			ObjectId: obj.id,
			Dataset:  dataset,
			RecordId: fmt.Sprintf("r%08d", i),
			Data:     fbText(rng, zipf, words, rng.Float64() < 0.50, rng.Float64() < 0.02, rare, phrase),
			ApplySeq: uint64(i + 1),
		}
		ups = append(ups, DocUpsert{Entry: e})
		docIds = append(docIds, docId(e.ObjectId, e.Dataset, e.RecordId))
		vecs = append(vecs, fbVec(rng, centre, near))
		if len(ups) == page {
			flush()
		}
	}
	flush()

	return &fbFixture{
		chunks: chunks, perObject: perObject, space: fbSpace,
		st: st, objColl: objColl, objects: objs,
		queries: fbQueries(), filters: fbFilters(),
		queryVec: fbVec(rand.New(rand.NewSource(123)), centre, true),
		docCount: chunks, recentCut: recentCut,
	}
}

// --- queries and filters ----------------------------------------------

type fbQuery struct {
	name   string
	text   string
	vector bool
}

func fbQueries() []fbQuery {
	sent := make([]string, fbSentence)
	for i := range sent {
		sent[i] = fmt.Sprintf("w%04d", i)
	}
	return []fbQuery{
		{name: "rare", text: "rareterm"},
		{name: "mid", text: "midterm"},
		{name: "common", text: "commonterm"},
		{name: "phrase", text: `"midterm rareterm"`},
		{name: "sentence", text: strings.Join(sent, " ")},
		{name: "vector", vector: true},
	}
}

type fbFilter struct {
	name    string
	indexed bool // does an objects-collection index cover it?
	build   func(f *fbFixture) query.Filter
}

// fbTypeFilter is the `any.type` equality predicate.
func fbTypeFilter(t string) query.Filter {
	return query.Key{Path: []string{"any", "type"}, Filter: query.NewComp(query.CompOpEq, t)}
}

// fbCollectionFilter is the `any.collections` membership predicate (array
// field: Key matches any element).
func fbCollectionFilter(c string) query.Filter {
	arena := &anyenc.Arena{}
	return query.Key{Path: []string{"any", "collections"}, Filter: query.NewInValue(arena.NewString(c))}
}

func fbFilters() []fbFilter {
	notBin := query.Not{Filter: fbCollectionFilter(fbBin)}
	return []fbFilter{
		{"type50", true, func(*fbFixture) query.Filter { return fbTypeFilter("typ_note") }},
		{"type10", true, func(*fbFixture) query.Filter { return fbTypeFilter("typ_task") }},
		{"type1", true, func(*fbFixture) query.Filter { return fbTypeFilter("typ_a") }},
		{"type01", true, func(*fbFixture) query.Filter { return fbTypeFilter("typ_rare") }},
		{"notbin", false, func(*fbFixture) query.Filter { return notBin }},
		{"recent", true, func(f *fbFixture) query.Filter {
			arena := &anyenc.Arena{}
			return query.Key{Path: []string{"modifiedAt"}, Filter: query.NewCompValue(query.CompOpGte, arena.NewDateTime(f.recentCut))}
		}},
		{"prop", false, func(*fbFixture) query.Filter {
			return query.Key{Path: []string{fbPropType, fbPropId}, Filter: query.NewComp(query.CompOpEq, "val07")}
		}},
		{"combo", true, func(*fbFixture) query.Filter {
			return query.And{fbTypeFilter("typ_task"), notBin}
		}},
	}
}

// --- mode implementations ---------------------------------------------

// fbRun is one execution's accounting: what each mode had to read to
// answer, and whether it reached the requested number of records.
type fbRun struct {
	rows       int // index-store rows yielded
	objQueries int // objects-store queries issued
	idsChecked int // object ids sent to the objects store
	records    int // distinct matching records reached
	idSet      int // mode 1: size of the resolved id set
	resolve    time.Duration
	capped     bool // the scan cap ended the pull
}

// fbResolveIds runs the filter on the objects collection and returns the
// matching ids — mode 1's first half.
func fbResolveIds(ctx context.Context, f *fbFixture, filter query.Filter) ([]string, error) {
	iter, err := f.objColl.Find(filter).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var ids []string
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		ids = append(ids, string(doc.Value().GetStringBytes("id")))
	}
	return ids, iter.Err()
}

// fbIdIn builds the `objectId $in [ids]` residual. any-store drops the
// index bounds of an $in at or past 10000 members (query.filter.go
// orExpressionLimit), so a big id set can only ever drive the posting
// lists — that ceiling is the crossover the table shows.
func fbIdIn(ids []string) query.Filter {
	arena := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(ids))
	for i, id := range ids {
		vals[i] = arena.NewString(id)
	}
	return query.Key{Path: []string{"objectId"}, Filter: query.NewInValue(vals...)}
}

// fbIdRanges builds the primary-key variant: an $or of per-object doc-id
// ranges (`objectId:` … `objectId;`). The bounds are ranges, not points,
// so AllBoundsFixed is false and the planner has no pk probe form for
// them (text_plan.go) — the point of measuring it.
func fbIdRanges(ids []string) query.Filter {
	or := make(query.Or, 0, len(ids))
	for _, id := range ids {
		p := id + ":"
		or = append(or, query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, p)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(p))},
		})
	}
	return or
}

// fbRow is one pulled index row, reduced to what a mode needs to decide.
type fbRow struct{ objectId, record string }

// fbPullRows pulls up to n rows off an open cursor.
func fbPullRows(iter anystore.Iterator, n int) ([]fbRow, error) {
	out := make([]fbRow, 0, n)
	for len(out) < n && iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return out, err
		}
		v := doc.Value()
		objectId := string(v.GetStringBytes("objectId"))
		out = append(out, fbRow{
			objectId: objectId,
			record:   objectId + "/" + string(v.GetStringBytes("dataset")) + "/" + string(v.GetStringBytes("recordId")),
		})
	}
	return out, iter.Err()
}

// fbPullPre pulls a pre-filtered cursor until limit distinct records are
// covered, the leg is exhausted or the scan cap is spent. Every row
// already satisfies the filter — it was a residual on the query.
func fbPullPre(ctx context.Context, coll anystore.Collection, filter query.Filter, limit int) (fbRun, error) {
	var r fbRun
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return r, err
	}
	defer iter.Close()
	seen := map[string]struct{}{}
	for len(seen) < limit && r.rows < fbScanCap {
		rows, err := fbPullRows(iter, 1)
		if err != nil {
			return r, err
		}
		if len(rows) == 0 {
			break
		}
		r.rows++
		seen[rows[0].record] = struct{}{}
	}
	r.records = len(seen)
	r.capped = r.rows >= fbScanCap && r.records < limit
	return r, iter.Err()
}

// fbPre is MODE 1: resolve the filter on the objects collection, then
// run the leg with the residual. pk selects the $or-of-ranges variant.
func fbPre(ctx context.Context, f *fbFixture, q fbQuery, filter query.Filter, limit int, pk bool) (fbRun, error) {
	t0 := time.Now()
	ids, err := fbResolveIds(ctx, f, filter)
	if err != nil {
		return fbRun{}, err
	}
	resolve := time.Since(t0)
	coll, err := f.st.spaceColl(ctx, f.space)
	if err != nil {
		return fbRun{}, err
	}
	var residual query.Filter
	if pk {
		residual = fbIdRanges(ids)
	} else {
		residual = fbIdIn(ids)
	}
	var r fbRun
	if len(ids) == 0 {
		r.idSet, r.resolve = 0, resolve
		return r, nil
	}
	if q.vector {
		r, err = fbKnnRounds(ctx, coll, f.queryVec, residual, limit, nil)
	} else {
		r, err = fbPullPre(ctx, coll, query.And{query.Text{Search: q.text}, residual}, limit)
	}
	r.idSet, r.resolve = len(ids), resolve
	return r, err
}

// fbVerdicts caches per-object filter verdicts and resolves a batch of
// unknown ids with ONE objects query (`id $in [batch] AND filter`).
type fbVerdicts struct {
	coll       anystore.Collection
	filter     query.Filter
	known      map[string]bool
	objQueries int
	idsChecked int
}

func newFbVerdicts(coll anystore.Collection, filter query.Filter) *fbVerdicts {
	return &fbVerdicts{coll: coll, filter: filter, known: map[string]bool{}}
}

// resolve judges every id in rows that has no cached verdict. One query
// per batch; ids the query doesn't return are cached as non-matching.
func (v *fbVerdicts) resolve(ctx context.Context, rows []fbRow) error {
	var unknown []string
	seen := map[string]struct{}{}
	for _, r := range rows {
		if _, ok := v.known[r.objectId]; ok {
			continue
		}
		if _, dup := seen[r.objectId]; dup {
			continue
		}
		seen[r.objectId] = struct{}{}
		unknown = append(unknown, r.objectId)
	}
	if len(unknown) == 0 {
		return nil
	}
	arena := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(unknown))
	for i, id := range unknown {
		vals[i] = arena.NewString(id)
	}
	v.objQueries++
	v.idsChecked += len(unknown)
	iter, err := v.coll.Find(query.And{
		query.Key{Path: idPath, Filter: query.NewInValue(vals...)},
		v.filter,
	}).Iter(ctx)
	if err != nil {
		return err
	}
	defer iter.Close()
	for _, id := range unknown {
		v.known[id] = false
	}
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return err
		}
		v.known[string(doc.Value().GetStringBytes("id"))] = true
	}
	return iter.Err()
}

// fbPost is MODE 2: run the leg unrestricted, pull rows in batches, and
// resolve each batch's distinct unknown object ids with one objects
// query. The index cursor stays open across that query — a different DB,
// so the two never share a reader slot.
func fbPost(ctx context.Context, f *fbFixture, q fbQuery, filter query.Filter, limit int) (fbRun, error) {
	coll, err := f.st.spaceColl(ctx, f.space)
	if err != nil {
		return fbRun{}, err
	}
	v := newFbVerdicts(f.objColl, filter)
	var r fbRun
	if q.vector {
		r, err = fbKnnRounds(ctx, coll, f.queryVec, nil, limit, v)
	} else {
		r, err = fbPullPost(ctx, coll, query.Text{Search: q.text}, limit, v)
	}
	r.objQueries, r.idsChecked = v.objQueries, v.idsChecked
	return r, err
}

// fbPullPost is mode 2's lexical pull: fbVerdictBatch rows, one verdict
// query, keep the matching rows, repeat until limit distinct matching
// records are covered, the leg is exhausted or the cap is spent.
func fbPullPost(ctx context.Context, coll anystore.Collection, filter query.Filter, limit int, v *fbVerdicts) (fbRun, error) {
	var r fbRun
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return r, err
	}
	defer iter.Close()
	seen := map[string]struct{}{}
	for len(seen) < limit && r.rows < fbScanCap {
		rows, err := fbPullRows(iter, fbVerdictBatch)
		if err != nil {
			return r, err
		}
		if len(rows) == 0 {
			break
		}
		r.rows += len(rows)
		if err := v.resolve(ctx, rows); err != nil {
			return r, err
		}
		for _, row := range rows {
			if v.known[row.objectId] {
				seen[row.record] = struct{}{}
			}
		}
	}
	r.records = len(seen)
	r.capped = r.rows >= fbScanCap && r.records < limit
	return r, iter.Err()
}

// fbKnnRounds is the vector leg: $knn has no cursor, so "more" is a
// re-query at K ×4 until the window covers limit records, the index runs
// out of reachable candidates or the cap is spent. residual is mode 1's
// `objectId $in`; v is mode 2's verdict cache (exactly one of the two).
func fbKnnRounds(ctx context.Context, coll anystore.Collection, qv []float32, residual query.Filter, limit int, v *fbVerdicts) (fbRun, error) {
	var r fbRun
	k := max(3*limit, 30)
	prevN := -1
	for {
		var filter query.Filter = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(qv, k)}
		if residual != nil {
			filter = query.And{filter, residual}
		}
		iter, err := coll.Find(filter).Iter(ctx)
		if err != nil {
			return r, err
		}
		rows, err := fbPullRows(iter, k)
		if cerr := iter.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return r, err
		}
		seen := map[string]struct{}{}
		for off := 0; off < len(rows); off += fbVerdictBatch {
			batch := rows[off:min(off+fbVerdictBatch, len(rows))]
			if v != nil {
				if err := v.resolve(ctx, batch); err != nil {
					return r, err
				}
			}
			for _, row := range batch {
				if v == nil || v.known[row.objectId] {
					seen[row.record] = struct{}{}
				}
			}
		}
		n := len(rows)
		r.rows += n
		r.records = len(seen)
		if len(seen) >= limit || (n < k && n <= prevN) || k >= fbMaxKnnK || r.rows >= fbScanCap {
			r.capped = r.records < limit
			return r, nil
		}
		prevN, k = n, min(k*4, fbMaxKnnK)
	}
}

// --- measurement ------------------------------------------------------

type fbStat struct {
	p50, p95 time.Duration
	run      fbRun
}

// fbMeasure runs fn once to warm and runs times to time, returning the
// median and the 95th percentile (the max, at the default 5 runs).
func fbMeasure(tb testing.TB, runs int, fn func() (fbRun, error)) fbStat {
	tb.Helper()
	if _, err := fn(); err != nil {
		tb.Fatal(err)
	}
	ds := make([]time.Duration, 0, runs)
	var last fbRun
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		r, err := fn()
		d := time.Since(t0)
		if err != nil {
			tb.Fatal(err)
		}
		ds = append(ds, d)
		last = r
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	p95 := ds[min(int(math.Ceil(0.95*float64(len(ds))))-1, len(ds)-1)]
	return fbStat{p50: ds[len(ds)/2], p95: p95, run: last}
}

// fbExplain runs Explain and returns the chosen plan name, its estimated
// rows and what the call itself cost — Explain is also a candidate
// request-time estimator, so its own latency matters.
func fbExplain(ctx context.Context, coll anystore.Collection, filter query.Filter) (plan string, estRows float64, d time.Duration, err error) {
	t0 := time.Now()
	ex, err := coll.Find(filter).Explain(ctx)
	d = time.Since(t0)
	if err != nil {
		return "", 0, d, err
	}
	plan, estRows = fbParsePlan(ex.Plan)
	return plan, estRows, d, nil
}

// fbParsePlan pulls the chosen candidate's name and est_rows out of the
// rich explain text ("Candidates:" lines carry `est_rows=` and the
// chosen one is tagged), falling back to the header plan name and the
// Selectivity line.
func fbParsePlan(s string) (string, float64) {
	name, est := "", 0.0
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.Contains(line, "[chosen]"):
			fields := strings.Fields(line)
			if len(fields) > 1 {
				name = fields[1]
			}
			for _, f := range fields {
				if v, ok := strings.CutPrefix(f, "est_rows="); ok {
					est, _ = strconv.ParseFloat(v, 64)
				}
			}
			return name, est
		case strings.HasPrefix(line, "Plan: "):
			name = strings.Fields(strings.TrimPrefix(line, "Plan: "))[0]
		case strings.Contains(line, "Selectivity:"):
			// "  Selectivity: 0.50 (5000 of 10000 docs)"
			if i := strings.Index(line, "("); i >= 0 {
				if fields := strings.Fields(line[i+1:]); len(fields) > 0 {
					est, _ = strconv.ParseFloat(fields[0], 64)
				}
			}
		}
	}
	return name, est
}

// --- report -----------------------------------------------------------

// fbTable is a named result grid written out as markdown and CSV.
type fbTable struct {
	name   string
	header []string
	rows   [][]string
}

func (t *fbTable) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *fbTable) markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n### %s\n\n| %s |\n|%s\n", t.name, strings.Join(t.header, " | "),
		strings.Repeat("---|", len(t.header)))
	for _, r := range t.rows {
		fmt.Fprintf(&sb, "| %s |\n", strings.Join(r, " | "))
	}
	return sb.String()
}

func fbWrite(tb testing.TB, dir, title string, tables []*fbTable) {
	tb.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	var md strings.Builder
	md.WriteString("# " + title + "\n")
	for _, t := range tables {
		md.WriteString(t.markdown())
		f, err := os.Create(filepath.Join(dir, strings.ReplaceAll(t.name, " ", "-")+".csv"))
		if err != nil {
			tb.Fatal(err)
		}
		w := csv.NewWriter(f)
		_ = w.Write(t.header)
		_ = w.WriteAll(t.rows)
		w.Flush()
		if err := f.Close(); err != nil {
			tb.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(md.String()), 0o644); err != nil {
		tb.Fatal(err)
	}
	fmt.Print(md.String())
	fmt.Printf("\nwritten to %s\n", dir)
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 2, 64)
}

func fbInts(env string, def ...int) []int {
	v := os.Getenv(env)
	if v == "" {
		return def
	}
	var out []int
	for _, s := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

// --- the harness ------------------------------------------------------

// TestFilterModesBench measures mode 1 (pre-resolve) against mode 2
// (post-filter) across corpus size, chunks per object, query selectivity,
// filter selectivity and limit — plus the request-time estimation
// primitives a mode decision could use.
func TestFilterModesBench(t *testing.T) {
	if os.Getenv("ANY_FILTER_BENCH") == "" {
		t.Skip("set ANY_FILTER_BENCH=1")
	}
	out := os.Getenv("ANY_FILTER_BENCH_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "any-filter-bench")
	}
	sizes := fbInts("ANY_FILTER_BENCH_SIZES", 20_000, 100_000)
	shapes := fbInts("ANY_FILTER_BENCH_SHAPES", 1, 20)
	runs := fbInts("ANY_FILTER_BENCH_RUNS", 5)[0]
	limits := fbInts("ANY_FILTER_BENCH_LIMITS", 10, 50)
	ctx := context.Background()

	modes := &fbTable{name: "modes", header: []string{
		"corpus", "chunks/obj", "query", "filter", "limit", "mode",
		"p50 ms", "p95 ms", "rows", "objQ", "idsChecked", "records", "capped",
		"idSet", "resolve ms", "plan", "est rows",
	}}
	est := &fbTable{name: "estimation-objects", header: []string{
		"corpus", "filter", "indexed", "count", "count ms", "capped count ms", "capped iter ms", "capped hit", "explain ms", "explain est",
	}}
	ftsEst := &fbTable{name: "estimation-fts", header: []string{
		"corpus", "query", "count", "count ms", "capped count", "capped count ms", "explain ms", "explain est", "plan", "first row ms", "drain ms", "drain rows",
	}}
	corpora := &fbTable{name: "corpora", header: []string{
		"corpus", "objects", "docs", "build s", "index MB", "fts docs", "vocab", "stats ms",
	}}

	for _, size := range sizes {
		for _, shape := range shapes {
			t0 := time.Now()
			dir := t.TempDir()
			f := fbBuild(t, dir, size, shape)
			build := time.Since(t0)
			t.Logf("built %s in %s", f.name(), build.Round(time.Millisecond))

			coll, err := f.st.spaceColl(ctx, f.space)
			if err != nil {
				t.Fatal(err)
			}
			t1 := time.Now()
			st, err := coll.Stats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			statsMs := time.Since(t1)
			ftsDocs, vocab := 0, 0
			if len(st.FtsIndexes) > 0 {
				ftsDocs, vocab = st.FtsIndexes[0].DocCount, st.FtsIndexes[0].VocabSize
			}
			corpora.add(f.name(), strconv.Itoa(len(f.objects)), strconv.Itoa(f.docCount),
				strconv.FormatFloat(build.Seconds(), 'f', 1, 64),
				strconv.Itoa(st.TotalSizeBytes/(1<<20)), strconv.Itoa(ftsDocs), strconv.Itoa(vocab), ms(statsMs))

			t2 := time.Now()
			fbEstimateObjects(t, ctx, f, est, runs)
			fbEstimateFTS(t, ctx, f, coll, ftsEst, runs)
			t.Logf("%s estimation in %s", f.name(), time.Since(t2).Round(time.Millisecond))
			t2 = time.Now()
			fbGrid(t, ctx, f, coll, modes, runs, limits, false)
			t.Logf("%s pass A in %s", f.name(), time.Since(t2).Round(time.Millisecond))

			// Pass B: the same grid for mode 1 with a secondary index on
			// objectId in the index store — does the $text planner pick a
			// probe plan off it?
			t2 = time.Now()
			if err := coll.EnsureIndex(ctx, anystore.IndexInfo{Name: "objectId", Fields: []string{"objectId"}}); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s objectId index built in %s", f.name(), time.Since(t2).Round(time.Millisecond))
			t2 = time.Now()
			fbGrid(t, ctx, f, coll, modes, runs, limits, true)
			t.Logf("%s pass B in %s", f.name(), time.Since(t2).Round(time.Millisecond))
		}
	}
	fbWrite(t, out, "Search filter modes — store-level benchmark", []*fbTable{corpora, modes, est, ftsEst})
}

// fbGrid runs every (query, filter, limit) cell. withIdIndex selects the
// pass: false = mode 1 (no objectId index), its pk-range variant and
// mode 2; true = mode 1 again, now with the index in place.
func fbGrid(t *testing.T, ctx context.Context, f *fbFixture, coll anystore.Collection, tbl *fbTable, runs int, limits []int, withIdIndex bool) {
	for _, q := range f.queries {
		for _, flt := range f.filters {
			cell := time.Now()
			filter := flt.build(f)
			ids, err := fbResolveIds(ctx, f, filter)
			if err != nil {
				t.Fatal(err)
			}
			// Which plan the residual form actually gets, and what it
			// estimates — one Explain per cell, off the timed path. The
			// $knn planner has probe forms too (KnnProbeIds /
			// KnnProbeSeek), so the vector leg is explained as well.
			explainLeg := func(limit int) (string, float64) {
				if len(ids) == 0 {
					return "", 0
				}
				var leg query.Filter = query.Text{Search: q.text}
				if q.vector {
					leg = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(f.queryVec, max(3*limit, 30))}
				}
				plan, est, _, err := fbExplain(ctx, coll, query.And{leg, fbIdIn(ids)})
				if err != nil {
					t.Fatal(err)
				}
				return plan, est
			}
			for _, limit := range limits {
				plan, estRows := explainLeg(limit)
				row := func(mode string, s fbStat, plan string, est float64) {
					tbl.add(f.name(), strconv.Itoa(f.perObject), q.name, flt.name, strconv.Itoa(limit), mode,
						ms(s.p50), ms(s.p95), strconv.Itoa(s.run.rows), strconv.Itoa(s.run.objQueries),
						strconv.Itoa(s.run.idsChecked), strconv.Itoa(s.run.records), strconv.FormatBool(s.run.capped),
						strconv.Itoa(s.run.idSet), ms(s.run.resolve), plan,
						strconv.FormatFloat(est, 'f', 0, 64))
				}
				name := "pre"
				if withIdIndex {
					name = "pre_idx"
				}
				row(name, fbMeasure(t, runs, func() (fbRun, error) {
					return fbPre(ctx, f, q, filter, limit, false)
				}), plan, estRows)

				if withIdIndex {
					continue
				}
				if len(ids) > 0 && len(ids) <= fbPkRangeMax {
					var leg query.Filter = query.Text{Search: q.text}
					if q.vector {
						leg = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(f.queryVec, max(3*limit, 30))}
					}
					pkPlan, pkEst, _, err := fbExplain(ctx, coll, query.And{leg, fbIdRanges(ids)})
					if err != nil {
						t.Fatal(err)
					}
					row("pre_pk", fbMeasure(t, runs, func() (fbRun, error) {
						return fbPre(ctx, f, q, filter, limit, true)
					}), pkPlan, pkEst)
				}
				var postLeg query.Filter = query.Text{Search: q.text}
				if q.vector {
					postLeg = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(f.queryVec, max(3*limit, 30))}
				}
				postPlan, postEst, _, err := fbExplain(ctx, coll, postLeg)
				if err != nil {
					t.Fatal(err)
				}
				row("post", fbMeasure(t, runs, func() (fbRun, error) {
					return fbPost(ctx, f, q, filter, limit)
				}), postPlan, postEst)
			}
			t.Logf("%s %s/%s ids=%d in %s", f.name(), q.name, flt.name, len(ids), time.Since(cell).Round(time.Millisecond))
		}
	}
}

// fbEstimateObjects prices the objects-side selectivity probes a mode
// decision could take: an exact Count, a capped "more than N?" iterate,
// and the CBO's own estimate via Explain.
func fbEstimateObjects(t *testing.T, ctx context.Context, f *fbFixture, tbl *fbTable, runs int) {
	for _, flt := range f.filters {
		filter := flt.build(f)
		var count int
		cnt := fbMeasure(t, runs, func() (fbRun, error) {
			n, err := f.objColl.Find(filter).Count(ctx)
			count = n
			return fbRun{}, err
		})
		// Two capped "is it more than N?" probes: any-store's Count honors
		// Limit (countPlanRoot → LimitIter.CountDistinct), so it can stop
		// early without fetching documents; the iterate form fetches.
		var cappedHit int
		cappedCnt := fbMeasure(t, runs, func() (fbRun, error) {
			n, err := f.objColl.Find(filter).Limit(uint(fbCapN + 1)).Count(ctx)
			cappedHit = n
			return fbRun{}, err
		})
		capped := fbMeasure(t, runs, func() (fbRun, error) {
			iter, err := f.objColl.Find(filter).Limit(uint(fbCapN + 1)).Iter(ctx)
			if err != nil {
				return fbRun{}, err
			}
			defer iter.Close()
			n := 0
			for iter.Next() {
				n++
			}
			return fbRun{}, iter.Err()
		})
		var estRows float64
		var plan string
		ex := fbMeasure(t, runs, func() (fbRun, error) {
			var err error
			plan, estRows, _, err = fbExplain(ctx, f.objColl, filter)
			return fbRun{}, err
		})
		_ = plan
		tbl.add(f.name(), flt.name, strconv.FormatBool(flt.indexed), strconv.Itoa(count), ms(cnt.p50),
			ms(cappedCnt.p50), ms(capped.p50), strconv.Itoa(cappedHit), ms(ex.p50),
			strconv.FormatFloat(estRows, 'f', 0, 64))
	}
}

// fbEstimateFTS prices the lexical-side candidate-count probes: an exact
// Count over the $text predicate, Explain's estimate (which reads exact
// per-term df), the time to the FIRST row (any-store ranks every match
// before it, so it is a candidate proxy for match count) and the full
// drain.
func fbEstimateFTS(t *testing.T, ctx context.Context, f *fbFixture, coll anystore.Collection, tbl *fbTable, runs int) {
	for _, q := range f.queries {
		if q.vector {
			continue
		}
		text := query.Text{Search: q.text}
		var count int
		cnt := fbMeasure(t, runs, func() (fbRun, error) {
			n, err := coll.Find(text).Count(ctx)
			count = n
			return fbRun{}, err
		})
		// Does a capped Count short-circuit the BM25 accumulation? (The
		// count plan drops rank mode, so LimitIter.CountDistinct may stop
		// at N — a candidate "is this query broad?" probe.)
		var cappedHit int
		cappedCnt := fbMeasure(t, runs, func() (fbRun, error) {
			n, err := coll.Find(text).Limit(uint(fbCapN + 1)).Count(ctx)
			cappedHit = n
			return fbRun{}, err
		})
		var estRows float64
		var plan string
		ex := fbMeasure(t, runs, func() (fbRun, error) {
			var err error
			plan, estRows, _, err = fbExplain(ctx, coll, text)
			return fbRun{}, err
		})
		first := fbMeasure(t, runs, func() (fbRun, error) {
			iter, err := coll.Find(text).Iter(ctx)
			if err != nil {
				return fbRun{}, err
			}
			defer iter.Close()
			if iter.Next() {
				if _, err := iter.Doc(); err != nil {
					return fbRun{}, err
				}
			}
			return fbRun{}, iter.Err()
		})
		var drained int
		drain := fbMeasure(t, runs, func() (fbRun, error) {
			iter, err := coll.Find(text).Iter(ctx)
			if err != nil {
				return fbRun{}, err
			}
			defer iter.Close()
			n := 0
			for iter.Next() {
				if _, err := iter.Doc(); err != nil {
					return fbRun{}, err
				}
				n++
			}
			drained = n
			return fbRun{}, iter.Err()
		})
		tbl.add(f.name(), q.name, strconv.Itoa(count), ms(cnt.p50),
			strconv.Itoa(cappedHit), ms(cappedCnt.p50), ms(ex.p50),
			strconv.FormatFloat(estRows, 'f', 0, 64), plan, ms(first.p50), ms(drain.p50), strconv.Itoa(drained))
	}
}

// --- real data --------------------------------------------------------

// The same grid against a COPY of a running server's two databases: the
// indexer's index.db (the hits) and the SDK's sdk.db (the host object
// rows, in the `<spaceId>_objects` collection). Point it only at copies
// — both stores are opened read-write (spaceColl ensures its indexes,
// pass B adds the objectId index), and a running server holds the live
// files.
//
// Run:
//
//	ANY_FILTER_BENCH_REAL_INDEX=<copy>/index.db \
//	ANY_FILTER_BENCH_REAL_SDK=<copy>/sdk.db \
//	ANY_FILTER_BENCH_REAL_SPACE=<spaceId> \
//	ANY_FILTER_BENCH_REAL_FILTERS=<spec>.json \
//	ANY_FILTER_BENCH_OUT=<report dir> \
//	go test -tags llamacpp -run TestFilterModesBenchReal -v -count=1 \
//		-timeout 180m ./internal/indexer/
//
// The spec file carries both the queries and the filters, so every
// space-specific id lives there and never in this file:
//
//	{"queries": [{"name": "engine", "text": "engine failure"},
//	             {"name": "vector", "vector": true}],
//	 "filters": [{"name": "type70", "indexed": true,
//	              "filter": {"any.type": "<typeId>"}}]}
//
// Filters are the /objects/query grammar (query.ParseCondition, so
// {"$date": …} literals work); `indexed` is documentation — it says
// whether an objects-collection index covers the filter and the
// estimation table prints it back. A "vector": true query runs the knn
// leg against a SAMPLED DOCUMENT vector (fbRealVector): the embedder
// isn't in this process, so a real query embedding is out of reach and
// the leg measures mechanics (widening rounds, residual and verdict
// cost), not relevance. It is dropped when the copy carries no vector
// index or no embedded doc.

// fbObjectsCollection mirrors the SDK's per-space shared objects
// collection name (spaceobjects.SpaceObjectsCollection): the host rows
// a search filter is evaluated against live in `<spaceId>_objects`.
const fbObjectsCollection = "objects"

// fbRealSpec is the JSON driving the real mode.
type fbRealSpec struct {
	Queries []struct {
		Name   string `json:"name"`
		Text   string `json:"text"`
		Vector bool   `json:"vector"`
	} `json:"queries"`
	Filters []struct {
		Name    string          `json:"name"`
		Indexed bool            `json:"indexed"`
		Filter  json.RawMessage `json:"filter"`
	} `json:"filters"`
}

// fbOpenReal builds a fixture over the two copies named by the
// environment. The index store is opened through OpenStore — not bare
// anystore.Open — so the schema/dim meta check runs: a db this build
// cannot read must fail here rather than answer wrong. dim 0 adopts
// whatever the db was built with.
func fbOpenReal(tb testing.TB, ctx context.Context) *fbFixture {
	tb.Helper()
	idxPath := os.Getenv("ANY_FILTER_BENCH_REAL_INDEX")
	sdkPath := os.Getenv("ANY_FILTER_BENCH_REAL_SDK")
	space := os.Getenv("ANY_FILTER_BENCH_REAL_SPACE")
	specPath := os.Getenv("ANY_FILTER_BENCH_REAL_FILTERS")
	// The harness opens both files read-write (the objectId index is
	// ensured on open): refuse anything that sits inside a live data
	// dir — <root>/<account>/{index,sdk}/*.db next to server.lock.
	for _, p := range []string{idxPath, sdkPath} {
		if lock := filepath.Join(filepath.Dir(filepath.Dir(p)), "server.lock"); fileExists(lock) {
			tb.Fatalf("%s sits in a data dir (%s exists): run the harness on copies", p, lock)
		}
	}
	for name, v := range map[string]string{"SDK": sdkPath, "SPACE": space, "FILTERS": specPath} {
		if v == "" {
			tb.Fatalf("ANY_FILTER_BENCH_REAL_%s is required", name)
		}
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		tb.Fatal(err)
	}
	var spec fbRealSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		tb.Fatalf("spec: %v", err)
	}
	if len(spec.Queries) == 0 || len(spec.Filters) == 0 {
		tb.Fatal("spec needs both queries and filters")
	}

	st, err := OpenStore(ctx, idxPath, 0, true)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = st.Close() })
	objDB, err := anystore.Open(ctx, sdkPath, nil)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = objDB.Close() })
	// OpenCollection, not Collection: a wrong space id must fail here,
	// never create an empty collection and benchmark against nothing.
	objColl, err := objDB.OpenCollection(ctx, space+"_"+fbObjectsCollection)
	if err != nil {
		tb.Fatalf("open %s_%s: %v", space, fbObjectsCollection, err)
	}

	f := &fbFixture{label: "real", space: space, st: st, objColl: objColl}
	for _, fs := range spec.Filters {
		flt, err := query.ParseCondition(string(fs.Filter))
		if err != nil {
			tb.Fatalf("filter %s: %v", fs.Name, err)
		}
		f.filters = append(f.filters, fbFilter{
			name: fs.Name, indexed: fs.Indexed,
			build: func(*fbFixture) query.Filter { return flt },
		})
	}
	coll, err := f.st.spaceColl(ctx, space)
	if err != nil {
		tb.Fatal(err)
	}
	if f.docCount, err = coll.Count(ctx); err != nil {
		tb.Fatal(err)
	}
	objects, err := objColl.Count(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	f.chunks = f.docCount
	if objects > 0 {
		f.perObject = int(math.Round(float64(f.docCount) / float64(objects)))
	}
	f.queryVec = fbRealVector(tb, ctx, coll)
	for _, qs := range spec.Queries {
		if qs.Vector && f.queryVec == nil {
			tb.Logf("no embedded doc — skipping vector query %q", qs.Name)
			continue
		}
		f.queries = append(f.queries, fbQuery{name: qs.Name, text: qs.Text, vector: qs.Vector})
	}
	if len(f.queries) == 0 {
		tb.Fatal("no runnable query in the spec")
	}
	return f
}

// fbRealVector samples one embedded document's vector to stand in for a
// query embedding. nil when the collection has no vector index or no
// live vector in it — the caller then drops the vector queries.
func fbRealVector(tb testing.TB, ctx context.Context, coll anystore.Collection) []float32 {
	tb.Helper()
	st, err := coll.Stats(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	live := 0
	for _, vi := range st.VectorIndexes {
		live += vi.LiveCount
	}
	if live == 0 {
		return nil
	}
	iter, err := coll.Find(query.Key{Path: []string{"vector"}, Filter: query.Exists{}}).Limit(1).Iter(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	defer iter.Close()
	if !iter.Next() {
		return nil
	}
	doc, err := iter.Doc()
	if err != nil {
		tb.Fatal(err)
	}
	// Copied out: the document's buffer is reused by the next Next().
	return append([]float32(nil), doc.Value().GetVectorF32("vector")...)
}

// fbRealFacts describes the copy: per-dataset doc counts (with how many
// still await embedding and how many carry a vector), the store-level
// totals, and the index list of BOTH collections as found — the shape a
// mode decision would actually meet in production.
func fbRealFacts(tb testing.TB, ctx context.Context, f *fbFixture) []*fbTable {
	tb.Helper()
	coll, err := f.st.spaceColl(ctx, f.space)
	if err != nil {
		tb.Fatal(err)
	}
	docs := fbDatasetCounts(tb, ctx, coll, "")
	embedded := fbDatasetCounts(tb, ctx, coll, `{"$match":{"vector":{"$exists":true}}},`)

	corpus := &fbTable{name: "real-corpus", header: []string{"dataset", "scope", "docs", "pending", "embedded"}}
	keys := make([]string, 0, len(docs))
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return docs[keys[i]].docs > docs[keys[j]].docs })
	for _, k := range keys {
		d := docs[k]
		corpus.add(d.dataset, d.scope, strconv.Itoa(d.docs), strconv.Itoa(d.pending), strconv.Itoa(embedded[k].docs))
	}

	cs, err := coll.Stats(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	objects, err := f.objColl.Count(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	store := &fbTable{name: "real-store", header: []string{"fact", "value"}}
	store.add("space", f.space)
	store.add("index docs", strconv.Itoa(f.docCount))
	store.add("objects rows", strconv.Itoa(objects))
	store.add("docs per object (mean)", strconv.Itoa(f.perObject))
	store.add("index total size MB", strconv.Itoa(cs.TotalSizeBytes/(1<<20)))
	for _, fi := range cs.FtsIndexes {
		store.add("fts "+fi.Name+" docs", strconv.Itoa(fi.DocCount))
		store.add("fts "+fi.Name+" vocab", strconv.Itoa(fi.VocabSize))
		store.add("fts "+fi.Name+" avg doc len", strconv.FormatFloat(fi.AvgDocLen, 'f', 1, 64))
	}
	for _, vi := range cs.VectorIndexes {
		store.add("vector "+vi.Name+" live", strconv.Itoa(vi.LiveCount))
		store.add("vector "+vi.Name+" dim/metric/mode", fmt.Sprintf("%d/%s/%s", vi.Dim, vi.Metric, vi.Mode))
	}
	store.add("query vector", map[bool]string{true: "sampled document vector", false: "none — vector queries skipped"}[f.queryVec != nil])

	idx := &fbTable{name: "real-indexes", header: []string{"collection", "index", "fields", "kind", "sparse", "unique", "entries"}}
	for _, c := range []struct {
		label string
		coll  anystore.Collection
	}{{"index.db " + f.space, coll}, {"sdk.db " + f.space + "_" + fbObjectsCollection, f.objColl}} {
		for _, in := range c.coll.GetIndexes() {
			info := in.Info()
			n, err := in.Len(ctx)
			if err != nil {
				tb.Fatal(err)
			}
			kind := map[anystore.IndexKind]string{
				anystore.IndexKindRange:    "range",
				anystore.IndexKindVector:   "vector",
				anystore.IndexKindFulltext: "fulltext",
			}[info.Kind]
			idx.add(c.label, info.Name, strings.Join(info.Fields, "+"), kind,
				strconv.FormatBool(info.Sparse), strconv.FormatBool(info.Unique), strconv.Itoa(n))
		}
	}
	return []*fbTable{corpus, store, idx}
}

// fbDatasetCount is one (dataset, scope) row of the corpus table.
type fbDatasetCount struct {
	dataset, scope string
	docs, pending  int
}

// fbDatasetCounts groups the index collection by (dataset, scope).
// match is an optional leading pipeline stage, comma included.
func fbDatasetCounts(tb testing.TB, ctx context.Context, coll anystore.Collection, match string) map[string]fbDatasetCount {
	tb.Helper()
	pipeline := "[" + match + `{"$group":{"_id":{"dataset":"$dataset","scope":"$scope"},` +
		`"docs":{"$sum":1},"pending":{"$sum":"$pending"}}}]`
	iter, err := coll.Aggregate(pipeline).Iter(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	defer iter.Close()
	out := map[string]fbDatasetCount{}
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			tb.Fatal(err)
		}
		v := doc.Value()
		r := fbDatasetCount{
			dataset: string(v.GetStringBytes("id", "dataset")),
			scope:   string(v.GetStringBytes("id", "scope")),
			docs:    v.GetInt("docs"),
			pending: v.GetInt("pending"),
		}
		out[r.dataset+"|"+r.scope] = r
	}
	if err := iter.Err(); err != nil {
		tb.Fatal(err)
	}
	return out
}

// TestFilterModesBenchReal is TestFilterModesBench's grid over a real
// space: same modes, same metrics, same estimation probes, with the
// corpus, the queries and the filters coming from the copy and the spec
// file instead of the generator.
func TestFilterModesBenchReal(t *testing.T) {
	if os.Getenv("ANY_FILTER_BENCH_REAL_INDEX") == "" {
		t.Skip("set ANY_FILTER_BENCH_REAL_INDEX (plus _SDK, _SPACE, _FILTERS)")
	}
	ctx := context.Background()
	out := os.Getenv("ANY_FILTER_BENCH_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "any-filter-bench-real")
	}
	runs := fbInts("ANY_FILTER_BENCH_RUNS", 5)[0]
	limits := fbInts("ANY_FILTER_BENCH_LIMITS", 10, 50)

	f := fbOpenReal(t, ctx)
	t.Logf("real corpus: %d index docs, ~%d per object, %d queries × %d filters × %d limits",
		f.docCount, f.perObject, len(f.queries), len(f.filters), len(limits))
	facts := fbRealFacts(t, ctx, f)

	modes := &fbTable{name: "modes", header: []string{
		"corpus", "chunks/obj", "query", "filter", "limit", "mode",
		"p50 ms", "p95 ms", "rows", "objQ", "idsChecked", "records", "capped",
		"idSet", "resolve ms", "plan", "est rows",
	}}
	est := &fbTable{name: "estimation-objects", header: []string{
		"corpus", "filter", "indexed", "count", "count ms", "capped count ms", "capped iter ms", "capped hit", "explain ms", "explain est",
	}}
	ftsEst := &fbTable{name: "estimation-fts", header: []string{
		"corpus", "query", "count", "count ms", "capped count", "capped count ms", "explain ms", "explain est", "plan", "first row ms", "drain ms", "drain rows",
	}}

	e2e := &fbTable{name: "end-to-end", header: []string{
		"corpus", "query", "filter", "limit", "mode", "strategy",
		"p50 ms", "p95 ms", "rows", "objQ", "idsChecked", "records", "capped",
		"idSet", "resolve ms", "fts hits", "vec hits", "from fts", "from vec", "from both",
		"fts plan", "vec plan", "truncated", "probe",
	}}

	coll, err := f.st.spaceColl(ctx, f.space)
	if err != nil {
		t.Fatal(err)
	}
	tables := append(facts, modes, est, ftsEst)

	// The per-leg passes (A and B) are the original grid; skip them with
	// ANY_FILTER_BENCH_REAL_LEGS=0 to measure only whole requests.
	if os.Getenv("ANY_FILTER_BENCH_REAL_LEGS") != "0" {
		t0 := time.Now()
		fbEstimateObjects(t, ctx, f, est, runs)
		fbEstimateFTS(t, ctx, f, coll, ftsEst, runs)
		t.Logf("estimation in %s", time.Since(t0).Round(time.Millisecond))

		t0 = time.Now()
		fbGrid(t, ctx, f, coll, modes, runs, limits, false)
		t.Logf("pass A in %s", time.Since(t0).Round(time.Millisecond))
	}

	// The objectId secondary index — on the COPY. Pass B re-runs mode 1
	// on it (does the $text / $knn planner pick a probe plan?); the
	// end-to-end pass needs it too, since pre_idx is the only mode-1
	// variant a request-time implementation would use.
	t0 := time.Now()
	if err := coll.EnsureIndex(ctx, anystore.IndexInfo{Name: "objectId", Fields: []string{"objectId"}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("objectId index built in %s", time.Since(t0).Round(time.Millisecond))

	if os.Getenv("ANY_FILTER_BENCH_REAL_LEGS") != "0" {
		t0 = time.Now()
		fbGrid(t, ctx, f, coll, modes, runs, limits, true)
		t.Logf("pass B in %s", time.Since(t0).Round(time.Millisecond))
	}

	// Pass C: whole requests. Query vectors are embedded once, before
	// any timing, and cached on the fixture.
	if os.Getenv("ANY_FILTER_BENCH_REAL_E2E") != "0" {
		t0 = time.Now()
		embed := fbEmbedQueries(t, ctx, f, runs)
		t.Logf("query embedding in %s", time.Since(t0).Round(time.Millisecond))
		modeList := strings.Split(os.Getenv("ANY_FILTER_BENCH_REAL_MODES"), ",")
		if len(modeList) == 1 && modeList[0] == "" {
			modeList = []string{"hybrid", "fts", "vector"}
		}
		t0 = time.Now()
		fbE2EGrid(t, ctx, f, coll, e2e, runs, limits, modeList)
		t.Logf("pass C (end-to-end) in %s", time.Since(t0).Round(time.Millisecond))
		tables = append(tables, embed, e2e)
	}

	fbWrite(t, out, "Search filter modes — real space", tables)
}

// --- end-to-end request measurement -----------------------------------

// The per-leg grid above prices ONE leg under a filter. This section
// prices what a client actually calls: a whole `/search` request —
// hybrid / fts / vector — assembled from the package's own pieces
// (ftsLeg's cover loop, vectorLeg's widening rule, fuseRRF, groupHits)
// under each filter strategy, plus the unfiltered baseline.
//
// Cover rule is Indexer.Search's, verbatim: fetch = clamp(3·limit, 30,
// 100), the lexical leg covers 2·limit records, the ANN leg covers
// limit. Search-time options are the server's defaults for a config
// that sets none of them (the bench server's): stop-word stripping ON
// (except when the query carries a quote), OR-combined terms, RRF
// weights 1/1, no adaptive weighting, similarity floor 0.
//
// Strategies:
//
//	pre_idx — resolve the filter on the objects collection ONCE per
//	  request and hand the same `objectId $in [ids]` residual to both
//	  legs (measured with the objectId index in place, so the planner
//	  has its probe forms).
//	post    — legs run unrestricted; rows are judged in batches of 64
//	  against ONE verdict cache shared by both legs, so an object the
//	  vector leg already judged costs the lexical leg nothing.
//	none    — no filter at all: the baseline every filtered number is
//	  read against.
const (
	fbStratPre     = "pre_idx"
	fbStratPost    = "post"
	fbStratNone    = "none"
	fbStratProduct = "product"
	// fbE2EStopWords mirrors the server default (cfg.Search.StopWords
	// nil ⇒ on): the lexical leg strips stop words unless the query
	// carries a quote.
	fbE2EStopWords = true
)

// fbE2ERun is one request's accounting: the per-leg counters plus what
// the reply was made of.
type fbE2ERun struct {
	fbRun
	ftsHits, vecHits           int  // rows each leg kept (post: after verdicts)
	fromFts, fromVec, fromBoth int  // which leg produced each returned record
	truncated                  bool // product path: the reply's own flag
	probeIds                   int  // product path: ids the probe found
	probeMore                  bool // product path: the set continued past the probe bound
}

// fbFtsLegE2E is ftsLeg's cover loop with a filter strategy bolted on:
// `residual` restricts the query itself (mode 1), `v` judges pulled rows
// against the objects store in batches (mode 2). Exactly one is set;
// both nil is the unfiltered baseline.
func fbFtsLegE2E(ctx context.Context, coll anystore.Collection, text string, residual query.Filter, v *fbVerdicts, cover legCover, r *fbE2ERun) ([]Hit, error) {
	var filter query.Filter = query.Text{Search: text}
	if residual != nil {
		filter = query.And{filter, residual}
	}
	iter, err := coll.Find(filter).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var hits []Hit
	seen := map[string]struct{}{}
	pending := make([]Hit, 0, fbVerdictBatch)
	rows := 0
	keep := func(h Hit) {
		hits = append(hits, h)
		seen[groupKey(h)] = struct{}{}
	}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batch := make([]fbRow, len(pending))
		for i, h := range pending {
			batch[i] = fbRow{objectId: h.ObjectId}
		}
		if err := v.resolve(ctx, batch); err != nil {
			return err
		}
		for _, h := range pending {
			if v.known[h.ObjectId] {
				keep(h)
			}
		}
		pending = pending[:0]
		return nil
	}
	for len(hits) < maxLegFetch && !cover.covered(hits) && rows < fbScanCap {
		if !iter.Next() {
			break
		}
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		rows++
		h := hitFromDoc(doc.Value(), iter.Score())
		if v == nil {
			keep(h)
			continue
		}
		pending = append(pending, h)
		if len(pending) == fbVerdictBatch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if v != nil {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	r.rows += rows
	if rows >= fbScanCap && !cover.covered(hits) {
		r.capped = true
	}
	return hits, iter.Err()
}

// fbVecLegE2E is vectorLeg's widening loop under the same two
// strategies. `scoped` is passed as "a residual is present": any-store
// sizes the ANN candidate beam from K when one is, which is exactly the
// case vectorStop's scoped arm exists for.
func fbVecLegE2E(ctx context.Context, coll anystore.Collection, qv []float32, residual query.Filter, v *fbVerdicts, cover legCover, r *fbE2ERun) ([]Hit, error) {
	k, prevN := cover.fetch, -1
	for {
		var filter query.Filter = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(qv, k)}
		if residual != nil {
			filter = query.And{filter, residual}
		}
		iter, err := coll.Find(filter).Iter(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := collectHits(iter, func(it anystore.Iterator) float64 { return 1 - float64(it.Distance()) })
		if cerr := iter.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		n := len(raw)
		r.rows += n
		// Similarity floor 0 (the server default): non-positive cosine
		// carries no signal.
		kept := make([]Hit, 0, len(raw))
		for _, h := range raw {
			if h.Score > 0 {
				kept = append(kept, h)
			}
		}
		floorDropped := len(kept) < n
		if v != nil {
			passed := kept[:0]
			for off := 0; off < len(kept); off += fbVerdictBatch {
				batch := kept[off:min(off+fbVerdictBatch, len(kept))]
				rows := make([]fbRow, len(batch))
				for i, h := range batch {
					rows[i] = fbRow{objectId: h.ObjectId}
				}
				if err := v.resolve(ctx, rows); err != nil {
					return nil, err
				}
				for _, h := range batch {
					if v.known[h.ObjectId] {
						passed = append(passed, h)
					}
				}
			}
			kept = passed
		}
		if vectorStop(cover, kept, n, k, prevN, floorDropped, residual != nil) || r.rows >= fbScanCap {
			if !cover.covered(kept) && r.rows >= fbScanCap {
				r.capped = true
			}
			return kept, nil
		}
		k, prevN = min(k*4, maxLegFetch), n
	}
}

// fbE2ESearch runs one whole request: legs, fusion, grouping — the
// shape Indexer.Search has, with the filter strategy as the variable.
func fbE2ESearch(ctx context.Context, f *fbFixture, q fbQuery, filter query.Filter, mode, strat string, limit int) (fbE2ERun, error) {
	var r fbE2ERun
	coll, err := f.st.spaceColl(ctx, f.space)
	if err != nil {
		return r, err
	}
	fetch := min(max(limit*3, 30), 100)
	ftsCover := legCover{fetch: fetch, groups: 2 * limit}
	vecCover := legCover{fetch: fetch, groups: limit}

	var residual query.Filter
	var verdicts *fbVerdicts
	switch strat {
	case fbStratPre:
		t0 := time.Now()
		ids, err := fbResolveIds(ctx, f, filter)
		if err != nil {
			return r, err
		}
		r.resolve, r.idSet = time.Since(t0), len(ids)
		if len(ids) == 0 {
			return r, nil // no host object can match: the answer is empty
		}
		residual = fbIdIn(ids)
	case fbStratPost:
		verdicts = newFbVerdicts(f.objColl, filter)
	}

	var ftsHits, vecHits []Hit
	// Vector first, then the lexical cursor — Search's order, so no
	// iterator is held across another store call.
	if mode == "vector" || mode == "hybrid" {
		qv := f.vectorFor(q)
		if qv == nil {
			return r, fmt.Errorf("query %q has no vector: %s needs one", q.name, mode)
		}
		if vecHits, err = fbVecLegE2E(ctx, coll, qv, residual, verdicts, vecCover, &r); err != nil {
			return r, err
		}
	}
	if mode == "fts" || mode == "hybrid" {
		text := q.text
		if fbE2EStopWords && !strings.Contains(text, `"`) {
			text = stripStopWords(text)
		}
		if ftsHits, err = fbFtsLegE2E(ctx, coll, text, residual, verdicts, ftsCover, &r); err != nil {
			return r, err
		}
	}

	var fused []Hit
	switch mode {
	case "hybrid":
		fused = fuseRRF([][]Hit{ftsHits, vecHits}, nil, 0) // weights 1/1 (server default)
	case "fts":
		fused = ftsHits
	case "vector":
		fused = vecHits
	}
	groups := groupHits(fused, limit, 0)
	r.records = len(groups)
	r.ftsHits, r.vecHits = len(ftsHits), len(vecHits)
	if verdicts != nil {
		r.objQueries, r.idsChecked = verdicts.objQueries, verdicts.idsChecked
	}
	if r.records >= limit {
		r.capped = false // the window was covered despite any cap
	}

	inFts, inVec := map[string]bool{}, map[string]bool{}
	for _, h := range ftsHits {
		inFts[hitDocId(h)] = true
	}
	for _, h := range vecHits {
		inVec[hitDocId(h)] = true
	}
	for _, g := range groups {
		id := hitDocId(g.Hit)
		switch {
		case inFts[id] && inVec[id]:
			r.fromBoth++
		case inFts[id]:
			r.fromFts++
		case inVec[id]:
			r.fromVec++
		}
	}
	return r, nil
}

// --- query embedding ---------------------------------------------------

// fbEmbedQueries embeds every query text ONCE with the package's own
// local embedder and caches the vectors on the fixture, so no mode's
// timing ever contains an embedding. The embedder is the in-process
// `Local` — the same decoder the `any run embedder` child runs, minus
// the child: NewLocal takes the model path and the llama.cpp lib dir
// straight from the environment, so the test needs no server binary and
// nothing is re-exec'd. It is closed as soon as the vectors are cached,
// so the model does not hold memory or threads during the timed passes.
//
// Env: ANY_FILTER_BENCH_REAL_MODEL (GGUF path; absent = skip, the
// vector legs then fall back to the sampled document vector),
// ANY_FILTER_BENCH_REAL_LIBDIR (llama.cpp shared libs),
// ANY_FILTER_BENCH_REAL_THREADS (default 16), ANY_FILTER_BENCH_REAL_CTX
// (default 2048). GPU offload is off: this measures the CPU path the
// bench server runs.
func fbEmbedQueries(tb testing.TB, ctx context.Context, f *fbFixture, runs int) *fbTable {
	tb.Helper()
	tbl := &fbTable{name: "embed-queries", header: []string{
		"query", "chars", "first call ms", "p50 ms", "p95 ms", "dim",
	}}
	model := os.Getenv("ANY_FILTER_BENCH_REAL_MODEL")
	if model == "" {
		tb.Log("ANY_FILTER_BENCH_REAL_MODEL unset — query vectors fall back to the sampled document vector")
		return tbl
	}
	gpu := 0
	cfg := config.IndexLocal{
		ModelPath:   model,
		LibDir:      os.Getenv("ANY_FILTER_BENCH_REAL_LIBDIR"),
		ContextSize: fbInts("ANY_FILTER_BENCH_REAL_CTX", 2048)[0],
		Threads:     fbInts("ANY_FILTER_BENCH_REAL_THREADS", 16)[0],
		GpuLayers:   &gpu,
		BatchDocs:   1,
	}
	emb, err := benchLocalEmbedder(cfg)
	if err != nil {
		tb.Fatalf("local embedder: %v", err)
	}
	defer func() {
		if err := emb.Close(); err != nil {
			tb.Logf("close embedder: %v", err)
		}
	}()

	f.vectors = map[string][]float32{}
	for _, q := range f.queries {
		if q.text == "" {
			continue // the sampled-vector pseudo-query has no text to embed
		}
		t0 := time.Now()
		vec, err := emb.EmbedQuery(ctx, q.text)
		first := time.Since(t0)
		if err != nil {
			tb.Fatalf("embed %q: %v", q.name, err)
		}
		if dim := f.st.Dim(); dim > 0 && len(vec) != dim {
			tb.Fatalf("embed %q: dim %d, but the index db was built with %d", q.name, len(vec), dim)
		}
		f.vectors[q.name] = vec
		ds := make([]time.Duration, 0, runs)
		for i := 0; i < runs; i++ {
			t := time.Now()
			if _, err := emb.EmbedQuery(ctx, q.text); err != nil {
				tb.Fatalf("embed %q: %v", q.name, err)
			}
			ds = append(ds, time.Since(t))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		p95 := ds[min(int(math.Ceil(0.95*float64(len(ds))))-1, len(ds)-1)]
		tbl.add(q.name, strconv.Itoa(len(q.text)), ms(first), ms(ds[len(ds)/2]), ms(p95), strconv.Itoa(len(vec)))
	}
	return tbl
}

// --- the end-to-end grid -----------------------------------------------

// fbE2EGrid measures every (query, mode, filter, strategy, limit) cell,
// plus one unfiltered baseline per (query, mode, limit). It expects the
// objectId index to exist already — pre_idx is the only mode-1 variant
// here, and it is the one a request-time implementation would use.
func fbE2EGrid(t *testing.T, ctx context.Context, f *fbFixture, coll anystore.Collection, tbl *fbTable, runs int, limits []int, modes []string) {
	// The shipped algorithm, over the same Store: StopWords matches the
	// server's default (withDefaults leaves it off), the embedder hands
	// back the vectors cached before any timing.
	ix := &Indexer{store: f.st, lg: logger.NewNamed("indexer.bench"), opts: Options{
		Embedder:      fbCachedEmbedder{vecs: fbTextVectors(f), dim: f.st.Dim()},
		StopWords:     fbE2EStopWords,
		AnnounceAfter: -1,
	}.withDefaults()}
	explain := func(q fbQuery, mode string, ids []string, limit int) (string, string) {
		var ftsPlan, vecPlan string
		fetch := min(max(limit*3, 30), 100)
		residual := query.Filter(nil)
		if ids != nil {
			residual = fbIdIn(ids)
		}
		with := func(leg query.Filter) query.Filter {
			if residual == nil {
				return leg
			}
			return query.And{leg, residual}
		}
		if mode == "fts" || mode == "hybrid" {
			text := q.text
			if fbE2EStopWords && !strings.Contains(text, `"`) {
				text = stripStopWords(text)
			}
			p, _, _, err := fbExplain(ctx, coll, with(query.Text{Search: text}))
			if err != nil {
				t.Fatal(err)
			}
			ftsPlan = p
		}
		if mode == "vector" || mode == "hybrid" {
			p, _, _, err := fbExplain(ctx, coll, with(query.Key{Path: []string{"vector"}, Filter: query.NewKnn(f.vectorFor(q), fetch)}))
			if err != nil {
				t.Fatal(err)
			}
			vecPlan = p
		}
		return ftsPlan, vecPlan
	}
	row := func(q fbQuery, filterName, mode, strat string, limit int, s fbStat, e fbE2ERun, ftsPlan, vecPlan string) {
		probe := ""
		if strat == fbStratProduct && filterName != "-" {
			probe = strconv.Itoa(e.probeIds)
			if e.probeMore {
				probe += "+" // the set continued past the probe bound: the lazy path
			}
		}
		tbl.add(f.name(), q.name, filterName, strconv.Itoa(limit), mode, strat,
			ms(s.p50), ms(s.p95), strconv.Itoa(e.rows), strconv.Itoa(e.objQueries),
			strconv.Itoa(e.idsChecked), strconv.Itoa(e.records), strconv.FormatBool(e.capped),
			strconv.Itoa(e.idSet), ms(e.resolve), strconv.Itoa(e.ftsHits), strconv.Itoa(e.vecHits),
			strconv.Itoa(e.fromFts), strconv.Itoa(e.fromVec), strconv.Itoa(e.fromBoth),
			ftsPlan, vecPlan, strconv.FormatBool(e.truncated), probe)
	}
	measure := func(fn func() (fbE2ERun, error)) (fbStat, fbE2ERun) {
		var last fbE2ERun
		st := fbMeasure(t, runs, func() (fbRun, error) {
			r, err := fn()
			last = r
			return r.fbRun, err
		})
		return st, last
	}

	needsVector := func(mode string) bool { return mode == "vector" || mode == "hybrid" }
	for _, q := range f.queries {
		if q.vector && len(f.vectors) > 0 {
			continue // the sampled-vector pseudo-query is redundant once real query vectors exist
		}
		cell := time.Now()
		for _, mode := range modes {
			if needsVector(mode) && f.vectorFor(q) == nil {
				continue // no query embedding and no vector in this db
			}
			for _, limit := range limits {
				ftsPlan, vecPlan := explain(q, mode, nil, limit)
				st, last := measure(func() (fbE2ERun, error) {
					return fbE2ESearch(ctx, f, q, nil, mode, fbStratNone, limit)
				})
				row(q, "-", mode, fbStratNone, limit, st, last, ftsPlan, vecPlan)

				st, last = measure(func() (fbE2ERun, error) {
					return fbProductSearch(ctx, ix, f, q, nil, mode, limit)
				})
				row(q, "-", mode, fbStratProduct, limit, st, last, ftsPlan, vecPlan)
			}
		}
		for _, flt := range f.filters {
			filter := flt.build(f)
			ids, err := fbResolveIds(ctx, f, filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range modes {
				if needsVector(mode) && f.vectorFor(q) == nil {
					continue
				}
				for _, limit := range limits {
					ftsPlan, vecPlan := explain(q, mode, ids, limit)
					st, last := measure(func() (fbE2ERun, error) {
						return fbE2ESearch(ctx, f, q, filter, mode, fbStratPre, limit)
					})
					row(q, flt.name, mode, fbStratPre, limit, st, last, ftsPlan, vecPlan)

					ftsPlanPost, vecPlanPost := explain(q, mode, nil, limit)
					st, last = measure(func() (fbE2ERun, error) {
						return fbE2ESearch(ctx, f, q, filter, mode, fbStratPost, limit)
					})
					row(q, flt.name, mode, fbStratPost, limit, st, last, ftsPlanPost, vecPlanPost)

					host := &fbHostFilter{coll: f.objColl, cond: filter}
					probeIds, probeMore, err := fbProbe(ctx, host)
					if err != nil {
						t.Fatal(err)
					}
					st, last = measure(func() (fbE2ERun, error) {
						return fbProductSearch(ctx, ix, f, q, host, mode, limit)
					})
					last.probeIds, last.probeMore = probeIds, probeMore
					row(q, flt.name, mode, fbStratProduct, limit, st, last, "", "")
				}
			}
		}
		t.Logf("%s e2e %s in %s", f.name(), q.name, time.Since(cell).Round(time.Millisecond))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// --- the product path ---------------------------------------------------

// Strategy `product` calls the shipped algorithm — Indexer.Search with a
// HostFilter (host_filter.go) — so the grid prices what the server will
// actually run next to the two hand-rolled strategies. The Indexer is
// built over the same opened Store the way the unit tests build theirs;
// the embedder is a fixed one handing back the query vector cached by
// fbEmbedQueries, so the product's embed step costs nothing measurable
// and every mode's number stays embed-free (add the embed-queries p50
// for a real request).
//
// StopWords is set explicitly: withDefaults leaves it false, while the
// server (and the other strategies here) strip stop words on the lexical
// leg.

// fbCachedEmbedder answers from the fixture's cache — the query vectors
// were embedded once, before any timing.
type fbCachedEmbedder struct {
	vecs map[string][]float32
	dim  int
}

func (e fbCachedEmbedder) EmbedDocs(context.Context, []string) ([][]float32, error) {
	return nil, fmt.Errorf("bench embedder: docs are already embedded")
}

func (e fbCachedEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	if v, ok := e.vecs[text]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("bench embedder: no cached vector for %q", text)
}

func (e fbCachedEmbedder) Dim(context.Context) (int, error) { return e.dim, nil }

// fbTextVectors keys the cached embeddings by query TEXT, which is what
// Indexer.Search hands the embedder.
func fbTextVectors(f *fbFixture) map[string][]float32 {
	out := make(map[string][]float32, len(f.queries))
	for _, q := range f.queries {
		if v := f.vectors[q.name]; v != nil {
			out[q.text] = v
		}
	}
	return out
}

// fbHostFilter is the harness's HostFilter over the copy's objects
// collection: the two reads the product asks for. Tombstones are
// skipped (a row with `_deletedAt` is gone for readers), which is the
// one semantic the server's implementation must also carry.
type fbHostFilter struct {
	coll anystore.Collection
	cond query.Filter
	// counters, read after a run: how the product used the filter.
	resolves, matches, idsAsked int
}

func (f *fbHostFilter) Resolve(ctx context.Context, max int) (ids []string, more bool, err error) {
	f.resolves++
	iter, err := f.coll.Find(f.cond).Iter(ctx)
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, false, err
		}
		v := doc.Value()
		if v.Get("_deletedAt") != nil {
			continue
		}
		if max > 0 && len(ids) == max {
			return ids, true, iter.Err()
		}
		ids = append(ids, string(v.GetStringBytes("id")))
	}
	return ids, false, iter.Err()
}

func (f *fbHostFilter) Match(ctx context.Context, ids []string) (map[string]bool, error) {
	f.matches++
	f.idsAsked += len(ids)
	arena := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(ids))
	for i, id := range ids {
		vals[i] = arena.NewString(id)
	}
	iter, err := f.coll.Find(query.And{
		query.Key{Path: idPath, Filter: query.NewInValue(vals...)},
		f.cond,
	}).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := make(map[string]bool, len(ids))
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		v := doc.Value()
		if v.Get("_deletedAt") != nil {
			continue
		}
		out[string(v.GetStringBytes("id"))] = true
	}
	return out, iter.Err()
}

// fbProbe re-runs the product's own probe (Resolve at filterIdsMax) so
// the table can report the id set it found without reaching inside
// hostSet: the size, and whether the set continued past the bound (the
// lazy path).
func fbProbe(ctx context.Context, host *fbHostFilter) (int, bool, error) {
	if host == nil {
		return 0, false, nil
	}
	ids, more, err := host.Resolve(ctx, filterIdsMax)
	return len(ids), more, err
}

// fbProductSearch runs one request through the shipped Search and
// reports it in the harness's accounting: records is the page the
// caller gets, objQ / idsChecked are how the product used the filter,
// and truncated is the reply's own flag.
func fbProductSearch(ctx context.Context, ix *Indexer, f *fbFixture, q fbQuery, host *fbHostFilter, mode string, limit int) (fbE2ERun, error) {
	var r fbE2ERun
	var hf HostFilter
	if host != nil {
		host.resolves, host.matches, host.idsAsked = 0, 0, 0
		hf = host
	}
	res, err := ix.Search(ctx, f.space, api.SearchRequest{
		Query: q.text, Mode: mode, Limit: limit,
	}, hf)
	if err != nil {
		return r, err
	}
	r.records = len(res.Hits)
	r.truncated = res.Truncated
	if host != nil {
		r.objQueries, r.idsChecked = host.resolves+host.matches, host.idsAsked
	}
	return r, nil
}
