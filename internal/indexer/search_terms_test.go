//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// axisEmbedder maps every text to the same vector, so the vector leg
// returns every embedded doc as a top hit regardless of its words.
type axisEmbedder struct{ dim int }

func (a axisEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		v := make([]float32, a.dim)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}
func (a axisEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	v := make([]float32, a.dim)
	v[0] = 1
	return v, nil
}
func (a axisEmbedder) Dim(context.Context) (int, error) { return a.dim, nil }

// Hybrid mode honors require / exclude on the fused result (SYN-187):
// the vector leg ranks every doc equally here, so without the
// post-filter fusion would re-admit docs the FTS leg refused.
func TestIndexer_SearchHybridRequireExclude(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{Embedder: axisEmbedder{dim: 4}, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	const sp = "sp1"

	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m1", "email_messages", "r", "Anytype for Android release notes", 1)},
		{Entry: entry("email", "m2", "email_messages", "r", "Anytype for iOS release notes", 2)},
		{Entry: entry("email", "m3", "email_messages", "r", "Anytype desktop and Android sync", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}

	objects := func(res api.SearchResponse) map[string]bool {
		out := map[string]bool{}
		for _, h := range res.Hits {
			out[h.ObjectId] = true
		}
		return out
	}

	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "anytype", Require: []string{"android"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != api.SearchModeHybrid || res.VectorStatus != api.VectorStatusUsed {
		t.Fatalf("mode=%s vectorStatus=%s, want hybrid/used", res.Mode, res.VectorStatus)
	}
	if got := objects(res); len(got) != 2 || !got["m1"] || !got["m3"] {
		t.Fatalf("require android = %v, want m1+m3", got)
	}

	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "anytype", Exclude: []string{"android"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := objects(res); len(got) != 1 || !got["m2"] {
		t.Fatalf("exclude android = %v, want only m2", got)
	}

	// Pure vector mode is post-filtered the same way.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "anytype", Mode: api.SearchModeVector, Require: []string{"ios"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := objects(res); len(got) != 1 || !got["m2"] {
		t.Fatalf("vector require ios = %v, want only m2", got)
	}
}

// A long record indexes as several chunk hits, each windowed to
// maxData around its match (SYN-188); -1 returns the whole chunk.
func TestIndexer_SearchChunksAndMaxData(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{ChunkRunes: 300, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"

	body := "Subject line\n" + strings.Repeat("plain filler text. ", 40) + "the needle sentence is here. " + strings.Repeat("more filler text. ", 40)
	e := entry("email", "m1", "email_messages", "r1", body, 1)
	e.Title = "Subject line"
	var page pageOps
	planDocs([]index.IndexEntry{e}, nil, ix.opts.ChunkRunes, &page)
	if len(page.ups) < 3 {
		t.Fatalf("chunks = %d, want >= 3", len(page.ups))
	}
	if err := st.Apply(ctx, sp, page.ups, nil, nil); err != nil {
		t.Fatal(err)
	}

	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("needle hits = %d, want the one chunk holding it", len(res.Hits))
	}
	h := res.Hits[0]
	if h.RecordId != "r1" || h.Chunk == 0 {
		t.Fatalf("hit = %+v, want a later chunk of r1", h)
	}
	if !strings.Contains(h.Data, "needle") || utf8.RuneCountInString(h.Data) > api.DefaultSearchMaxData {
		t.Fatalf("data window = %q", h.Data)
	}

	// "filler" is in every chunk: the record is ONE hit (its best chunk),
	// and the other chunks come back as passages when asked for —
	// distinct, windowed like the hit, capped at the request.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "filler", Mode: api.SearchModeFTS, Limit: 10, MaxData: 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].RecordId != "r1" || res.Hits[0].Passages != nil {
		t.Fatalf("filler hits = %+v, want one r1 hit without passages", res.Hits)
	}
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "filler", Mode: api.SearchModeFTS, Limit: 10, MaxData: 40, Passages: api.MaxSearchPassages})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("filler hits = %+v, want one", res.Hits)
	}
	h = res.Hits[0]
	seen := map[int]bool{h.Chunk: true}
	if n := utf8.RuneCountInString(h.Data); n > 40 || h.DataTotal < n {
		t.Fatalf("maxData 40: data %d runes, total %d", n, h.DataTotal)
	}
	for i, p := range h.Passages {
		if seen[p.Chunk] {
			t.Fatalf("passage %d repeats chunk %d: %+v", i, p.Chunk, h.Passages)
		}
		seen[p.Chunk] = true
		if i > 0 && p.Score > h.Passages[i-1].Score {
			t.Fatalf("passages not best-first: %+v", h.Passages)
		}
		if n := utf8.RuneCountInString(p.Data); n > 40 || p.DataTotal < n {
			t.Fatalf("passage maxData 40: data %d runes, total %d", n, p.DataTotal)
		}
	}
	if len(seen) != len(page.ups) {
		t.Fatalf("hit + passages cover %d chunks, want %d", len(seen), len(page.ups))
	}
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "filler", Mode: api.SearchModeFTS, Limit: 10, Passages: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || len(res.Hits[0].Passages) != 2 {
		t.Fatalf("passages: 2 → %+v", res.Hits)
	}

	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 10, MaxData: -1})
	if err != nil {
		t.Fatal(err)
	}
	if h := res.Hits[0]; h.DataOffset != 0 || utf8.RuneCountInString(h.Data) != h.DataTotal {
		t.Fatalf("maxData -1 must return the whole chunk: off %d len %d total %d", h.DataOffset, utf8.RuneCountInString(h.Data), h.DataTotal)
	}
}

