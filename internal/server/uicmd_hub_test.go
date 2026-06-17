package server

import (
	"testing"

	"github.com/anyproto/any/internal/api"
)

func TestUICmdHubFanout(t *testing.T) {
	h := newUICmdHub()

	_, a := h.subscribe()
	_, b := h.subscribe()

	cmd := api.UICommand{Action: api.UICommandOpenObject, SpaceId: "s1", ObjectId: "o1"}
	if n := h.publish(cmd); n != 2 {
		t.Fatalf("publish delivered to %d subscribers, want 2", n)
	}
	if got := <-a; got != cmd {
		t.Fatalf("subscriber a got %+v, want %+v", got, cmd)
	}
	if got := <-b; got != cmd {
		t.Fatalf("subscriber b got %+v, want %+v", got, cmd)
	}
}

func TestUICmdHubNoSubscribers(t *testing.T) {
	h := newUICmdHub()
	if n := h.publish(api.UICommand{Action: api.UICommandOpenSpace, SpaceId: "s1"}); n != 0 {
		t.Fatalf("publish with no subscribers delivered %d, want 0", n)
	}
}

func TestUICmdHubUnsubscribe(t *testing.T) {
	h := newUICmdHub()
	id, ch := h.subscribe()
	h.unsubscribe(id)
	// Channel is closed and drained.
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel after unsubscribe")
	}
	// Idempotent.
	h.unsubscribe(id)
	if n := h.publish(api.UICommand{Action: api.UICommandOpenSpace, SpaceId: "s1"}); n != 0 {
		t.Fatalf("publish after unsubscribe delivered %d, want 0", n)
	}
}

func TestUICmdHubOverflowDropsSlowSubscriber(t *testing.T) {
	h := newUICmdHub()
	id, ch := h.subscribe()

	// Fill the buffer plus one — the overflowing publish must drop and
	// close the subscriber rather than block.
	cmd := api.UICommand{Action: api.UICommandOpenSpace, SpaceId: "s1"}
	for i := 0; i < uiCmdSubBuffer; i++ {
		if n := h.publish(cmd); n != 1 {
			t.Fatalf("publish %d delivered %d, want 1", i, n)
		}
	}
	if n := h.publish(cmd); n != 0 {
		t.Fatalf("overflowing publish delivered %d, want 0 (subscriber dropped)", n)
	}

	// Drain the buffered commands, then observe the close.
	for i := 0; i < uiCmdSubBuffer; i++ {
		if _, ok := <-ch; !ok {
			t.Fatalf("channel closed early at %d", i)
		}
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after overflow drop")
	}

	// The dropped subscriber is gone; unsubscribe is a safe no-op.
	h.unsubscribe(id)
}
