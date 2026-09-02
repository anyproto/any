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
// drains.
//
// The zero value is an open gate — hand-built test deps need nothing.
type engineGate struct {
	mu      sync.Mutex
	n       int
	closed  bool
	drained chan struct{} // closed once n reaches 0 after close
}

// enter admits one request; false once the gate is closed.
func (g *engineGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.n++
	return true
}

// leave releases one admission.
func (g *engineGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n--
	if g.n == 0 && g.closed && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

// close refuses further entries and returns a channel closed once every
// current holder has left. Idempotent while closed.
func (g *engineGate) close() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		g.closed = true
		g.drained = make(chan struct{})
		if g.n == 0 {
			close(g.drained)
			g.drained = nil
		}
	}
	if g.drained == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return g.drained
}

// reset reopens the gate for the next engine. A holder that outlived a
// timed-out drain stays counted: it never blocks entries, but every
// later close waits for it (up to the deadline) until it leaves —
// the honest reading of a request that is still executing.
func (g *engineGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = false
	g.drained = nil
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
