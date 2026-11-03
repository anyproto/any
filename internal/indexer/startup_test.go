package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/stretchr/testify/require"
)

func awaitStartupSignal(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func workerIds(ix *Indexer) map[string]bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	out := make(map[string]bool, len(ix.workers))
	for id := range ix.workers {
		out[id] = true
	}
	return out
}

// blockUntilDone returns a stage that closes entered on first entry and
// parks until ctx ends — what a space stuck in the SDK looks like.
func blockUntilDone(entered chan<- struct{}) func(context.Context) error {
	var once sync.Once
	return func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestStartReturnsWhileSpaceOpenIsBlocked(t *testing.T) {
	entered := make(chan struct{})
	block := blockUntilDone(entered)
	ix := newTestIndexer(t, &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{{Id: "slow", Status: space.StatusActive}}, nil
		},
		get: func(ctx context.Context, _ string) (space.Space, error) {
			return nil, block(ctx)
		},
	})
	returned := make(chan struct{})
	go func() {
		ix.Start(context.Background())
		close(returned)
	}()
	awaitStartupSignal(t, entered, "bootstrap never attempted to open the space")
	awaitStartupSignal(t, returned, "Start blocked on space loading instead of returning to sign-in")
}

func TestCloseCancelsBlockedBootstrap(t *testing.T) {
	for _, stage := range []string{"list", "open", "align"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			block := blockUntilDone(entered)
			spaces := &fakeSpaces{
				list: func(ctx context.Context) ([]space.SpaceInfo, error) {
					if stage == "list" {
						return nil, block(ctx)
					}
					return []space.SpaceInfo{{Id: "slow", Status: space.StatusActive}}, nil
				},
				get: func(ctx context.Context, id string) (space.Space, error) {
					if stage == "open" {
						return nil, block(ctx)
					}
					return &startupSpace{stubSpace: stubSpace{id: id}, block: block}, nil
				},
			}
			ix := newTestIndexer(t, spaces)
			require.NoError(t, ix.store.SetCursor(context.Background(), "slow", 1, "gen-1"))
			ix.Start(context.Background())
			awaitStartupSignal(t, entered, "bootstrap did not reach the blocked stage")
			closed := make(chan struct{})
			var closeErr error
			go func() {
				closeErr = ix.Close()
				close(closed)
			}()
			awaitStartupSignal(t, closed, "Close waited for blocked bootstrap work instead of cancelling it")
			require.NoError(t, closeErr)
			require.False(t, spaces.subscribed(), "Close leaked the space subscription")
		})
	}
}

type startupSpace struct {
	stubSpace
	block func(context.Context) error
}

func (s *startupSpace) Changes() space.ChangeIndexAPI {
	return startupChanges{block: s.block}
}

type startupChanges struct {
	stubChanges
	block func(context.Context) error
}

func (c startupChanges) MaxApplySeq(ctx context.Context) (uint64, error) {
	return 0, c.block(ctx)
}

