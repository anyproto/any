//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"fmt"
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

func setBudgets(t *testing.T, idsMax, scanRows, scanRowsMax int) {
	t.Helper()
	oi, os, om, oc, orr := filterIdsMax, filterScanRows, filterScanRowsMax, filterMaterializeMax, filterResidualMax
	filterIdsMax, filterScanRows, filterScanRowsMax = idsMax, scanRows, scanRowsMax
	t.Cleanup(func() {
		filterIdsMax, filterScanRows, filterScanRowsMax, filterMaterializeMax, filterResidualMax = oi, os, om, oc, orr
	})
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
	setBudgets(t, 4, 5000, 100000)
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
	setBudgets(t, 1, 8, 64)
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
	filterMaterializeMax = 2
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
	filterMaterializeMax = 50000

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
	setBudgets(t, 4, 5000, 100000)
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

	// Past the residual bound the set stays lazy: the vector leg judges
	// its rounds through lookups, each object once.
	filterResidualMax = 8
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Limit: 5}, newSetFilter(ids, odd...))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 5 {
		t.Fatalf("lazy hybrid: hits=%d", len(res.Hits))
	}
	filterResidualMax = 9999

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
