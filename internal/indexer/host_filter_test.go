//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// setFilter is a HostFilter over a fixed id set that counts its reads.
type setFilter struct {
	order    []string // every object id in the corpus, corpus order
	in       map[string]bool
	resolves []int // max of each Resolve call
	matches  int
	asked    map[string]int
}

func newSetFilter(order []string, in ...string) *setFilter {
	f := &setFilter{order: order, in: map[string]bool{}, asked: map[string]int{}}
	for _, id := range in {
		f.in[id] = true
	}
	return f
}

func (f *setFilter) Resolve(_ context.Context, max int) ([]string, bool, error) {
	f.resolves = append(f.resolves, max)
	var out []string
	for _, id := range f.order {
		if f.in[id] {
			out = append(out, id)
		}
	}
	if max > 0 && len(out) > max {
		return out[:max], true, nil
	}
	return out, false, nil
}

func (f *setFilter) Match(_ context.Context, ids []string) (map[string]bool, error) {
	f.matches++
	out := map[string]bool{}
	for _, id := range ids {
		f.asked[id]++
		if f.in[id] {
			out[id] = true
		}
	}
	return out, nil
}

// filterCorpus writes n single-chunk docs "o<i>" all matching "needle",
// in id order, and returns the ids.
func filterCorpus(t *testing.T, st *Store, sp string, n int) []string {
	t.Helper()
	ids := make([]string, n)
	ups := make([]DocUpsert, n)
	for i := range n {
		ids[i] = fmt.Sprintf("o%04d", i)
		ups[i] = DocUpsert{Entry: entry("basic", ids[i], "editor_blocks", "r", fmt.Sprintf("needle number %d", i), uint64(i+1))}
	}
	if err := st.Apply(context.Background(), sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
	return ids
}

func hitObjects(res api.SearchResponse) []string {
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.ObjectId)
	}
	return out
}

// filterBudgets snapshots every tunable the filter paths read.
type filterBudgets struct{ idsMax, scanRows, scanRowsMax, materializeMax, residualMax, legFetch int }

func getBudgets() filterBudgets {
	return filterBudgets{filterIdsMax, filterScanRows, filterScanRowsMax, filterMaterializeMax, filterResidualMax, maxLegFetch}
}

func applyBudgets(b filterBudgets) {
	filterIdsMax, filterScanRows, filterScanRowsMax, filterMaterializeMax, filterResidualMax, maxLegFetch =
		b.idsMax, b.scanRows, b.scanRowsMax, b.materializeMax, b.residualMax, b.legFetch
}

// setBudgets installs b for the test and restores the package values
// after it.
func setBudgets(t *testing.T, b filterBudgets) {
	t.Helper()
	prev := getBudgets()
	applyBudgets(b)
	t.Cleanup(func() { applyBudgets(prev) })
}

// budgets returns the defaults with the given overrides applied.
func budgets(mod func(*filterBudgets)) filterBudgets {
	b := filterBudgets{idsMax: 256, scanRows: 5000, scanRowsMax: 100000, materializeMax: 50000, residualMax: 9999, legFetch: 1000}
	mod(&b)
	return b
}

// A set the probe resolves whole rides the query as a residual: the
// legs read only matching rows and the filter is never asked to match.
func TestIndexer_FilterSmallSetIsResidual(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp"
	ids := filterCorpus(t, st, sp, 300)
	f := newSetFilter(ids, ids[7], ids[150], ids[299])

	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 10}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 3 {
		t.Fatalf("hits = %v, want the three set members", got)
	}
	for _, h := range res.Hits {
		if !f.in[h.ObjectId] {
			t.Fatalf("hit outside the set: %s", h.ObjectId)
		}
	}
	if len(f.resolves) != 1 || f.resolves[0] != filterIdsMax || f.matches != 0 {
		t.Fatalf("reads: resolves=%v matches=%d, want one bounded resolve and no match", f.resolves, f.matches)
	}
	if res.Truncated {
		t.Fatal("truncated set on a residual path")
	}
}

// A large set post-filters: rows are judged in batches, each object is
// asked about once, and limit counts matching records.
func TestIndexer_FilterLargeSetPostFilters(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp"
	setBudgets(t, budgets(func(b *filterBudgets) { b.idsMax = 4 }))
	ids := filterCorpus(t, st, sp, 200)
	var even []string
	for i := 0; i < len(ids); i += 2 {
		even = append(even, ids[i])
	}
	f := newSetFilter(ids, even...)

	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 10}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 10 {
		t.Fatalf("hits = %d, want limit worth of matching records", len(res.Hits))
	}
	for _, h := range res.Hits {
		if !f.in[h.ObjectId] {
			t.Fatalf("hit outside the set: %s", h.ObjectId)
		}
	}
	if len(f.resolves) != 1 || f.matches == 0 {
		t.Fatalf("reads: resolves=%v matches=%d, want the probe then batched matches", f.resolves, f.matches)
	}
	for id, n := range f.asked {
		if n != 1 {
			t.Fatalf("object %s asked %d times, want once", id, n)
		}
	}
}

