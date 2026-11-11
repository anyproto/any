package indexer

import "testing"

func scored(data string, score float64) Hit { return Hit{Data: data, Score: score} }

func TestQueryCoverage(t *testing.T) {
	fts := []Hit{scored("the hydraulic pump failed", 3), scored("Гидравлика отказала", 2), scored("x", 1), scored("pump hydraulic failure ice", 1)}
	for _, c := range []struct {
		q    string
		want float64
	}{
		{"hydraulic pump", 1},      // both words in chunk 1
		{"the hydraulic ice", 0.5}, // "the" is a stop word; "ice" only in chunk 4, past the top 3
		{"гидравлика", 1},          // Unicode words, lowercased
		{"what is it", 0},          // all stop words: no words
		{"engine fire", 0},         // no word found
		{"hydraulic_pump", 0},      // '_' keeps a word whole
	} {
		if got := queryCoverage(c.q, fts); got != c.want {
			t.Errorf("queryCoverage(%q) = %v, want %v", c.q, got, c.want)
		}
	}
	if got := queryCoverage("pump", nil); got != 0 {
		t.Errorf("no FTS hits: coverage %v, want 0", got)
	}
}

func TestGaps(t *testing.T) {
	vec := []Hit{scored("", 0.8), scored("", 0.75), scored("", 0.7), scored("", 0.65), scored("", 0.6), scored("", 0.1)}
	if g := vectorGap(vec); g < 0.1999 || g > 0.2001 {
		t.Errorf("vectorGap = %v, want 0.2 (first minus fifth)", g)
	}
	if g := vectorGap(vec[:2]); g < 0.0499 || g > 0.0501 {
		t.Errorf("short leg: vectorGap = %v, want 0.05 (first minus last)", g)
	}
	if g := lexicalGap([]Hit{scored("", 10), scored("", 8)}); g != 0.2 {
		t.Errorf("lexicalGap = %v, want 0.2", g)
	}
	if lexicalGap([]Hit{scored("", 10)}) != 1 || lexicalGap(nil) != 0 || vectorGap(nil) != 0 {
		t.Error("edge gaps: one FTS hit is 1, empty legs are 0")
	}
}

func TestPlanFusion_Branches(t *testing.T) {
	flatVec := []Hit{scored("", 0.70), scored("", 0.69), scored("", 0.68), scored("", 0.67), scored("", 0.66)}
	peakedVec := []Hit{scored("", 0.90), scored("", 0.70), scored("", 0.69), scored("", 0.68), scored("", 0.67)}
	match := func(top, second float64) []Hit {
		return []Hit{scored("pfand bottle deposit", top), scored("pfand", second)}
	}
	for _, c := range []struct {
		name     string
		q        string
		fts, vec []Hit
		want     fusionPlan
	}{
		{"low coverage, flat vector leg", "returnable bottles", match(5, 4), flatVec, fusionPlan{fts: 1, vec: 2, k: 20}},
		{"low coverage, peaked vector leg", "returnable bottles", match(5, 4), peakedVec, fusionPlan{vec: 1}},
		{"full coverage, no lexical winner", "pfand", match(5, 4.5), flatVec, fusionPlan{fts: 1, vec: 0.5, k: 20}},
		{"full coverage, lexical winner", "pfand", match(5, 2), flatVec, fusionPlan{fts: 1}},
	} {
		if got := planFusion(c.q, c.fts, c.vec); got != c.want {
			t.Errorf("%s: plan %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestFusionPlan_NormalizedTopIsOne(t *testing.T) {
	a, b := h("ds", "a"), h("ds", "b")
	for _, p := range []fusionPlan{{fts: 1, vec: 2, k: 20}, {vec: 1}, {fts: 1, vec: 0.5, k: 20}, {fts: 1}} {
		lists, weights := p.lists([]Hit{a, b}, []Hit{a, b})
		fused := fuseRRFK(lists, weights, p.k, 0)
		normalizeRRF(fused, weights, len(lists), p.k)
		if d := fused[0].Score - 1; d > 1e-12 || d < -1e-12 {
			t.Errorf("plan %+v: top score %v, want 1", p, fused[0].Score)
		}
	}
}

func TestFusionPlan_SingleLegScoresByRank(t *testing.T) {
	a, b, c := h("ds", "a"), h("ds", "b"), h("ds", "c")
	p := fusionPlan{fts: 1}
	lists, weights := p.lists([]Hit{a, b, c}, nil)
	fused := fuseRRFK(lists, weights, p.k, 0)
	normalizeRRF(fused, weights, len(lists), p.k)
	for i, want := range []float64{1, 0.5, 1.0 / 3} {
		if d := fused[i].Score - want; d > 1e-12 || d < -1e-12 {
			t.Errorf("rank %d: score %v, want %v", i+1, fused[i].Score, want)
		}
	}
}
