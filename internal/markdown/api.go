package markdown

import (
	"context"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"
)

// listFilter restricts List/Get to records that actually carry the
// block payload. Pre-parsed once via query.MustParseCondition so we
// pass an already-built query.Filter to space.Query.Filter, avoiding
// a re-parse on every call. Defensive — under normal Set traffic
// every record has FieldText, but the dataset's handler does not
// enforce it, so a malformed write from elsewhere could otherwise
// surface as an empty Block.
var listFilter = query.MustParseCondition(map[string]any{
	FieldText: map[string]any{"$exists": true},
})

// Block is one stored markdown block as returned by List.
type Block struct {
	// Id is the record's lexid. Sort ascending to recover the
	// document order; insert between two ids to add a new block at
	// that position (see lexid.NextBefore).
	Id string
	// Text is the raw markdown source for this block — exactly what
	// went in via Set. No normalisation, no canonicalisation.
	Text string
}

// SetResult bundles the per-call summary returned by Set, so callers
// that want to log / surface what changed can inspect it without
// re-running the diff.
type SetResult struct {
	// Inserted is the lexid of every newly created block, in
	// document order.
	Inserted []string
	// Updated is the lexid of every block whose text was replaced.
	Updated []string
	// Deleted is the lexid of every block that was tombstoned.
	Deleted []string
	// Unchanged is the count of blocks that were kept verbatim — no
	// DB write touched them.
	Unchanged int
}

// Set replaces the markdown content of objectId with content,
// computing the per-block ops needed to get there from whatever is
// currently stored.
//
// Two writes are issued, in order:
//
//  1. space.Modify with one upsert RecordModify per kept-with-text-
//     change (Update) and per new block (Insert). Set ops carry the
//     full block text under FieldText. Records that don't change
//     are skipped — no `_ver` churn.
//  2. space.Delete with the lexids of blocks that disappeared.
//
// They are NOT a single atomic any-sync change. Because the CRDT
// resolves concurrent update-vs-delete as delete-wins, the
// observable end state is correct regardless of partial-failure
// retry, but a reader that catches the gap between (1) and (2) sees
// a transient state with both old-deleted and new-created blocks.
//
// content is stored verbatim — no markdown normalisation. Empty
// content tombstones every existing block.
func Set(ctx context.Context, sp space.Space, objectId, content string) (SetResult, error) {
	existing, err := List(ctx, sp, objectId)
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: list existing: %w", err)
	}
	oldTexts := make([]string, len(existing))
	oldLexids := make([]string, len(existing))
	for i, b := range existing {
		oldTexts[i] = b.Text
		oldLexids[i] = b.Id
	}

	newBlocks := Split(content)
	diff := Diff(oldTexts, newBlocks)
	newLexids, err := AllocateLexids(oldLexids, diff, "")
	if err != nil {
		return SetResult{}, fmt.Errorf("markdown: Set: allocate lexids: %w", err)
	}

	var (
		upserts []space.RecordModify
		result  SetResult
	)
	for i, op := range diff.NewSeq {
		switch op.Kind {
		case OpKeep:
			result.Unchanged++
			continue
		case OpInsert:
			result.Inserted = append(result.Inserted, newLexids[i])
		case OpUpdate:
			result.Updated = append(result.Updated, newLexids[i])
		}
		upserts = append(upserts, space.RecordModify{
			Id:     newLexids[i],
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  FieldText,
				Value: newBlocks[i],
			}},
		})
	}
	if len(upserts) > 0 {
		_, err := sp.Modify(ctx, space.ModifyBatch{
			ObjectId: objectId,
			Dataset:  Dataset,
			Records:  upserts,
		})
		if err != nil {
			return result, fmt.Errorf("markdown: Set: modify upserts: %w", err)
		}
	}

	if len(diff.Deletes) > 0 {
		ids := make([]string, len(diff.Deletes))
		for i, oldIdx := range diff.Deletes {
			ids[i] = oldLexids[oldIdx]
		}
		result.Deleted = ids
		_, err := sp.Delete(ctx, space.DeleteBatch{
			ObjectId:  objectId,
			Dataset:   Dataset,
			RecordIds: ids,
		})
		if err != nil {
			return result, fmt.Errorf("markdown: Set: delete tombstones: %w", err)
		}
	}
	return result, nil
}

// List returns the current blocks of objectId in document (lexid
// ascending) order. Empty result when the object has no markdown
// blocks yet.
func List(ctx context.Context, sp space.Space, objectId string) ([]Block, error) {
	q := sp.Query(objectId, Dataset).Filter(listFilter).Sort("id")
	docs, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("markdown: List: query: %w", err)
	}
	out := make([]Block, 0, len(docs))
	for _, d := range docs {
		idVal := d.Get("id")
		if idVal == nil || idVal.Type() != anyenc.TypeString {
			continue
		}
		textVal := d.Get(FieldText)
		text := ""
		if textVal != nil && textVal.Type() == anyenc.TypeString {
			text = string(textVal.GetStringBytes())
		}
		out = append(out, Block{
			Id:   string(idVal.GetStringBytes()),
			Text: text,
		})
	}
	return out, nil
}

// Get returns the full markdown content of objectId by joining the
// stored blocks. Round-trip is canonical: blank lines between blocks
// are always exactly one.
func Get(ctx context.Context, sp space.Space, objectId string) (string, error) {
	blocks, err := List(ctx, sp, objectId)
	if err != nil {
		return "", err
	}
	texts := make([]string, len(blocks))
	for i, b := range blocks {
		texts[i] = b.Text
	}
	return Join(texts), nil
}
