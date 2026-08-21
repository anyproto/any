package markdown

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/editor"
)

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
// content tombstones every existing top-level block. Blank lines
// beyond the one separating two blocks become empty paragraph
// records, so a document's vertical spacing survives the round trip
// (see Split).
func Set(ctx context.Context, sp space.Space, objectId, content string) (SetResult, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: list existing: %w", err)
	}
	return applyDiff(ctx, sp, objectId, existing, content)
}

// applyDiff is the shared write pipeline behind Set and EditContent:
// diff content against the already-listed existing blocks, then emit
// the create / update / delete ops. Taking `existing` (instead of
// listing internally) lets EditContent resolve matches and diff
// against the same listing.
func applyDiff(ctx context.Context, sp space.Space, objectId string, existing []existingBlock, content string) (SetResult, error) {
	oldRendered := renderExisting(existing)

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
		return SetResult{}, fmt.Errorf("markdown: apply: allocate pos: %w", err)
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
		if err := editor.EnsureType(ctx, sp, objectId); err != nil {
			return result, fmt.Errorf("markdown: apply: ensure type: %w", err)
		}
		res, err := sp.Modify(ctx, space.ModifyBatch{
			ObjectId: objectId,
			Dataset:  editor.Dataset,
			Records:  records,
		})
		if err != nil {
			return result, fmt.Errorf("markdown: apply: modify: %w", err)
		}
		if len(res.Rejections) > 0 {
			return result, fmt.Errorf("markdown: apply: rejected: %s", res.Rejections[0].Reason)
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
			return result, fmt.Errorf("markdown: apply: delete tombstones: %w", err)
		}
	}
	return result, nil
}

// Append parses content into blocks and appends them all after the
// object's current last top-level block, in a single ModifyBatch. It
// never reads the existing block bodies and never diffs — only the
// tail position is looked up (one indexed `-nav.pos` query via
// editor.MaxPos) — so the cost is O(appended content), independent of
// how large the document already is.
//
// This is the append-only fast path for whole-document round-trips:
// callers like the agent debug-log collector grow a page turn-by-turn
// and would otherwise pay Set's O(document) GET+diff on every append,
// making a full run O(N²) in page size. Append makes each call O(chunk)
// and the run O(N).
//
// Semantics differ from Set in two ways the caller must accept:
//   - No diffing. Append is purely additive: every parsed block becomes
//     a new record. It cannot update or delete existing blocks, and it
//     will happily create a block identical to an existing one.
//   - No separator control. A fragment is positioned by the append
//     itself: its blocks simply follow the current last one, and
//     blank lines wrapping the fragment are framing, not content, so
//     Split's leading/trailing empty paragraphs are dropped here.
//     Empty paragraphs BETWEEN blocks of the fragment are kept, as
//     they are for Set.
//
// Empty (or blank-only) content is a no-op that returns a zero result.
func Append(ctx context.Context, sp space.Space, objectId, content string) (SetResult, error) {
	rawNew := trimEdgeEmpties(Split(content))
	if len(rawNew) == 0 {
		return SetResult{}, nil
	}

	maxPos, err := editor.MaxPos(ctx, sp, objectId, editor.RootParentId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: max pos: %w", err)
	}
	positions, err := editor.AllocateRun(maxPos, "", len(rawNew))
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: allocate pos: %w", err)
	}

	records := make([]space.RecordModify, len(rawNew))
	for i, raw := range rawNew {
		records[i] = buildCreateRecord(ParseBlock(raw), positions[i])
	}

	// Attach the editor type before the membership-gated editor_blocks
	// write — the append fast-path skips Set's full-doc read, so it must
	// ensure type membership itself or the SDK rejects a first write to a
	// fresh object ("object does not implement type (editor)").
	if err := editor.EnsureType(ctx, sp, objectId); err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: ensure type: %w", err)
	}
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  editor.Dataset,
		Records:  records,
	})
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return SetResult{}, fmt.Errorf("markdown: Append: rejected: %s", res.Rejections[0].Reason)
	}

	result := SetResult{Inserted: make([]string, 0, len(records))}
	for i := range records {
		if i < len(res.RecordIds) {
			result.Inserted = append(result.Inserted, res.RecordIds[i])
		}
	}
	return result, nil
}

// Get returns the full markdown content of objectId by rendering each
// top-level block and joining with "\n\n". Round-trip is canonical:
// blank lines between two content blocks are exactly one plus one per
// empty paragraph between them, and block-type canonicalisation
// (setext → ATX, `+` → `-`) applies the same way as on Set.
func Get(ctx context.Context, sp space.Space, objectId string) (string, error) {
	existing, err := listTopLevel(ctx, sp, objectId)
	if err != nil {
		return "", err
	}
	return Join(renderExisting(existing)), nil
}

// trimEdgeEmpties drops leading and trailing empty entries, keeping
// the ones between content blocks.
func trimEdgeEmpties(blocks []string) []string {
	start := 0
	for start < len(blocks) && blocks[start] == "" {
		start++
	}
	end := len(blocks)
	for end > start && blocks[end-1] == "" {
		end--
	}
	return blocks[start:end]
}

// renderExisting renders each listed block to its canonical markdown
// bytes, in document order.
func renderExisting(existing []existingBlock) []string {
	rendered := make([]string, len(existing))
	for i, b := range existing {
		rendered[i] = RenderBlock(ParsedBlock{
			Type:  b.Type,
			Style: b.Style,
			Text:  b.Text,
		})
	}
	return rendered
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
