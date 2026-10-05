package markdown

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

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
	// Version is the document version of the body as saved (see
	// Document). Set fills it; EditContent and Append leave it empty.
	Version string
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
//  5. Submit creates, updates and tombstones as one ModifyBatch — one
//     atomic any-sync change.
//
// content is stored as INLINE markdown inside each block's `text`
// field — no full-block markdown bytes survive on disk. Empty
// content tombstones every existing top-level block. Blank lines
// beyond the one separating two blocks become empty paragraph
// records, so a document's vertical spacing survives the round trip
// (see Split).
//
// A non-empty ifVersion makes the write conditional: unless the
// document is still at that version, Set writes nothing and returns a
// ConflictError carrying the current body and version.
//
// The diff is written as one change, conditional on the collection
// being unchanged since the read it was computed from, so no other
// writer — a markdown write, a …/blocks call, another device — lands
// in between; the reported Version is the saved body's. Without
// ifVersion, a write that lost that race is redone against the new
// state, and written unconditionally when other writers keep landing
// first (writeAgainstRead).
func Set(ctx context.Context, sp space.Space, objectId, collection, content, ifVersion string) (SetResult, error) {
	parsed, rendered, err := parseContent(content)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: %w", err)
	}
	gen, err := sp.Changes().Generation(ctx)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: generation: %w", err)
	}
	saved := Join(rendered)
	return writeAgainstRead(ctx, sp, objectId, collection, gen, ifVersion == "", func(read Document, existing []existingBlock, ifUnchangedSince *uint64) (SetResult, error) {
		if ifVersion != "" && ifVersion != read.Version {
			return SetResult{}, ConflictError{Current: read}
		}
		res, applied, err := applyDiff(ctx, sp, objectId, collection, existing, ifUnchangedSince, parsed, rendered)
		if err != nil {
			return SetResult{}, err
		}
		res.Version = read.Version
		if len(res.Inserted)+len(res.Updated)+len(res.Deleted) > 0 {
			res.Version = formatVersion(gen, applied, saved)
		}
		return res, nil
	})
}

// maxWriteAttempts bounds how often writeAgainstRead redoes a write
// that other writers keep beating.
const maxWriteAttempts = 3

// writeAgainstRead reads the document and runs write against that read,
// conditional on the collection being unchanged since it
// (ifUnchangedSince); a write that lost the race to another writer
// (space.ErrPreconditionFailed) is redone against a fresh read. When the
// writers keep winning, a caller that asked for no condition
// (unconditional) gets its last attempt written without one, as it
// asked; a conditional caller's write closure answers a moved read with
// a ConflictError.
func writeAgainstRead(ctx context.Context, sp space.Space, objectId, collection, gen string, unconditional bool, write func(read Document, existing []existingBlock, ifUnchangedSince *uint64) (SetResult, error)) (SetResult, error) {
	for attempt := 1; ; attempt++ {
		read, existing, seq, err := readDocument(ctx, sp, objectId, collection, gen)
		if err != nil {
			return SetResult{}, fmt.Errorf("markdown: list existing: %w", err)
		}
		ifUnchangedSince := &seq
		if unconditional && attempt == maxWriteAttempts {
			ifUnchangedSince = nil
		}
		res, err := write(read, existing, ifUnchangedSince)
		if !errors.Is(err, space.ErrPreconditionFailed) {
			return res, err
		}
		// Not reached in practice: an unconditional caller's last attempt
		// has no precondition, and a conditional caller's next read no
		// longer matches its ifVersion.
		if attempt == maxWriteAttempts {
			return res, err
		}
	}
}

// parseContent splits content into blocks and parses them (parseBlocks).
func parseContent(content string) ([]ParsedBlock, []string, error) {
	return parseBlocks(Split(content))
}

// parseBlocks parses raw markdown blocks, returning the parsed blocks
// and their canonical renderings, or a BlockTooLargeError.
func parseBlocks(raw []string) ([]ParsedBlock, []string, error) {
	parsed := make([]ParsedBlock, len(raw))
	rendered := make([]string, len(raw))
	for i, r := range raw {
		parsed[i] = ParseBlock(r)
		rendered[i] = RenderBlock(parsed[i])
	}
	if err := checkBlockSizes(parsed); err != nil {
		return nil, nil, err
	}
	return parsed, rendered, nil
}

