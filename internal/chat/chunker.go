package chat

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// NewChunker constructs the chat module chunker: chat_messages records
// as index entries under scope "chat", one entry per message, on every
// chat collection the space declares (the canonical one, chat being
// shared-only). Data is the message `text` only — creator, reactions,
// and attachments are deliberately excluded. Deleted messages (and
// empty-text messages) yield Data "".
func NewChunker() *index.ModuleChunker {
	return index.NewModuleChunker(Module, index.ModuleStream(streamMessages))
}

// streamMessages streams one chat collection's messages past the
// cursor, ascending by ApplySeq. Deleted rows yield a tombstone (Data
// ""); live rows yield their text.
func streamMessages(ctx context.Context, sp space.Space, objectId, collection string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, collection)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeChat,
			ObjectId: objectId,
			Dataset:  collection,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
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
