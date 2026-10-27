package indexer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/internal/index"
)

type startupSpaces struct {
	space.Service
	list func(context.Context) ([]space.SpaceInfo, error)
	get  func(context.Context, string) (space.Space, error)
	mu   sync.Mutex
	cb   func(space.SpaceListEvent)
}

func (s *startupSpaces) List(ctx context.Context) ([]space.SpaceInfo, error) {
	return s.list(ctx)
}

func (s *startupSpaces) Get(ctx context.Context, id string) (space.Space, error) {
	return s.get(ctx, id)
}

func (s *startupSpaces) Subscribe(cb func(space.SpaceListEvent)) func() {
	s.mu.Lock()
	s.cb = cb
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.cb = nil
		s.mu.Unlock()
	}
}

func (s *startupSpaces) emit(ev space.SpaceListEvent) <-chan struct{} {
	s.mu.Lock()
	cb := s.cb
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if cb != nil {
			cb(ev)
		}
	}()
	return done
}

func (s *startupSpaces) subscribed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cb != nil
}

func newStartupIndexer(t *testing.T, spaces *startupSpaces) *Indexer {
	t.Helper()
	store, err := OpenStoreInMemory(context.Background(), 0, false)
	require.NoError(t, err)
	ix := New(nil, index.NewRegistry(), store, Options{})
	ix.spacesAPI = spaces
	return ix
}

func awaitStartupSignal(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func TestStartReturnsWhileSpaceOpenIsBlocked(t *testing.T) {
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	ix := newStartupIndexer(t, &startupSpaces{
		list: func(context.Context) ([]space.SpaceInfo, error) {
			return []space.SpaceInfo{{Id: "slow", Status: space.StatusActive}}, nil
		},
		get: func(ctx context.Context, _ string) (space.Space, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	returned := make(chan struct{})
	go func() {
		ix.Start(ctx)
		close(returned)
	}()
	t.Cleanup(func() {
		cancel()
		<-returned
		require.NoError(t, ix.Close())
	})
	awaitStartupSignal(t, entered, "bootstrap never attempted to open the space")
	awaitStartupSignal(t, returned, "Start blocked on space loading instead of returning to sign-in")
}

func TestCloseCancelsBlockedBootstrap(t *testing.T) {
	for _, stage := range []string{"list", "open", "align"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			block := func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			}
			spaces := &startupSpaces{
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
			ix := newStartupIndexer(t, spaces)
			require.NoError(t, ix.store.SetCursor(context.Background(), "slow", 1, "gen-1"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ix.Start(ctx)
			awaitStartupSignal(t, entered, "bootstrap did not reach the blocked stage")
			closed := make(chan struct{})
			var closeErr error
			go func() {
				closeErr = ix.Close()
				close(closed)
			}()
			t.Cleanup(func() {
				cancel()
				awaitStartupSignal(t, closed, "Close did not join bootstrap after cancellation")
			})
			awaitStartupSignal(t, closed, "Close waited for the bootstrap lock before cancelling its work")
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
	ix := newStartupIndexer(t, &startupSpaces{
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
	t.Cleanup(func() { require.NoError(t, ix.Close()) })
	awaitStartupSignal(t, entered, "bootstrap did not reach List")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ix.Sync(ctx), context.DeadlineExceeded, "Sync must not report success before discovery finishes")
	close(release)
	require.NoError(t, ix.Sync(context.Background()))
}

func TestBootstrapKeepsSpaceChangesAndDeduplicatesWorkers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var getsMu sync.Mutex
	gets := map[string]int{}
	spaces := &startupSpaces{
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
	ix := newStartupIndexer(t, spaces)
	ix.Start(context.Background())
	t.Cleanup(func() { require.NoError(t, ix.Close()) })
	awaitStartupSignal(t, entered, "bootstrap did not open the first space")
	added := space.SpaceInfo{Id: "new", Status: space.StatusActive}
	delivered := spaces.emit(space.SpaceListEvent{
		Added: []space.SpaceInfo{added}, Updated: []space.SpaceInfo{added}, Removed: []string{"old"},
	})
	close(release)
	awaitStartupSignal(t, delivered, "space changes during bootstrap were not delivered")
	require.NoError(t, ix.Sync(context.Background()))
	ix.mu.Lock()
	_, old := ix.workers["old"]
	_, addedWorker := ix.workers["new"]
	ix.mu.Unlock()
	require.False(t, old, "the initial snapshot resurrected a removed space")
	require.True(t, addedWorker, "a space added during bootstrap was missed")
	getsMu.Lock()
	defer getsMu.Unlock()
	require.Equal(t, 1, gets["new"], "duplicate events opened duplicate workers")
}

func TestCancelledStartDoesNotSubscribe(t *testing.T) {
	spaces := &startupSpaces{}
	ix := newStartupIndexer(t, spaces)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ix.Start(ctx)
	require.NoError(t, ix.Close())
	require.False(t, spaces.subscribed())
}
