package bundles

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

// Uninstall stands in for the SDK's: the losers, then the winner, or
// the refusal the test planted under the bundle id.
func (f *fakeBundles) Uninstall(_ context.Context, bundleId string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.failOn[bundleId]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, f.row.Losers...)
	f.deleted = append(f.deleted, f.row.RootId)
	return nil
}

func (f *fakeSpace) SpaceIndexObjectId() string { return "index" }

// Query serves the registry row the way the raw `bundles` read sees
// it: the register and the claim set, live winner or not.
func (f *fakeSpace) Query(string, string) space.Query { return &fakeRowQuery{row: f.bundles.row} }

type fakeRowQuery struct {
	space.Query
	row space.Bundle
}

func (q *fakeRowQuery) Filter(any) space.Query { return q }
func (q *fakeRowQuery) One(context.Context) (*anyenc.Value, error) {
	a := &anyenc.Arena{}
	v := a.NewObject()
	v.Set("id", a.NewString(q.row.Id))
	v.Set("rootId", a.NewString(q.row.RootId))
	roots := a.NewArray()
	for i, id := range q.row.Roots {
		roots.SetArrayItem(i, a.NewString(id))
	}
	v.Set("roots", roots)
	return v, nil
}

// TestUninstallMapsUnknownToNotInstalled pins the one translation the
// resolver makes: the SDK's unknown bundle is the server's not
// installed.
func TestUninstallMapsUnknownToNotInstalled(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["test/v1"] = space.ErrBundleUnknown
	err := newTestResolver(time.Minute).Uninstall(context.Background(), sp, "test/v1")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("unknown bundle: %v, want ErrNotInstalled", err)
	}
}

// TestUninstallKeepsNotLocalSentinels pins that the SDK's retryable
// refusals reach the caller as they are, with nothing deleted.
func TestUninstallKeepsNotLocalSentinels(t *testing.T) {
	for _, sentinel := range []error{space.ErrLoserNotSynced, space.ErrBundleRootNotSynced} {
		sp, fb := newFake()
		fb.failOn["test/v1"] = sentinel
		err := newTestResolver(time.Minute).Uninstall(context.Background(), sp, "test/v1")
		if !errors.Is(err, sentinel) {
			t.Fatalf("refusal %v came back as %v", sentinel, err)
		}
		if len(fb.deleted) != 0 {
			t.Fatalf("a refusal deleted %v", fb.deleted)
		}
	}
}

// TestUninstallDeletesThenForgets pins the order the SDK keeps and the
// bookkeeping after it: an observed loser's clock is dropped off the
// registry row once its root is gone.
func TestUninstallDeletesThenForgets(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(time.Minute)
	ctx := context.Background()
	if _, err := r.Get(ctx, sp, "test/v1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.firstSeen["space1/l1"]; !ok {
		t.Fatal("reading the row must date its loser")
	}
	if err := r.Uninstall(ctx, sp, "test/v1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fb.deleted, []string{"l1", "w"}) {
		t.Fatalf("deleted %v, want the loser then the winner", fb.deleted)
	}
	if _, ok := r.firstSeen["space1/l1"]; ok {
		t.Fatal("a swept loser must not keep its observation")
	}
}
