package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
func TestRefuseDamaged_CauseUntyped(t *testing.T) {
	if err := RefuseDamaged(nil); err != nil {
		t.Fatalf("RefuseDamaged(nil) = %v", err)
	}
	// A cancelled open deletes nothing, damaged page or not.
	for _, other := range []error{
		errors.New("open: permission denied"),
		errors.Join(fmt.Errorf("btree: page 3: %w", anystore.ErrPageIntegrity), context.Canceled),
	} {
		if err := RefuseDamaged(other); err != other {
			t.Fatalf("RefuseDamaged(%v) = %v, want it unchanged", other, err)
		}
	}
	for _, cause := range []error{
		fmt.Errorf("%w: btree: page 2: checksum mismatch", anystore.ErrQuickCheckFailed),
		fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, syscall.EIO),
		fmt.Errorf("btree: page 3: %w", anystore.ErrPageIntegrity),
		fmt.Errorf("btree: open: %w", anystore.ErrHeaderCorrupt),
		fmt.Errorf("btree: read page 4: %w", anystore.ErrWALCorrupt),
		fmt.Errorf("btree: page 5: %w", anystore.ErrCorrupt),
	} {
		err := RefuseDamaged(cause)
		if !errors.Is(err, ErrIndexRebuildRequired) {
			t.Fatalf("%v: want ErrIndexRebuildRequired", err)
		}
		if errors.Is(err, anystore.ErrCorrupt) {
			t.Fatalf("%v: carries a typed any-store integrity failure", err)
		}
	}
}

// OpenStore refuses a torn index.db: the first read fails its page
// checksum, and the refusal carries no typed integrity error.
func TestOpenStore_RefusesTornPage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := OpenStore(ctx, path, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "space", 1, "g1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const pageSize = 4096
	for i := pageSize + 100; i < pageSize+600; i++ {
		b[i] ^= 0xff
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = OpenStore(ctx, path, 0, false)
	if !errors.Is(err, ErrIndexRebuildRequired) || errors.Is(err, anystore.ErrPageIntegrity) {
		t.Fatalf("OpenStore over a torn page: %v, want an untyped rebuild refusal", err)
	}
}

// OpenStore refuses an index.db whose header is damaged (overwritten,
// cut inside the header, or not a database), and the refusal carries no
// typed integrity error.
func TestOpenStore_RefusesDamagedHeader(t *testing.T) {
	ctx := context.Background()
	for name, damage := range map[string]func(t *testing.T, path string){
		"header overwritten": func(t *testing.T, path string) {
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteAt(make([]byte, 100), 0); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		},
		"cut inside the header": func(t *testing.T, path string) {
			if err := os.Truncate(path, 50); err != nil {
				t.Fatal(err)
			}
		},
		"not a database": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "index.db")
			s, err := OpenStore(ctx, path, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetCursor(ctx, "space", 1, "g1"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			damage(t, path)

			if db, err := anystore.Open(ctx, path, nil); !errors.Is(err, anystore.ErrHeaderCorrupt) {
				if err == nil {
					_ = db.Close()
				}
				t.Fatalf("anystore.Open over the damage: %v, want ErrHeaderCorrupt", err)
			}
			_, err = OpenStore(ctx, path, 0, false)
			if !errors.Is(err, ErrIndexRebuildRequired) || errors.Is(err, anystore.ErrCorrupt) {
				t.Fatalf("OpenStore over a damaged header: %v, want an untyped rebuild refusal", err)
			}
		})
	}
}
