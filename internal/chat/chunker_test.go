package chat

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestMessageData(t *testing.T) {
	arena := &anyenc.Arena{}

	withText := arena.NewObject()
	withText.Set("id", arena.NewString("m1"))
	withText.Set(FieldText, arena.NewString("hello world"))
	// creator / reactions present but deliberately excluded from Data.
	withText.Set(FieldCreator, arena.NewString("acct1"))
	reactions := arena.NewObject()
	reactions.Set("👍", arena.NewObject())
	withText.Set(FieldReactions, reactions)
	if got := messageData(withText); got != "hello world" {
		t.Errorf("messageData = %q, want %q", got, "hello world")
	}

	noText := arena.NewObject()
	noText.Set("id", arena.NewString("m2"))
	noText.Set(FieldCreator, arena.NewString("acct1"))
	if got := messageData(noText); got != "" {
		t.Errorf("messageData (no text) = %q, want empty", got)
	}

	// Deleted messages are tombstones — content wiped.
	tomb := arena.NewObject()
	tomb.Set("id", arena.NewString("m3"))
	tomb.Set("_deletedAt", arena.NewNumberInt(123))
	if got := messageData(tomb); got != "" {
		t.Errorf("messageData (tombstone) = %q, want empty", got)
	}

	if got := messageData(nil); got != "" {
		t.Errorf("messageData(nil) = %q, want empty", got)
	}
}
