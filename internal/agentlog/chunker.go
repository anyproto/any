package agentlog

import (
	"context"
	"strings"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// The agent conversation history (turns + chunks) enters the search
// index under scope "history" (ADR-006 §2 / docs/13-index.md). Both
// datasets live on the chat object and are gated on agent_log type
// membership. Two chunkers — one per dataset — so old history becomes
// semantically reachable without exact seqs (restoring the old
// chat_chunk-as-memory recall as a recall SCOPE, storage kept separate).

// TurnChunker streams agent_turns as history entries: Data is the
// conversational content (userText + replies), Title boosts the user's
// text (the question is the strongest recall anchor). Deleted/empty
// rows yield Data "".
type TurnChunker struct{}

func NewTurnChunker() *TurnChunker { return &TurnChunker{} }

func (TurnChunker) Dataset() string { return DatasetTurns }
func (TurnChunker) TypeId() string  { return TypeId }

func (TurnChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, DatasetTurns)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeHistory,
			ObjectId: objectId,
			Dataset:  DatasetTurns,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = turnData(rec)
			entry.Title = strings.TrimSpace(string(rec.GetStringBytes(FieldUserText)))
		}
		return yield(entry)
	})
}

// turnData is the indexable text of a turn: the user's text plus the
// agent's reply bubbles. Think/effects are excluded — narration and
// one-liners are noise for recall; the raw record stays reachable by
// seq for drill-down.
func turnData(rec *anyenc.Value) string {
	var parts []string
	if s := strings.TrimSpace(string(rec.GetStringBytes(FieldUserText))); s != "" {
		parts = append(parts, s)
	}
	if s := joinStrings(rec.GetArray(FieldReplies)); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

// ChunkChunker streams agent_chunks summaries as history entries: Data
// is the summary (already dense). No separate Title — the whole record
// is the summary.
type ChunkChunker struct{}

func NewChunkChunker() *ChunkChunker { return &ChunkChunker{} }

func (ChunkChunker) Dataset() string { return DatasetChunks }
func (ChunkChunker) TypeId() string  { return TypeId }

func (ChunkChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, DatasetChunks)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    index.ScopeHistory,
			ObjectId: objectId,
			Dataset:  DatasetChunks,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = strings.TrimSpace(string(rec.GetStringBytes(FieldSummary)))
		}
		return yield(entry)
	})
}

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
