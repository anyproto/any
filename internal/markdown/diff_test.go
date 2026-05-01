package markdown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiff_NoChange(t *testing.T) {
	old := []string{"a", "b", "c"}
	res := Diff(old, old)
	assert.Empty(t, res.Deletes)
	require.Len(t, res.NewSeq, 3)
	for i, op := range res.NewSeq {
		assert.Equal(t, OpKeep, op.Kind, "pos %d", i)
		assert.Equal(t, i, op.OldIdx)
	}
}

func TestDiff_PureInsertAtTop(t *testing.T) {
	res := Diff([]string{"a", "b"}, []string{"new", "a", "b"})
	assert.Empty(t, res.Deletes)
	assert.Equal(t, []NewOp{
		{OpInsert, -1},
		{OpKeep, 0},
		{OpKeep, 1},
	}, res.NewSeq)
}

func TestDiff_PureInsertAtBottom(t *testing.T) {
	res := Diff([]string{"a", "b"}, []string{"a", "b", "new"})
	assert.Empty(t, res.Deletes)
	assert.Equal(t, []NewOp{
		{OpKeep, 0},
		{OpKeep, 1},
		{OpInsert, -1},
	}, res.NewSeq)
}

func TestDiff_PureInsertBetween(t *testing.T) {
	res := Diff([]string{"a", "c"}, []string{"a", "b", "c"})
	assert.Empty(t, res.Deletes)
	assert.Equal(t, []NewOp{
		{OpKeep, 0},
		{OpInsert, -1},
		{OpKeep, 1},
	}, res.NewSeq)
}

func TestDiff_PureDelete(t *testing.T) {
	res := Diff([]string{"a", "b", "c"}, []string{"a", "c"})
	assert.Equal(t, []int{1}, res.Deletes)
	assert.Equal(t, []NewOp{
		{OpKeep, 0},
		{OpKeep, 2},
	}, res.NewSeq)
}

func TestDiff_UpdateInPlace(t *testing.T) {
	res := Diff(
		[]string{"alpha paragraph here", "second paragraph"},
		[]string{"alpha paragraph edited", "second paragraph"},
	)
	assert.Empty(t, res.Deletes)
	require.Len(t, res.NewSeq, 2)
	assert.Equal(t, NewOp{OpUpdate, 0}, res.NewSeq[0])
	assert.Equal(t, NewOp{OpKeep, 1}, res.NewSeq[1])
}

func TestDiff_TotallyDifferentBlocksAreDeleteInsert(t *testing.T) {
	res := DiffWithThreshold(
		[]string{"the quick brown fox jumps over the lazy dog"},
		[]string{"completely unrelated content nothing in common"},
		DefaultSimilarityThreshold,
	)
	require.Len(t, res.NewSeq, 1)
	assert.Equal(t, OpInsert, res.NewSeq[0].Kind)
	assert.Equal(t, []int{0}, res.Deletes)
}

func TestDiff_GapPicksBestPair(t *testing.T) {
	old := []string{
		"this is paragraph one with content",
		"completely separate idea over here",
	}
	newBlocks := []string{
		"this is paragraph one with EDITED content",
		"completely separate idea over THERE",
	}
	res := Diff(old, newBlocks)
	assert.Empty(t, res.Deletes)
	require.Len(t, res.NewSeq, 2)
	assert.Equal(t, NewOp{OpUpdate, 0}, res.NewSeq[0])
	assert.Equal(t, NewOp{OpUpdate, 1}, res.NewSeq[1])
}

func TestDiff_FromEmpty(t *testing.T) {
	res := Diff(nil, []string{"a", "b"})
	assert.Empty(t, res.Deletes)
	assert.Equal(t, []NewOp{{OpInsert, -1}, {OpInsert, -1}}, res.NewSeq)
}

func TestDiff_ToEmpty(t *testing.T) {
	res := Diff([]string{"a", "b"}, nil)
	assert.Equal(t, []int{0, 1}, res.Deletes)
	assert.Empty(t, res.NewSeq)
}

func TestDiff_Reorder(t *testing.T) {
	res := Diff([]string{"a", "b", "c"}, []string{"c", "a", "b"})
	require.Len(t, res.NewSeq, 3)
	assert.Equal(t, OpInsert, res.NewSeq[0].Kind)
	assert.Equal(t, NewOp{OpKeep, 0}, res.NewSeq[1])
	assert.Equal(t, NewOp{OpKeep, 1}, res.NewSeq[2])
	assert.Equal(t, []int{2}, res.Deletes)
}

func TestDiff_DuplicateBlocksMatchByPosition(t *testing.T) {
	old := []string{"same", "same", "tail"}
	res := Diff(old, []string{"same", "same edited", "tail"})
	require.Len(t, res.NewSeq, 3)
	assert.Equal(t, OpKeep, res.NewSeq[0].Kind)
	assert.Equal(t, OpUpdate, res.NewSeq[1].Kind)
	assert.Equal(t, 1, res.NewSeq[1].OldIdx)
	assert.Equal(t, NewOp{OpKeep, 2}, res.NewSeq[2])
	assert.Empty(t, res.Deletes)
}
