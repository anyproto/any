package server

import (
	"sync"
	"time"
)

// engineGate counts the requests and streams currently executing
// against the live engine, so a teardown can refuse new ones and wait
// for the rest to leave before the engine's fields are closed and
// replaced. It is what makes the per-request `d.sdk` reads safe across
// an in-place account switch: a handler that entered the gate sees one
// engine for its whole lifetime, and the swap happens only between
// drains. A drain that timed out is the exception: its straggler keeps
// executing while the fields belong to the next engine, or to none.
//
// The zero value is an open gate — hand-built test deps need nothing.
type engineGate struct {
	mu  sync.Mutex
	cur *gateGen
}

// gateGen is one engine's admissions: each close ends a generation and
// reset starts the next. A holder is counted against the generation it
// entered, so one that outlives a timed-out drain completes that drain
// when it leaves — never the next engine's — and the next engine's
// teardown does not wait for it.
type gateGen struct {
	n       int
	closed  bool
	drained chan struct{} // closed once n reaches 0 after close
}

// enter admits one request and returns the token leave takes; nil once
// the gate is closed.
func (g *engineGate) enter() *gateGen {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cur == nil {
		g.cur = &gateGen{}
	}
	if g.cur.closed {
		return nil
	}
	g.cur.n++
	return g.cur
}

// leave releases the admission h.
func (g *engineGate) leave(h *gateGen) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h.n--
	if h.n == 0 && h.closed && h.drained != nil {
		close(h.drained)
		h.drained = nil
	}
}

// close refuses further entries and returns a channel closed once every
// current holder has left. Idempotent while closed.
func (g *engineGate) close() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cur == nil {
		g.cur = &gateGen{}
	}
	gen := g.cur
	if !gen.closed {
		gen.closed = true
		gen.drained = make(chan struct{})
		if gen.n == 0 {
			close(gen.drained)
			gen.drained = nil
		}
	}
	if gen.drained == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return gen.drained
}

// reset opens the gate for the next engine with a fresh generation. A
// holder that outlived the previous drain stays counted against that
// one.
func (g *engineGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cur = &gateGen{}
}

// enterGate admits a request to the live engine: nil while unauthorized
// or once the gate is closed.
func (d *deps) enterGate() *gateGen {
	if !d.ready.Load() {
		return nil
	}
	return d.gate.enter()
}

// waitClosed waits for the channel with a deadline; false on timeout.
func waitClosed(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}
