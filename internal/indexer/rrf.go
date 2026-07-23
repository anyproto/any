package indexer

import (
	"cmp"
	"slices"
)

// rrfK is the standard reciprocal-rank-fusion damping constant: a doc at
// rank r contributes 1/(rrfK+r+1). 60 is the value from the original
// Cormack/Clarke paper and works well without tuning.
const rrfK = 60

// legConfidence scores how "peaked" a leg's result scores are, in [0,1]:
// ~1 when the top hit dominates (a confident retrieval), →0 when scores
// are flat (the leg matched broadly but weakly). Scale-free (a ratio
// within the leg), so it's a per-query strength signal.
//
// Used ONLY for the FTS/BM25 leg in adaptive fusion: BM25 has a
// meaningful weak signal (flat/low scores when few query terms hit),
// whereas cosine is compressed and uncalibrated (the minVectorSim
// finding) — a flat cosine distribution does NOT mean the vector leg is
// weak, so we never adapt on it.
func legConfidence(hits []Hit) float64 {
	if len(hits) < 2 {
		return 1 // nothing to compare against — don't penalize
	}
	top := hits[0].Score
	if top <= 0 {
		return 0
	}
	var rest float64
	for _, h := range hits[1:] {
		rest += h.Score
	}
	mean := rest / float64(len(hits)-1)
	c := 1 - mean/top
	if c < 0 {
		c = 0
	}
	if c > 1 {
		c = 1
	}
	return c
}

// fuseRRF merges ranked hit lists by reciprocal rank fusion, deduping by
// (dataset, recordId). The fused Score replaces the per-leg scores —
// BM25 and cosine similarity aren't comparable, ranks are. Ties break by
// doc key for determinism.
//
// weights scales each list's contribution (per-leg trust). A nil/short
// weights slice defaults missing entries to 1, so fuseRRF(lists, nil,
// limit) is plain unweighted RRF.
func fuseRRF(lists [][]Hit, weights []float64, limit int) []Hit {
	type acc struct {
		hit   Hit
		score float64
	}
	byKey := map[string]*acc{}
	for li, list := range lists {
		w := 1.0
		if li < len(weights) && weights[li] > 0 {
			w = weights[li]
		}
		for rank, h := range list {
			key := h.Dataset + "/" + h.RecordId
			a, ok := byKey[key]
			if !ok {
				a = &acc{hit: h}
				byKey[key] = a
			}
			a.score += w / float64(rrfK+rank+1)
		}
	}
	out := make([]Hit, 0, len(byKey))
	for _, a := range byKey {
		a.hit.Score = a.score
		out = append(out, a.hit)
	}
	slices.SortFunc(out, func(a, b Hit) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(a.Dataset+"/"+a.RecordId, b.Dataset+"/"+b.RecordId),
		)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
