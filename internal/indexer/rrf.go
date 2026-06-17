package indexer

import "sort"

// rrfK is the standard reciprocal-rank-fusion damping constant: a doc at
// rank r contributes 1/(rrfK+r+1). 60 is the value from the original
// Cormack/Clarke paper and works well without tuning.
const rrfK = 60

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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Dataset+"/"+out[i].RecordId < out[j].Dataset+"/"+out[j].RecordId
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
