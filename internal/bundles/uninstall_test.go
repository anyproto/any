package bundles

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

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
// bookkeeping the caller does after: an observed loser's clock is
// dropped once its root is gone.
func TestUninstallDeletesThenForgets(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(time.Minute)
	ctx := context.Background()
	b, err := r.Get(ctx, sp, "test/v1")
	if err != nil {
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
	for _, id := range b.Roots {
		r.Forget(sp, id)
	}
	if _, ok := r.firstSeen["space1/l1"]; ok {
		t.Fatal("a swept loser must not keep its observation")
	}
	r.Forget(sp, "never-seen") // a no-op, not a panic
}