// A page still short after filterScanRows rows materializes the set
// and continues on the same cursor; past filterScanRowsMax the reply
// is truncated.
func TestIndexer_FilterRescueAndTruncation(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp"
	setBudgets(t, budgets(func(b *filterBudgets) { b.idsMax, b.scanRows, b.scanRowsMax = 1, 8, 64 }))
	ids := filterCorpus(t, st, sp, 100)
	// Three members: the lexical order is BM25 over near-identical
	// texts, so pick by rank instead of by id.
	base, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ranked := hitObjects(base)
	if len(ranked) != 100 {
		t.Fatalf("baseline hits = %d", len(ranked))
	}
	f := newSetFilter(ids, ranked[20], ranked[40], ranked[60])

	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 3}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 3 {
		t.Fatalf("hits = %v, want the three members found past the lookup budget", got)
	}
	if len(f.resolves) != 2 || f.resolves[1] != filterMaterializeMax {
		t.Fatalf("resolves = %v, want the probe then a materialize", f.resolves)
	}
	if f.matches != 1 {
		t.Fatalf("matches = %d, want one batch before the materialize", f.matches)
	}
	if res.Truncated {
		t.Fatal("truncated although the members were within the read budget")
	}

	// A set past filterMaterializeMax stays lazy through the rescue: the
	// members are still found, through lookups.
	applyBudgets(budgets(func(b *filterBudgets) { b.idsMax, b.scanRows, b.scanRowsMax, b.materializeMax = 1, 8, 64, 2 }))
	f = newSetFilter(ids, ranked[20], ranked[40], ranked[60])
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 3}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 3 || res.Truncated {
		t.Fatalf("lazy past the cap: hits=%v truncated=%v", got, res.Truncated)
	}
	if len(f.resolves) != 2 || f.resolves[1] != filterMaterializeMax || f.matches < 2 {
		t.Fatalf("reads: resolves=%v matches=%d, want a capped materialize attempt then more lookups", f.resolves, f.matches)
	}
	applyBudgets(budgets(func(b *filterBudgets) { b.idsMax, b.scanRows, b.scanRowsMax = 1, 8, 64 }))

	// A member ranked past the total budget is out of reach: truncated.
	f = newSetFilter(ids, ranked[10], ranked[90])
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 2}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 1 || got[0] != ranked[10] {
		t.Fatalf("hits = %v, want only the member within the budget", got)
	}
	if !res.Truncated {
		t.Fatal("expected the truncated flag past filterScanRowsMax")
	}
}

// Hybrid shares one set between the legs: the vector leg resolves a
// lazy set up to the residual bound and both legs then ride it as a
// residual — no lookups at all — and a small set restricts the vector
// leg too.
func TestIndexer_FilterHybridSharesSet(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{Embedder: axisEmbedder{dim: 4}, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	sp := staticIdSpace{}.Id()
	setBudgets(t, budgets(func(b *filterBudgets) { b.idsMax = 4 }))
	ids := filterCorpus(t, st, sp, 60)
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	var odd []string
	for i := 1; i < len(ids); i += 2 {
		odd = append(odd, ids[i])
	}
	f := newSetFilter(ids, odd...)
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 10}, f)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != api.SearchModeHybrid || len(res.Hits) != 10 {
		t.Fatalf("mode=%s hits=%d", res.Mode, len(res.Hits))
	}
	for _, h := range res.Hits {
		if !f.in[h.ObjectId] {
			t.Fatalf("hit outside the set: %s", h.ObjectId)
		}
	}
	if len(f.resolves) != 2 || f.resolves[1] != filterResidualMax || f.matches != 0 {
		t.Fatalf("reads: resolves=%v matches=%d, want the probe, one bounded materialize, no lookup", f.resolves, f.matches)
	}

	// Past the residual bound the set stays lazy: the vector leg's
	// bounded materialize comes back short, both legs judge their rows
	// through lookups, each object once.
	applyBudgets(budgets(func(b *filterBudgets) { b.idsMax, b.residualMax = 4, 8 }))
	f = newSetFilter(ids, odd...)
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 5}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 5 || res.Truncated {
		t.Fatalf("lazy hybrid: hits=%d truncated=%v", len(res.Hits), res.Truncated)
	}
	if len(f.resolves) != 2 || f.resolves[1] != 8 || f.matches == 0 {
		t.Fatalf("reads: resolves=%v matches=%d, want the probe, one short materialize, then lookups", f.resolves, f.matches)
	}
	for id, n := range f.asked {
		if n != 1 {
			t.Fatalf("object %s asked %d times across the legs, want once", id, n)
		}
	}

	small := newSetFilter(ids, ids[1], ids[3])
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeVector, Limit: 10}, small)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 2 || small.matches != 0 {
		t.Fatalf("vector residual: hits=%v matches=%d, want the two members and no lookup", got, small.matches)
	}
}

