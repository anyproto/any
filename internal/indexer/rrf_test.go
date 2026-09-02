package indexer

import "testing"

func h(dataset, recordId string) Hit {
	return Hit{Dataset: dataset, RecordId: recordId}
}

func TestFuseRRF_OverlapWins(t *testing.T) {
	fts := []Hit{h("chat_messages", "a"), h("chat_messages", "b"), h("editor_blocks", "c")}
	vec := []Hit{h("chat_messages", "b"), h("editor_blocks", "d")}

	out := fuseRRF([][]Hit{fts, vec}, nil, 10)
	if len(out) != 4 {
		t.Fatalf("fused = %d hits, want 4: %+v", len(out), out)
	}
	// b appears in both lists (ranks 1 and 0) — it must outrank a (rank 0
	// in one list only).
	if out[0].RecordId != "b" {
		t.Errorf("top hit = %s, want b (in both lists)", out[0].RecordId)
	}
	wantB := 1.0/float64(rrfK+2) + 1.0/float64(rrfK+1)
	if out[0].Score != wantB {
		t.Errorf("b score = %v, want %v", out[0].Score, wantB)
	}
}

func TestFuseRRF_LimitAndDeterminism(t *testing.T) {
	// Two docs with identical contribution — tie broken by key.
	l1 := []Hit{h("ds", "x")}
	l2 := []Hit{h("ds", "a")}
	out := fuseRRF([][]Hit{l1, l2}, nil, 10)
	if len(out) != 2 || out[0].RecordId != "a" {
		t.Fatalf("tie should break by key: %+v", out)
	}

	out = fuseRRF([][]Hit{l1, l2}, nil, 1)
	if len(out) != 1 {
		t.Fatalf("limit not applied: %+v", out)
	}
}

func TestFuseRRF_CrossObjectSameRecordId(t *testing.T) {
	// Two objects whose prop/name records both match — recordIds repeat
	// across objects (propIds do), so they must stay separate hits, each
	// capped at the single-doc RRF ceiling of 2/(rrfK+1).
	o1 := Hit{ObjectId: "obj1", Dataset: "prop", RecordId: "name"}
	o2 := Hit{ObjectId: "obj2", Dataset: "prop", RecordId: "name"}
	fts := []Hit{o1, o2}
	vec := []Hit{o2, o1}

	out := fuseRRF([][]Hit{fts, vec}, nil, 10)
	if len(out) != 2 {
		t.Fatalf("fused = %d hits, want 2 (no cross-object collapse): %+v", len(out), out)
	}
	ceiling := 2.0 / float64(rrfK+1)
	for _, h := range out {
		if h.Score > ceiling {
			t.Errorf("%s score = %v, above single-doc ceiling %v (colliding contributions summed)", h.ObjectId, h.Score, ceiling)
		}
	}
}

func TestFuseRRF_Empty(t *testing.T) {
	if out := fuseRRF([][]Hit{nil, {}}, nil, 5); len(out) != 0 {
		t.Fatalf("empty legs should fuse to nothing: %+v", out)
	}
}

func TestFuseRRF_Weights(t *testing.T) {
	// Same doc at the same rank in each leg; an absent doc in the other.
	// With the vector leg (index 1) down-weighted, the FTS-only doc must
	// outrank the vector-only doc.
	fts := []Hit{h("ds", "ftsonly")}
	vec := []Hit{h("ds", "veconly")}
	out := fuseRRF([][]Hit{fts, vec}, []float64{1.0, 0.1}, 10)
	if len(out) != 2 || out[0].RecordId != "ftsonly" {
		t.Fatalf("down-weighted vector leg should rank below fts: %+v", out)
	}
	wantFts := 1.0 / float64(rrfK+1)
	wantVec := 0.1 / float64(rrfK+1)
	if out[0].Score != wantFts || out[1].Score != wantVec {
		t.Errorf("weighted scores = %v/%v, want %v/%v", out[0].Score, out[1].Score, wantFts, wantVec)
	}
	// A zero/negative weight entry falls back to 1 (nil-safe default).
	out = fuseRRF([][]Hit{fts, vec}, []float64{1.0, 0}, 10)
	if out[0].Score != out[1].Score {
		t.Errorf("zero weight should default to 1: %v vs %v", out[0].Score, out[1].Score)
	}
}

