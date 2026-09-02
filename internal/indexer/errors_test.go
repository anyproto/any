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

	st, err := OpenStore(ctx, path, 0, false, 0)
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
	st, err := OpenStore(ctx, path, 768, true, 0)
	if err != nil {
		t.Fatalf("OpenStore (first, dim 768): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopened with a different configured dim.
	st, err = OpenStore(ctx, path, 1024, true, 0)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore with a contradicting dim succeeded, want ErrIndexRebuildRequired")
	}
	if !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("OpenStore err = %v, want it to wrap ErrIndexRebuildRequired", err)
	}
}

// TestOpenStore_ChunkRunesMismatchIsRebuildRequired covers the chunk
// target: it decides every doc id and every doc's text, so a db written
// under a different one holds records on stale boundaries. A db written
// before the pin existed (0) is adopted, not refused.
func TestOpenStore_ChunkRunesMismatchIsRebuildRequired(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	st, err := OpenStore(ctx, path, 0, false, 2000)
	if err != nil {
		t.Fatalf("OpenStore (first, 2000 runes): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st, err = OpenStore(ctx, path, 0, false, 1000)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore with a contradicting chunk target succeeded, want ErrIndexRebuildRequired")
	}
	if !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("OpenStore err = %v, want it to wrap ErrIndexRebuildRequired", err)
	}

	// Same target, and the unpinned default, both open.
	for _, n := range []int{2000, 0} {
		st, err = OpenStore(ctx, path, 0, false, n)
		if err != nil {
			t.Fatalf("OpenStore(chunkRunes %d) on a 2000-rune db: %v", n, err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

// A db from before the pin carries no chunkRunes; it is adopted rather
// than refused — the docs are on whatever the build of the day used, and
// refusing would be a rebuild nobody asked for.
func TestOpenStore_UnpinnedChunkRunesIsAdopted(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	seedSchema(t, ctx, path, indexSchemaVersion)

	st, err := OpenStore(ctx, path, 0, false, 0)
	if err != nil {
		t.Fatalf("OpenStore on a db with no pinned chunk target: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestEnsureDim_ModelChangedIsRebuildRequired covers the third site: an
// embedder that returns a dimension the populated db can't hold.
func TestEnsureDim_ModelChangedIsRebuildRequired(t *testing.T) {
	ctx := context.Background()

	st, err := OpenStoreInMemory(ctx, 768, true, 0)
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