// countingEmbedder counts query embeddings; every vector is the axis.
type countingEmbedder struct {
	axisEmbedder
	queries int
}

func (c *countingEmbedder) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	c.queries++
	return c.axisEmbedder.EmbedQuery(ctx, q)
}

// An empty set answers empty without embedding the query or opening a
// leg: no lookups, no hits, not truncated.
func TestIndexer_FilterEmptySet(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	emb := &countingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, opts: Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp"
	ids := filterCorpus(t, st, sp, 20)
	f := newSetFilter(ids)
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 10}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || res.Truncated || f.matches != 0 || emb.queries != 0 {
		t.Fatalf("empty set: hits=%d truncated=%v matches=%d embeds=%d", len(res.Hits), res.Truncated, f.matches, emb.queries)
	}
	if res.Mode != api.SearchModeHybrid || res.VectorStatus != api.VectorStatusSkipped {
		t.Fatalf("empty set reply: mode=%s vectorStatus=%s", res.Mode, res.VectorStatus)
	}
}

// rankEmbedder ranks docs by their number: doc i sits at angle i·1° from
// the query axis, so the vector order is the id order, deterministic.
type rankEmbedder struct{}

func (rankEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		var n int
		if _, err := fmt.Sscanf(text, "needle number %d", &n); err != nil {
			return nil, err
		}
		a := float64(n) * math.Pi / 180
		out[i] = []float32{float32(math.Cos(a)), float32(math.Sin(a)), 0, 0}
	}
	return out, nil
}
func (rankEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0, 0}, nil
}
func (rankEmbedder) Dim(context.Context) (int, error) { return 4, nil }

// Vector only, lazy set whose members rank past the K ceiling: the
// page comes back short and truncated; with the ceiling back the same
// request fills through lookups.
func TestIndexer_FilterVectorTruncated(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{Embedder: rankEmbedder{}, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	sp := staticIdSpace{}.Id()
	ids := filterCorpus(t, st, sp, 60)
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	deep := []string{ids[57], ids[58], ids[59]}

	setBudgets(t, budgets(func(b *filterBudgets) { b.idsMax, b.residualMax, b.legFetch = 1, 2, 32 }))
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeVector, Limit: 3}, newSetFilter(ids, deep...))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || !res.Truncated {
		t.Fatalf("vector at the ceiling: hits=%d truncated=%v, want an empty truncated page", len(res.Hits), res.Truncated)
	}

	applyBudgets(budgets(func(b *filterBudgets) { b.idsMax, b.residualMax = 1, 2 }))
	f := newSetFilter(ids, deep...)
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeVector, Limit: 3}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitObjects(res); len(got) != 3 || res.Truncated || f.matches == 0 {
		t.Fatalf("vector past the bound: hits=%v truncated=%v matches=%d", got, res.Truncated, f.matches)
	}
}

// Without an embedder hybrid degrades to fts before any leg runs: the
// filter still binds every hit, a small set rides the lexical leg as a
// residual with no lookup, a large one post-filters through lookups.
func TestIndexer_FilterWithoutEmbedder(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp"
	setBudgets(t, budgets(func(b *filterBudgets) { b.idsMax = 4 }))
	ids := filterCorpus(t, st, sp, 40)

	small := newSetFilter(ids, ids[3], ids[30])
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 10}, small)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != api.SearchModeFTS || res.VectorStatus != api.VectorStatusDisabled {
		t.Fatalf("mode=%s vectorStatus=%s, want fts/disabled", res.Mode, res.VectorStatus)
	}
	if got := hitObjects(res); len(got) != 2 || small.matches != 0 || len(small.resolves) != 1 {
		t.Fatalf("small set: hits=%v matches=%d resolves=%v", got, small.matches, small.resolves)
	}

	var even []string
	for i := 0; i < len(ids); i += 2 {
		even = append(even, ids[i])
	}
	large := newSetFilter(ids, even...)
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 10}, large)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 10 || res.Truncated || large.matches == 0 || len(large.resolves) != 1 {
		t.Fatalf("large set: hits=%d truncated=%v matches=%d resolves=%v", len(res.Hits), res.Truncated, large.matches, large.resolves)
	}
	for _, h := range res.Hits {
		if !large.in[h.ObjectId] {
			t.Fatalf("hit outside the set: %s", h.ObjectId)
		}
	}
}
