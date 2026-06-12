package indexer

import "testing"

func h(dataset, recordId string) Hit {
	return Hit{Dataset: dataset, RecordId: recordId}
}

func TestFuseRRF_OverlapWins(t *testing.T) {
	fts := []Hit{h("chat_messages", "a"), h("chat_messages", "b"), h("editor_blocks", "c")}
	vec := []Hit{h("chat_messages", "b"), h("editor_blocks", "d")}

	out := fuseRRF([][]Hit{fts, vec}, 10)
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
	out := fuseRRF([][]Hit{l1, l2}, 10)
	if len(out) != 2 || out[0].RecordId != "a" {
		t.Fatalf("tie should break by key: %+v", out)
	}

	out = fuseRRF([][]Hit{l1, l2}, 1)
	if len(out) != 1 {
		t.Fatalf("limit not applied: %+v", out)
	}
}

func TestFuseRRF_Empty(t *testing.T) {
	if out := fuseRRF([][]Hit{nil, {}}, 5); len(out) != 0 {
		t.Fatalf("empty legs should fuse to nothing: %+v", out)
	}
}
