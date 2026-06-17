package agentmem

import (
	"context"
	"strings"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker indexes agent_memory_items as one search doc per item (scope
// "agent"), gated on the agent_memory type. Memory is the agent's recall
// surface, so it participates in hybrid search like chat and editor.
//
// Per-record (not coalesced): each memory item is an independently
// retrievable, filterable unit (by category / recency / confidence), so
// merging items into windows would both lose that addressability and the
// per-item filtering the recall layer needs. Per-record also means the
// SDK's _applySeq delta makes indexing incremental for free — only
// changed items re-stream — and the indexer's content-hash check skips
// re-embedding an item that re-streamed on a metadata-only bump
// (accessCount on recall, confidence/salience/edges on evolution) without
// its indexed text changing.
type Chunker struct{}

// NewChunker constructs the memory chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Dataset() string { return Dataset }

// TypeId gates the chunker on agent_memory membership: the indexer evicts
// objectId:agent_memory_items: when the type is detached.
func (Chunker) TypeId() string { return TypeId }

// ChunksSince streams the brain object's memory items past the cursor,
// ascending by ApplySeq. Deleted items yield a tombstone (Data "").
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, Dataset)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeAgent,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = memoryData(rec)
		}
		return yield(entry)
	})
}

// memoryData builds an item's indexable text from its semantic + lexical
// fields — context and body (the meaning) plus category, keywords,
// entities and tags (lexical handles for FTS). Numeric/structural fields
// (confidence, salience, accessCount, edges, timestamps) are excluded by
// design: bumping them leaves this text — and thus the content hash —
// unchanged, so the indexer skips re-embedding.
func memoryData(rec *anyenc.Value) string {
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	add(string(rec.GetStringBytes(FieldContext)))
	add(string(rec.GetStringBytes(FieldBody)))
	add(string(rec.GetStringBytes(FieldCategory)))
	add(joinStrings(rec.GetArray(FieldKeywords)))
	add(joinStrings(rec.GetArray(FieldEntities)))
	add(joinStrings(rec.GetArray(FieldTags)))
	return strings.Join(parts, "\n")
}

// joinStrings space-joins the string elements of an anyenc array,
// skipping non-strings. Empty for a nil/empty array.
func joinStrings(arr []*anyenc.Value) string {
	if len(arr) == 0 {
		return ""
	}
	out := make([]string, 0, len(arr))
	for _, el := range arr {
		if el.Type() == anyenc.TypeString {
			if s := strings.TrimSpace(string(el.GetStringBytes())); s != "" {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, " ")
}
