package indexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// genSpace reports a fixed SDK generation.
type genSpace struct {
	space.Space
	id  string
	gen string
	max uint64
}

func (s *genSpace) Id() string                    { return s.id }
func (s *genSpace) Changes() space.ChangeIndexAPI { return genChanges{gen: s.gen, max: s.max} }

type genChanges struct {
	space.ChangeIndexAPI
	gen string
	max uint64
}

func (g genChanges) Generation(context.Context) (string, error)  { return g.gen, nil }
func (g genChanges) MaxApplySeq(context.Context) (uint64, error) { return g.max, nil }
func (g genChanges) Subscribe(func(space.ObjectChange)) func()   { return func() {} }

func genWorker(t *testing.T, gen string, max uint64) *spaceWorker {
	t.Helper()
	ctx := context.Background()
	st, err := OpenStoreInMemory(ctx, 0, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ix := New(nil, index.NewRegistry(), st, Options{})
	return &spaceWorker{ix: ix, sp: &genSpace{id: "sp1", gen: gen, max: max}}
}

// A rebuilt SDK store restarts the applySeq axis at zero, so a cursor
// from the previous generation sits past everything the feed will ever
// report again — the index would silently freeze. The generation change
// is the signal to drop the space's docs and start over.
func TestAlignIndex_GenerationChangeResetsIndex(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-2", 1000)
	require.NoError(t, w.ix.store.Apply(ctx, "sp1",
		[]DocUpsert{{Entry: entry("chat", "obj1", "chat_messages", "rec1", "hello", 4)}}, nil, nil))
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 500, "gen-1"))

	w.alignIndex(ctx)
	cursor, gen := storedCursor(t, w)
	require.Zero(t, cursor, "indexing restarts from the beginning of the new axis")
	require.Equal(t, "gen-2", gen)
	require.Equal(t, "gen-2", w.generation)

	hits, err := w.ix.store.SearchFTS(ctx, "sp1", "hello", nil, 10)
	require.NoError(t, err)
	require.Empty(t, hits, "docs from the old axis are dropped, not left to drift")
}

func TestAlignIndex_SameGenerationKeepsCursor(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1", 1000)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 500, "gen-1"))

	w.alignIndex(ctx)
	cursor, gen := storedCursor(t, w)
	require.EqualValues(t, 500, cursor)
	require.Equal(t, "gen-1", gen)
}

// An older sdk.db restored from backup leaves the cursor past everything
// the feed can report — same freeze as a rebuild, different cause, and
// the generation is unchanged so only the axis bound catches it.
func TestAlignIndex_CursorPastAxisResetsIndex(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1", 120)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 500, "gen-1"))

	w.alignIndex(ctx)
	cursor, _ := storedCursor(t, w)
	require.Zero(t, cursor, "a cursor beyond MaxApplySeq restarts")
}

// A cursor stored before generations were tracked adopts the current one
// instead of forcing a re-index nobody asked for — the axis bound is
// what protects that case.
func TestAlignIndex_UnknownStoredGenerationAdopts(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1", 1000)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 42, ""))

	w.alignIndex(ctx)
	cursor, gen := storedCursor(t, w)
	require.EqualValues(t, 42, cursor)
	require.Equal(t, "gen-1", w.generation)
	require.Equal(t, "gen-1", gen, "the epoch is stamped on the row without waiting for a change")
}

// A store that cannot report its generation must not erase the one on
// record — the next rebuild would then go undetected.
func TestAlignIndex_EmptyGenerationKeepsStored(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "", 1000)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 42, "gen-1"))

	w.alignIndex(ctx)
	require.Equal(t, "gen-1", w.generation, "the known epoch survives")
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 43, w.generation))
	_, gen := storedCursor(t, w)
	require.Equal(t, "gen-1", gen)
}

// A worker can reach the advance loop with no epoch in hand: the
// boot-time cursor read failed, or SyncSpace built it without
// alignIndex. Persisting that empty value would erase the stamp on the
// row, and the next SDK store rebuild would then pass both re-index
// triggers unnoticed — the silent freeze the stamp exists to catch.
func TestSetCursor_AdvanceWithoutEpochKeepsStamp(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-2", 1000)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 100, "gen-1"))

	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 200, ""))
	cursor, gen := storedCursor(t, w)
	require.EqualValues(t, 200, cursor, "the cursor still advances")
	require.Equal(t, "gen-1", gen, "the stamp survives an advance that carries no epoch")

	// So the rebuild is still caught on the next boot.
	w.alignIndex(ctx)
	cursor, gen = storedCursor(t, w)
	require.Zero(t, cursor)
	require.Equal(t, "gen-2", gen)
}

// The first cursor for a space creates the row, epoch or not.
func TestSetCursor_InsertsRow(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1", 1000)
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 7, ""))
	cursor, gen := storedCursor(t, w)
	require.EqualValues(t, 7, cursor)
	require.Empty(t, gen)
}

func storedCursor(t *testing.T, w *spaceWorker) (uint64, string) {
	t.Helper()
	cursor, gen, err := w.ix.store.Cursor(context.Background(), "sp1")
	require.NoError(t, err)
	return cursor, gen
}
