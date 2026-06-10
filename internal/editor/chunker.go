package editor

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker streams editor_blocks records as index entries under scope
// "basic". One entry per block; Data is the block's inline-markdown
// text. Deleted blocks (and blocks with empty text) yield Data "" — for
// a tombstone that means "remove from index"; for an empty live block it
// means "nothing to index" (the indexer treats both the same).
type Chunker struct{}

// NewChunker constructs the editor blocks chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Scope() string   { return index.ScopeBasic }
func (Chunker) Dataset() string { return Dataset }

// ChunksSince streams the object's blocks past the cursor, ascending by
// AddSeq. Deleted rows yield a tombstone (Data ""); live rows yield
// their text.
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, Dataset)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeBasic,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: string(rec.GetStringBytes("id")),
			AddSeq:   seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = blockData(rec)
		}
		return yield(entry)
	})
}

// blockData extracts the indexable text of a block — the inline-markdown
// `text` field. Empty when absent.
func blockData(rec *anyenc.Value) string {
	if rec == nil {
		return ""
	}
	return string(rec.GetStringBytes(FieldText))
}
