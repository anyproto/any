//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestIndexer_SchemaChunker drives the schema-driven chunker end to
// end: a runtime dataset with an x-search mapping indexes its records
// under scope "basic" (title boosted), a search-less dataset indexes
// nothing, and all three eviction paths hold — record delete, type
// detach, definition removal (retired-set, in-process).
func TestIndexer_SchemaChunker(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId

	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/datasets", `{
		"name": "articles", "idRule": "user",
		"search": {"title": "title", "text": "body"},
		"fields": [
			{"key": "title", "kind": "string", "required": true, "mutableBy": "any"},
			{"key": "body", "kind": "string", "mutableBy": "any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var added api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	// A second dataset WITHOUT x-search — must never index.
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/datasets", `{
		"name": "silent", "idRule": "user",
		"fields": [{"key": "note", "kind": "string", "mutableBy": "any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add silent dataset: %d %s", rec.Code, rec.Body.String())
	}

	upsert := func(body string) api.UpsertResult {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, base+"/upsert", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	upsert(`{"objectId":"` + objectId + `","dataset":"articles","records":[
		{"id":"a1","fields":{"title":"Glacier retreat","body":"annual mass balance measurements"}},
		{"id":"a2","fields":{"title":"Tidal power","body":"estuary turbine deployment"}}]}`)
	upsert(`{"objectId":"` + objectId + `","dataset":"silent","records":[
		{"id":"s1","fields":{"note":"unsearchable zeppelin cargo"}}]}`)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	sync := func() {
		t.Helper()
		if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
			t.Fatal(err)
		}
	}
	search := func(q string) api.SearchResponse {
		t.Helper()
		return doSearch(t, e, spaceId, api.SearchRequest{
			Query: q, Mode: api.SearchModeFTS, Scopes: []string{"basic"}, Limit: 10,
		}, http.StatusOK)
	}
	sync()

	t.Run("indexes with title and text", func(t *testing.T) {
		res := search("glacier")
		if len(res.Hits) != 1 || res.Hits[0].RecordId != "a1" || res.Hits[0].Dataset != "articles" {
			t.Fatalf("title hit = %v", hitRecordIds(res))
		}
		res = search("turbine")
		if len(res.Hits) != 1 || res.Hits[0].RecordId != "a2" {
			t.Fatalf("body hit = %v", hitRecordIds(res))
		}
	})

	t.Run("no x-search means no docs", func(t *testing.T) {
		if res := search("zeppelin"); len(res.Hits) != 0 {
			t.Fatalf("silent dataset leaked into the index: %v", hitRecordIds(res))
		}
	})

	t.Run("mutable edit reindexes", func(t *testing.T) {
		upsert(`{"objectId":"` + objectId + `","dataset":"articles","records":[
			{"id":"a2","fields":{"title":"Tidal power","body":"barrage lagoon feasibility"}}]}`)
		sync()
		if res := search("barrage"); len(res.Hits) != 1 || res.Hits[0].RecordId != "a2" {
			t.Fatalf("edited body not reindexed: %v", hitRecordIds(res))
		}
		if res := search("turbine"); len(res.Hits) != 0 {
			t.Fatalf("stale body still indexed: %v", hitRecordIds(res))
		}
	})

	t.Run("record delete evicts", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base+"/delete-records",
			`{"objectId":"`+objectId+`","dataset":"articles","recordIds":["a2"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete-records: %d %s", rec.Code, rec.Body.String())
		}
		sync()
		if res := search("barrage"); len(res.Hits) != 0 {
			t.Fatalf("deleted record still indexed: %v", hitRecordIds(res))
		}
		if res := search("glacier"); len(res.Hits) != 1 {
			t.Fatalf("sibling record must survive: %v", hitRecordIds(res))
		}
	})

	t.Run("type detach evicts", func(t *testing.T) {
		if _, err := sdkSpace.Properties().DetachType(ctx, objectId, typeId); err != nil {
			t.Fatal(err)
		}
		sync()
		if res := search("glacier"); len(res.Hits) != 0 {
			t.Fatalf("detached type's docs still indexed: %v", hitRecordIds(res))
		}
		// Re-attach for the removal subtest below.
		if _, err := sdkSpace.Properties().AttachType(ctx, objectId, typeId); err != nil {
			t.Fatal(err)
		}
		upsert(`{"objectId":"` + objectId + `","dataset":"articles","records":[
			{"id":"a3","fields":{"title":"Permafrost cores","body":"borehole sampling"}}]}`)
		sync()
		if res := search("permafrost"); len(res.Hits) != 1 {
			t.Fatalf("re-attached dataset must index again: %v", hitRecordIds(res))
		}
	})

	t.Run("definition removal evicts on next dirty", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodDelete, base+"/types/"+typeId+"/datasets/"+added.DatasetDefId, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("remove dataset: %d %s", rec.Code, rec.Body.String())
		}
		// The removal dirties the TYPE object; the data object needs its
		// own dirty tick for the lazy eviction — any write on it works.
		upsert(`{"objectId":"` + objectId + `","dataset":"silent","records":[
			{"id":"s2","fields":{"note":"touch"}}]}`)
		sync()
		if res := search("permafrost"); len(res.Hits) != 0 {
			t.Fatalf("removed definition's docs still indexed: %v", hitRecordIds(res))
		}
	})
}
