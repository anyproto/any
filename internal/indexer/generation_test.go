//go:build fts && vector && !gomobile

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
}

func (s *genSpace) Id() string                    { return s.id }
func (s *genSpace) Changes() space.ChangeIndexAPI { return genChanges{gen: s.gen} }

type genChanges struct {
	space.ChangeIndexAPI
	gen string
}

func (g genChanges) Generation(context.Context) (string, error) { return g.gen, nil }

func genWorker(t *testing.T, gen string) *spaceWorker {
	t.Helper()
	ctx := context.Background()
	st, err := OpenStoreInMemory(ctx, 0, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ix := New(nil, index.NewRegistry(), st, Options{})
	return &spaceWorker{ix: ix, sp: &genSpace{id: "sp1", gen: gen}}
}

// A rebuilt SDK store restarts the applySeq axis at zero, so a cursor
// from the previous generation sits past everything the feed will ever
// report again — the index would silently freeze. The generation change
// is the signal to drop the space's docs and start over.
func TestResolveCursor_GenerationChangeResetsIndex(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-2")
	require.NoError(t, w.ix.store.Apply(ctx, "sp1",
		[]DocUpsert{{Entry: entry("chat", "obj1", "chat_messages", "rec1", "hello", 4)}}, nil, nil))
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 500, "gen-1"))

	cursor, gen, err := w.resolveCursor(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor, "indexing restarts from the beginning of the new axis")
	require.Equal(t, "gen-2", gen)

	hits, err := w.ix.store.SearchFTS(ctx, "sp1", "hello", nil, 10)
	require.NoError(t, err)
	require.Empty(t, hits, "docs from the old axis are dropped, not left to drift")
}

func TestResolveCursor_SameGenerationKeepsCursor(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1")
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 500, "gen-1"))

	cursor, gen, err := w.resolveCursor(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 500, cursor)
	require.Equal(t, "gen-1", gen)
}

// A cursor stored before generations were tracked adopts the current one
// instead of forcing a re-index nobody asked for.
func TestResolveCursor_UnknownStoredGenerationAdopts(t *testing.T) {
	ctx := context.Background()
	w := genWorker(t, "gen-1")
	require.NoError(t, w.ix.store.SetCursor(ctx, "sp1", 42, ""))

	cursor, gen, err := w.resolveCursor(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 42, cursor)
	require.Equal(t, "gen-1", gen)
}
