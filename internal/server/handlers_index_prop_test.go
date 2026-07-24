//go:build fts && vector && !gomobile

package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestIndexer_TypeDefinitionsExcluded pins the prop-chunker exclusion of
// type-definition objects (`any.types = ["__type__"]`): a created type's
// name must never surface as a search hit — its one-word name otherwise
// wins BM25 on field-length normalization and shadows real content. The
// control object proves the exclusion is selective (same token, indexed).
func TestIndexer_TypeDefinitionsExcluded(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "TypeDefScope")

	// A custom type named with a unique token, and a control object
	// whose name carries the same token.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Zebrafinch","xKey":"zebrafinch"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("type create: %d %s", rec.Code, rec.Body.String())
	}
	ctrlObj := mustCreateNamed(t, e, spaceId, nil, "zebrafinch sightings journal")

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "zebrafinch", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %v, want only the control object", hitRecordIds(res))
	}
	if h := res.Hits[0]; h.ObjectId != ctrlObj || h.Dataset != "prop" {
		t.Fatalf("hit = {dataset:%s obj:%s}, want the control's prop/name (obj %s)", h.Dataset, h.ObjectId, ctrlObj)
	}
}
