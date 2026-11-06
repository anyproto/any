package indexer

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"unicode/utf8"

	"github.com/anyproto/any/internal/api"
)

// beirQuery is a BEIR query reduced to what beirCentering needs.
type beirQuery struct {
	text string
	rel  []string
}

// centerEmbedder applies query-side centering: q' = normalize(q − a·μ).
type centerEmbedder struct {
	Embedder
	mu []float32
	a  float32
}

func (c centerEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := c.Embedder.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	return centerVec(v, c.mu, c.a), nil
}

func centerVec(q, mu []float32, a float32) []float32 {
	out := make([]float32, len(q))
	for i := range q {
		out[i] = q[i] - a*mu[i]
	}
	return l2norm(out)
}

// beirCentering reports the short-doc bias of the vector leg and the effect
// of query-side centering, μ being the unnormalized mean of 100 random doc
// vectors, on exact and IVF vector/hybrid search. Deltas are paired
// bootstrap 95% CIs against a = 0.
func beirCentering(t *testing.T, ctx context.Context, name string, ix *Indexer, emb Embedder,
	qs []beirQuery, corpus []beirDoc, docVecs []vecDoc, alphas []float64) {
	const k, fetch, short = 10, 30, 200
	runes := map[string]int{}
	for _, d := range corpus {
		runes[d.id] = utf8.RuneCountInString(d.text)
	}
	qv := make([][]float32, len(qs))
	for i, q := range qs {
		v, err := emb.EmbedQuery(ctx, q.text)
		if err != nil {
			t.Fatalf("embed query: %v", err)
		}
		qv[i] = l2norm(v)
	}

	// Short-doc bias: within-query slope of cosine over log10(runes), and
	// the share of short docs in the exact top-10 against the corpus rate.
	logLen := make([]float64, len(docVecs))
	base := 0
	for i, d := range docVecs {
		logLen[i] = math.Log10(float64(max(1, runes[d.id])))
		if runes[d.id] < short {
			base++
		}
	}
	var sxy, sxx float64
	for _, q := range qv {
		cos := make([]float64, len(docVecs))
		var mc, ml float64
		for i, d := range docVecs {
			var dot float32
			for j := range q {
				dot += q[j] * d.vec[j]
			}
			cos[i] = float64(dot)
			mc += cos[i]
			ml += logLen[i]
		}
		mc /= float64(len(cos))
		ml /= float64(len(cos))
		for i := range cos {
			dx := logLen[i] - ml
			sxy += dx * (cos[i] - mc)
			sxx += dx * dx
		}
	}
	t.Logf("=== %s: short-doc bias — cosine slope %+.4f per decade of length; corpus <%d runes %.1f%% ===",
		name, sxy/sxx, short, 100*float64(base)/float64(len(docVecs)))

	ix.opts = Options{StopWords: true}.withDefaults()
	ix.opts.Embedder = emb
	ftsLeg := make([][]string, len(qs))
	for i, q := range qs {
		resp, err := ix.Search(ctx, beirSpace, api.SearchRequest{Query: q.text, Mode: api.SearchModeFTS, Limit: fetch}, nil)
		if err != nil {
			t.Fatalf("fts: %v", err)
		}
		ftsLeg[i] = distinctRecordIds(t, resp.Hits)
	}

	rng := rand.New(rand.NewSource(1))
	mu := make([]float32, len(docVecs[0].vec))
	n := min(100, len(docVecs))
	for _, i := range rng.Perm(len(docVecs))[:n] {
		for j, x := range docVecs[i].vec {
			mu[j] += x / float32(n)
		}
	}

	type cols struct{ exV, exH, ivV, ivH []float64 }
	run := func(a float64) (cols, float64) {
		var c cols
		shortHits, hits := 0, 0
		ce := centerEmbedder{Embedder: emb, mu: mu, a: float32(a)}
		ix.opts = Options{StopWords: true}.withDefaults()
		ix.opts.Embedder = ce
		for i, q := range qs {
			exact := cosineTopK(centerVec(qv[i], mu, float32(a)), docVecs, fetch)
			for _, id := range exact[:min(k, len(exact))] {
				hits++
				if runes[id] < short {
					shortHits++
				}
			}
			c.exV = append(c.exV, evalOne(exact[:min(k, len(exact))], q.rel, k).ndcgAtK)
			var fused []string
			for _, h := range fuseRRF([][]Hit{beirHits(ftsLeg[i]), beirHits(exact)}, nil, k) {
				fused = append(fused, h.RecordId)
			}
			c.exH = append(c.exH, evalOne(fused, q.rel, k).ndcgAtK)
			for _, mode := range []string{api.SearchModeVector, api.SearchModeHybrid} {
				resp, err := ix.Search(ctx, beirSpace, api.SearchRequest{Query: q.text, Mode: mode, Limit: k}, nil)
				if err != nil {
					t.Fatalf("search: %v", err)
				}
				m := evalOne(distinctRecordIds(t, resp.Hits), q.rel, k).ndcgAtK
				if mode == api.SearchModeVector {
					c.ivV = append(c.ivV, m)
				} else {
					c.ivH = append(c.ivH, m)
				}
			}
		}
		return c, float64(shortHits) / float64(hits)
	}

	b0, s0 := run(0)
	t.Logf("=== %s: query centering q' = normalize(q − a·μ) — nDCG@%d, Δ vs a=0 [95%% CI] ===", name, k)
	t.Logf("%-5s %-26s %-26s %-26s %-26s %s", "a", "exact vector", "exact hybrid", "IVF vector", "IVF hybrid", "short@10")
	t.Logf("%-5s %-26.4f %-26.4f %-26.4f %-26.4f %.1f%%", "0", avgF(b0.exV), avgF(b0.exH), avgF(b0.ivV), avgF(b0.ivH), 100*s0)
	for _, a := range alphas {
		c, s := run(a)
		t.Logf("%-5.2f %-26s %-26s %-26s %-26s %.1f%%", a,
			bootDelta(c.exV, b0.exV), bootDelta(c.exH, b0.exH), bootDelta(c.ivV, b0.ivV), bootDelta(c.ivH, b0.ivH), 100*s)
	}
	ix.opts = Options{StopWords: true}.withDefaults()
	ix.opts.Embedder = emb
}

func avgF(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// bootDelta formats mean(a−b) with a paired bootstrap 95% CI; * marks a CI
// that excludes 0.
func bootDelta(a, b []float64) string {
	d := make([]float64, len(a))
	for i := range a {
		d[i] = a[i] - b[i]
	}
	rng := rand.New(rand.NewSource(1))
	const n = 4000
	ms := make([]float64, n)
	for r := range ms {
		var s float64
		for range d {
			s += d[rng.Intn(len(d))]
		}
		ms[r] = s / float64(len(d))
	}
	sort.Float64s(ms)
	lo, hi := ms[n*25/1000], ms[n*975/1000]
	star := ""
	if lo > 0 || hi < 0 {
		star = "*"
	}
	return fmtDelta(avgF(d), lo, hi) + star
}

func fmtDelta(m, lo, hi float64) string {
	return fmt.Sprintf("%+.4f [%+.3f,%+.3f]", m, lo, hi)
}
