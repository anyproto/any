package markdown

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/editor"
)

// Block is one rendered markdown block as returned by List. Mirrors
// the old shape but reads through the editor_blocks dataset, so the id
// is now the block record's stable id (auto-derived from change CID
// on first create) rather than a position-bearing lexid.
type Block struct {
	Id   string
	Text string
}

// SetResult bundles the per-call summary returned by Set, so callers
// that want to log / surface what changed can inspect it without
// re-running the diff.
type SetResult struct {
	// Inserted is the id of every newly created block, in document
	// order.
	Inserted []string
	// Updated is the id of every block whose fields were replaced.
	Updated []string
	// Deleted is the id of every block that was tombstoned.
	Deleted []string
	// Unchanged is the count of blocks that were kept verbatim — no
	// DB write touched them.
	Unchanged int
}

// Set replaces the markdown content of objectId with content,
// operating over the editor_blocks dataset.
//
// Pipeline:
//
//  1. Split content into raw markdown blocks, then ParseBlock each
//     into a typed ParsedBlock {type, style, text}.
//  2. List existing top-level editor_blocks records in pos order.
//  3. Diff old-vs-new by their canonical-rendered string. Matched
//     pairs become Update (only fields that changed land as $set);
//     leftover new entries become Insert; leftover old ones become
//     Delete.
//  4. Allocate nav.pos lexids for new inserts so they slot between
//     their kept neighbours in document order.
//  5. Submit one ModifyBatch (creates + updates) followed by one
//     DeleteBatch (tombstones). Same two-write split the old md_blocks
//     path used.
//
// The two writes are NOT a single atomic any-sync change. Because the
// CRDT resolves concurrent update-vs-delete as delete-wins, the
// observable end state is correct regardless of partial-failure
// retry, but a reader that catches the gap between (1) and (2) sees
// a transient state with both old-deleted and new-created blocks.
//
// content is stored as INLINE markdown inside each block's `text`
// field — no full-block markdown bytes survive on disk. Empty
// content tombstones every existing top-level block.
func Set(ctx context.Context, sp space.Space, objectId, content string) (SetResult, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: list existing: %w", err)
	}

	oldRendered := make([]string, len(existing))
	for i, b := range existing {
		oldRendered[i] = RenderBlock(ParsedBlock{
			Type:  b.Type,
			Style: b.Style,
			Text:  b.Text,
		})
	}

	rawNew := Split(content)
	newParsed := make([]ParsedBlock, len(rawNew))
	newRendered := make([]string, len(rawNew))
	for i, raw := range rawNew {
		newParsed[i] = ParseBlock(raw)
		newRendered[i] = RenderBlock(newParsed[i])
	}

	plan := Diff(oldRendered, newRendered)

	// Allocate per-insert nav.pos lexids. neighbouring positions come
	// from the existing blocks' pos values (or the kept entries to
	// the left/right of an insert run).
	posByNewIdx, err := allocateInsertPositions(existing, plan)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: allocate pos: %w", err)
	}

	var (
		records []space.RecordModify
		result  SetResult
	)
	for i, op := range plan.NewSeq {
		switch op.Kind {
		case OpKeep:
			result.Unchanged++
			continue
		case OpInsert:
			pos := posByNewIdx[i]
			records = append(records, buildCreateRecord(newParsed[i], pos))
			// We don't yet know the new block's id (SDK auto-derives
			// from the change CID at apply time). The caller's
			// Inserted slice will get filled in below from
			// ModifyResult.RecordIds, aligned to records[]
			// position-by-position.
			result.Inserted = append(result.Inserted, "")
		case OpUpdate:
			old := existing[op.OldIdx]
			records = append(records, buildUpdateRecord(old, newParsed[i]))
			result.Updated = append(result.Updated, old.Id)
		}
	}

	if len(records) > 0 {
		res, err := sp.Modify(ctx, space.ModifyBatch{
			ObjectId: objectId,
			Dataset:  editor.Dataset,
			Records:  records,
		})
		if err != nil {
			return result, fmt.Errorf("markdown: Set: modify: %w", err)
		}
		if len(res.Rejections) > 0 {
			return result, fmt.Errorf("markdown: Set: rejected: %s", res.Rejections[0].Reason)
		}
		// Fill in the auto-derived ids for the Inserted slots, aligned
		// to records[] by position. result.Inserted's slot-per-insert
		// shape was already populated above.
		insertIdx := 0
		for i, rec := range records {
			if rec.Id == "" {
				if i < len(res.RecordIds) {
					for insertIdx < len(result.Inserted) && result.Inserted[insertIdx] != "" {
						insertIdx++
					}
					if insertIdx < len(result.Inserted) {
						result.Inserted[insertIdx] = res.RecordIds[i]
						insertIdx++
					}
				}
			}
		}
	}

	if len(plan.Deletes) > 0 {
		ids := make([]string, len(plan.Deletes))
		for i, oldIdx := range plan.Deletes {
			ids[i] = existing[oldIdx].Id
		}
		result.Deleted = ids
		if _, err := sp.Delete(ctx, space.DeleteBatch{
			ObjectId:  objectId,
			Dataset:   editor.Dataset,
			RecordIds: ids,
		}); err != nil {
			return result, fmt.Errorf("markdown: Set: delete tombstones: %w", err)
		}
	}
	return result, nil
}

