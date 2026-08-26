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
	"strings"
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

// TestRebuildRequiredMessagesKeepAndroidSubstrings pins the reason the
// sentinel is wrapped as a PREFIX rather than a suffix or a replacement.
//
// any-kotlin's AnyRuntimeImpl decides whether to wipe a user's local
// index by substring-matching "indexer:", "remove " and "to rebuild" out
// of this error text. That's the defect the sentinel fixes on the iOS
// side (start code 4), but Android hasn't adopted the code yet, and
// changing its behaviour is not ours to do. So until it does, these three
// substrings are load-bearing: rewording a message here silently
// disables a user's index recovery on Android, with no compile error and
// no failing Go test anywhere else.
//
// Delete this test once Android keys off ErrIndexRebuildRequired.
func TestRebuildRequiredMessagesKeepAndroidSubstrings(t *testing.T) {
	ctx := context.Background()

	messages := map[string]string{
		"schema mismatch": openStoreErrText(t, ctx, indexSchemaVersion+1),
		"dim mismatch":    dimMismatchErrText(t, ctx),
		"EnsureDim":       ensureDimErrText(t, ctx),
	}

	// The exact literals any-kotlin matches on.
	needles := []string{"indexer:", "remove ", "to rebuild"}

	for name, msg := range messages {
		t.Run(name, func(t *testing.T) {
			for _, needle := range needles {
				if !strings.Contains(msg, needle) {
					t.Errorf("message lost the any-kotlin substring %q: %s", needle, msg)
				}
			}
		})
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

func openStoreErrText(t *testing.T, ctx context.Context, schema int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "index.db")
	seedSchema(t, ctx, path, schema)

	st, err := OpenStore(ctx, path, 0, false)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore on a foreign schema succeeded")
	}
	return err.Error()
}

func dimMismatchErrText(t *testing.T, ctx context.Context) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "index.db")
	st, err := OpenStore(ctx, path, 768, true)
	if err != nil {
		t.Fatalf("OpenStore (first, dim 768): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st, err = OpenStore(ctx, path, 1024, true)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore with a contradicting dim succeeded")
	}
	return err.Error()
}

func ensureDimErrText(t *testing.T, ctx context.Context) string {
	t.Helper()

	st, err := OpenStoreInMemory(ctx, 768, true)
	if err != nil {
		t.Fatalf("OpenStoreInMemory: %v", err)
	}
	defer st.Close()

	err = st.EnsureDim(ctx, 1024)
	if err == nil {
		t.Fatal("EnsureDim with a contradicting dim succeeded")
	}
	return err.Error()
}
