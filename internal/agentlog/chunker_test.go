package agentlog

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestTurnData(t *testing.T) {
	arena := &anyenc.Arena{}
	strArr := func(vals ...string) *anyenc.Value {
		a := arena.NewArray()
		for i, v := range vals {
			a.SetArrayItem(i, arena.NewString(v))
		}
		return a
	}

	rec := arena.NewObject()
	rec.Set(FieldUserText, arena.NewString("which digest is bigger?"))
	rec.Set(FieldReplies, strArr("The HN digest is bigger.", "Want the breakdown?"))
	// Narration / effects / scalars must NOT enter the indexed text.
	rec.Set(FieldThink, arena.NewString("user compares digests"))
	rec.Set(FieldEffects, strArr("created Digest [x](any://s/o)"))

	got := turnData(rec)
	want := "which digest is bigger?\nThe HN digest is bigger. Want the breakdown?"
	if got != want {
		t.Fatalf("turnData =\n%q\nwant\n%q", got, want)
	}
}

func TestTurnData_UserTextOnly(t *testing.T) {
	arena := &anyenc.Arena{}
	rec := arena.NewObject()
	rec.Set(FieldUserText, arena.NewString("hello"))
	if got := turnData(rec); got != "hello" {
		t.Fatalf("turnData = %q, want %q", got, "hello")
	}
}

func TestChunkerDatasetsAndGate(t *testing.T) {
	if NewTurnChunker().Dataset() != DatasetTurns {
		t.Error("turn chunker wrong dataset")
	}
	if NewChunkChunker().Dataset() != DatasetChunks {
		t.Error("chunk chunker wrong dataset")
	}
	// Both gated on agent_log type membership.
	if NewTurnChunker().TypeId() != TypeId || NewChunkChunker().TypeId() != TypeId {
		t.Error("history chunkers must be gated on agent_log TypeId")
	}
}
