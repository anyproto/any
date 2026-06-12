package editor

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestBlockData(t *testing.T) {
	arena := &anyenc.Arena{}

	withText := arena.NewObject()
	withText.Set("id", arena.NewString("b1"))
	withText.Set(FieldText, arena.NewString("**bold** inline"))
	if got := blockData(withText); got != "**bold** inline" {
		t.Errorf("blockData = %q, want %q", got, "**bold** inline")
	}

	noText := arena.NewObject()
	noText.Set("id", arena.NewString("b2"))
	noText.Set(FieldType, arena.NewString("paragraph"))
	if got := blockData(noText); got != "" {
		t.Errorf("blockData (no text) = %q, want empty", got)
	}

	// Deleted blocks are tombstones — content wiped, so blockData reads
	// empty. The chunker further gates on IsDeleted to emit Data "".
	tomb := arena.NewObject()
	tomb.Set("id", arena.NewString("b3"))
	tomb.Set("_deletedAt", arena.NewNumberInt(123))
	if got := blockData(tomb); got != "" {
		t.Errorf("blockData (tombstone) = %q, want empty", got)
	}

	if got := blockData(nil); got != "" {
		t.Errorf("blockData(nil) = %q, want empty", got)
	}
}
