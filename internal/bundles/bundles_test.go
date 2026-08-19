package bundles

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakeSpace satisfies space.Space for the resolver's needs: Resolve
// touches only Id() and Bundles(), and the hooks under test are
// closures that never reach for the handle. The embedded interface is
// nil — any other method panics, which is the point: it pins what
// Resolve is allowed to use.
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
	deleted   []string
	failOn    map[string]error
	getResult space.Bundle
	getErr    error
}

func (f *fakeBundles) ResolveLoser(_ context.Context, _, loserRootId string) error {
	if err := f.failOn[loserRootId]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, loserRootId)
	return nil
}

func (f *fakeBundles) Get(_ context.Context, _ string) (space.Bundle, error) {
	return f.getResult, f.getErr
}

func newFake() (*fakeSpace, *fakeBundles) {
	fb := &fakeBundles{failOn: map[string]error{}}
	return &fakeSpace{id: "space1", bundles: fb}, fb
}

// newTestResolver removes the sync-state gate, which cannot be
// exercised without a live SDK.
func newTestResolver(grace time.Duration) *Resolver {
	r := NewResolver(grace)
	r.Quiescent = func(space.Space, string) bool { return true }
	return r
}

func bundle(winner string, losers ...string) space.Bundle {
	return space.Bundle{Id: "test/v1", RootId: winner, Roots: append([]string{winner}, losers...), Losers: losers}
}

// TestResolveDeletesQuietLosers pins the happy path: a loser nothing
// refuses is merged then deleted, and merge runs BEFORE deletion —
// salvaging content after a cascade delete would salvage nothing.
func TestResolveDeletesQuietLosers(t *testing.T) {
	sp, fb := newFake()
	var order []string
	inst := Install{
		Id: "test/v1",
		Merge: func(_ context.Context, _ space.Space, winner, loser string) error {
			order = append(order, "merge:"+winner+"<-"+loser)
			return nil
		},
	}
	fb.failOn = map[string]error{}

	resolved, pending, err := newTestResolver(0).Resolve(context.Background(), sp, inst, bundle("w", "l1"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != 1 || pending != 0 {
		t.Fatalf("resolved=%d pending=%d, want 1/0", resolved, pending)
	}
	if len(order) != 1 || order[0] != "merge:w<-l1" {
		t.Fatalf("merge not run against the winner: %v", order)
	}
	if len(fb.deleted) != 1 || fb.deleted[0] != "l1" {
		t.Fatalf("deleted = %v, want [l1]", fb.deleted)
	}
}

// TestResolveHoldsUntilQuiescent pins both quiescence guards: a loser
// inside the grace window, and one the SDK still reports as syncing,
// are pending — never deleted on a half-arrived tree.
func TestResolveHoldsUntilQuiescent(t *testing.T) {
	sp, fb := newFake()
	inst := Install{Id: "test/v1"}

	r := newTestResolver(time.Hour)
	_, pending, err := r.Resolve(context.Background(), sp, inst, bundle("w", "l1"))
	if err != nil || pending != 1 || len(fb.deleted) != 0 {
		t.Fatalf("grace window: pending=%d deleted=%v err=%v", pending, fb.deleted, err)
	}

	r = newTestResolver(0)
	r.Quiescent = func(space.Space, string) bool { return false }
	_, pending, err = r.Resolve(context.Background(), sp, inst, bundle("w", "l1"))
	if err != nil || pending != 1 || len(fb.deleted) != 0 {
		t.Fatalf("syncing loser: pending=%d deleted=%v err=%v", pending, fb.deleted, err)
	}
}

// TestResolveKeepIsStickyAndNotPending pins the Keep contract: a
// refused loser is never deleted, is not reported pending (there is
// nothing to retry), and its probe stops running once decided.
func TestResolveKeepIsStickyAndNotPending(t *testing.T) {
	sp, fb := newFake()
	probes := 0
	inst := Install{
		Id: "test/v1",
		Keep: func(context.Context, space.Space, string) (bool, error) {
			probes++
			return true, nil
		},
		Merge: func(context.Context, space.Space, string, string) error {
			t.Fatal("merge ran on a kept loser")
			return nil
		},
	}

	r := newTestResolver(0)
	for range 3 {
		resolved, pending, err := r.Resolve(context.Background(), sp, inst, bundle("w", "l1"))
		if err != nil || resolved != 0 || pending != 0 {
			t.Fatalf("resolved=%d pending=%d err=%v", resolved, pending, err)
		}
	}
	if probes != 1 {
		t.Fatalf("keep probed %d times, want 1 (verdict memoized)", probes)
	}
	if len(fb.deleted) != 0 {
		t.Fatalf("kept loser deleted: %v", fb.deleted)
	}
}

// TestResolveReportsUndeletableLoser pins the retry contract: a loser
// the SDK refuses to delete (its tree has not synced here) surfaces as
// both an error and a pending count, so the caller comes back.
func TestResolveReportsUndeletableLoser(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l2"] = errors.New("tree not synced")
	inst := Install{Id: "test/v1"}

	resolved, pending, err := newTestResolver(0).Resolve(context.Background(), sp, inst, bundle("w", "l1", "l2"))
	if err == nil {
		t.Fatal("undeletable loser reported no error")
	}
	if resolved != 1 || pending != 1 {
		t.Fatalf("resolved=%d pending=%d, want 1/1", resolved, pending)
	}
	if len(fb.deleted) != 1 || fb.deleted[0] != "l1" {
		t.Fatalf("deleted = %v, want [l1] — one failure must not block the rest", fb.deleted)
	}
}

// TestResolveSkipsMergeFailure pins that a loser whose salvage failed
// is never deleted: losing content is worse than leaving a duplicate.
func TestResolveSkipsMergeFailure(t *testing.T) {
	sp, fb := newFake()
	inst := Install{
		Id: "test/v1",
		Merge: func(context.Context, space.Space, string, string) error {
			return errors.New("merge failed")
		},
	}

	resolved, pending, err := newTestResolver(0).Resolve(context.Background(), sp, inst, bundle("w", "l1"))
	if err == nil {
		t.Fatal("merge failure reported no error")
	}
	if resolved != 0 || pending != 1 {
		t.Fatalf("resolved=%d pending=%d, want 0/1", resolved, pending)
	}
	if len(fb.deleted) != 0 {
		t.Fatalf("deleted after a failed merge: %v", fb.deleted)
	}
}

// TestResolveRetryStopsWhenNothingPending pins that the async loop
// exits on the first clean pass instead of sleeping out its schedule.
func TestResolveRetryStopsWhenNothingPending(t *testing.T) {
	sp, fb := newFake()
	fb.getResult = bundle("w")
	inst := Install{Id: "test/v1"}

	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(context.Background(), sp, inst, bundle("w", "l1"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveRetry did not return after resolving every loser")
	}
	if len(fb.deleted) != 1 {
		t.Fatalf("deleted = %v, want one loser", fb.deleted)
	}
}

// TestResolveRetryHonorsCancellation pins that a cancelled context
// ends the loop instead of burning its whole backoff schedule.
func TestResolveRetryHonorsCancellation(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")
	inst := Install{Id: "test/v1"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(ctx, sp, inst, bundle("w", "l1"))
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveRetry ignored context cancellation")
	}
}
