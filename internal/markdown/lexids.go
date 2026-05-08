package markdown

import (
	"fmt"

	"github.com/anyproto/lexid"
)

// lexidGen is the allocator for block-record ids. Mirrors the
// parameters used in any-sync-sdk's internal/object/lexid.go (the
// any-sync tree allocator) so the alphabet, blockSize, and stepSize
// match the rest of the stack.
var lexidGen = lexid.Must(lexid.CharsAllNoEscape, 4, 100)

// AllocateLexids returns one lexid per entry in diff.NewSeq, aligned
// 1:1. Keep / Update entries reuse the matched old block's lexid;
// Insert entries are allocated to fall lexicographically between
// their bounding fixed neighbours so the document sort order
// matches the new-document order.
//
// The optional seed argument is consulted only on a fresh document
// (no fixed-position neighbours, every entry is an Insert). It picks
// the starting lexid for the first inserted block — randomising it
// across writers keeps two clients that each create the first block
// in a doc concurrently from colliding on the same id. Pass "" to
// fall back to lexid.Middle().
//
// Collision avoidance with deleted blocks: when an insert run has no
// Keep/Update neighbours but the old document had blocks (i.e. all of
// them are being deleted by this Set), the run's first id is seeded
// past the highest existing oldLexid. Without this, a fresh-doc seed
// (Middle) would generate ids that exactly match the to-be-deleted
// ones — and since the SDK applies the per-record Modify *before* the
// Delete batch in markdown.Set, and Delete writes a tombstone that
// overwrites the freshly-modified record (and tombstones are sticky),
// the net result on every peer would be empty md_blocks — even though
// markdown.Set reports inserted/updated lexids. Seeding past the
// existing range keeps the new inserts and deletions on disjoint
// lexid space, so the inserts survive their own Set call.
func AllocateLexids(oldLexids []string, diff DiffResult, seed string) ([]string, error) {
	if len(diff.NewSeq) == 0 {
		return nil, nil
	}
	out := make([]string, len(diff.NewSeq))

	for i, op := range diff.NewSeq {
		switch op.Kind {
		case OpKeep, OpUpdate:
			if op.OldIdx < 0 || op.OldIdx >= len(oldLexids) {
				return nil, fmt.Errorf("markdown: AllocateLexids: NewSeq[%d] OldIdx=%d out of range (oldLexids len=%d)", i, op.OldIdx, len(oldLexids))
			}
			out[i] = oldLexids[op.OldIdx]
		}
	}

	// maxOld is the lexically-largest old id that is NOT being reused
	// (every block currently in oldLexids minus the ones that show up
	// as Keep/Update in NewSeq). If non-empty it's used as a fallback
	// "prev" for boundary-less insert runs to avoid colliding with the
	// to-be-deleted ids.
	maxOld := unreusedMaxLexid(oldLexids, diff.NewSeq)

	i := 0
	for i < len(diff.NewSeq) {
		if diff.NewSeq[i].Kind != OpInsert {
			i++
			continue
		}
		j := i
		for j < len(diff.NewSeq) && diff.NewSeq[j].Kind == OpInsert {
			j++
		}
		runLen := j - i
		var prev, next string
		if i > 0 {
			prev = out[i-1]
		}
		if j < len(diff.NewSeq) {
			next = out[j]
		}
		// Boundary-less run + outgoing oldLexids: seed past the
		// highest unreused old id so we don't recreate ids that the
		// Delete batch is about to tombstone.
		if prev == "" && next == "" && maxOld != "" {
			prev = maxOld
		}
		ids, err := allocateRun(prev, next, runLen, seed)
		if err != nil {
			return nil, fmt.Errorf("markdown: AllocateLexids: run [%d,%d): %w", i, j, err)
		}
		copy(out[i:j], ids)
		i = j
	}
	return out, nil
}

// unreusedMaxLexid returns the lexically-largest oldLexid that NewSeq
// does not preserve via a Keep or Update entry. Empty when every old
// block is reused (the loop won't enter the boundary-less seeding
// path anyway in that case, but the explicit check keeps callers
// honest).
func unreusedMaxLexid(oldLexids []string, newSeq []NewOp) string {
	if len(oldLexids) == 0 {
		return ""
	}
	reused := make(map[int]struct{}, len(newSeq))
	for _, op := range newSeq {
		if (op.Kind == OpKeep || op.Kind == OpUpdate) && op.OldIdx >= 0 {
			reused[op.OldIdx] = struct{}{}
		}
	}
	var best string
	for i, id := range oldLexids {
		if _, kept := reused[i]; kept {
			continue
		}
		if id > best {
			best = id
		}
	}
	return best
}

// allocateRun produces n lexids that sort strictly between prev and
// next, in ascending order. Empty prev means "before everything";
// empty next means "after everything"; both empty (the fresh-doc
// case) seeds from the supplied seed (or lexid.Middle() when the
// caller did not provide one).
func allocateRun(prev, next string, n int, seed string) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out := make([]string, n)
	switch {
	case prev == "" && next == "":
		start := seed
		if start == "" {
			start = lexidGen.Middle()
		}
		out[0] = start
		for k := 1; k < n; k++ {
			out[k] = lexidGen.Next(out[k-1])
		}
	case next == "":
		out[0] = lexidGen.Next(prev)
		for k := 1; k < n; k++ {
			out[k] = lexidGen.Next(out[k-1])
		}
	case prev == "":
		first, err := lexidGen.NextBefore("", next)
		if err != nil {
			return nil, err
		}
		out[0] = first
		for k := 1; k < n; k++ {
			id, err := lexidGen.NextBefore(out[k-1], next)
			if err != nil {
				return nil, err
			}
			out[k] = id
		}
	default:
		cur := prev
		for k := 0; k < n; k++ {
			id, err := lexidGen.NextBefore(cur, next)
			if err != nil {
				return nil, err
			}
			out[k] = id
			cur = id
		}
	}
	return out, nil
}
