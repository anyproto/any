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

func TestDiff_EmptyParagraphKeepsItsId(t *testing.T) {
	// Typing into a blank line, and clearing a paragraph, are updates
	// of that record — not delete + insert. An empty entry scores 0
	// against any text, so the similarity pass can never pair it; the
	// positional pass has to.
	typed := Diff([]string{"a", "", "b"}, []string{"a", "hello", "b"})
	assert.Empty(t, typed.Deletes)
	assert.Equal(t, []NewOp{{OpKeep, 0}, {OpUpdate, 1}, {OpKeep, 2}}, typed.NewSeq)

	cleared := Diff([]string{"a", "hello", "b"}, []string{"a", "", "b"})
	assert.Empty(t, cleared.Deletes)
	assert.Equal(t, []NewOp{{OpKeep, 0}, {OpUpdate, 1}, {OpKeep, 2}}, cleared.NewSeq)
}

func TestDiff_EmptyParagraphRunsStayAnchored(t *testing.T) {
	// Identical empties are one hash, so the LCS anchors them; adding
	// one must not reshuffle the others.
	res := Diff([]string{"a", "", "b"}, []string{"a", "", "", "b"})
	assert.Empty(t, res.Deletes)
	// Which of the interchangeable empties is the new one is not
	// meaningful; that every old block survives, exactly one block is
	// created, and the kept order is ascending, is.
	inserts, prevOld := 0, -1
	for i, op := range res.NewSeq {
		if op.Kind == OpInsert {
			inserts++
			continue
		}
		assert.Greater(t, op.OldIdx, prevOld, "pos %d out of order: %+v", i, res.NewSeq)
		prevOld = op.OldIdx
	}
	assert.Equal(t, 1, inserts, "%+v", res.NewSeq)
}

func TestDiff_EmptyPairingStaysMonotonic(t *testing.T) {
	// The positional pass must respect pairs the similarity pass
	// already accepted — a crossing would hand a later position a
	// smaller lexid.
	res := Diff([]string{"", "alpha text"}, []string{"alpha text!", ""})
	for i, op := range res.NewSeq {
		if op.Kind == OpUpdate && i > 0 {
			prev := res.NewSeq[i-1]
			if prev.Kind != OpInsert && prev.OldIdx > op.OldIdx {
				t.Errorf("non-monotonic plan: %+v", res.NewSeq)
			}
		}
	}
}
