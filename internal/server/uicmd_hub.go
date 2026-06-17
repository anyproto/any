package server

import (
	"sync"

	"github.com/anyproto/any/internal/api"
)

// uiCmdSubBuffer bounds each subscriber's in-flight command backlog. A
// subscriber whose SSE writer falls this far behind is dropped (its
// channel closed) so a slow or stuck UI never blocks the publisher; the
// stream handler then emits closed{reason:"overflow"} and the client
// reconnects. UI commands are navigation directives — small and rare —
// so a modest buffer is ample.
const uiCmdSubBuffer = 16

// uiCmdHub is the process-global, in-memory broadcast channel behind the
// account-wide UI command endpoints (POST /v1/ui/commands →
// GET /v1/ui/commands/subscribe). publish fans a command out to every
// current subscriber with a non-blocking send: nothing is stored, there
// is no replay, delivery is at-most-once (a command fired with no
// subscriber — or to one whose buffer is full — is dropped for that
// subscriber). One account per server, so a single global hub.
type uiCmdHub struct {
	mu   sync.Mutex
	next int
	subs map[int]chan api.UICommand
}

func newUICmdHub() *uiCmdHub {
	return &uiCmdHub{subs: make(map[int]chan api.UICommand)}
}

// subscribe registers a new subscriber and returns its id and command
// channel. The caller must unsubscribe when done. The channel is
// buffered; publish closes and drops it on overflow, so a receiver must
// treat a closed channel (ok == false) as an overflow signal.
func (h *uiCmdHub) subscribe() (int, <-chan api.UICommand) {
	ch := make(chan api.UICommand, uiCmdSubBuffer)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	return id, ch
}

// unsubscribe removes a subscriber and closes its channel. Idempotent:
// a no-op if the subscriber was already dropped by an overflow (publish
// holds the same lock, so there is no double close).
func (h *uiCmdHub) unsubscribe(id int) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

// publish fans cmd out to every current subscriber and returns the
// number that accepted it. A subscriber whose buffer is full is dropped
// (channel closed and removed) rather than blocking the publisher, and
// is not counted as delivered.
func (h *uiCmdHub) publish(cmd api.UICommand) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	delivered := 0
	for id, ch := range h.subs {
		select {
		case ch <- cmd:
			delivered++
		default:
			delete(h.subs, id)
			close(ch)
		}
	}
	return delivered
}

// uiHub lazily creates the per-process hub on first use. Done this way
// so every deps construction path (server.Run and the test helpers)
// gets a working hub with no explicit wiring — the hub has no engine or
// SDK dependency.
func (d *deps) uiHub() *uiCmdHub {
	d.uiHubOnce.Do(func() { d.uiCommands = newUICmdHub() })
	return d.uiCommands
}
