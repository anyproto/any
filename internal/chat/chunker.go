package chat

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker streams chat_messages records as index entries under scope
// "chat". One entry per message; Data is the message `text` only —
// creator, reactions, and attachments are deliberately excluded in
// phase 1. Deleted messages (and empty-text messages) yield Data "".
type Chunker struct{}

// NewChunker constructs the chat messages chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Dataset() string { return Dataset }

// TypeId gates the chunker on chat-type membership: the indexer evicts
// objectId:chat_messages: when the type is detached.
func (Chunker) TypeId() string { return TypeId }

// ChunksSince streams the object's messages past the cursor, ascending
// by AddSeq. Deleted rows yield a tombstone (Data ""); live rows yield
// their text.
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, Dataset)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeChat,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: string(rec.GetStringBytes("id")),
			AddSeq:   seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = messageData(rec)
		}
		return yield(entry)
	})
}

// messageData extracts the indexable text of a message — the `text`
// field only. Empty when absent.
func messageData(rec *anyenc.Value) string {
	if rec == nil {
		return ""
	}
	return string(rec.GetStringBytes(FieldText))
}
