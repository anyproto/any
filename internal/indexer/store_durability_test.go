package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
)

// index.db runs with the dirty sentinel: a write creates index.db.lock
// and a clean close removes it.
func TestOpenStore_SentinelClearsOnClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := OpenStore(ctx, path, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "space", 1, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("sentinel after write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("sentinel after close: err=%v", err)
	}
}

// A damaged index.db is a rebuild and never carries a typed any-store
// integrity failure: a boot chain holding one reads as a corrupt sdk.db.
func TestIntegrityRebuildErr_CauseUntyped(t *testing.T) {
	for _, cause := range []error{
		fmt.Errorf("%w: btree: page 2: checksum mismatch", anystore.ErrQuickCheckFailed),
		fmt.Errorf("btree: page 3: %w", anystore.ErrPageIntegrity),
	} {
		err := integrityRebuildErr(cause)
		if !errors.Is(err, ErrIndexRebuildRequired) {
			t.Fatalf("%v: want ErrIndexRebuildRequired", err)
		}
		if errors.Is(err, anystore.ErrQuickCheckFailed) || errors.Is(err, anystore.ErrPageIntegrity) {
			t.Fatalf("%v: carries a typed any-store integrity failure", err)
		}
	}
}
