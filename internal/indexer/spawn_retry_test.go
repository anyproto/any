package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// fakeSpaces is the indexer tests' space.Service seam. Get fails failN
// times, then returns a stub space, unless get overrides it; list and
// the subscription are optional. Embeds the interface so unimplemented
// methods panic if reached.
type fakeSpaces struct {
	space.Service
	mu    sync.Mutex
	failN int
	gets  int
	list  func(context.Context) ([]space.SpaceInfo, error)
	get   func(context.Context, string) (space.Space, error)
	cb    func(space.SpaceListEvent)
}

func (f *fakeSpaces) List(ctx context.Context) ([]space.SpaceInfo, error) {
	if f.list == nil {
		return nil, nil
	}
	return f.list(ctx)
}

// Get releases the lock before calling a get hook: hooks block.
func (f *fakeSpaces) Get(ctx context.Context, id string) (space.Space, error) {
	f.mu.Lock()
	f.gets++
	n := f.gets
	f.mu.Unlock()
	if f.get != nil {
		return f.get(ctx, id)
	}
	if n <= f.failN {
		return nil, errors.New("space not materialized yet")
	}
	return &stubSpace{id: id}, nil
}

func (f *fakeSpaces) Subscribe(cb func(space.SpaceListEvent)) func() {
	f.mu.Lock()
	f.cb = cb
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.cb = nil
		f.mu.Unlock()
	}
}

// emit delivers one delta the way the SDK does, on a goroutine of its
// own; the returned channel closes when the callback returned.
func (f *fakeSpaces) emit(ev space.SpaceListEvent) <-chan struct{} {
	f.mu.Lock()
	cb := f.cb
	f.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if cb != nil {
			cb(ev)
		}
	}()
	return done
}

func (f *fakeSpaces) subscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cb != nil
}

func (f *fakeSpaces) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

type stubSpace struct {
	space.Space
	id string
}

func (s *stubSpace) Id() string                    { return s.id }
func (s *stubSpace) Changes() space.ChangeIndexAPI { return stubChanges{} }
func (s *stubSpace) Types() space.TypesAPI         { return stubTypes{} }

type stubTypes struct{ space.TypesAPI }

func (stubTypes) SubscribeIndexBuilds(func(space.IndexBuild)) func() { return func() {} }

type stubChanges struct{ space.ChangeIndexAPI }

func (stubChanges) Subscribe(func(space.ObjectChange)) func() { return func() {} }
func (stubChanges) ChangedSince(context.Context, uint64, int) ([]space.ObjectChange, error) {
	return nil, nil
}
func (stubChanges) Generation(context.Context) (string, error) { return "gen-1", nil }

// newTestIndexer builds an indexer on an in-memory store over fs and
// closes it with the test. Call Start (or set ctx) before spawning.
func newTestIndexer(t *testing.T, fs *fakeSpaces) *Indexer {
	t.Helper()
	st, err := OpenStoreInMemory(context.Background(), 0, false)
	require.NoError(t, err)
	ix := New(nil, index.NewRegistry(), st, Options{})
	ix.spacesAPI = fs
	t.Cleanup(func() {
		// A test that closed the indexer itself finds the store closed.
		if err := ix.Close(); err != nil && !errors.Is(err, anystore.ErrDBIsClosed) {
			t.Errorf("close indexer: %v", err)
		}
	})
	return ix
}

func newRetryTestIndexer(t *testing.T, fs *fakeSpaces) *Indexer {
	t.Helper()
	ix := newTestIndexer(t, fs)
	ix.ctx, ix.cancel = context.WithCancel(context.Background())
	return ix
}

func TestSpawnWorkerRetriesUntilGetSucceeds(t *testing.T) {
	fs := &fakeSpaces{failN: 3}
	ix := newRetryTestIndexer(t, fs)

	ix.mu.Lock()
	ix.spawnLocked("sp1", 5*time.Millisecond)
	ix.mu.Unlock()

	require.Eventually(t, func() bool {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		_, ok := ix.workers["sp1"]
		return ok
	}, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, fs.failN+1, fs.getCount())
	ix.mu.Lock()
	require.Empty(t, ix.retries)
	ix.mu.Unlock()
}

func TestSpawnRetryCancelledOnDrop(t *testing.T) {
	fs := &fakeSpaces{failN: 1 << 30} // never succeeds
	ix := newRetryTestIndexer(t, fs)

	// Long enough that the retry cannot fire between the check below
	// and the drop, short enough that an uncancelled one fires before
	// the final count.
	ix.mu.Lock()
	ix.spawnLocked("sp1", 100*time.Millisecond)
	ix.mu.Unlock()
	require.Eventually(t, func() bool {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		return len(ix.retries) == 1
	}, time.Second, time.Millisecond, "the failed open did not schedule a retry")

	ix.dropSpace("sp1")

	ix.mu.Lock()
	require.Empty(t, ix.retries)
	ix.mu.Unlock()

	got := fs.getCount()
	time.Sleep(250 * time.Millisecond)
	require.Equal(t, got, fs.getCount(), "no Get attempts after drop")
	ix.mu.Lock()
	_, ok := ix.workers["sp1"]
	ix.mu.Unlock()
	require.False(t, ok)
}
