//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/indexer"
)

// countingEmbedder wraps fakeEmbedder and counts how many doc texts were
// embedded — lets a test prove that a metadata-only bump does NOT trigger
// re-embedding (the content-hash skip).
type countingEmbedder struct {
	fakeEmbedder
	docs atomic.Int64
}

func (c *countingEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	c.docs.Add(int64(len(texts)))
	return c.fakeEmbedder.EmbedDocs(ctx, texts)
}

// TestIndexer_MemorySearchAndHashSkip: memory items are hybrid-searchable
// under scope "agent", and a metadata-only evolve (accessCount) re-streams
// the item WITHOUT re-embedding it, while a context change does re-embed.
func TestIndexer_MemorySearchAndHashSkip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	emb := &countingEmbedder{fakeEmbedder: fakeEmbedder{dim: 16}}
	st, err := indexer.OpenStoreInMemory(ctx, emb.dim, true)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.chunkers, st, indexer.Options{Embedder: emb})
	d.indexer = ix
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "MemIndex")

	// Create a memory item via the bespoke endpoint (server resolves the
	// brain object).
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/agent/memory",
		`{"category":"preference","context":"user prefers dark mode in the editor","keywords":["dark","mode"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create memory: %d %s", rec.Code, rec.Body.String())
	}
	var created api.ModifyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	itemId := created.RecordIds[0]

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	embedsAfterCreate := emb.docs.Load()
	if embedsAfterCreate == 0 {
		t.Fatal("memory item was never embedded")
	}

	// Searchable under scope "agent".
	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "dark mode editor", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("memory search: hits = %v, want 1", hitRecordIds(res))
	}
	if h := res.Hits[0]; h.Scope != "agent" || h.RecordId != itemId {
		t.Fatalf("memory hit shape wrong: %+v", h)
	}
	// Scope filter: excluding "agent" drops it; including it keeps it.
	if got := doSearch(t, e, spaceId, api.SearchRequest{Query: "dark mode", Scopes: []string{"basic"}}, http.StatusOK); len(got.Hits) != 0 {
		t.Errorf("memory leaked into basic scope: %v", hitRecordIds(got))
	}

	// Metadata-only evolve (accessCount): the item re-streams but its
	// indexed text is unchanged, so the hash matches and NO re-embed runs.
	r := doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/agent/memory/"+itemId, `{"accessCount":5}`)
	if r.Code != http.StatusOK {
		t.Fatalf("evolve accessCount: %d %s", r.Code, r.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := emb.docs.Load(); got != embedsAfterCreate {
		t.Errorf("accessCount bump re-embedded: %d → %d (want no change)", embedsAfterCreate, got)
	}
	// Still searchable after the bump.
	if got := doSearch(t, e, spaceId, api.SearchRequest{Query: "dark mode", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK); len(got.Hits) != 1 {
		t.Fatalf("after accessCount bump: hits = %v, want 1", hitRecordIds(got))
	}

	// Context change: indexed text changes → the hash differs → re-embed.
	r = doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/agent/memory/"+itemId,
		`{"context":"user now prefers a light theme"}`)
	if r.Code != http.StatusOK {
		t.Fatalf("evolve context: %d %s", r.Code, r.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := emb.docs.Load(); got <= embedsAfterCreate {
		t.Errorf("context change did not re-embed: %d (want > %d)", got, embedsAfterCreate)
	}
	if got := doSearch(t, e, spaceId, api.SearchRequest{Query: "light theme", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK); len(got.Hits) != 1 || got.Hits[0].RecordId != itemId {
		t.Fatalf("after context change: hits = %v, want the item", hitRecordIds(got))
	}
}
