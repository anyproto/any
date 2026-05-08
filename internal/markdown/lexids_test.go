package markdown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocate_NoChange(t *testing.T) {
	old := []string{"a", "b", "c"}
	oldLex := []string{"L1", "L2", "L3"}
	res := Diff(old, old)
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	assert.Equal(t, oldLex, got)
}

func TestAllocate_FreshDoc(t *testing.T) {
	res := Diff(nil, []string{"a", "b", "c"})
	got, err := AllocateLexids(nil, res, "")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assertStrictlyMonotonic(t, got)
}

func TestAllocate_FreshDocWithSeed(t *testing.T) {
	res := Diff(nil, []string{"a", "b"})
	got, err := AllocateLexids(nil, res, "MMMM")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "MMMM", got[0])
	assertStrictlyMonotonic(t, got)
}

func TestAllocate_HeadInsert(t *testing.T) {
	old := []string{"b", "c"}
	oldLex := []string{"BBBB", "CCCC"}
	res := Diff(old, []string{"a", "b", "c"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Less(t, got[0], "BBBB", "head insert must sort before existing first lexid")
	assert.Equal(t, "BBBB", got[1])
	assert.Equal(t, "CCCC", got[2])
}

func TestAllocate_TailInsert(t *testing.T) {
	old := []string{"a", "b"}
	oldLex := []string{"AAAA", "BBBB"}
	res := Diff(old, []string{"a", "b", "c"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "AAAA", got[0])
	assert.Equal(t, "BBBB", got[1])
	assert.Greater(t, got[2], "BBBB")
}

func TestAllocate_BetweenInsert(t *testing.T) {
	old := []string{"a", "c"}
	oldLex := []string{"AAAA", "CCCC"}
	res := Diff(old, []string{"a", "b", "c"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	assertStrictlyMonotonic(t, got)
	assert.Equal(t, "AAAA", got[0])
	assert.Equal(t, "CCCC", got[2])
	assert.Greater(t, got[1], "AAAA")
	assert.Less(t, got[1], "CCCC")
}

func TestAllocate_MultipleInsertsBetween(t *testing.T) {
	old := []string{"a", "z"}
	oldLex := []string{"AAAA", "ZZZZ"}
	res := Diff(old, []string{"a", "b", "c", "d", "e", "z"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, 6)
	assertStrictlyMonotonic(t, got)
	assert.Equal(t, "AAAA", got[0])
	assert.Equal(t, "ZZZZ", got[5])
}

func TestAllocate_ReorderProducesValidLexids(t *testing.T) {
	old := []string{"a", "b", "c"}
	oldLex := []string{"AAAA", "BBBB", "CCCC"}
	res := Diff(old, []string{"c", "a", "b"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assertStrictlyMonotonic(t, got)
}

func TestAllocate_UpdateKeepsLexid(t *testing.T) {
	old := []string{"alpha paragraph here", "second"}
	oldLex := []string{"AAAA", "BBBB"}
	res := Diff(old, []string{"alpha paragraph EDITED", "second"})
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"AAAA", "BBBB"}, got, "updated block keeps lexid")
}

func TestAllocate_DenseInsertions(t *testing.T) {
	old := []string{"start", "end"}
	oldLex := []string{"AAAA", "AAAB"}
	newBlocks := []string{"start"}
	for k := 0; k < 100; k++ {
		newBlocks = append(newBlocks, "ins")
	}
	newBlocks = append(newBlocks, "end")
	res := Diff(old, newBlocks)
	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, len(newBlocks))
	assertStrictlyMonotonic(t, got)
	assert.Equal(t, "AAAA", got[0])
	assert.Equal(t, "AAAB", got[len(got)-1])
}

// TestAllocate_FullReplaceDoesNotCollide locks the cross-peer write
// bug where a Set that completely replaces the document (every old
// block deleted, every new block inserted, no Keep/Update anchors)
// previously generated lexids from lexidGen.Middle() — exactly the
// same range owners use for the original blocks. With Set's apply
// order being modify-then-delete and tombstones being sticky, that
// collision turned the new blocks into tombstones on every peer; the
// per-peer query returned [] even though the SDK reported successful
// inserts. Allocator must seed past the highest unreused old lexid
// so new ids and to-be-deleted ids stay on disjoint ranges.
func TestAllocate_FullReplaceDoesNotCollide(t *testing.T) {
	oldLex := []string{"PPPP", "PPQY"}
	res := Diff(
		[]string{"OWNER LINE 1", "OWNER LINE 2"},
		[]string{"BOB EDIT 1", "BOB EDIT 2"},
	)
	// Sanity: with default similarity threshold this is delete-all +
	// insert-all (the bug's trigger condition).
	require.Equal(t, []int{0, 1}, res.Deletes)
	require.Len(t, res.NewSeq, 2)
	for i, op := range res.NewSeq {
		require.Equal(t, OpInsert, op.Kind, "expected NewSeq[%d] to be Insert", i)
	}

	got, err := AllocateLexids(oldLex, res, "")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assertStrictlyMonotonic(t, got)
	for _, newId := range got {
		for _, oldId := range oldLex {
			require.NotEqual(t, oldId, newId,
				"new lexid %q collides with about-to-be-deleted oldLexid %q (would tombstone the just-inserted record)",
				newId, oldId)
		}
	}
}

func assertStrictlyMonotonic(t *testing.T, ids []string) {
	t.Helper()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("lexid order violation at i=%d: %q >= %q (full: %v)", i, ids[i-1], ids[i], ids)
		}
	}
}
