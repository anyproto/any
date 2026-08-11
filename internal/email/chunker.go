package email

import (
	"context"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker streams email_messages records as index entries under scope
// "email". One entry per message; Data is subject + bodyText (subject
// doubles as Title for the BM25F boost) — addressing, labels, and the
// attachment manifest are deliberately excluded. Deleted messages
// (and empty ones) yield Data "".
type Chunker struct{}

// NewChunker constructs the email messages chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Dataset() string { return Dataset }

// TypeId gates the chunker on email-type membership: the indexer
// evicts objectId:email_messages: when the type is detached.
func (Chunker) TypeId() string { return TypeId }

// ChunksSince streams the object's messages past the cursor, ascending
// by ApplySeq. Deleted rows yield a tombstone (Data ""); live rows
// yield subject + body.
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, Dataset)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeEmail,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Title, entry.Data = messageData(rec)
		}
		return yield(entry)
	})
}

// messageData extracts the indexable text of a message: Title is the
// subject (BM25F boost), Data is subject + bodyText so the Title terms
// also appear in the body field. Empty on tombstones handled by the
// caller; empty subject/body degrade gracefully.
func messageData(rec *anyenc.Value) (title, data string) {
	if rec == nil {
		return "", ""
	}
	subject := string(rec.GetStringBytes(FieldSubject))
	body := string(rec.GetStringBytes(FieldBodyText))
	data = subject
	if body != "" {
		if data != "" {
			data += "\n"
		}
		data += body
	}
	return subject, data
}
