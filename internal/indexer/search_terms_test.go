//go:build fts && vector && !gomobile

package indexer

import (
	"context"
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

	// "filler" is in every chunk: one hit per chunk, all r1, distinct chunks.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "filler", Mode: api.SearchModeFTS, Limit: 10, MaxData: 40})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, h := range res.Hits {
		if h.RecordId != "r1" || seen[h.Chunk] {
			t.Fatalf("hits = %+v", res.Hits)
		}
		seen[h.Chunk] = true
		if n := utf8.RuneCountInString(h.Data); n > 40 || h.DataTotal < n {
			t.Fatalf("maxData 40: data %d runes, total %d", n, h.DataTotal)
		}
	}
	if len(seen) != len(page.ups) {
		t.Fatalf("filler hits cover %d chunks, want %d", len(seen), len(page.ups))
	}

	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "needle", Mode: api.SearchModeFTS, Limit: 10, MaxData: -1})
	if err != nil {
		t.Fatal(err)
	}
	if h := res.Hits[0]; h.DataOffset != 0 || utf8.RuneCountInString(h.Data) != h.DataTotal {
		t.Fatalf("maxData -1 must return the whole chunk: off %d len %d total %d", h.DataOffset, utf8.RuneCountInString(h.Data), h.DataTotal)
	}
}
