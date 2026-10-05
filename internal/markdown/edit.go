package markdown

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"
)

// EditContent applies targeted replacements to the rendered markdown
// of objectId — the surgical alternative to Set for callers that
// know the text they want changed but not the block ids. Edits
// resolve against the current rendering and the result goes through
// Set's diff pipeline, so a one-block change lands as one record op
// and a stale quote fails (NoMatchError) instead of clobbering
// concurrent edits. All-or-nothing: any unresolvable edit aborts
// before any write; byte-identical output is a no-op. The write is
// conditional on the read the edits resolved against, like Set's, is
// redone against a fresh read when another writer landed in between,
// and is written unconditionally when other writers keep landing first.
func EditContent(ctx context.Context, sp space.Space, objectId, collection string, edits []Edit) (SetResult, error) {
	gen, err := sp.Changes().Generation(ctx)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Edit: generation: %w", err)
	}
	return writeAgainstRead(ctx, sp, objectId, collection, gen, true, func(read Document, existing []existingBlock, ifUnchangedSince *uint64) (SetResult, error) {
		edited, err := applyEdits(read.Content, edits)
		if err != nil {
			return SetResult{}, fmt.Errorf("markdown: Edit: %w", err)
		}
		parsed, rendered, err := parseContent(edited)
		if err != nil {
			return SetResult{}, fmt.Errorf("markdown: Edit: %w", err)
		}
		res, _, err := applyDiff(ctx, sp, objectId, collection, existing, ifUnchangedSince, parsed, rendered)
		return res, err
	})
}