func TestSyncWaitsForBootstrapAndHonorsCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ix := newTestIndexer(t, &fakeSpaces{
		list: func(ctx context.Context) ([]space.SpaceInfo, error) {
			close(entered)
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	ix.Start(context.Background())
	awaitStartupSignal(t, entered, "bootstrap did not reach List")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ix.Sync(ctx), context.DeadlineExceeded, "Sync must not report success before discovery finishes")
	close(release)
	require.NoError(t, ix.Sync(context.Background()))
}

// Deltas keep flowing while a listed space is still opening: the SDK's
// dispatch goroutine must not park behind the open (its mailbox would
// overflow and close the subscription), a removal wins over the stale
// list entry, and repeated deltas do not open twice.
func TestBootstrapKeepsSpaceChangesAndDeduplicatesWorkers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var getsMu sync.Mutex
	gets := map[string]int{}
	spaces := &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{{Id: "old", Status: space.StatusActive}}, nil
		},
		get: func(ctx context.Context, id string) (space.Space, error) {
			getsMu.Lock()
			gets[id]++
			getsMu.Unlock()
			if id == "old" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &stubSpace{id: id}, nil
		},
	}
	ix := newTestIndexer(t, spaces)
	ix.Start(context.Background())
	awaitStartupSignal(t, entered, "bootstrap did not open the first space")
	added := space.SpaceInfo{Id: "new", Status: space.StatusActive}
	delivered := spaces.emit(space.SpaceListEvent{
		Added: []space.SpaceInfo{added}, Updated: []space.SpaceInfo{added}, Removed: []string{"old"},
	})
	awaitStartupSignal(t, delivered, "a space-list delta waited behind a blocked space open")
	close(release)
	require.NoError(t, ix.Sync(context.Background()))
	ids := workerIds(ix)
	require.False(t, ids["old"], "the initial list resurrected a removed space")
	require.True(t, ids["new"], "a space added during bootstrap was missed")
	getsMu.Lock()
	defer getsMu.Unlock()
	require.Equal(t, 1, gets["new"], "duplicate events opened duplicate workers")
	require.Equal(t, 1, gets["old"], "a dropped open was retried")
}

// A removal delivered while the initial list is being read must win
// over the list entry it invalidates.
func TestRemovalDuringListWinsOverStaleEntry(t *testing.T) {
	listing, release := make(chan struct{}), make(chan struct{})
	var getsMu sync.Mutex
	gets := 0
	spaces := &fakeSpaces{
		list: func(ctx context.Context) ([]space.SpaceInfo, error) {
			close(listing)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []space.SpaceInfo{{Id: "gone", Status: space.StatusActive}}, nil
		},
		get: func(_ context.Context, id string) (space.Space, error) {
			getsMu.Lock()
			gets++
			getsMu.Unlock()
			return &stubSpace{id: id}, nil
		},
	}
	ix := newTestIndexer(t, spaces)
	ix.Start(context.Background())
	awaitStartupSignal(t, listing, "bootstrap did not reach List")
	awaitStartupSignal(t, spaces.emit(space.SpaceListEvent{Removed: []string{"gone"}}),
		"a removal waited behind the space listing")
	close(release)
	require.NoError(t, ix.Sync(context.Background()))
	require.False(t, workerIds(ix)["gone"], "the stale list entry resurrected a removed space")
	getsMu.Lock()
	defer getsMu.Unlock()
	require.Zero(t, gets, "a removed space was opened")
}

// An open that a removal overtook must not write the index store after
// the drop: here the removal lands while the open is parked in the
// alignment's generation read.
func TestOvertakenOpenDoesNotWriteAfterDrop(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	sp := &alignGateSpace{stubSpace: stubSpace{id: "sp1"}, entered: entered, release: release}
	ix := newTestIndexer(t, &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{{Id: "sp1", Status: space.StatusActive}}, nil
		},
		get: func(context.Context, string) (space.Space, error) { return sp, nil },
	})
	// A cursor from before generation tracking: the alignment stamps it.
	require.NoError(t, ix.store.SetCursor(context.Background(), "sp1", 5, ""))
	ix.Start(context.Background())
	awaitStartupSignal(t, entered, "the open did not reach the alignment")
	dropped := ix.spacesAPI.(*fakeSpaces).emit(space.SpaceListEvent{Removed: []string{"sp1"}})
	// The drop waits for the alignment in flight, so it cannot have
	// returned yet; release the alignment and let both settle.
	select {
	case <-dropped:
		t.Fatal("dropSpace returned while the alignment was still writing")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	awaitStartupSignal(t, dropped, "dropSpace did not return after the alignment")
	require.NoError(t, ix.Sync(context.Background()))
	cursor, gen, err := ix.store.Cursor(context.Background(), "sp1")
	require.NoError(t, err)
	require.Zero(t, cursor, "the overtaken open wrote its cursor after the drop")
	require.Empty(t, gen)
}

