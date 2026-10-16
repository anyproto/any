package server

import (
	"sync"
	"testing"
	"time"
)

// TestEngineGate covers the lifecycle the teardown path relies on: an
// open zero value, close refusing new entries and draining only once
// the last holder leaves, reset reopening it.
func TestEngineGate(t *testing.T) {
	var g engineGate
	h1 := g.enter()
	if h1 == nil {
		t.Fatal("zero-value gate must admit")
	}
	h2 := g.enter()
	if h2 == nil {
		t.Fatal("second entry must be admitted")
	}

	drained := g.close()
	if g.enter() != nil {
		t.Fatal("closed gate must refuse")
	}
	select {
	case <-drained:
		t.Fatal("drained before the holders left")
	case <-time.After(20 * time.Millisecond):
	}
	g.leave(h1)
	select {
	case <-drained:
		t.Fatal("drained with one holder still inside")
	case <-time.After(20 * time.Millisecond):
	}
	g.leave(h2)
	if !waitClosed(drained, time.Second) {
		t.Fatal("not drained after the last holder left")
	}
	// close is idempotent while closed and answers an already-drained
	// channel.
	if !waitClosed(g.close(), time.Second) {
		t.Fatal("second close must report drained")
	}

	g.reset()
	h3 := g.enter()
	if h3 == nil {
		t.Fatal("reset gate must admit again")
	}
	g.leave(h3)
	// Closing with nobody inside drains at once.
	if !waitClosed(g.close(), time.Second) {
		t.Fatal("empty gate must drain immediately")
	}
}

// TestEngineGate_Concurrent hammers enter/leave against a close: no
// holder is ever lost and the drain fires exactly when the count hits
// zero (the -race run is the point).
func TestEngineGate_Concurrent(t *testing.T) {
	var g engineGate
	var wg sync.WaitGroup
	var admitted sync.WaitGroup
	release := make(chan struct{})
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := g.enter()
			if h == nil {
				return
			}
			admitted.Add(1)
			<-release
			g.leave(h)
			admitted.Done()
		}()
	}
	// Let every goroutine reach enter before closing so the drain has
	// holders to wait for; stragglers that lose the race are refused,
	// which is also correct.
	time.Sleep(20 * time.Millisecond)
	drained := g.close()
	select {
	case <-drained:
		t.Fatal("drained while holders are parked")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if !waitClosed(drained, time.Second) {
		t.Fatal("drain did not complete")
	}
	wg.Wait()
	admitted.Wait()
}

// TestEngineGate_StragglerCompletesItsOwnDrain: a holder that outlives
// a timed-out drain neither holds up the next engine's drain nor loses
// its own, which completes when it leaves.
func TestEngineGate_StragglerCompletesItsOwnDrain(t *testing.T) {
	var g engineGate
	straggler := g.enter()
	drained := g.close() // its teardown times out
	g.reset()            // the next engine boots in place
	stream := g.enter()  // one of the next engine's requests
	next := g.close()    // the next engine's teardown
	select {
	case <-next:
		t.Fatal("the next engine's drain completed with its holder inside")
	case <-time.After(20 * time.Millisecond):
	}
	g.leave(stream)
	if !waitClosed(next, time.Second) {
		t.Fatal("the next engine's drain waited for the previous engine's straggler")
	}
	select {
	case <-drained:
		t.Fatal("the straggler's drain completed while it is inside")
	default:
	}
	g.leave(straggler)
	if !waitClosed(drained, time.Second) {
		t.Fatal("the straggler's drain did not complete when it left")
	}
}
