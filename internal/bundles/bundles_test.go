package bundles

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakeSpace satisfies space.Space for the resolver's needs: Resolve
// touches only Id() and Bundles(). The embedded interface is nil — any
// other method panics, which is the point: it pins what Resolve is
// allowed to use.
type fakeSpace struct {
	space.Space
	id      string
	bundles *fakeBundles
}

func (f *fakeSpace) Id() string                { return f.id }
func (f *fakeSpace) Bundles() space.BundlesAPI { return f.bundles }

// fakeBundles records the roots ResolveLoser was asked to delete and
// can fail a chosen one, standing in for a loser whose tree has not
// synced to this device.
type fakeBundles struct {
	space.BundlesAPI
	row     space.Bundle
	deleted []string
	failOn  map[string]error
	calls   int
}

func (f *fakeBundles) Get(context.Context, string) (space.Bundle, error) {
	return f.row, nil
}

func (f *fakeBundles) ResolveLoser(_ context.Context, _, loserRootId string) error {
	f.calls++
	if err := f.failOn[loserRootId]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, loserRootId)
	return nil
}

// row carries one winner and one live loser — the shape every timing
// test needs before it reaches the guard.

func newFake() (*fakeSpace, *fakeBundles) {
	fb := &fakeBundles{
		row:    space.Bundle{Id: "test/v1", RootId: "w", Roots: []string{"w", "l1"}, Losers: []string{"l1"}},
		failOn: map[string]error{},
	}
	return &fakeSpace{id: "space1", bundles: fb}, fb
}

// newTestResolver removes the sync-state gate, which cannot be
// exercised without a live SDK.
func newTestResolver(grace time.Duration) *Resolver {
	r := NewResolver(grace)
	r.RetryDelay = time.Millisecond
	r.Quiescent = func(space.Space, string) bool { return true }
	return r
}

// TestResolveDeletesQuietLoser pins the happy path: a settled loser is
// handed straight to the SDK's cascade delete.
func TestResolveDeletesQuietLoser(t *testing.T) {
	sp, fb := newFake()
	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(fb.deleted) != 1 || fb.deleted[0] != "l1" {
		t.Fatalf("deleted = %v, want [l1]", fb.deleted)
	}
}

// TestResolveHoldsUntilQuiescent pins both timing guards. A client can
// only have merged what has arrived, so a loser inside the grace
// window or still syncing is refused — never deleted on a half-arrived
// tree.
func TestResolveHoldsUntilQuiescent(t *testing.T) {
	sp, fb := newFake()

	err := newTestResolver(time.Hour).Resolve(context.Background(), sp, "test/v1", "l1")
	if !errors.Is(err, ErrLoserNotReady) {
		t.Fatalf("grace window: err = %v, want ErrLoserNotReady", err)
	}

	r := newTestResolver(0)
	r.Quiescent = func(space.Space, string) bool { return false }
	err = r.Resolve(context.Background(), sp, "test/v1", "l1")
	if !errors.Is(err, ErrLoserNotReady) {
		t.Fatalf("syncing loser: err = %v, want ErrLoserNotReady", err)
	}

	if len(fb.deleted) != 0 || fb.calls != 0 {
		t.Fatalf("deletion attempted before the loser settled: %v", fb.deleted)
	}
}

// TestResolvePropagatesSDKRefusal pins that an undeletable loser (its
// tree has not synced here) surfaces as an error for the caller to
// retry, rather than being swallowed.
func TestResolvePropagatesSDKRefusal(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")

	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err == nil {
		t.Fatal("undeletable loser reported no error")
	}
	if len(fb.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", fb.deleted)
	}
}

// TestResolveRetryStopsOnSuccess pins that the background loop exits
// once the loser is gone instead of sleeping out its schedule.
func TestResolveRetryStopsOnSuccess(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(0)

	done := make(chan struct{})
	go func() {
		r.ResolveRetry(context.Background(), sp, "test/v1", "l1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveRetry did not return after the loser resolved")
	}
	if len(fb.deleted) != 1 {
		t.Fatalf("deleted = %v, want one loser", fb.deleted)
	}
}

// TestResolveRejectsNonLoser pins the permanent verdicts, which are
// reached before any timing guard: the winner is not a loser, and
// neither is a root nobody ever claimed.
func TestResolveRejectsNonLoser(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(time.Hour) // grace would refuse first if consulted

	for _, target := range []string{"w", "never-claimed"} {
		err := r.Resolve(context.Background(), sp, "test/v1", target)
		if !errors.Is(err, space.ErrBundleNotLoser) {
			t.Fatalf("resolve %q: err = %v, want ErrBundleNotLoser", target, err)
		}
	}
	if fb.calls != 0 {
		t.Fatal("deletion attempted on a non-loser")
	}
}

// TestResolveIsIdempotent pins that a claimed root already gone from
// the live loser set resolves clean instead of erroring — the client
// may retry a call that already landed.
func TestResolveIsIdempotent(t *testing.T) {
	sp, fb := newFake()
	fb.row.Losers = nil

	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err != nil {
		t.Fatalf("resolve of an already-deleted loser: %v", err)
	}
	if fb.calls != 0 {
		t.Fatal("re-deleted an already-resolved loser")
	}
}

// TestResolveRetryStopsOnNotLoser pins that a target the registry says
// is not a loser ends the loop — retrying cannot change that verdict.
func TestResolveRetryStopsOnNotLoser(t *testing.T) {
	sp, fb := newFake()

	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(context.Background(), sp, "test/v1", "w")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveRetry kept retrying a non-loser")
	}
	if fb.calls != 0 {
		t.Fatalf("attempted deletion %d times on a non-loser", fb.calls)
	}
}

// TestResolveRetryHonorsCancellation pins that a cancelled context
// ends the loop instead of burning its whole backoff schedule.
func TestResolveRetryHonorsCancellation(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(ctx, sp, "test/v1", "l1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveRetry ignored context cancellation")
	}
}
