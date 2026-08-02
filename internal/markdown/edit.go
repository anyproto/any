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
// before any write; byte-identical output is a no-op.
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
