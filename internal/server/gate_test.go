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
	if !g.enter() {
		t.Fatal("zero-value gate must admit")
	}
	if !g.enter() {
		t.Fatal("second entry must be admitted")
	}

	drained := g.close()
	if g.enter() {
		t.Fatal("closed gate must refuse")
	}
	select {
	case <-drained:
		t.Fatal("drained before the holders left")
	case <-time.After(20 * time.Millisecond):
	}
	g.leave()
	select {
	case <-drained:
		t.Fatal("drained with one holder still inside")
	case <-time.After(20 * time.Millisecond):
	}
	g.leave()
	if !waitClosed(drained, time.Second) {
		t.Fatal("not drained after the last holder left")
	}
	// close is idempotent while closed and answers an already-drained
	// channel.
	if !waitClosed(g.close(), time.Second) {
		t.Fatal("second close must report drained")
	}

	g.reset()
	if !g.enter() {
		t.Fatal("reset gate must admit again")
	}
	g.leave()
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
			if !g.enter() {
				return
			}
			admitted.Add(1)
			<-release
			g.leave()
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
