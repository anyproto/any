//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestIndexer_SchemaChunkerMultiText drives the multi-field text
// mapping through the whole pipeline: a dataset declared
// with `text: ["body", "notes"]` indexes terms from every mapped
// field under the declared scope, an empty field contributes nothing,
// and re-mapping via PATCH takes effect on the next re-index.
func TestIndexer_SchemaChunkerMultiText(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"idx"}`)
	base := "/v1/spaces/" + spaceId

	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "emails", "idRule": "user",
		"search": {"title": "subject", "text": ["body", "notes"], "scope": "email"},
		"fields": [
			{"key": "subject", "kind": "string", "mutableBy": "any"},
			{"key": "body", "kind": "string", "mutableBy": "any"},
			{"key": "notes", "kind": "string", "mutableBy": "any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var added api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+objectId+`","dataset":"`+typeId+`_emails","records":[
		{"id":"m1","fields":{"subject":"Quarterly numbers","body":"revenue is up","notes":"follow up with procurement"}},
		{"id":"m2","fields":{"subject":"Standup","body":"skipped today"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	search := func(q string) api.SearchResponse {
		t.Helper()
		return doSearch(t, e, spaceId, api.SearchRequest{
			Query: q, Mode: api.SearchModeFTS, Scopes: []string{"email"}, Limit: 10,
		}, http.StatusOK)
	}

	t.Run("hits terms from every mapped field", func(t *testing.T) {
		// "procurement" lives ONLY in the second mapped field.
		res := search("procurement")
		if len(res.Hits) != 1 || res.Hits[0].RecordId != "m1" || res.Hits[0].Dataset != typeId+"_emails" {
			t.Fatalf("notes-only hit = %v", hitRecordIds(res))
		}
		if res.Hits[0].Scope != "email" {
			t.Errorf("scope = %q, want email", res.Hits[0].Scope)
		}
		if res := search("revenue"); len(res.Hits) != 1 || res.Hits[0].RecordId != "m1" {
			t.Fatalf("body hit = %v", hitRecordIds(res))
		}
		if res := search("quarterly"); len(res.Hits) != 1 || res.Hits[0].RecordId != "m1" {
			t.Fatalf("title hit = %v", hitRecordIds(res))
		}
		// m2 has no notes value — still indexed on the fields it has.
		if res := search("skipped"); len(res.Hits) != 1 || res.Hits[0].RecordId != "m2" {
			t.Fatalf("empty-notes record = %v", hitRecordIds(res))
		}
	})

	t.Run("re-mapping applies on next re-index", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPatch, base+"/types/"+typeId+"/datasets/"+added.DatasetDefId,
			`{"set":{"search.text":["notes"]}}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
		// Stored entries keep their old mapping until the record
		// re-indexes on its next change (docs/03-api.md § Runtime
		// dataset schemas).
		rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+objectId+`","dataset":"`+typeId+`_emails","records":[
			{"id":"m1","fields":{"subject":"Quarterly numbers","body":"revenue is up","notes":"follow up with legal"}}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
			t.Fatal(err)
		}
		if res := search("revenue"); len(res.Hits) != 0 {
			t.Fatalf("unmapped body still indexed: %v", hitRecordIds(res))
		}
		if res := search("legal"); len(res.Hits) != 1 || res.Hits[0].RecordId != "m1" {
			t.Fatalf("re-mapped notes hit = %v", hitRecordIds(res))
		}
	})
}

// TestIndexer_SchemaChunker drives the schema-driven chunker end to
// end: a runtime dataset with an x-search mapping indexes its records
// under scope "basic" (title boosted), a search-less dataset indexes
// nothing, and all three eviction paths hold — record delete, retype,
// definition removal (retired-set, in-process).
func TestIndexer_SchemaChunker(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"idx"}`)
	base := "/v1/spaces/" + spaceId

	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "articles", "idRule": "user",
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
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "silent", "idRule": "user",
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
	upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_articles","records":[
		{"id":"a1","fields":{"title":"Glacier retreat","body":"annual mass balance measurements"}},
		{"id":"a2","fields":{"title":"Tidal power","body":"estuary turbine deployment"}}]}`)
	upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_silent","records":[
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
		if len(res.Hits) != 1 || res.Hits[0].RecordId != "a1" || res.Hits[0].Dataset != typeId+"_articles" {
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
		upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_articles","records":[
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
			`{"objectId":"`+objectId+`","dataset":"`+typeId+`_articles","recordIds":["a2"]}`)
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

	t.Run("retype evicts", func(t *testing.T) {
		// Retyping to a type that declares nothing drops the dataset's
		// owner: the object's docs go.
		if _, err := sdkSpace.Properties().SetType(ctx, objectId, plainType(t, e, spaceId)); err != nil {
			t.Fatal(err)
		}
		sync()
		if res := search("glacier"); len(res.Hits) != 0 {
			t.Fatalf("retyped object's docs still indexed: %v", hitRecordIds(res))
		}
		// Set the declaring type back for the removal subtest below.
		if _, err := sdkSpace.Properties().SetType(ctx, objectId, typeId); err != nil {
			t.Fatal(err)
		}
		upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_articles","records":[
			{"id":"a3","fields":{"title":"Permafrost cores","body":"borehole sampling"}}]}`)
		sync()
		if res := search("permafrost"); len(res.Hits) != 1 {
			t.Fatalf("dataset must index again once its type is back: %v", hitRecordIds(res))
		}
	})

	t.Run("cleared x-search evicts on next dirty", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
			"key": "clippings", "idRule": "user",
			"search": {"text": "quote"},
			"fields": [{"key": "quote", "kind": "string", "mutableBy": "any"}]}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add clippings: %d %s", rec.Code, rec.Body.String())
		}
		var clip api.AddDatasetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &clip); err != nil {
			t.Fatal(err)
		}
		upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_clippings","records":[
			{"id":"c1","fields":{"quote":"antikythera mechanism fragment"}}]}`)
		sync()
		if res := search("antikythera"); len(res.Hits) != 1 {
			t.Fatalf("clipping not indexed: %v", hitRecordIds(res))
		}

		// Clearing the annotation makes the dataset unsearchable — its
		// docs must evict on the object's next dirty tick, not go stale.
		rec = doJSON(t, e, http.MethodPatch, base+"/types/"+typeId+"/datasets/"+clip.DatasetDefId,
			`{"unset":["search.text"]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("clear x-search: %d %s", rec.Code, rec.Body.String())
		}
		upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_silent","records":[
			{"id":"s-touch","fields":{"note":"tick"}}]}`)
		sync()
		if res := search("antikythera"); len(res.Hits) != 0 {
			t.Fatalf("cleared x-search docs still indexed: %v", hitRecordIds(res))
		}
	})

	t.Run("definition removal evicts on next dirty", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodDelete, base+"/types/"+typeId+"/datasets/"+added.DatasetDefId, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("remove dataset: %d %s", rec.Code, rec.Body.String())
		}
		// The removal dirties the TYPE object; the data object needs its
		// own dirty tick for the lazy eviction — any write on it works.
		upsert(`{"objectId":"` + objectId + `","dataset":"` + typeId + `_silent","records":[
			{"id":"s2","fields":{"note":"touch"}}]}`)
		sync()
		if res := search("permafrost"); len(res.Hits) != 0 {
			t.Fatalf("removed definition's docs still indexed: %v", hitRecordIds(res))
		}
	})
}