func TestLegConfidence(t *testing.T) {
	mk := func(scores ...float64) []Hit {
		h := make([]Hit, len(scores))
		for i, s := range scores {
			h[i] = Hit{RecordId: string(rune('a' + i)), Score: s}
		}
		return h
	}
	// Peaked (top dominates) → high confidence.
	if c := legConfidence(mk(10, 1, 1, 1)); c < 0.8 {
		t.Errorf("peaked confidence = %.2f, want > 0.8", c)
	}
	// Flat → low confidence.
	if c := legConfidence(mk(1.0, 0.99, 0.98, 0.97)); c > 0.1 {
		t.Errorf("flat confidence = %.2f, want < 0.1", c)
	}
	// Single hit / empty → no penalty.
	if c := legConfidence(mk(0.5)); c != 1 {
		t.Errorf("single-hit confidence = %.2f, want 1", c)
	}
	if c := legConfidence(nil); c != 1 {
		t.Errorf("empty confidence = %.2f, want 1", c)
	}
}

// Chunks of one record are distinct fusion keys: they neither merge
// nor sum their rank mass.
func TestFuseRRF_ChunksStayDistinct(t *testing.T) {
	c0 := Hit{ObjectId: "o", Dataset: "d", RecordId: "r", Chunk: 0, Data: "zero"}
	c1 := Hit{ObjectId: "o", Dataset: "d", RecordId: "r", Chunk: 1, Data: "one"}
	out := fuseRRF([][]Hit{{c0}, {c1}}, nil, 10)
	if len(out) != 2 {
		t.Fatalf("fused = %+v, want both chunks", out)
	}
	if out[0].Score != out[1].Score {
		t.Fatalf("scores differ %v / %v — one chunk absorbed the other's mass", out[0].Score, out[1].Score)
	}
}

func TestGroupHits_MaxNotSum(t *testing.T) {
	// Two chunks of one record fuse to ONE group scored by its best
	// chunk; the best chunk is the representative, the other a passage.
	c0 := Hit{ObjectId: "o", Dataset: "d", RecordId: "r", Chunk: 0, Score: 0.2}
	c1 := Hit{ObjectId: "o", Dataset: "d", RecordId: "r", Chunk: 1, Score: 0.5}
	out := groupHits([]Hit{c0, c1}, 10, 3)
	if len(out) != 1 {
		t.Fatalf("groups = %+v, want one record", out)
	}
	if g := out[0]; g.Hit.Chunk != 1 || g.Hit.Score != 0.5 || len(g.Passages) != 1 || g.Passages[0].Chunk != 0 {
		t.Fatalf("group = %+v, want chunk 1 on top, chunk 0 as passage", g)
	}
	if out := groupHits([]Hit{c0, c1}, 10, 0); out[0].Passages != nil {
		t.Fatalf("passages 0 must carry none: %+v", out)
	}
}

func TestGroupHits_CrossObjectSameRecordId(t *testing.T) {
	// recordIds repeat across objects (propIds do): two groups.
	o1 := Hit{ObjectId: "obj1", Dataset: "prop", RecordId: "name", Score: 1}
	o2 := Hit{ObjectId: "obj2", Dataset: "prop", RecordId: "name", Score: 1}
	if out := groupHits([]Hit{o1, o2}, 10, 0); len(out) != 2 {
		t.Fatalf("groups = %+v, want 2", out)
	}
}

func TestGroupHits_OrderLimitPassages(t *testing.T) {
	mk := func(rec string, chunk int, score float64) Hit {
		return Hit{ObjectId: "o", Dataset: "d", RecordId: rec, Chunk: chunk, Score: score}
	}
	hits := []Hit{
		mk("a", 0, 0.3), mk("b", 2, 0.9), mk("b", 0, 0.4), mk("b", 1, 0.6), mk("b", 3, 0.5),
		mk("c", 0, 0.9), // ties b on score: key order decides, deterministically
	}
	out := groupHits(hits, 0, 2)
	if len(out) != 3 || out[0].Hit.RecordId != "b" || out[1].Hit.RecordId != "c" || out[2].Hit.RecordId != "a" {
		t.Fatalf("order = %+v", out)
	}
	if p := out[0].Passages; len(p) != 2 || p[0].Chunk != 1 || p[1].Chunk != 3 {
		t.Fatalf("b passages = %+v, want chunks 1 (0.6) then 3 (0.5)", p)
	}
	if out := groupHits(hits, 2, 0); len(out) != 2 || out[1].Hit.RecordId != "c" {
		t.Fatalf("limit 2 = %+v", out)
	}
	if out := groupHits(nil, 5, 5); len(out) != 0 {
		t.Fatalf("empty = %+v", out)
	}
}