// Get returns the full markdown content of objectId by rendering each
// top-level block and joining with "\n\n". Round-trip is canonical:
// blank lines between blocks are always exactly one, and block-type
// canonicalisation (setext → ATX, `+` → `-`) applies the same way as
// on Set.
func Get(ctx context.Context, sp space.Space, objectId string) (string, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return "", err
	}
	rendered := make([]string, len(existing))
	for i, b := range existing {
		rendered[i] = RenderBlock(ParsedBlock{
			Type:  b.Type,
			Style: b.Style,
			Text:  b.Text,
		})
	}
	return Join(rendered), nil
}

// List returns the current top-level blocks of objectId in document
// (nav.pos ascending) order, with the raw text field. Provided for
// callers that want the block ids alongside their text without
// running ParseBlock/RenderBlock.
func List(ctx context.Context, sp space.Space, objectId string) ([]Block, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0, len(existing))
	for _, b := range existing {
		out = append(out, Block{
			Id: b.Id,
			Text: RenderBlock(ParsedBlock{
				Type:  b.Type,
				Style: b.Style,
				Text:  b.Text,
			}),
		})
	}
	return out, nil
}

// --- helpers ---------------------------------------------------------------

type existingBlock struct {
	Id    string
	Type  string
	Style map[string]any
	Text  string
	Pos   string
}

// listTopLevel returns the object's top-level editor_blocks
// (nav.parentId == "") sorted by nav.pos ascending. The markdown path
// stays flat: nested blocks (children of list items, for example)
// live under their parents but the markdown round-trip only walks the
// top level. Mirrors today's md_blocks behaviour.
func listTopLevel(ctx context.Context, sp space.Space, objectId string) ([]existingBlock, error) {
	all, err := editor.List(ctx, sp, objectId)
	if err != nil {
		return nil, err
	}
	out := make([]existingBlock, 0, len(all))
	for _, b := range all {
		if b.Nav.ParentId != editor.RootParentId {
			continue
		}
		out = append(out, existingBlock{
			Id:    b.Id,
			Type:  b.Type,
			Style: b.Style,
			Text:  b.Text,
			Pos:   b.Nav.Pos,
		})
	}
	return out, nil
}

// buildCreateRecord assembles the RecordModify for an Insert op. Id
// is empty so the SDK derives one from the change CID; payload is
// the full block fields.
func buildCreateRecord(p ParsedBlock, pos string) space.RecordModify {
	payload := map[string]any{
		editor.FieldType: p.Type,
		editor.FieldNav: map[string]any{
			editor.NavParentId: editor.RootParentId,
			editor.NavPos:      pos,
		},
	}
	if p.Text != "" {
		payload[editor.FieldText] = p.Text
	}
	if len(p.Style) > 0 {
		payload[editor.FieldStyle] = p.Style
	}
	return space.RecordModify{
		Id:     "",
		Upsert: true,
		Ops: []space.Op{{
			Type:  space.OpSet,
			Path:  "",
			Value: payload,
		}},
	}
}