// BlockTooLargeError: a block's text is over the editor's per-block cap.
// The markdown writes refuse it before writing anything; the editor
// would reject it mid-change and leave the rest of the save applied.
type BlockTooLargeError struct {
	Index int // the block's position in the parsed document
	Bytes int
}

func (e BlockTooLargeError) Error() string {
	return fmt.Sprintf("block %d: text is %d bytes, over the %d-byte cap", e.Index, e.Bytes, editor.MaxTextBytes)
}

func checkBlockSizes(parsed []ParsedBlock) error {
	for i, p := range parsed {
		if len(p.Text) > editor.MaxTextBytes {
			return BlockTooLargeError{Index: i, Bytes: len(p.Text)}
		}
	}
	return nil
}

// checkPositions refuses insert positions over the editor's nav.pos cap
// before writing: the editor would reject that insert mid-change.
func checkPositions(positions []string) error {
	for _, pos := range positions {
		if len(pos) > editor.MaxPosBytes {
			return fmt.Errorf("insert position is %d bytes, over the %d-byte cap", len(pos), editor.MaxPosBytes)
		}
	}
	return nil
}

// applyDiff is the shared write pipeline behind Set and EditContent:
// diff the parsed new document against the existing blocks and write the
// creates, updates and deletes as one change, conditional — when
// ifUnchangedSince is set — on the collection being unchanged since the
// read the blocks came from (ModifyBatch.IfUnchangedSince). A write that
// lost that race is space.ErrPreconditionFailed with nothing written.
// appliedSeq is the change's _applySeq, 0 when the diff wrote nothing.
func applyDiff(ctx context.Context, sp space.Space, objectId, collection string, existing []existingBlock, ifUnchangedSince *uint64, newParsed []ParsedBlock, newRendered []string) (result SetResult, appliedSeq uint64, err error) {
	oldRendered := renderExisting(existing)

	plan := Diff(oldRendered, newRendered)

	// Allocate per-insert nav.pos lexids. neighbouring positions come
	// from the existing blocks' pos values (or the kept entries to
	// the left/right of an insert run).
	posByNewIdx, err := allocateInsertPositions(existing, plan)
	if err != nil {
		return SetResult{}, 0, fmt.Errorf("markdown: apply: allocate pos: %w", err)
	}
	if err := checkPositions(slices.Collect(maps.Values(posByNewIdx))); err != nil {
		return SetResult{}, 0, fmt.Errorf("markdown: apply: %w", err)
	}

	var records []space.RecordModify
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
	// Nested blocks of a deleted block stay: the diff reads a moved block
	// as a delete plus a create, and their content is nothing the
	// markdown shows.
	for _, oldIdx := range plan.Deletes {
		id := existing[oldIdx].Id
		records = append(records, space.RecordModify{Id: id, Ops: []space.Op{{Type: space.OpDelete}}})
		result.Deleted = append(result.Deleted, id)
	}
	if len(records) == 0 {
		return result, 0, nil
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId:         objectId,
		Dataset:          collection,
		Records:          records,
		IfUnchangedSince: ifUnchangedSince,
	})
	if err != nil {
		return SetResult{}, 0, fmt.Errorf("markdown: apply: modify: %w", err)
	}
	// The block sizes and positions the editor validates are checked
	// before writing. With a precondition nothing else wrote the
	// collection since the read, so any rejection is a refusal those
	// checks missed. Without one (writeAgainstRead's last attempt), a
	// block deleted after the read absorbs its update — delete wins —
	// and the rest of the change has landed.
	for _, rej := range res.Rejections {
		if ifUnchangedSince == nil && errors.Is(rej.ReasonErr, space.ErrRecordDeleted) {
			result.Updated = slices.DeleteFunc(result.Updated, func(id string) bool { return id == rej.RecordId })
			continue
		}
		return SetResult{}, 0, fmt.Errorf("markdown: apply: rejected: %s", rej.Reason)
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
	return result, res.ApplySeq, nil
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
func Append(ctx context.Context, sp space.Space, objectId, collection, content string) (SetResult, error) {
	rawNew := trimEdgeEmpties(Split(content))
	if len(rawNew) == 0 {
		return SetResult{}, nil
	}
	parsed, _, err := parseBlocks(rawNew)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: %w", err)
	}

	maxPos, err := editor.MaxPos(ctx, sp, objectId, collection, editor.RootParentId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: max pos: %w", err)
	}
	positions, err := editor.AllocateRun(maxPos, "", len(rawNew))
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: allocate pos: %w", err)
	}
	if err := checkPositions(positions); err != nil {
		return SetResult{}, fmt.Errorf("markdown: Append: %w", err)
	}

	records := make([]space.RecordModify, len(parsed))
	for i, p := range parsed {
		records[i] = buildCreateRecord(p, positions[i])
	}

	// Attach the editor type before the membership-gated editor_blocks
	// write — the append fast-path skips Set's full-doc read, so it must
	// ensure type membership itself or the SDK rejects a first write to a
	// fresh object ("object does not implement type (editor)").
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  collection,
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
// top-level block and joining with "\n\n", plus the version it was read
// at. Round-trip is canonical: blank lines between two content blocks
// are exactly one plus one per empty paragraph between them, and
// block-type canonicalisation (setext → ATX, `+` → `-`) applies the
// same way as on Set.
func Get(ctx context.Context, sp space.Space, objectId, collection string) (Document, error) {
	gen, err := sp.Changes().Generation(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("markdown: Get: generation: %w", err)
	}
	doc, _, _, err := readDocument(ctx, sp, objectId, collection, gen)
	return doc, err
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
// (nav.parentId == "") sorted by nav.pos ascending, and the collection's
// highest _applySeq from the same read, nested blocks and tombstones
// included (editor.ListWithSeq). The markdown path stays flat: nested
// blocks (children of list items, for example) live under their
// parents but the markdown round-trip only walks the top level.
func listTopLevel(ctx context.Context, sp space.Space, objectId, collection string) ([]existingBlock, uint64, error) {
	all, seq, err := editor.ListWithSeq(ctx, sp, objectId, collection)
	if err != nil {
		return nil, 0, err
	}
	return topLevel(all), seq, nil
}

func topLevel(all []editor.Block) []existingBlock {
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
	return out
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

// markdownStyleKeys are the style keys the markdown expresses, per block
// type. An update owns these and nothing else: any other key set through
// …/blocks — a paragraph's level used as indentation, a color — survives
// a save that updates the block.
var markdownStyleKeys = map[string][]string{
	editor.TypeHeading:       {editor.StyleLevel},
	editor.TypeListItem:      {editor.StyleOrdered, editor.StyleNumber},
	editor.TypeCheckListItem: {editor.StyleChecked},
	editor.TypeCode:          {editor.StyleLang},
}

// buildUpdateRecord emits one op per changed field: $set on type and
// text when they differ, and per owned style key (the old and the new
// type's, plus any the parse produced), $set for one whose value changed
// and $unset for one the new block no longer carries.
func buildUpdateRecord(old existingBlock, p ParsedBlock) space.RecordModify {
	var ops []space.Op
	if old.Type != p.Type {
		ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldType, Value: p.Type})
	}
	if old.Text != p.Text {
		ops = append(ops, space.Op{Type: space.OpSet, Path: editor.FieldText, Value: p.Text})
	}
	owned := slices.Concat(markdownStyleKeys[old.Type], markdownStyleKeys[p.Type])
	for key := range p.Style {
		owned = append(owned, key)
	}
	slices.Sort(owned)
	for _, key := range slices.Compact(owned) {
		path := editor.FieldStyle + "." + key
		nv, inNew := p.Style[key]
		ov, inOld := old.Style[key]
		switch {
		case inNew && (!inOld || !scalarEqual(ov, nv)):
			ops = append(ops, space.Op{Type: space.OpSet, Path: path, Value: nv})
		case !inNew && inOld:
			ops = append(ops, space.Op{Type: space.OpUnset, Path: path})
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
