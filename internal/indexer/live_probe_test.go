package indexer

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/config"
)

// TestLiveVectorScoreProbe inspects the cosine-similarity distribution of
// the vector leg against a REAL index, to choose index.search.minVectorSim.
// Gated/opt-in (touches a real DB + loads the local model):
//
//	ANY_LIVE_INDEX=~/.any/index/index.db \
//	ANY_LIVE_SPACE=<spaceId> \
//	ANY_EVAL_LOCAL_MODEL=~/.any/models/<model>.gguf \
//	ANY_EVAL_LOCAL_LIBDIR=./bin/llamacpp \
//	go test -tags llamacpp -run TestLiveVectorScoreProbe -v ./internal/indexer
//
// The server must be stopped (it holds the DB open).
func TestLiveVectorScoreProbe(t *testing.T) {
	idxPath := os.Getenv("ANY_LIVE_INDEX")
	spaceId := os.Getenv("ANY_LIVE_SPACE")
	if idxPath == "" || spaceId == "" {
		t.Skip("set ANY_LIVE_INDEX + ANY_LIVE_SPACE (+ ANY_EVAL_LOCAL_MODEL/LIBDIR)")
	}
	ctx := context.Background()

	emb, err := NewEmbedder(config.Index{
		Embedder: "local",
		Local: config.IndexLocal{
			ModelPath: os.Getenv("ANY_EVAL_LOCAL_MODEL"),
			LibDir:    os.Getenv("ANY_EVAL_LOCAL_LIBDIR"),
		},
	}, t.TempDir(), "", nil)
	if err != nil || emb == nil {
		t.Fatalf("local embedder: %v", err)
	}

	st, err := OpenStore(ctx, idxPath, 0, true)
	if err != nil {
		t.Fatalf("open live index: %v", err)
	}
	defer func() { _ = st.Close() }()

	// On/off-topic query sets are overridable per space via env
	// (comma-separated) so the probe is reusable across spaces.
	splitEnv := func(k string, def []string) []string {
		if v := os.Getenv(k); v != "" {
			parts := strings.Split(v, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			return parts
		}
		return def
	}
	groups := []struct {
		label   string
		queries []string
	}{
		{"on-topic", splitEnv("ANY_LIVE_ONTOPIC", []string{
			"binary search algorithm",
			"clean architecture principles",
			"how does SEO ranking work",
			"graph database neo4j",
		})},
		{"off-topic", splitEnv("ANY_LIVE_OFFTOPIC", []string{
			"risotto recipe with saffron",
			"olympic swimming world records",
			"how to knit a wool sweater",
		})},
		{"nonsense", []string{
			"xyzzy plugh frobnitz wibble",
			"asdf qwer zxcv hjkl",
		}},
	}

	t.Logf("vector cosine scores per query (top-1, top-3 mean, top-10 mean), space=%s", spaceId[:16])
	for _, g := range groups {
		for _, q := range g.queries {
			qv, err := emb.EmbedQuery(ctx, q)
			if err != nil {
				t.Fatalf("embed %q: %v", q, err)
			}
			hits, err := st.SearchVector(ctx, spaceId, qv, nil, 10, 0) // floor 0 → see everything positive
			if err != nil {
				t.Fatalf("search %q: %v", q, err)
			}
			var top1, sum3, sum10 float64
			for i, h := range hits {
				if i == 0 {
					top1 = h.Score
				}
				if i < 3 {
					sum3 += h.Score
				}
				sum10 += h.Score
			}
			n3 := min(3, len(hits))
			mean3, mean10 := 0.0, 0.0
			if n3 > 0 {
				mean3 = sum3 / float64(n3)
			}
			if len(hits) > 0 {
				mean10 = sum10 / float64(len(hits))
			}
			t.Logf("  [%-9s] top1=%.3f top3=%.3f top10=%.3f n=%2d  %q", g.label, top1, mean3, mean10, len(hits), q)
		}
	}
}