// limit counts records: one 17-chunk record whose every
// chunk outranks a short exact match must not fill the reply. The
// short record's single chunk is one BM25 hit against seventeen
// stronger ones, and the axis embedder ranks every chunk equally on the
// vector side, so both legs — and hybrid — need the deeper pull.
func TestIndexer_SearchLimitCountsRecords(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{Embedder: axisEmbedder{dim: 4}, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"

	long := entry("chat", "o1", "chat_messages", "long", strings.Repeat("reranker notes and more reranker notes. ", 17*DefaultChunkRunes/40), 1)
	ups := expandEntry(long, DefaultChunkRunes)
	if len(ups) < 17 {
		t.Fatalf("chunks = %d, want >= 17", len(ups))
	}
	ups = append(ups, DocUpsert{Entry: entry("chat", "o2", "chat_messages", "short", "the reranker", 2)})
	for i := range ups {
		ups[i].Vector = []float32{1, 0, 0, 0}
	}
	if err := st.Apply(ctx, sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.EnsureVectorIndex(ctx, sp); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}

	for _, mode := range []string{api.SearchModeFTS, api.SearchModeHybrid, api.SearchModeVector} {
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "reranker", Mode: mode, Limit: 10, Passages: 3})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if res.Mode != mode {
			t.Fatalf("%s ran as %s (vector %s)", mode, res.Mode, res.VectorStatus)
		}
		if len(res.Hits) != 2 {
			t.Fatalf("%s: hits = %d, want both records: %+v", mode, len(res.Hits), res.Hits)
		}
		byRec := map[string]api.SearchHit{}
		for _, h := range res.Hits {
			byRec[h.RecordId] = h
		}
		if _, ok := byRec["short"]; !ok {
			t.Fatalf("%s: the short exact match is missing: %+v", mode, res.Hits)
		}
		lh := byRec["long"]
		if len(lh.Passages) != 3 {
			t.Fatalf("%s: long record passages = %+v, want 3", mode, lh.Passages)
		}
		for _, p := range lh.Passages {
			if p.Chunk == lh.Chunk || p.Score > lh.Score {
				t.Fatalf("%s: passage %+v vs hit chunk %d score %v", mode, p, lh.Chunk, lh.Score)
			}
		}
		if len(byRec["short"].Passages) != 0 {
			t.Fatalf("%s: single-chunk record carries passages: %+v", mode, byRec["short"])
		}
	}
	// limit 1 returns one record, not one chunk of each.
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "reranker", Mode: api.SearchModeFTS, Limit: 1})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("limit 1: %v %+v", err, res.Hits)
	}
}

// The cover rule reads past the fixed over-fetch: sixty chunks of one
// record outrank forty short records on BM25, so the first thirty rows
// are all one record and the leg must keep pulling to cover limit
// records. The ceiling case is one record of over a thousand chunks —
// the leg stops at maxLegFetch and answers with that one record.
func TestIndexer_SearchLegCoverRule(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, opts: Options{Embedder: axisEmbedder{dim: 4}, ChunkRunes: 300, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"

	var ups []DocUpsert
	long := entry("chat", "o-long", "chat_messages", "long", strings.Repeat("kestrelith kestrelith kestrelith notes. ", 60*300/40), 1)
	ups = append(ups, expandEntry(long, ix.opts.ChunkRunes)...)
	if len(ups) < 60 {
		t.Fatalf("long chunks = %d, want >= 60", len(ups))
	}
	for i := 0; i < 40; i++ {
		ups = append(ups, DocUpsert{Entry: entry("chat", "o-short", "chat_messages", "short"+strconv.Itoa(i), "the kestrelith item", uint64(2+i))})
	}
	for i := range ups {
		ups[i].Vector = []float32{1, 0, 0, 0}
	}
	if err := st.Apply(ctx, sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.EnsureVectorIndex(ctx, sp); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}
	// The premise: with the old fixed window the first 30 rows are all
	// the long record.
	first30, err := st.SearchFTS(ctx, sp, "kestrelith", nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n := countGroups(first30); n != 1 {
		t.Fatalf("first 30 fts rows cover %d records, the premise needs 1", n)
	}
	for _, mode := range []string{api.SearchModeFTS, api.SearchModeHybrid, api.SearchModeVector} {
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "kestrelith", Mode: mode, Limit: 10})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		seen := map[string]bool{}
		for _, h := range res.Hits {
			seen[h.RecordId] = true
		}
		if len(res.Hits) != 10 || len(seen) != 10 {
			t.Fatalf("%s: %d hits over %d records, want 10 distinct", mode, len(res.Hits), len(seen))
		}
	}

	// Ceiling: one record past maxLegFetch chunks, nothing else matches.
	st2 := mustStore(t, 0)
	ix2 := &Indexer{store: st2, opts: Options{ChunkRunes: 300, AnnounceAfter: -1}.withDefaults()}
	huge := entry("chat", "o-huge", "chat_messages", "huge", strings.Repeat("ospreyoid ospreyoid text. ", (maxLegFetch+100)*300/26), 1)
	hugeUps := expandEntry(huge, ix2.opts.ChunkRunes)
	if len(hugeUps) <= maxLegFetch {
		t.Fatalf("huge chunks = %d, want > %d", len(hugeUps), maxLegFetch)
	}
	for off := 0; off < len(hugeUps); off += 256 {
		if err := st2.Apply(ctx, sp, hugeUps[off:min(off+256, len(hugeUps))], nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ix2.Search(ctx, sp, api.SearchRequest{Query: "ospreyoid", Mode: api.SearchModeFTS, Limit: 10, Passages: api.MaxSearchPassages})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].RecordId != "huge" || len(res.Hits[0].Passages) != api.MaxSearchPassages {
		t.Fatalf("ceiling: %+v", res.Hits)
	}
}
