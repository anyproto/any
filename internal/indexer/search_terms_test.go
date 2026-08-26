//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"testing"

	"github.com/anyproto/any/internal/api"
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
