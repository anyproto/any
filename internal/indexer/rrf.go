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

// fuseRRF merges ranked hit lists by reciprocal rank fusion, keyed by
// doc id (objectId, dataset, recordId, chunk) — recordIds repeat across
// objects (propIds do) and a record spans several chunk docs, so a
// shorter key would collapse distinct hits and sum their contributions
// (a long record would outrank a precise short one on chunk count).
// Collapsing chunks into records is groupHits' job, after fusion, by
// max. The fused Score replaces the per-leg scores — BM25 and cosine
// similarity aren't comparable, ranks are. Ties break by doc key for
// determinism.
//
// weights scales each list's contribution (per-leg trust). A nil/short
// weights slice defaults missing entries to 1, so fuseRRF(lists, nil,
// limit) is plain unweighted RRF. limit 0 keeps every fused hit.
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
			key := hitDocId(h)
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
			cmp.Compare(hitDocId(a), hitDocId(b)),
		)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Group is one record in a reply: its best-ranked chunk plus, when
// asked for, the next best chunks that matched within the search
// window.
type Group struct {
	Hit      Hit
	Passages []Hit
}

// groupKey is the record a chunk hit belongs to — the reply's unit.
func groupKey(h Hit) string {
	return docId(h.ObjectId, h.Dataset, h.RecordId)
}

// countGroups is how many distinct records a leg's window covers.
func countGroups(hits []Hit) int {
	seen := map[string]struct{}{}
	for _, h := range hits {
		seen[groupKey(h)] = struct{}{}
	}
	return len(seen)
}

// groupHits collapses ranked chunk hits into records: one Group per
// (objectId, dataset, recordId), scored by its BEST member — never a
// sum, which would rank a long record on chunk count — represented by
// that chunk, and carrying the next best passages members (score desc,
// doc id asc; 0 = none). Groups sort by score desc then key asc and cut
// to limit (0 = all). Passages are chunks that matched within the
// window the legs read, not every chunk of the record.
func groupHits(hits []Hit, limit, passages int) []Group {
	byKey := map[string]*Group{}
	for _, h := range hits {
		key := groupKey(h)
		g, ok := byKey[key]
		if !ok {
			byKey[key] = &Group{Hit: h}
			continue
		}
		if hitLess(h, g.Hit) {
			h, g.Hit = g.Hit, h
		}
		g.Passages = append(g.Passages, h)
	}
	out := make([]Group, 0, len(byKey))
	for _, g := range byKey {
		if passages <= 0 {
			g.Passages = nil
		} else {
			slices.SortFunc(g.Passages, func(a, b Hit) int { return hitCmp(a, b) })
			if len(g.Passages) > passages {
				g.Passages = g.Passages[:passages]
			}
		}
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(
			cmp.Compare(b.Hit.Score, a.Hit.Score),
			cmp.Compare(groupKey(a.Hit), groupKey(b.Hit)),
		)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// hitCmp orders hits best first: score desc, doc id asc.
func hitCmp(a, b Hit) int {
	return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(hitDocId(a), hitDocId(b)))
}

// hitLess reports whether a ranks ahead of b.
func hitLess(a, b Hit) bool { return hitCmp(a, b) < 0 }
