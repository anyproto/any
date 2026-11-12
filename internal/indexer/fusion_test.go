package indexer

import (
	"math"
	"testing"
)

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

// A `word*` in the query is covered by any word it starts.
func TestQueryCoverage_Prefix(t *testing.T) {
	fts := []Hit{scored("the hydraulic pump failed", 3)}
	for _, c := range []struct {
		q    string
		want float64
	}{
		{"hydraul*", 1},
		{"hydraul", 0}, // not a prefix: a whole word or nothing
		{"pump hydr*", 1},
		{`"hydraulic pu"*`, 1},
		{`"hydraulic pu*"`, 0.5}, // a star inside the quotes is no prefix
		{"engine*", 0},
		{"the*", 1}, // a prefix term is never a stop word
	} {
		if got := queryCoverage(c.q, fts); got != c.want {
			t.Errorf("queryCoverage(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

// A leg that decides alone keeps its order and scores; the hits only
// the other leg holds follow as the next ranks.
func TestFusionPlan_Backfill(t *testing.T) {
	a, b, c, d := h("ds", "a"), h("ds", "b"), h("ds", "c"), h("ds", "d")
	fts, vec := []Hit{a}, []Hit{b, a, c, d}
	p := fusionPlan{fts: 1}
	lists, weights := p.lists(fts, vec)
	fused := fuseRRFK(lists, weights, p.k, 0)
	normalizeRRF(fused, weights, len(lists), p.k)
	fused = p.backfill(fused, fts, vec)
	if len(fused) != 4 {
		t.Fatalf("page holds %d hits, want the FTS hit and the three the vector leg adds", len(fused))
	}
	for i, want := range []struct {
		id    string
		score float64
	}{{"a", 1}, {"b", 0.5}, {"c", 1.0 / 3}, {"d", 0.25}} {
		if fused[i].RecordId != want.id || math.Abs(fused[i].Score-want.score) > 1e-12 {
			t.Errorf("rank %d: %s at %v, want %s at %v", i+1, fused[i].RecordId, fused[i].Score, want.id, want.score)
		}
	}

	both := fusionPlan{fts: 1, vec: 2, k: 20}
	if got := both.backfill([]Hit{a}, fts, vec); len(got) != 1 {
		t.Errorf("a two-leg plan backfilled %d hits", len(got)-1)
	}
}

// A word in a script without spaces is covered by the chunk containing it.
func TestQueryCoverage_Unsegmented(t *testing.T) {
	fts := []Hit{scored("議事録を共有しました", 3)}
	if got := queryCoverage("議事録", fts); got != 1 {
		t.Errorf("coverage of a contained run = %v, want 1", got)
	}
	if got := queryCoverage("東京", fts); got != 0 {
		t.Errorf("coverage of an absent run = %v, want 0", got)
	}
	mid := []Hit{scored("会議の議事録を共有しました", 3)}
	for _, q := range []string{"議事録*", `"議事録を共"*`} {
		if got := queryCoverage(q, mid); got != 1 {
			t.Errorf("coverage of the prefix %s = %v, want 1", q, got)
		}
	}
	for _, c := range [][2]string{{"ភាសាខ្មែរ", "ខ្ញុំនិយាយភាសាខ្មែរ"}, {"မြန်မာ", "ကျွန်ုပ်မြန်မာစကားပြောသည်"}} {
		if got := queryCoverage(c[0], []Hit{scored(c[1], 3)}); got != 1 {
			t.Errorf("coverage of %s inside a run = %v, want 1", c[0], got)
		}
	}
}

// Only what the query parser reads as a word or a prefix counts.
func TestQueryCoverage_NotWords(t *testing.T) {
	cases := []struct {
		q, doc string
		want   float64
	}{
		{"deploy ⚠️", "deploy succeeded", 1},
		{"✔️done", "all done", 1},
		{"paris ✈️", "paris trip", 1},
		{"hydraulic pump *", "hydraulic pumps failed", 0.5},
		{`hydraulic pump "*`, "hydraulic pumps failed", 0.5},
		{`"hydraulic" *`, "the hydraulics failed", 0},
		{"ｈｙｄｒａｕｌ＊", "hydraulic", 0},
		{"می‌خواهم", "خواهم رفت و می‌روم", 0},
	}
	for _, c := range cases {
		if got := queryCoverage(c.q, []Hit{scored(c.doc, 3)}); got != c.want {
			t.Errorf("queryCoverage(%q, %q) = %v, want %v", c.q, c.doc, got, c.want)
		}
	}
}

// A combining mark stays inside its word, in either normal form.
func TestQueryCoverage_Marks(t *testing.T) {
	cases := []struct {
		q, doc string
		want   float64
	}{
		{"दिल्ली मौसम", "दिल्ली में सम्मेलन", 0.5},
		{"ที่นี่", "ฉันทำงานที่บ้าน", 0},
		{"ที่บ้าน", "ฉันทำงานที่บ้าน", 1},
		{"café", "un cafe\u0301 noir", 1},
	}
	for _, c := range cases {
		if got := queryCoverage(c.q, []Hit{scored(c.doc, 3)}); got != c.want {
			t.Errorf("queryCoverage(%q, %q) = %v, want %v", c.q, c.doc, got, c.want)
		}
	}
}

// A `*` after a phrase that ends in a space marks the phrase's last word.
func TestQueryCoverage_PrefixAfterSpace(t *testing.T) {
	fts := []Hit{scored("the hydraulics failed", 3)}
	if got := queryCoverage(`"hydraulic "*`, fts); got != 1 {
		t.Errorf("coverage = %v, want 1", got)
	}
}

// Configured weights or AdaptiveWeights fix weighted RRF at rrfK; the
// query picks its own form only with none of them set.
func TestOptionsFusionPlan(t *testing.T) {
	fts := []Hit{scored("pfand bottle deposit", 5), scored("pfand", 2)}
	vec := []Hit{scored("", 0.70), scored("", 0.69)}
	if got := (Options{}).fusionPlan("pfand", fts, vec, 30); got != (fusionPlan{fts: 1}) {
		t.Errorf("no weights: plan %+v, want the query's own form", got)
	}
	if got := (Options{VectorWeight: 2}).fusionPlan("pfand", fts, vec, 30); got != (fusionPlan{fts: 1, vec: 2, k: rrfK}) {
		t.Errorf("one weight set: plan %+v", got)
	}
	got := (Options{AdaptiveWeights: true}).fusionPlan("pfand", fts, vec, 30)
	if got.k != rrfK || got.vec != 1 || got.fts <= 0 || got.fts > 1 {
		t.Errorf("adaptive alone: plan %+v, want fixed weights at k %d with the FTS leg scaled", got, rrfK)
	}
}

// The vector leg deciding alone keeps FTS-only hits on the page.
func TestFusionPlan_BackfillFromFTS(t *testing.T) {
	a, b, c := h("ds", "a"), h("ds", "b"), h("ds", "c")
	fts, vec := []Hit{c, a}, []Hit{a, b}
	p := fusionPlan{vec: 1}
	lists, weights := p.lists(fts, vec)
	fused := fuseRRFK(lists, weights, p.k, 0)
	normalizeRRF(fused, weights, len(lists), p.k)
	fused = p.backfill(fused, fts, vec)
	if len(fused) != 3 || fused[2].RecordId != "c" || math.Abs(fused[2].Score-1.0/3) > 1e-12 {
		t.Fatalf("fused = %+v, want a, b, then the FTS-only c at 1/3", fused)
	}
}
