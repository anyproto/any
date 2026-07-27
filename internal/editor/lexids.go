package editor

import (
	"fmt"

	"github.com/anyproto/lexid"
)

// lexidGen is the allocator for block `nav.pos` values. Mirrors the
// parameters used in any-sync-sdk's internal/object/lexid.go (the
// any-sync tree allocator) and in internal/markdown's md_blocks
// allocator, so the alphabet, blockSize, and stepSize match the rest
// of the stack.
var lexidGen = lexid.Must(lexid.CharsAllNoEscape, 4, 100)

// NextPos returns a lexid that sorts strictly after `prev`. Empty
// `prev` returns Middle() — used to drop the first block into an
// empty parent with headroom on both sides. Mirrors anytype-heart's
// pattern (Middle on empty, Next(prev) on append).
func NextPos(prev string) string {
	if prev == "" {
		return lexidGen.Middle()
	}
	return lexidGen.Next(prev)
}

// AllocateRun returns n lexids that sort strictly between prev and
// next, in ascending order. Empty prev means "before everything";
// empty next means "after everything"; both empty seeds from
// Middle(). Used by the markdown bulk-rewrite path.
func AllocateRun(prev, next string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	out := make([]string, n)
	switch {
	case prev == "" && next == "":
		out[0] = lexidGen.Middle()
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
			return nil, fmt.Errorf("blocks: AllocateRun head: %w", err)
		}
		out[0] = first
		for k := 1; k < n; k++ {
			id, err := lexidGen.NextBefore(out[k-1], next)
			if err != nil {
				return nil, fmt.Errorf("blocks: AllocateRun head k=%d: %w", k, err)
			}
			out[k] = id
		}
	default:
		cur := prev
		for k := 0; k < n; k++ {
			id, err := lexidGen.NextBefore(cur, next)
			if err != nil {
				return nil, fmt.Errorf("blocks: AllocateRun between k=%d: %w", k, err)
			}
			out[k] = id
			cur = id
		}
	}
	return out, nil
}
