package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// A dataset declared deleteBy none refuses delete-records before
// anything is written: 409 dataset.undeletable, every record stays
// live and every id stays writable.
func TestServer_DeleteRecords_Undeletable(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"entries","name":"Entries"}`)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "entries", "idRule": "user", "deleteBy": "none",
		"fields": [
			{"key": "pos", "kind": "string", "mutableBy": "any"},
			{"key": "removed", "kind": "boolean", "mutableBy": "any"}
		]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var added api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	collection := added.Collection

	// The policy reads back on the definition.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/types/"+typeId+"/datasets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list api.TypeDatasetsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Datasets) != 1 || list.Datasets[0].DeleteBy != api.DeleteByNone {
		t.Fatalf("datasets = %+v, want one with deleteBy none", list.Datasets)
	}

	upsert := func(records string) api.UpsertResult {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert",
			fmt.Sprintf(`{"objectId": %q, "dataset": %q, "records": [%s]}`, objectId, collection, records))
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		var res api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Rejections) > 0 {
			t.Fatalf("upsert rejections: %+v", res.Rejections)
		}
		return res
	}
	upsert(`{"id": "w:chat", "fields": {"pos": "a0"}}`)

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/delete-records",
		fmt.Sprintf(`{"objectId": %q, "dataset": %q, "recordIds": ["w:chat", "never"]}`, objectId, collection))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete-records: %d %s, want 409", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "dataset.undeletable")

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query",
		fmt.Sprintf(`{"objectId": %q, "dataset": %q}`, objectId, collection))
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Records) != 1 {
		t.Fatalf("records after the refused delete = %d, want 1", len(q.Records))
	}

	// Neither id was burned.
	res := upsert(`{"id": "w:chat", "fields": {"removed": true}}, {"id": "never", "fields": {"pos": "b0"}}`)
	if res.Updated != 1 || res.Created != 1 {
		t.Fatalf("upsert after the refused delete = %+v, want 1 updated and 1 created", res)
	}

	// A module dataset keeps its module's delete rule.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+typeId+"/parts/"+partId+"/datasets",
		`{"key": "notes", "module": "editor", "deleteBy": "none"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("editor dataset with a delete policy: %d %s, want 409", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "dataset.module_owned")

	// A shared dataset refuses the same way, an absent-only id included,
	// and the cross-object read still finds the record.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+typeId+"/parts/"+partId+"/datasets", `{
		"key": "shared_entries", "idRule": "user", "deleteBy": "none", "shared": true,
		"fields": [{"key": "pos", "kind": "string", "mutableBy": "any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add shared dataset: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	shared := added.Collection
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert",
		fmt.Sprintf(`{"objectId": %q, "dataset": %q, "records": [{"id": "s:bin", "fields": {"pos": "a0"}}]}`, objectId, shared))
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert shared: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/delete-records",
		fmt.Sprintf(`{"objectId": %q, "dataset": %q, "recordIds": ["nope"]}`, objectId, shared))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete-records on the shared dataset: %d %s, want 409", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "dataset.undeletable")
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/datasets/query", fmt.Sprintf(`{"dataset": %q}`, shared))
	if rec.Code != http.StatusOK {
		t.Fatalf("datasets/query: %d %s", rec.Code, rec.Body.String())
	}
	q = api.QueryResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Records) != 1 {
		t.Fatalf("shared records after the refused delete = %d, want 1", len(q.Records))
	}
}
