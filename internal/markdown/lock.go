package markdown

import (
	"context"
	"sync"
)

// writeLocks serializes the markdown writes to one document on this
// server: a write's read, version check, diff, write and re-read run
// under the document's lock, so two markdown writes cannot interleave.
// Reads and the …/blocks routes do not take it.
var writeLocks = docLocks{held: map[string]*docLock{}}

type docLocks struct {
	mu   sync.Mutex
	held map[string]*docLock
}

// docLock is one document's lock: holding the channel's single slot is
// holding the lock. refs counts the holder and the waiters, so the entry
// goes away with the last of them.
type docLock struct {
	slot chan struct{}
	refs int
}

// lock waits for key's lock or for ctx to end, and returns the func
// that releases it.
func (l *docLocks) lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	d := l.held[key]
	if d == nil {
		d = &docLock{slot: make(chan struct{}, 1)}
		l.held[key] = d
	}
	d.refs++
	l.mu.Unlock()

	select {
	case d.slot <- struct{}{}:
		return func() {
			<-d.slot
			l.release(key, d)
		}, nil
	case <-ctx.Done():
		l.release(key, d)
		return nil, ctx.Err()
	}
}

func (l *docLocks) release(key string, d *docLock) {
	l.mu.Lock()
	if d.refs--; d.refs == 0 {
		delete(l.held, key)
	}
	l.mu.Unlock()
}

// lockDocument takes the write lock of objectId's body in collection.
func lockDocument(ctx context.Context, spaceId, objectId, collection string) (func(), error) {
	return writeLocks.lock(ctx, spaceId+"/"+objectId+"/"+collection)
}
