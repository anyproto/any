package enricheddata

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker streams enriched_data records into the search index under scope
// "basic" — sourced enrichment facts are real, searchable knowledge. One entry
// per record; Data is the `text` field only. Deleted/empty rows yield Data ""
// (record-level eviction). Mirrors chat.Chunker.
type Chunker struct{}

// NewChunker constructs the enriched_data chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Dataset() string { return Dataset }

// TypeId gates the chunker on enriched_data-type membership: the indexer evicts
// objectId:enriched_data: when the type is detached.
func (Chunker) TypeId() string { return TypeId }

// ChunksSince streams the object's enrichment records past the cursor,
// ascending by ApplySeq. Deleted rows yield a tombstone (Data ""); live rows
// yield their text.
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, Dataset)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeBasic,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = string(rec.GetStringBytes(FieldText))
		}
		return yield(entry)
	})
}
