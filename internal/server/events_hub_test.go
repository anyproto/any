package server

import (
	"testing"

	"github.com/anyproto/any/internal/api"
)

func TestEventHubFanout(t *testing.T) {
	h := newEventHub()
	_, ch1 := h.subscribe(eventFilter{})
	_, ch2 := h.subscribe(eventFilter{})

	ev := api.Event{Type: "test.ping", Scope: api.EventScopeDevice}
	if n := h.publish(ev); n != 2 {
		t.Fatalf("publish returned %d, want 2", n)
	}
	for i, ch := range []<-chan api.Event{ch1, ch2} {
		got := <-ch
		if got.Type != ev.Type {
			t.Errorf("sub %d got type %q, want %q", i, got.Type, ev.Type)
		}
	}
}

func TestEventHubFilters(t *testing.T) {
	h := newEventHub()
	ev := api.Event{Type: "process.progress", Scope: api.EventScopeDevice, Target: "run1"}

	cases := []struct {
		name  string
		f     eventFilter
		match bool
	}{
		{"empty matches all", eventFilter{}, true},
		{"exact type", eventFilter{types: []string{"process.progress"}}, true},
		{"prefix type", eventFilter{types: []string{"process.*"}}, true},
		{"prefix matches root", eventFilter{types: []string{"process.progress.*"}}, true},
		{"other type", eventFilter{types: []string{"ui.open_space"}}, false},
		{"prefix is segment-bound", eventFilter{types: []string{"proc.*"}}, false},
		{"scope", eventFilter{scopes: []string{api.EventScopeDevice}}, true},
		{"other scope", eventFilter{scopes: []string{api.EventScopeSpace}}, false},
		{"target", eventFilter{targets: []string{"run1"}}, true},
		{"other target", eventFilter{targets: []string{"run2"}}, false},
		{"spaceId empty event", eventFilter{spaceIds: []string{"sp1"}}, false},
		{"and across dims", eventFilter{types: []string{"process.*"}, targets: []string{"run2"}}, false},
		{"or within dim", eventFilter{types: []string{"ui.*", "process.*"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ch := h.subscribe(tc.f)
			defer h.unsubscribe(id)
			n := h.publish(ev)
			if tc.match {
				if n != 1 {
					t.Fatalf("publish returned %d, want 1", n)
				}
				<-ch
			} else if n != 0 {
				t.Fatalf("publish returned %d, want 0", n)
			}
			if got := h.matchCount(&ev); (got == 1) != tc.match {
				t.Errorf("matchCount = %d, match = %v", got, tc.match)
			}
		})
	}
}

func TestEventHubNoSubscribers(t *testing.T) {
	h := newEventHub()
	if n := h.publish(api.Event{Type: "test.ping"}); n != 0 {
		t.Fatalf("publish returned %d, want 0", n)
	}
}

func TestEventHubUnsubscribe(t *testing.T) {
	h := newEventHub()
	id, ch := h.subscribe(eventFilter{})
	h.unsubscribe(id)
	if _, ok := <-ch; ok {
		t.Fatal("channel not closed after unsubscribe")
	}
	h.unsubscribe(id) // idempotent
	if n := h.publish(api.Event{Type: "test.ping"}); n != 0 {
		t.Fatalf("publish after unsubscribe returned %d, want 0", n)
	}
}

func TestEventHubOverflowDropsSlowSubscriber(t *testing.T) {
	h := newEventHub()
	_, ch := h.subscribe(eventFilter{})

	ev := api.Event{Type: "test.ping"}
	for i := 0; i < eventSubBuffer; i++ {
		if n := h.publish(ev); n != 1 {
			t.Fatalf("publish %d returned %d, want 1", i, n)
		}
	}
	// Buffer full: the next publish drops the subscriber.
	if n := h.publish(ev); n != 0 {
		t.Fatalf("overflow publish returned %d, want 0", n)
	}
	for i := 0; i < eventSubBuffer; i++ {
		if _, ok := <-ch; !ok {
			t.Fatalf("channel closed before draining buffered event %d", i)
		}
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel not closed after overflow drop")
	}
}
