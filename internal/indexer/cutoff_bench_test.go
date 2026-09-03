//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// BenchmarkCutoff measures what any-store charges for a leg that reads
// past a fixed Limit: an FTS query opened without Limit and closed
// after N rows, and a $knn at growing K (the vector leg has no cursor —
// "more" is a re-query). The numbers size the per-leg ceiling and the
// scope-residual ef choice in Indexer.Search; results in
// docs/13-index.md § Tuning.
//
// Corpus: n short chat docs over the 18-word benchWords vocabulary (a
// one-word query matches ~half the corpus — the high-df case where the
// accumulation dominates), split over two scopes so a scope filter is a
// real residual, plus a few 17-chunk records that match every query.
// Sizes via ANY_CUTOFF_BENCH_SIZES (comma-separated, default 10000).
func BenchmarkCutoff(b *testing.B) {
	sizes := []int{10_000}
	if v := os.Getenv("ANY_CUTOFF_BENCH_SIZES"); v != "" {
		sizes = sizes[:0]
		for _, s := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				b.Fatalf("ANY_CUTOFF_BENCH_SIZES: %v", err)
			}
			sizes = append(sizes, n)
		}
	}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			st, rng := cutoffStore(b, n)
			benchCutoffFTS(b, st)
			benchCutoffKnn(b, st, rng)
			benchCutoffSearch(b, st)
		})
	}
}

const (
	cutoffDim        = 256
	cutoffLongRecs   = 5
	cutoffLongChunks = 17
	cutoffSpace      = "sp"
)

// cutoffStore builds the corpus: n short docs alternating between the
// scopes chat / basic, then cutoffLongRecs records of cutoffLongChunks
// chunks each whose text repeats the query terms. Every doc gets a
// random vector; the IVF index trains on the full set.
func cutoffStore(tb testing.TB, n int) (*Store, *rand.Rand) {
	tb.Helper()
	st, err := OpenStore(context.Background(), filepath.Join(tb.TempDir(), "index.db"), cutoffDim, true)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = st.Close() })
	rng := rand.New(rand.NewSource(7))
	ctx := context.Background()
	const page = 1024
	for off := 0; off < n; off += page {
		ups := benchUpserts(rng, off, min(page, n-off))
		for i := range ups {
			if (off+i)%2 == 1 {
				ups[i].Entry.Scope = "basic"
			}
		}
		if err := st.Apply(ctx, cutoffSpace, ups, nil, nil); err != nil {
			tb.Fatal(err)
		}
	}
	// One short record carrying the rare term the long records repeat:
	// BM25 ranks the long chunks above it (tf saturates, the short doc
	// has one occurrence), so a chunk-counting limit never shows it.
	long := []DocUpsert{{Entry: index.IndexEntry{
		Scope: "chat", ObjectId: "needle", Dataset: "chat_messages", RecordId: "needle",
		Data: "reranker notes", ApplySeq: uint64(n + cutoffLongRecs + 1),
	}}}
	for i := 0; i < cutoffLongRecs; i++ {
		e := index.IndexEntry{
			Scope:    "chat",
			ObjectId: fmt.Sprintf("long%d", i),
			Dataset:  "chat_messages",
			RecordId: fmt.Sprintf("long%d", i),
			Data:     strings.Repeat("glacier tidal reranker sourdough orbital. ", cutoffLongChunks*DefaultChunkRunes/42),
			ApplySeq: uint64(n + i + 1),
		}
		long = append(long, expandEntry(e, DefaultChunkRunes)...)
	}
	if err := st.Apply(ctx, cutoffSpace, long, nil, nil); err != nil {
		tb.Fatal(err)
	}
	total := n + len(long)
	ids, _, err := st.Pending(ctx, cutoffSpace, total)
	if err != nil || len(ids) != total {
		tb.Fatalf("pending: %v (%d/%d)", err, len(ids), total)
	}
	for off := 0; off < len(ids); off += 256 {
		end := min(off+256, len(ids))
		vecs := make([][]float32, end-off)
		for i := range vecs {
			vecs[i] = benchVec(rng, cutoffDim)
		}
		if err := st.SetVectors(ctx, cutoffSpace, ids[off:end], vecs); err != nil {
			tb.Fatal(err)
		}
	}
	if ok, err := st.EnsureVectorIndex(ctx, cutoffSpace); err != nil || !ok {
		tb.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}
	return st, rng
}

// pullHits materializes up to n rows from iter the way collectHits does
// (every field copied out), then closes it — the leg's real per-row
// cost, not a bare Next loop.
//
// It fails the test on error, so it belongs on the test goroutine only:
// tb.Fatal off it merely Goexits that goroutine, which turns a real
// error into whatever the caller does when it never hears back. Use
// pullHitsErr from a spawned goroutine.
func pullHits(tb testing.TB, iter anystore.Iterator, n int) int {
	tb.Helper()
	got, err := pullHitsErr(iter, n)
	if err != nil {
		tb.Fatal(err)
	}
	return got
}

