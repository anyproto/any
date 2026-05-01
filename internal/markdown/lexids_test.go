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

func assertStrictlyMonotonic(t *testing.T, ids []string) {
	t.Helper()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("lexid order violation at i=%d: %q >= %q (full: %v)", i, ids[i-1], ids[i], ids)
		}
	}
}
