package index

import (
	"context"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"
)

// deletedAtField is the SDK's tombstone marker (crdt.DeletedAtField).
// Duplicated here for the same reason AddSeqField is.
const deletedAtField = "_deletedAt"

// addSeqPath is the static path of the cursor-window filter (filters
// are built with the typed any-store query package — never with
// map/JSON literals — and static pieces are built once).
var addSeqPath = []string{AddSeqField}

// RecordsSince is the shared windowed streamer every chunker uses. It
// chains the deletion opt-in (Projection{IncludeDeleted: true}), the
// cursor window (Filter {"_addSeq": {"$gt": since}}), and ascending
// AddSeq order onto the supplied query, then iterates and yields each
// record paired with its parsed AddSeq.
//
// Projection{IncludeDeleted: true} is what makes tombstones visible —
// without it the SDK's find path skips deleted rows, so deletions would
// never reach the indexer. The $gt window mirrors the SDK's own
// ChangedSince contract: rows never stamped with _addSeq (pre-branch
// writes) sort below any since >= 0 and are excluded — "index from the
// next change."
//
// A yield error stops iteration and is returned after the iterator is
// closed.
func RecordsSince(ctx context.Context, q space.Query, since uint64, yield func(rec *anyenc.Value, addSeq uint64) error) error {
	it, err := q.
		Projection(space.ProjectionOpts{IncludeDeleted: true}).
		Filter(query.Key{Path: addSeqPath, Filter: query.NewComp(query.CompOpGt, since)}).
		Sort(AddSeqField).
		Iter(ctx)
	if err != nil {
		return fmt.Errorf("index: RecordsSince: iter: %w", err)
	}
	defer it.Close()

	for it.Next() {
		doc, err := it.Doc()
		if err != nil {
			return fmt.Errorf("index: RecordsSince: doc: %w", err)
		}
		if doc == nil {
			continue
		}
		seq := uint64(doc.GetInt(AddSeqField))
		if err := yield(doc, seq); err != nil {
			return err
		}
	}
	return it.Err()
}

// IsDeleted reports whether rec is a tombstone — the SDK wipes content
// and sets _deletedAt on delete, preserving _ver / _traces / _addSeq.
func IsDeleted(rec *anyenc.Value) bool {
	return rec != nil && rec.Get(deletedAtField) != nil
}
