package editor

import (
	"context"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Chunker indexes editor_blocks as COALESCED WINDOWS under scope "basic":
// consecutive blocks (in document order) are grouped into ~1.5 KB windows
// broken at headings (see Windows), one index doc per window. This
// replaces the old one-doc-per-block shape, whose tiny chunks (mean ~98
// chars, half under 50) hurt both vector recall and BM25 length
// normalization (chunker-hybrid-search-report § 3).
//
// Because a window spans several records, the chunker is a Reconciler: it
// rebuilds the object's windows wholesale (prefix-delete + re-upsert)
// rather than streaming per-record deltas — a window's text needs sibling
// blocks below the cursor, and a deleted block's position is wiped from
// its tombstone, so the affected window can't be found incrementally.
// Cost: an edit re-reads (and re-embeds) the object's blocks — O(doc) per
// change. Acceptable for human-edited docs; the O(N) append-loop case
// (agent debug logs) is no longer indexed (see internal/server/sdk.go).
type Chunker struct{}

// NewChunker constructs the editor blocks chunker.
func NewChunker() *Chunker { return &Chunker{} }

func (Chunker) Dataset() string { return Dataset }

// TypeId gates the chunker on editor-type membership: the indexer evicts
// objectId:editor_blocks: when the type is detached.
func (Chunker) TypeId() string { return TypeId }

// Reconcile returns the object's current window set; the indexer diffs it
// against the stored docs (by content hash) and applies the minimal
// delta, preserving unchanged windows' vectors.
func (Chunker) Reconcile(ctx context.Context, sp space.Space, objectId string, _ uint64) ([]index.IndexEntry, error) {
	return windowEntries(ctx, sp, objectId)
}

// ChunksSince satisfies the Chunker interface. The indexer routes editor
// through Reconcile (above), so this is not used in the pipeline; it emits
// the current window set as upserts (no stale-eviction — that is
// Reconcile's PrefixDelete) for completeness and ad-hoc callers.
func (Chunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, _ uint64, yield func(index.IndexEntry) error) error {
	entries, err := windowEntries(ctx, sp, objectId)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := yield(e); err != nil {
			return err
		}
	}
	return nil
}

// windowEntries reads the object's live blocks in document order, coalesces
// them into windows, and renders one IndexEntry per non-empty window.
func windowEntries(ctx context.Context, sp space.Space, objectId string) ([]index.IndexEntry, error) {
	blocks, err := List(ctx, sp, objectId)
	if err != nil {
		return nil, err
	}
	wins := Windows(blocks)
	entries := make([]index.IndexEntry, 0, len(wins))
	for _, w := range wins {
		entries = append(entries, index.IndexEntry{
			Scope:    index.ScopeBasic,
			ObjectId: objectId,
			Dataset:  Dataset,
			RecordId: windowRecordPrefix + w.AnchorId,
			Data:     w.Text,
		})
	}
	return entries, nil
}
