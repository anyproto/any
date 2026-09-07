package editor

import (
	"context"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// NewChunker constructs the editor module chunker: one chunker for
// every editor collection in a space — the canonical `editor_blocks`
// and each namespaced instance — reconciled per collection as
// coalesced windows (index.ModuleChunker + index.MultiReconciler).
func NewChunker() *index.ModuleChunker {
	return index.NewModuleChunker(Module, index.ModuleReconcile(windowEntries))
}

// windowEntries renders one editor collection's blocks as coalesced
// windows — the index unit for a document (Windows). Entries carry the
// collection as their dataset, so doc ids stay per collection. Each
// window also reports the links of its member blocks, one edge per
// (block, target), under the block's own id (docs/13-index.md § Links).
func windowEntries(ctx context.Context, sp space.Space, objectId, collection string) ([]index.IndexEntry, error) {
	blocks, err := List(ctx, sp, objectId, collection)
	if err != nil {
		return nil, err
	}
	links := make(map[string][]index.LinkEntry, len(blocks))
	for _, b := range blocks {
		if b.Text == "" {
			continue
		}
		if ls := BlockLinks(sp.Id(), objectId, collection, b); len(ls) > 0 {
			links[b.Id] = ls
		}
	}
	wins := Windows(blocks)
	entries := make([]index.IndexEntry, 0, len(wins))
	for _, w := range wins {
		e := index.IndexEntry{
			Scope:    index.ScopeBasic,
			ObjectId: objectId,
			Dataset:  collection,
			RecordId: windowRecordPrefix + w.AnchorId,
			Data:     w.Text,
			Title:    w.Title, // heading — BM25F boosted (also in Data)
		}
		for _, id := range w.BlockIds {
			e.Links = append(e.Links, links[id]...)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
