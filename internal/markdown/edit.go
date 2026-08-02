package markdown

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"
)

// EditContent applies targeted replacements to the rendered markdown
// of objectId — the surgical alternative to Set for callers that know
// the text they want changed but not the block ids.
//
// Pipeline: list top-level blocks once, render the canonical content
// from that listing, resolve every edit against it (exact match
// first, whole-line fuzzy fallback — see resolveEdits), splice the
// replacements, then hand the edited content to the same diff+write
// path Set uses. Because the match runs against the current server
// state, a stale quote fails with NoMatchError instead of clobbering
// concurrent edits elsewhere in the document, and an edit that only
// flips one block lands as a single-record update — block ids and
// untouched blocks stay stable.
//
// All-or-nothing: any unresolvable edit (NoMatchError,
// AmbiguousMatchError, OverlapError) aborts before any write. Edits
// that produce byte-identical content are a no-op returning only an
// Unchanged count — idempotent by design.
func EditContent(ctx context.Context, sp space.Space, objectId string, edits []Edit) (SetResult, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Edit: list existing: %w", err)
	}
	content := Join(renderExisting(existing))
	edited, err := applyEdits(content, edits)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Edit: %w", err)
	}
	return applyDiff(ctx, sp, objectId, existing, edited)
}
