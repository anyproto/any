package server

import (
	"slices"
	"strings"
	"sync"

	"github.com/anyproto/any/internal/api"
)

// eventSubBuffer bounds each subscriber's in-flight event backlog. A
// subscriber whose SSE writer falls this far behind is dropped (its
// channel closed) so a slow or stuck consumer never blocks the
// publisher; the stream handler then emits closed{reason:"overflow"}
// and the client reconnects.
const eventSubBuffer = 16

// eventFilter selects which events a subscriber receives. An empty
// dimension matches everything; a non-empty one requires membership.
// types entries are either exact ("process.progress") or a prefix
// ("process.*" — matches "process" and everything under "process.").
type eventFilter struct {
	scopes   []string
	spaceIds []string
	types    []string
	targets  []string
}

func (f *eventFilter) matches(ev *api.Event) bool {
	if len(f.scopes) > 0 && !slices.Contains(f.scopes, ev.Scope) {
		return false
	}
	if len(f.spaceIds) > 0 && !slices.Contains(f.spaceIds, ev.SpaceId) {
		return false
	}
	if len(f.targets) > 0 && !slices.Contains(f.targets, ev.Target) {
		return false
	}
	if len(f.types) > 0 {
		ok := false
		for _, t := range f.types {
			if typeFilterMatches(t, ev.Type) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// typeFilterMatches reports whether one filter entry accepts typ.
// "a.b" matches exactly; "a.b.*" matches "a.b" and any "a.b.<rest>" —
// the subtree, root included (mirrors the pubsub topic mapping, where
// the target segment makes even a bare type one level deep).
func typeFilterMatches(entry, typ string) bool {
	if prefix, ok := strings.CutSuffix(entry, ".*"); ok {
		return typ == prefix || strings.HasPrefix(typ, prefix+".")
	}
	return typ == entry
}

type eventSub struct {
	ch     chan api.Event
	filter eventFilter
}

// eventHub is the process-global, in-memory broadcast hub behind the
// event bus (POST /v1/events → GET /v1/events/subscribe). publish fans
// an event out to every subscriber whose filter matches, with a
// non-blocking send: nothing is stored, there is no replay, delivery
// is at-most-once (an event fired with no matching subscriber — or to
// one whose buffer is full — is dropped for that subscriber). One
// account per server, so a single global hub.
type eventHub struct {
	mu   sync.Mutex
	next int
	subs map[int]eventSub
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[int]eventSub)}
}

// subscribe registers a new subscriber and returns its id and event
// channel. The caller must unsubscribe when done. The channel is
// buffered; publish closes and drops it on overflow, so a receiver
// must treat a closed channel (ok == false) as an overflow signal.
func (h *eventHub) subscribe(f eventFilter) (int, <-chan api.Event) {
	ch := make(chan api.Event, eventSubBuffer)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = eventSub{ch: ch, filter: f}
	h.mu.Unlock()
	return id, ch
}

// unsubscribe removes a subscriber and closes its channel. Idempotent:
// a no-op if the subscriber was already dropped by an overflow
// (publish holds the same lock, so there is no double close).
func (h *eventHub) unsubscribe(id int) {
	h.mu.Lock()
	if s, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(s.ch)
	}
	h.mu.Unlock()
}

// publish fans ev out to every subscriber whose filter matches and
// returns the number that accepted it. A matching subscriber whose
// buffer is full is dropped (channel closed and removed) rather than
// blocking the publisher, and is not counted as delivered.
func (h *eventHub) publish(ev api.Event) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	delivered := 0
	for id, s := range h.subs {
		if !s.filter.matches(&ev) {
			continue
		}
		select {
		case s.ch <- ev:
			delivered++
		default:
			delete(h.subs, id)
			close(s.ch)
		}
	}
	return delivered
}

// matchCount returns how many current subscribers' filters match ev
// AND can actually be reached by it. Used for the publish reply on
// network scopes, where delivery rides the SDK's loopback instead of
// a direct fan-out: a space-scope event only reaches subscribers whose
// filter names the space (only those hold a pub/sub interest on it —
// there is no "all spaces" interest), so a catch-all subscriber that
// merely matches is not counted.
func (h *eventHub) matchCount(ev *api.Event) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.subs {
		if !s.filter.matches(ev) {
			continue
		}
		if ev.Scope == api.EventScopeSpace && !slices.Contains(s.filter.spaceIds, ev.SpaceId) {
			continue
		}
		n++
	}
	return n
}

// eventsHub lazily creates the per-process hub on first use. Done this
// way so every deps construction path (server.Run and the test
// helpers) gets a working hub with no explicit wiring — the hub has no
// engine or SDK dependency. Internal producers (indexer, sync
// milestones) publish through it directly.
func (d *deps) eventsHub() *eventHub {
	d.eventsOnce.Do(func() { d.events = newEventHub() })
	return d.events
}