// alignGateSpace parks the alignment's generation read until released.
type alignGateSpace struct {
	stubSpace
	entered chan struct{}
	release chan struct{}
}

func (s *alignGateSpace) Changes() space.ChangeIndexAPI { return alignGateChanges{s} }

type alignGateChanges struct{ s *alignGateSpace }

func (c alignGateChanges) Subscribe(func(space.ObjectChange)) func() { return func() {} }
func (c alignGateChanges) ChangedSince(context.Context, uint64, int) ([]space.ObjectChange, error) {
	return nil, nil
}
func (c alignGateChanges) MaxApplySeq(context.Context) (uint64, error) { return 10, nil }
func (c alignGateChanges) Generation(ctx context.Context) (string, error) {
	close(c.s.entered)
	select {
	case <-c.s.release:
		return "gen-1", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// One space's alignment holds only that space: another space still
// publishes, and a delta for a third still returns to the dispatcher.
func TestAlignmentOfOneSpaceDoesNotStallOthers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := &alignGateSpace{stubSpace: stubSpace{id: "slow"}, entered: entered, release: release}
	spaces := &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{
				{Id: "slow", Status: space.StatusActive},
				{Id: "fast", Status: space.StatusActive},
			}, nil
		},
		get: func(_ context.Context, id string) (space.Space, error) {
			if id == "slow" {
				return slow, nil
			}
			return &stubSpace{id: id}, nil
		},
	}
	ix := newTestIndexer(t, spaces)
	ix.Start(context.Background())
	awaitStartupSignal(t, entered, "the slow space did not reach its alignment")
	require.Eventually(t, func() bool { return workerIds(ix)["fast"] },
		time.Second, 5*time.Millisecond, "a space waited for another space's alignment")
	awaitStartupSignal(t, spaces.emit(space.SpaceListEvent{Removed: []string{"other"}}),
		"a removal of an unrelated space waited for an alignment")
	close(release)
	require.NoError(t, ix.Sync(context.Background()))
	require.True(t, workerIds(ix)["slow"])
}

func TestSlowSpaceOpenDoesNotStallOthers(t *testing.T) {
	entered := make(chan struct{})
	block := blockUntilDone(entered)
	ix := newTestIndexer(t, &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{
				{Id: "slow", Status: space.StatusActive},
				{Id: "fast", Status: space.StatusActive},
			}, nil
		},
		get: func(ctx context.Context, id string) (space.Space, error) {
			if id == "slow" {
				return nil, block(ctx)
			}
			return &stubSpace{id: id}, nil
		},
	})
	ix.Start(context.Background())
	awaitStartupSignal(t, entered, "bootstrap did not open the slow space")
	require.Eventually(t, func() bool { return workerIds(ix)["fast"] },
		time.Second, 5*time.Millisecond, "a space listed after a slow one waited for it")
}

func TestBootstrapRetriesFailedList(t *testing.T) {
	var mu sync.Mutex
	lists := 0
	ix := newTestIndexer(t, &fakeSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			mu.Lock()
			defer mu.Unlock()
			lists++
			if lists < 3 {
				return nil, errors.New("tech space busy")
			}
			return []space.SpaceInfo{{Id: "sp1", Status: space.StatusActive}}, nil
		},
	})
	ix.start(context.Background(), 5*time.Millisecond)
	require.NoError(t, ix.Sync(context.Background()))
	require.True(t, workerIds(ix)["sp1"], "a space only the retried listing reports stayed unindexed")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 3, lists)
}

func TestCancelledStartDoesNotSubscribe(t *testing.T) {
	spaces := &fakeSpaces{}
	ix := newTestIndexer(t, spaces)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ix.Start(ctx)
	require.NoError(t, ix.Close())
	require.False(t, spaces.subscribed())
}
