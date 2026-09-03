package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// fakeSpaces fails Get failN times, then returns a stub space. Embeds
// the interface so unimplemented methods panic if reached.
type fakeSpaces struct {
	space.Service
	mu    sync.Mutex
	failN int
	gets  int
}

func (f *fakeSpaces) Get(_ context.Context, id string) (space.Space, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.gets <= f.failN {
		return nil, errors.New("space not materialized yet")
	}
	return &stubSpace{id: id}, nil
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

type stubChanges struct{ space.ChangeIndexAPI }

func (stubChanges) Subscribe(func(space.ObjectChange)) func() { return func() {} }
func (stubChanges) ChangedSince(context.Context, uint64, int) ([]space.ObjectChange, error) {
	return nil, nil
}
func (stubChanges) Generation(context.Context) (string, error) { return "gen-1", nil }

func newRetryTestIndexer(t *testing.T, fs *fakeSpaces) *Indexer {
	t.Helper()
	st, err := OpenStoreInMemory(context.Background(), 0, false)
	require.NoError(t, err)
	ix := New(nil, index.NewRegistry(), st, Options{})
	ix.spacesAPI = fs
	ix.ctx, ix.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { require.NoError(t, ix.Close()) })
	return ix
}

func TestSpawnWorkerRetriesUntilGetSucceeds(t *testing.T) {
	fs := &fakeSpaces{failN: 3}
	ix := newRetryTestIndexer(t, fs)

	ix.mu.Lock()
	ix.spawnWorkerLocked("sp1", 5*time.Millisecond)
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

	ix.mu.Lock()
	ix.spawnWorkerLocked("sp1", 20*time.Millisecond)
	require.Len(t, ix.retries, 1)
	ix.mu.Unlock()

	ix.dropSpace("sp1")

	ix.mu.Lock()
	require.Empty(t, ix.retries)
	ix.mu.Unlock()

	got := fs.getCount()
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, got, fs.getCount(), "no Get attempts after drop")
	ix.mu.Lock()
	_, ok := ix.workers["sp1"]
	ix.mu.Unlock()
	require.False(t, ok)
}