// pullHitsErr is pullHits with the error returned instead of fatal.
func pullHitsErr(iter anystore.Iterator, n int) (int, error) {
	defer iter.Close()
	got := 0
	for (n <= 0 || got < n) && iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return got, err
		}
		v := doc.Value()
		_ = Hit{
			Scope:    string(v.GetStringBytes("scope")),
			ObjectId: string(v.GetStringBytes("objectId")),
			Dataset:  string(v.GetStringBytes("dataset")),
			RecordId: string(v.GetStringBytes("recordId")),
			Chunk:    v.GetInt("chunk"),
			Data:     string(v.GetStringBytes("data")),
		}
		got++
	}
	return got, iter.Err()
}

// benchCutoffFTS: one high-df term. limit=30/drain is today's leg;
// nolimit/close@N opens without Limit and stops after N rows — the
// lazy leg. drain reads every matched doc (the ceiling-less worst case).
func benchCutoffFTS(b *testing.B, st *Store) {
	ctx := context.Background()
	coll, err := st.spaceColl(ctx, cutoffSpace)
	if err != nil {
		b.Fatal(err)
	}
	text := query.Text{Search: "glacier"}
	cases := []struct {
		name  string
		limit int // any-store Limit, 0 = none
		stop  int // rows consumed before Close, 0 = drain
	}{
		{"fts/limit=30/drain", 30, 0},
		{"fts/nolimit/close@30", 0, 30},
		{"fts/nolimit/close@300", 0, 300},
		{"fts/nolimit/close@1000", 0, 1000},
		{"fts/nolimit/drain", 0, 0},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			var rows int
			for i := 0; i < b.N; i++ {
				q := coll.Find(text)
				if c.limit > 0 {
					q = q.Limit(uint(c.limit))
				}
				iter, err := q.Iter(ctx)
				if err != nil {
					b.Fatal(err)
				}
				rows = pullHits(b, iter, c.stop)
			}
			b.ReportMetric(float64(rows), "rows/op")
		})
	}
}

// benchCutoffKnn: $knn at the widening K steps the vector leg takes
// (30 → ×4 → 1000), without and with the scope residual (auto ef =
// min(10K, 4096) under a residual, so K above ~400 starves), the
// explicit-ef escape for the residual case, and an early Close at
// K=1000 (VectorIter does all its work before the first row).
func benchCutoffKnn(b *testing.B, st *Store, rng *rand.Rand) {
	ctx := context.Background()
	coll, err := st.spaceColl(ctx, cutoffSpace)
	if err != nil {
		b.Fatal(err)
	}
	qv := benchVec(rng, cutoffDim)
	scope := scopeKey([]string{"chat"})
	type knnCase struct {
		name     string
		k, ef    int
		residual bool
		stop     int
	}
	var cases []knnCase
	for _, k := range []int{30, 120, 480, 1000} {
		cases = append(cases,
			knnCase{fmt.Sprintf("knn/k=%d/drain", k), k, 0, false, 0},
			knnCase{fmt.Sprintf("knn/k=%d/scope/drain", k), k, 0, true, 0},
		)
	}
	cases = append(cases,
		knnCase{"knn/k=1000/scope/ef=10000/drain", 1000, 10_000, true, 0},
		knnCase{"knn/k=1000/close@30", 1000, 0, false, 30},
	)
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			var filter query.Filter
			if c.ef > 0 {
				filter = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(qv, c.k, query.KnnEf(c.ef))}
			} else {
				filter = query.Key{Path: []string{"vector"}, Filter: query.NewKnn(qv, c.k)}
			}
			if c.residual {
				filter = query.And{filter, scope}
			}
			var rows int
			for i := 0; i < b.N; i++ {
				iter, err := coll.Find(filter).Iter(ctx)
				if err != nil {
					b.Fatal(err)
				}
				rows = pullHits(b, iter, c.stop)
			}
			b.ReportMetric(float64(rows), "rows/op")
		})
	}
}

// benchCutoffSearch: the endpoint end to end (limit 10) on the rare
// term only the long records and the needle carry — the ticket's
// collapse (records/op < 6 before the record-counting limit) and its
// before/after cost. The corpus vectors are random and the query is an
// axis vector, so the vector leg contributes a random window; the
// hybrid line measures fusion + grouping over both windows, not the
// vector leg's ranking.
func benchCutoffSearch(b *testing.B, st *Store) {
	ctx := context.Background()
	ix := &Indexer{
		store: st,
		opts:  Options{Embedder: axisEmbedder{dim: cutoffDim}, AnnounceAfter: -1}.withDefaults(),
		lg:    logger.NewNamed("indexer-bench"),
	}
	for _, mode := range []string{api.SearchModeFTS, api.SearchModeHybrid} {
		b.Run("search/"+mode+"/limit=10", func(b *testing.B) {
			var hits, records int
			for i := 0; i < b.N; i++ {
				res, err := ix.Search(ctx, cutoffSpace, api.SearchRequest{Query: "reranker", Mode: mode, Limit: 10})
				if err != nil {
					b.Fatal(err)
				}
				hits = len(res.Hits)
				seen := map[string]bool{}
				for _, h := range res.Hits {
					seen[h.ObjectId+"/"+h.Dataset+"/"+h.RecordId] = true
				}
				records = len(seen)
			}
			b.ReportMetric(float64(hits), "hits/op")
			b.ReportMetric(float64(records), "records/op")
		})
	}
}
