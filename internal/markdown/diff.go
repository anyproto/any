package markdown

import (
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/zeebo/xxh3"
)

// OpKind classifies the per-position outcome of a diff. The new
// document is described by a NewSeq of length len(newBlocks); each
// entry says how that position's text is sourced.
type OpKind uint8

const (
	// OpKeep — old block kept verbatim; same lexid, no DB write.
	OpKeep OpKind = iota
	// OpUpdate — same lexid as the matched old block, text replaced.
	OpUpdate
	// OpInsert — new block, needs a fresh lexid allocated.
	OpInsert
)

// NewOp is one entry in DiffResult.NewSeq, walked in new-document
// order. OldIdx is the index into the old block slice for Keep and
// Update, and -1 for Insert.
type NewOp struct {
	Kind   OpKind
	OldIdx int
}

// DiffResult is the full plan returned by Diff. NewSeq aligns 1:1
// with the new block slice. Deletes is the set of old indices that
// were neither kept nor updated, in ascending order.
type DiffResult struct {
	NewSeq  []NewOp
	Deletes []int
}

// DefaultSimilarityThreshold is the cutoff for "edit vs delete +
// insert" inside a gap. Below this score the pair is treated as
// unrelated. Tuned to be permissive — small typo corrections should
// be matched as edits — without being so loose that wholly different
// blocks merge.
const DefaultSimilarityThreshold = 0.5

// Diff plans the per-block ops needed to transform old into new.
//
// Two passes:
//
//   - Pass 1 anchors: Myers-LCS-on-xxh3-hashes finds blocks that are
//     byte-identical between old and new. Those become Keep ops at
//     their new positions.
//
//   - Pass 2 gap-fill: in each gap between anchors (and at the
//     edges), pair unmatched old blocks with unmatched new blocks by
//     similarity score (sergi/go-diff Levenshtein-like ratio).
//     Pairs above the threshold become Update ops; leftover olds go
//     to Deletes; leftover news go to Insert ops at their new
//     positions.
func Diff(oldBlocks, newBlocks []string) DiffResult {
	return DiffWithThreshold(oldBlocks, newBlocks, DefaultSimilarityThreshold)
}

// DiffWithThreshold is Diff with a caller-supplied similarity cutoff
// for the gap-fill phase. Useful for tests.
func DiffWithThreshold(oldBlocks, newBlocks []string, threshold float64) DiffResult {
	oldHashes := hashAll(oldBlocks)
	newHashes := hashAll(newBlocks)

	anchors := lcsByHash(oldHashes, newHashes)

	newSeq := make([]NewOp, len(newBlocks))
	for i := range newSeq {
		newSeq[i] = NewOp{Kind: OpInsert, OldIdx: -1}
	}
	oldUsed := make([]bool, len(oldBlocks))
	for _, a := range anchors {
		newSeq[a.newIdx] = NewOp{Kind: OpKeep, OldIdx: a.oldIdx}
		oldUsed[a.oldIdx] = true
	}

	prevOld, prevNew := -1, -1
	gap := func(oldEnd, newEnd int) {
		fillGap(
			oldBlocks, newBlocks,
			prevOld+1, oldEnd,
			prevNew+1, newEnd,
			threshold,
			newSeq, oldUsed,
		)
	}
	for _, a := range anchors {
		gap(a.oldIdx, a.newIdx)
		prevOld, prevNew = a.oldIdx, a.newIdx
	}
	gap(len(oldBlocks), len(newBlocks))

	var deletes []int
	for i, used := range oldUsed {
		if !used {
			deletes = append(deletes, i)
		}
	}

	return DiffResult{NewSeq: newSeq, Deletes: deletes}
}

type anchor struct{ oldIdx, newIdx int }

type gapPair struct {
	oldIdx, newIdx int
	score          float64
}

// lcsByHash runs a textbook longest-common-subsequence DP over
// uint64 hash arrays and backtracks to recover anchors in ascending
// (oldIdx, newIdx) order.
func lcsByHash(a, b []uint64) []anchor {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	out := make([]anchor, 0, dp[m][n])
	for i, j := m, n; i > 0 && j > 0; {
		if a[i-1] == b[j-1] {
			out = append(out, anchor{i - 1, j - 1})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// fillGap pairs the unmatched old blocks in [oldLo, oldHi) with the
// unmatched new blocks in [newLo, newHi) by similarity score. Pairs
// scoring at or above threshold become Update ops; the rest stay
// Insert / fall through to Delete.
//
// Greedy with monotonicity: a pair (o, n) crosses an accepted
// pair (o', n') if (o < o' ⇔ n > n'). Crossings would assign a
// later new-position a smaller old-lexid, breaking the document
// sort order. Reject conflicts so the resulting Update sequence
// stays monotonic in both old and new indices.
func fillGap(
	oldBlocks, newBlocks []string,
	oldLo, oldHi, newLo, newHi int,
	threshold float64,
	newSeq []NewOp, oldUsed []bool,
) {
	if oldLo >= oldHi || newLo >= newHi {
		return
	}
	dmp := diffmatchpatch.New()
	var pairs []gapPair
	for o := oldLo; o < oldHi; o++ {
		for n := newLo; n < newHi; n++ {
			s := similarity(dmp, oldBlocks[o], newBlocks[n])
			if s < threshold {
				continue
			}
			pairs = append(pairs, gapPair{o, n, s})
		}
	}
	sortPairsDescending(pairs)
	type acc struct{ o, n int }
	var accepted []acc
	usedOld := make(map[int]bool, len(pairs))
	usedNew := make(map[int]bool, len(pairs))
	for _, p := range pairs {
		if usedOld[p.oldIdx] || usedNew[p.newIdx] {
			continue
		}
		conflict := false
		for _, a := range accepted {
			if (p.oldIdx < a.o) != (p.newIdx < a.n) {
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}
		newSeq[p.newIdx] = NewOp{Kind: OpUpdate, OldIdx: p.oldIdx}
		oldUsed[p.oldIdx] = true
		usedOld[p.oldIdx] = true
		usedNew[p.newIdx] = true
		accepted = append(accepted, acc{p.oldIdx, p.newIdx})
	}
}

// similarity returns a [0,1] score where 1 == identical and 0 ==
// nothing in common. Defined as the equal-character mass over total
// length, the same shape as Python difflib's SequenceMatcher.ratio().
func similarity(dmp *diffmatchpatch.DiffMatchPatch, a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	diffs := dmp.DiffMain(a, b, false)
	equal := 0
	for _, d := range diffs {
		if d.Type == diffmatchpatch.DiffEqual {
			equal += len(d.Text)
		}
	}
	return 2.0 * float64(equal) / float64(len(a)+len(b))
}

func hashAll(blocks []string) []uint64 {
	out := make([]uint64, len(blocks))
	for i, b := range blocks {
		out[i] = xxh3.HashString(b)
	}
	return out
}

func sortPairsDescending(p []gapPair) {
	for i := 1; i < len(p); i++ {
		j := i
		for j > 0 && p[j-1].score < p[j].score {
			p[j-1], p[j] = p[j], p[j-1]
			j--
		}
	}
}
