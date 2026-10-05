package markdown

import (
	"context"
	"errors"
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

	acquired := make(chan func())
	go func() {
		unlock, err := l.lock(ctx, "a")
		if err != nil {
			t.Errorf("waiter: %v", err)
		}
		acquired <- unlock
	}()
	unlockA()
	(<-acquired)()

	if len(l.held) != 0 {
		t.Fatalf("held = %v after every lock was released, want empty", l.held)
	}
}