// buildUpdateRecord emits one $set op per changed field. text and
// type are always set; style replaces whole-cloth when the new value
// differs (no per-style-sub-key diffing, which keeps the diff
// boundary aligned with what Update means at the markdown layer).
func buildUpdateRecord(old existingBlock, p ParsedBlock) space.RecordModify {
	var ops []space.Op
	if old.Type != p.Type {
		ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldType, Value: p.Type})
	}
	if old.Text != p.Text {
		ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldText, Value: p.Text})
	}
	if !styleEqual(old.Style, p.Style) {
		if p.Style == nil {
			ops = append(ops, space.Op{Type: space.OpUnset, Path: editor.FieldStyle})
		} else {
			ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldStyle, Value: p.Style})
		}
	}
	if len(ops) == 0 {
		// The diff said Update but the canonical render didn't
		// distinguish — touch text to bump _ver. Should be rare.
		ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldText, Value: p.Text})
	}
	return space.RecordModify{
		Id:  old.Id,
		Ops: ops,
	}
}

func styleEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !scalarEqual(va, vb) {
			return false
		}
	}
	return true
}

// scalarEqual compares two style values for equality. Numbers can
// arrive as int / int64 / float64 (the JSON decoder picks float64;
// the handler reads int from anyenc). Compare numerically rather
// than relying on Go's type-strict ==.
func scalarEqual(a, b any) bool {
	switch ax := a.(type) {
	case bool:
		bx, ok := b.(bool)
		return ok && ax == bx
	case string:
		bx, ok := b.(string)
		return ok && ax == bx
	case int:
		return numEqual(float64(ax), b)
	case int64:
		return numEqual(float64(ax), b)
	case float64:
		return numEqual(ax, b)
	}
	return a == b
}

func numEqual(a float64, b any) bool {
	switch bx := b.(type) {
	case int:
		return a == float64(bx)
	case int64:
		return a == float64(bx)
	case float64:
		return a == bx
	}
	return false
}

// allocateInsertPositions returns a map from new-sequence index to
// the allocated nav.pos lexid for each Insert op. Run-aware: a
// contiguous run of inserts shares its left/right neighbours, so the
// allocator produces strictly-monotonic ids inside the run.
//
// Neighbours come from kept (Keep) / matched (Update) entries that
// already have stable pos values. Pure-insert runs at the head/tail
// (no kept neighbour on that side) use empty as the boundary, which
// AllocateRun maps to Middle()-based seeding.
func allocateInsertPositions(existing []existingBlock, plan DiffResult) (map[int]string, error) {
	out := map[int]string{}
	if len(plan.NewSeq) == 0 {
		return out, nil
	}

	posByNewIdx := make([]string, len(plan.NewSeq))
	for i, op := range plan.NewSeq {
		if op.Kind == OpKeep || op.Kind == OpUpdate {
			if op.OldIdx >= 0 && op.OldIdx < len(existing) {
				posByNewIdx[i] = existing[op.OldIdx].Pos
			}
		}
	}

	i := 0
	for i < len(plan.NewSeq) {
		if plan.NewSeq[i].Kind != OpInsert {
			i++
			continue
		}
		j := i
		for j < len(plan.NewSeq) && plan.NewSeq[j].Kind == OpInsert {
			j++
		}
		var prev, next string
		if i > 0 {
			prev = posByNewIdx[i-1]
		}
		if j < len(plan.NewSeq) {
			next = posByNewIdx[j]
		}
		runLen := j - i
		ids, err := editor.AllocateRun(prev, next, runLen)
		if err != nil {
			return nil, fmt.Errorf("allocateInsertPositions [%d,%d): %w", i, j, err)
		}
		for k := 0; k < runLen; k++ {
			out[i+k] = ids[k]
			posByNewIdx[i+k] = ids[k]
		}
		i = j
	}
	return out, nil
}
