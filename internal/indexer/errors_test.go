package indexer

// Deliberately TAG-FREE. Most of this package's tests sit behind
// `fts && vector`, but ErrIndexRebuildRequired is a host contract the
// mobile binds depend on, and the iOS bind builds without `vector`. A
// tagged test here would go green in CI while the thing it guards was
// broken for the only build that reads it.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
)

// TestOpenStore_SchemaMismatchIsRebuildRequired is the real half of the
// code-4 contract: a db written by another schema version must come back
// as ErrIndexRebuildRequired through OpenStore, not merely as some
// error. This is the arm mobile actually hits — an app updates, the new
// binary bumped indexSchemaVersion, and the user's on-disk index is
// suddenly unreadable.
//
// It seeds the meta row by hand rather than reusing an old db fixture so
// it keeps working when indexSchemaVersion is next bumped.
func TestOpenStore_SchemaMismatchIsRebuildRequired(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	seedSchema(t, ctx, path, indexSchemaVersion+1)

	st, err := OpenStore(ctx, path, 0, false)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore on a foreign schema succeeded, want ErrIndexRebuildRequired")
	}
	if !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("OpenStore err = %v, want it to wrap ErrIndexRebuildRequired", err)
	}
}

// TestOpenStore_DimMismatchIsRebuildRequired covers the second wrapped
// site: a db built for one vector dimension, opened with another
// configured. Unreachable on mobile today (vector is off there) and
// wrapped anyway, so the contract doesn't quietly depend on that.
func TestOpenStore_DimMismatchIsRebuildRequired(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	// A db that knows its own dim.
	st, err := OpenStore(ctx, path, 768, true)
	if err != nil {
		t.Fatalf("OpenStore (first, dim 768): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopened with a different configured dim.
	st, err = OpenStore(ctx, path, 1024, true)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore with a contradicting dim succeeded, want ErrIndexRebuildRequired")
	}
	if !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("OpenStore err = %v, want it to wrap ErrIndexRebuildRequired", err)
	}
}

// TestPinChunkRunes covers the chunk target: it decides every doc id and
// every doc's text, so a db written under a different one holds records
// on stale boundaries. 0 resolves to the build default, and a db written
// before the pin existed is adopted — then pinned, since nothing else
// writes that row.
func TestPinChunkRunes(t *testing.T) {
	ctx := context.Background()

	pin := func(t *testing.T, path string, n int) error {
		t.Helper()
		st, err := OpenStore(ctx, path, 0, false)
		if err != nil {
			t.Fatalf("OpenStore: %v", err)
		}
		defer st.Close()
		return st.PinChunkRunes(ctx, n)
	}

	t.Run("mismatch is rebuild required", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "index.db")
		if err := pin(t, path, 2000); err != nil {
			t.Fatalf("first pin: %v", err)
		}
		if err := pin(t, path, 1000); !errors.Is(err, ErrIndexRebuildRequired) {
			t.Fatalf("pin(1000) on a 2000-rune db = %v, want it to wrap ErrIndexRebuildRequired", err)
		}
		// The same target, and 0 — which resolves to that same default.
		for _, n := range []int{2000, 0} {
			if err := pin(t, path, n); err != nil {
				t.Errorf("pin(%d) on a 2000-rune db: %v", n, err)
			}
		}
	})

	t.Run("unpinned db is adopted and pinned", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "index.db")
		seedSchema(t, ctx, path, indexSchemaVersion)

		if err := pin(t, path, 1500); err != nil {
			t.Fatalf("pin on a db with no pinned chunk target: %v", err)
		}
		if err := pin(t, path, 900); !errors.Is(err, ErrIndexRebuildRequired) {
			t.Fatalf("adoption left the target unpinned — pin(900) = %v", err)
		}
	})
}

// TestEnsureDim_ModelChangedIsRebuildRequired covers the third site: an
// embedder that returns a dimension the populated db can't hold.
func TestEnsureDim_ModelChangedIsRebuildRequired(t *testing.T) {
	ctx := context.Background()

	st, err := OpenStoreInMemory(ctx, 768, true)
	if err != nil {
		t.Fatalf("OpenStoreInMemory: %v", err)
	}
	defer st.Close()

	if err := st.EnsureDim(ctx, 1024); !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("EnsureDim(1024) on a dim-768 db = %v, want it to wrap ErrIndexRebuildRequired", err)
	}
}

// seedSchema writes a meta row carrying the given schema version, so a
// later OpenStore reads a db it considers foreign.
func seedSchema(t *testing.T, ctx context.Context, path string, schema int) {
	t.Helper()

	db, err := anystore.Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("anystore.Open: %v", err)
	}
	defer db.Close()

	coll, err := db.Collection(ctx, cursorsCollection)
	if err != nil {
		t.Fatalf("open %s collection: %v", cursorsCollection, err)
	}
	arena := &anyenc.Arena{}
	meta := arena.NewObject()
	meta.Set("id", arena.NewString(metaDocId))
	meta.Set("schema", arena.NewNumberInt(schema))
	meta.Set("dim", arena.NewNumberInt(0))
	if err := coll.UpsertOne(ctx, meta); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
}
