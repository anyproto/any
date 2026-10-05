package markdown

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestDocLocks(t *testing.T) {
	l := docLocks{held: map[string]*docLock{}}
	ctx := context.Background()

	unlockA, err := l.lock(ctx, "a")
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}

	// Another document's lock is independent.
	unlockB, err := l.lock(ctx, "b")
	if err != nil {
		t.Fatalf("lock b while a is held: %v", err)
	}
	unlockB()

	// A second writer of the same document waits; giving up drops its
	// place without touching the holder.
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := l.lock(waitCtx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock a while held: %v, want DeadlineExceeded", err)
	}

	// A waiter registered while a is held gets the lock only on release.
	acquired := make(chan func(), 1)
	go func() {
		unlock, err := l.lock(ctx, "a")
		if err != nil {
			t.Errorf("waiter: %v", err)
			unlock = func() {}
		}
		acquired <- unlock
	}()
	for refs := 0; refs != 2; runtime.Gosched() {
		l.mu.Lock()
		refs = l.held["a"].refs
		l.mu.Unlock()
	}
	select {
	case <-acquired:
		t.Fatal("the waiter took the lock while it was held")
	default:
	}
	unlockA()
	(<-acquired)()

	if len(l.held) != 0 {
		t.Fatalf("held = %v after every lock was released, want empty", l.held)
	}
}
