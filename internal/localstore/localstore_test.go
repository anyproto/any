package localstore

import (
	"context"
	"errors"
	"slices"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/stretchr/testify/require"
)

const (
	spaceA = "bafyreiaybsrmdicsv7fmuupnzojtdnhpna7its6evawg7lhw67pu4yiulq.1f5isugmolo56"
	spaceB = "bafyreicb4xgogwdp4bbb7k62c32j26sebr2h25wryacy6thr6d4j5sgdia.h7e0kxm1u5ng"
)

func openDB(t *testing.T) anystore.DB {
	t.Helper()
	db, err := anystore.Open(context.Background(), "", &anystore.Config{InMemory: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestParseRef(t *testing.T) {
	ok := []struct {
		scope   Scope
		spaceId string
		name    string
		storage string
	}{
		{ScopeAccount, "", "scratch", "l_a_scratch"},
		{ScopeAccount, "", "with_under_score", "l_a_with_under_score"},
		{ScopeAccount, "", "with-dash-9", "l_a_with-dash-9"},
		{ScopeSpace, spaceA, "cache", "l_s_" + spaceA + "_cache"},
		{ScopeSpace, spaceA, "a_b_c", "l_s_" + spaceA + "_a_b_c"},
		// A name that looks like a storage name is still just a name.
		{ScopeAccount, "", "l_a_x", "l_a_l_a_x"},
	}
	for _, tc := range ok {
		ref, err := ParseRef(tc.scope, tc.spaceId, tc.name)
		require.NoError(t, err, tc.storage)
		require.Equal(t, tc.storage, ref.StorageName())
		back, found := ParseStorageName(tc.storage)
		require.True(t, found, tc.storage)
		require.Equal(t, ref, back)
	}

	bad := []struct {
		scope   Scope
		spaceId string
		name    string
	}{
		{ScopeAccount, "", ""},
		{ScopeAccount, "", "_lead"},
		{ScopeAccount, "", "-lead"},
		{ScopeAccount, "", "Upper"},
		{ScopeAccount, "", "dot.name"},
		{ScopeAccount, spaceA, "x"}, // spaceId on account scope
		{ScopeSpace, "", "x"},
		{ScopeSpace, "has_underscore", "x"},
		{ScopeSpace, "has/slash", "x"},
		{Scope("device"), "", "x"},
		{Scope(""), "", "x"},
	}
	for _, tc := range bad {
		_, err := ParseRef(tc.scope, tc.spaceId, tc.name)
		require.ErrorIs(t, err, ErrBadName, "%+v", tc)
	}
}

func TestParseStorageName_RejectsUntagged(t *testing.T) {
	for _, s := range []string{
		"",
		"l",
		"l_",
		"l_a",
		"l_a_",
		"l_s_" + spaceA,
		"l_s_" + spaceA + "_",
		"l_x_name",
		"l_s__name",
		"L_a_name",
		"la_name",
		"_meta",
		"_detached",
		"_history_meta",
		"_read_state",
		spaceA + "_objects",
		spaceA + "_members",
		spaceB + "_chat_messages",
		"files_cache",
		"cursors",
	} {
		_, ok := ParseStorageName(s)
		require.False(t, ok, "%q must not parse as local", s)
		_, err := SinkTarget(s)
		require.ErrorIs(t, err, ErrNotLocal, s)
	}
}

func TestEnsureIdempotentAndIndexes(t *testing.T) {
	ctx := context.Background()
	s := New(openDB(t))
	ref, err := ParseRef(ScopeAccount, "", "notes")
	require.NoError(t, err)

	created, err := s.Ensure(ctx, ref, nil)
	require.NoError(t, err)
	require.True(t, created)

	created, err = s.Ensure(ctx, ref, []anystore.IndexInfo{{Fields: []string{"kind", "-at"}}})
	require.NoError(t, err)
	require.False(t, created)

	// Re-ensuring the same index is a no-op; a second index is added.
	created, err = s.Ensure(ctx, ref, []anystore.IndexInfo{
		{Fields: []string{"kind", "-at"}},
		{Fields: []string{"key"}, Unique: true},
	})
	require.NoError(t, err)
	require.False(t, created)

	coll, err := s.Collection(ctx, ref)
	require.NoError(t, err)
	require.Len(t, coll.GetIndexes(), 2)

	require.NoError(t, coll.Insert(ctx, anyenc.MustParseJson(`{"id":"1","key":"k","kind":"a","at":1}`)))
	err = coll.Insert(ctx, anyenc.MustParseJson(`{"id":"2","key":"k","kind":"a","at":2}`))
	require.ErrorIs(t, err, anystore.ErrUniqueConstraint)
}

func TestCollectionAndDrop(t *testing.T) {
	ctx := context.Background()
	s := New(openDB(t))
	ref, err := ParseRef(ScopeSpace, spaceA, "cache")
	require.NoError(t, err)

	_, err = s.Collection(ctx, ref)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, s.Drop(ctx, ref), ErrNotFound)

	_, err = s.Ensure(ctx, ref, nil)
	require.NoError(t, err)
	coll, err := s.Collection(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, ref.StorageName(), coll.Name())
	require.NoError(t, coll.Insert(ctx, anyenc.MustParseJson(`{"id":"x"}`)))

	require.NoError(t, s.Drop(ctx, ref))
	_, err = s.Collection(ctx, ref)
	require.ErrorIs(t, err, ErrNotFound)

	// Re-ensure after drop starts empty.
	created, err := s.Ensure(ctx, ref, nil)
	require.NoError(t, err)
	require.True(t, created)
	coll, err = s.Collection(ctx, ref)
	require.NoError(t, err)
	n, err := coll.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// TestListFence is the inverse fence test: with CRDT-shaped, SDK-fixed
// and tagged collections side by side in one DB, List returns exactly
// the tagged set, and only what the (scope, spaceId) filter selects.
func TestListFence(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := New(db)

	// What the SDK would put next to us.
	for _, name := range []string{
		"_meta", "_detached", "_history_meta", "_read_state",
		spaceA + "_objects", spaceA + "_members", spaceB + "_objects",
		"bafyreiez5sj4lntadunhsvgi7mhf7ul3tzqrrau6r7osce5k4jgwyyp76e_chat_messages",
		"files_cache", "l", "l_", "lx_name", "l_x_name",
	} {
		_, err := db.CreateCollection(ctx, name)
		require.NoError(t, err)
	}

	mk := func(scope Scope, spaceId, name string) Ref {
		ref, err := ParseRef(scope, spaceId, name)
		require.NoError(t, err)
		_, err = s.Ensure(ctx, ref, nil)
		require.NoError(t, err)
		return ref
	}
	accScratch := mk(ScopeAccount, "", "scratch")
	accZ := mk(ScopeAccount, "", "z_last")
	aCache := mk(ScopeSpace, spaceA, "cache")
	aIngest := mk(ScopeSpace, spaceA, "ingest_stage")
	bCache := mk(ScopeSpace, spaceB, "cache")

	coll, err := s.Collection(ctx, aCache)
	require.NoError(t, err)
	require.NoError(t, coll.Insert(ctx,
		anyenc.MustParseJson(`{"id":"1"}`), anyenc.MustParseJson(`{"id":"2"}`)))
	require.NoError(t, coll.EnsureIndex(ctx, anystore.IndexInfo{Fields: []string{"k"}}))

	refs := func(infos []Info) []Ref {
		out := make([]Ref, 0, len(infos))
		for _, i := range infos {
			out = append(out, i.Ref)
		}
		return out
	}

	all, err := s.List(ctx, "", "")
	require.NoError(t, err)
	require.ElementsMatch(t, []Ref{accScratch, accZ, aCache, aIngest, bCache}, refs(all))
	for _, i := range all {
		if i.Ref == aCache {
			require.Equal(t, 2, i.Count)
			require.Len(t, i.Indexes, 1)
			require.Equal(t, []string{"k"}, i.Indexes[0].Fields)
		} else {
			require.Equal(t, 0, i.Count)
			require.Empty(t, i.Indexes)
		}
	}

	acc, err := s.List(ctx, ScopeAccount, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []Ref{accScratch, accZ}, refs(acc))

	spaces, err := s.List(ctx, ScopeSpace, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []Ref{aCache, aIngest, bCache}, refs(spaces))

	onlyA, err := s.List(ctx, ScopeSpace, spaceA)
	require.NoError(t, err)
	require.ElementsMatch(t, []Ref{aCache, aIngest}, refs(onlyA))

	none, err := s.List(ctx, ScopeSpace, "bafynothing.x")
	require.NoError(t, err)
	require.Empty(t, none)

	_, err = s.List(ctx, Scope("device"), "")
	require.ErrorIs(t, err, ErrBadName)
	_, err = s.List(ctx, ScopeSpace, "bad_id")
	require.ErrorIs(t, err, ErrBadName)
	_, err = s.List(ctx, ScopeAccount, spaceA)
	require.ErrorIs(t, err, ErrBadName)

	// The other direction: enumerate the raw DB and assert the tagged
	// set is exactly what ParseStorageName accepts.
	names, err := db.GetCollectionNames(ctx)
	require.NoError(t, err)
	var tagged []Ref
	for _, n := range names {
		if ref, ok := ParseStorageName(n); ok {
			tagged = append(tagged, ref)
		}
	}
	require.ElementsMatch(t, refs(all), tagged)
	// And every tagged name the store minted is a name the SDK's owner
	// split would classify as ownerNone: the segment before the first "_"
	// is the bare tag, never a space or object id.
	for _, r := range refs(all) {
		require.True(t, slices.Contains(names, r.StorageName()))
		require.Equal(t, "l_", r.StorageName()[:2])
	}
}

func TestSinkTarget(t *testing.T) {
	ref, err := SinkTarget("l_s_" + spaceA + "_rollup")
	require.NoError(t, err)
	require.Equal(t, Ref{Scope: ScopeSpace, SpaceId: spaceA, Name: "rollup"}, ref)

	ref, err = SinkTarget("l_a_totals")
	require.NoError(t, err)
	require.Equal(t, Ref{Scope: ScopeAccount, Name: "totals"}, ref)

	for _, bad := range []string{spaceA + "_objects", "_meta", "rollup", "l_a_", "l_s_" + spaceA} {
		_, err := SinkTarget(bad)
		require.True(t, errors.Is(err, ErrNotLocal), bad)
	}
}

// TestEnsureAtomic: a rejected index rolls the create back — no
// collection is left behind, and a later valid Ensure reports created.
func TestEnsureAtomic(t *testing.T) {
	ctx := context.Background()
	s := New(openDB(t))
	ref, err := ParseRef(ScopeAccount, "", "atomic")
	require.NoError(t, err)

	_, err = s.Ensure(ctx, ref, []anystore.IndexInfo{
		{Name: "a", Fields: []string{"x"}},
		{Name: "a", Fields: []string{"y"}},
	})
	require.ErrorIs(t, err, anystore.ErrIndexMismatch)
	_, err = s.Collection(ctx, ref)
	require.ErrorIs(t, err, ErrNotFound)

	created, err := s.Ensure(ctx, ref, []anystore.IndexInfo{{Name: "a", Fields: []string{"x"}}})
	require.NoError(t, err)
	require.True(t, created)
	// An existing collection with a conflicting index: error, collection kept.
	_, err = s.Ensure(ctx, ref, []anystore.IndexInfo{{Name: "a", Fields: []string{"y"}}})
	require.ErrorIs(t, err, anystore.ErrIndexMismatch)
	coll, err := s.Collection(ctx, ref)
	require.NoError(t, err)
	require.Len(t, coll.GetIndexes(), 1)
}
