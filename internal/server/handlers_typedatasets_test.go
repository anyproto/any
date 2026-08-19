package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func TestDatasetEnumParsers(t *testing.T) {
	if v, ok := parseMutability(""); !ok || v != space.MutableNever {
		t.Error("empty mutableBy must default to never")
	}
	if v, ok := parseMutability("author"); !ok || v != space.MutableByAuthor {
		t.Error("author")
	}
	if v, ok := parseMutability("any"); !ok || v != space.MutableByAnyone {
		t.Error("any")
	}
	if _, ok := parseMutability("owner"); ok {
		t.Error("unknown mutableBy must fail")
	}

	if v, ok := parseStamp(""); !ok || v != space.StampNone {
		t.Error("empty stamp must default to none")
	}
	for label, want := range map[string]space.Stamp{
		"creator": space.StampCreator, "createTime": space.StampCreateTime, "modifyTime": space.StampModifyTime,
	} {
		if v, ok := parseStamp(label); !ok || v != want {
			t.Errorf("stamp %q", label)
		}
	}
	if _, ok := parseStamp("none"); ok {
		t.Error(`stamp "none" is not a wire label (omit the field)`)
	}

	if v, ok := parseIdRule(""); !ok || v != space.IdAuto {
		t.Error("empty idRule must default to auto")
	}
	if v, ok := parseIdRule("user"); !ok || v != space.IdUser {
		t.Error("user")
	}
	if _, ok := parseIdRule("derived"); ok {
		t.Error("unknown idRule must fail")
	}

	if v, ok := parseDeletePolicy(""); !ok || v != space.DeleteByAnyone {
		t.Error("empty deleteBy must default to anyone")
	}
	if v, ok := parseDeletePolicy("author"); !ok || v != space.DeleteByAuthor {
		t.Error("author")
	}
	if _, ok := parseDeletePolicy("admin"); ok {
		t.Error("unknown deleteBy must fail")
	}
}

func TestDatasetDraftFromAPI(t *testing.T) {
	req := api.DatasetDraftRequest{
		Name:     "articles",
		IdRule:   "user",
		DeleteBy: "author",
		Search:   &api.DatasetSearchFields{Title: "title", Text: "body"},
		Fields: []api.DatasetFieldDraft{
			{Key: "title", Kind: "string", Required: true, MutableBy: "author"},
			{Key: "author", Stamp: "creator"},
		},
	}
	draft, code, reason := datasetDraftFromAPI(req)
	if code != "" {
		t.Fatalf("unexpected error %s: %s", code, reason)
	}
	if draft.IdRule != space.IdUser || draft.DeleteBy != space.DeleteByAuthor {
		t.Errorf("behavioral enums not mapped: %+v", draft)
	}
	if draft.Search == nil || draft.Search.Title != "title" || draft.Search.Text != "body" {
		t.Errorf("search not mapped: %+v", draft.Search)
	}
	if len(draft.Fields) != 2 || draft.Fields[0].Kind != space.PropertyKindString ||
		!draft.Fields[0].Required || draft.Fields[0].MutableBy != space.MutableByAuthor ||
		draft.Fields[1].Stamp != space.StampCreator {
		t.Errorf("fields not mapped: %+v", draft.Fields)
	}

	// Field errors carry the offending key.
	req.Fields[0].MutableBy = "bogus"
	if _, code, reason := datasetDraftFromAPI(req); code != "request.invalid_field" || reason == "" {
		t.Errorf("bad mutableBy: code=%q reason=%q", code, reason)
	}
	req.Fields[0].MutableBy = ""
	req.Fields = append(req.Fields, api.DatasetFieldDraft{Kind: "string"})
	if _, code, _ := datasetDraftFromAPI(req); code != "request.missing_field" {
		t.Errorf("empty key: code=%q", code)
	}
}

func TestDatasetShapeFromAPI(t *testing.T) {
	sh, ok := datasetShapeFromAPI(&api.DatasetFieldShape{
		Kind:  "array",
		Items: &api.DatasetFieldShape{Kind: "string"},
	})
	if !ok || sh == nil || sh.Items == nil {
		t.Fatalf("array-of-string shape: ok=%v sh=%+v", ok, sh)
	}
	if _, ok := datasetShapeFromAPI(&api.DatasetFieldShape{Kind: "blob"}); ok {
		t.Error("unknown shape kind must fail")
	}
	if sh, ok := datasetShapeFromAPI(nil); !ok || sh != nil {
		t.Error("nil shape is valid")
	}
}

func TestUpsertResultToAPI_RejectionCodes(t *testing.T) {
	res := space.UpsertResult{
		Created: 1, Updated: 2, Skipped: 3,
		Rejections: []space.UpsertRejection{
			{Index: 0, Id: "a", Err: space.ErrImmutableFieldChanged},
			{Index: 1, Id: "b", Err: space.ErrUpsertNotAuthor},
			{Index: 2, Id: "c", Err: space.ErrRecordDeleted},
			{Index: 3, Id: "d", Err: errors.New("upsert: missing required field \"title\"")},
		},
	}
	out := upsertResultToAPI(res)
	if out.Created != 1 || out.Updated != 2 || out.Skipped != 3 {
		t.Errorf("counters: %+v", out)
	}
	wantCodes := []string{"upsert.immutable_field", "upsert.not_author", "upsert.record_deleted", "upsert.rejected"}
	for i, w := range wantCodes {
		if out.Rejections[i].Code != w {
			t.Errorf("rejection[%d].Code = %q, want %q", i, out.Rejections[i].Code, w)
		}
	}
	// SDK package prefixes never ride the wire.
	if got := out.Rejections[3].Reason; got != `missing required field "title"` {
		t.Errorf("reason = %q, want prefix stripped", got)
	}
	if out.Pages == nil {
		t.Error("Pages must marshal as [], not null")
	}
}

// TestTypeDatasets_Lifecycle drives the full runtime-dataset surface
// over the in-process server: define → discover → upsert (create /
// idempotent re-run / mutable update / immutable rejection) → stamps →
// patch → evolve → remove.
func TestTypeDatasets_Lifecycle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/types/" + typeId + "/datasets"

	draft := `{
		"name": "articles", "displayName": "Articles",
		"idRule": "user", "deleteBy": "author",
		"search": {"title": "title", "text": "body"},
		"fields": [
			{"key": "title", "kind": "string", "required": true, "mutableBy": "author"},
			{"key": "body", "kind": "string", "mutableBy": "author"},
			{"key": "slug", "kind": "string"},
			{"key": "author", "stamp": "creator"},
			{"key": "createdAt", "stamp": "createTime"},
			{"key": "updatedAt", "stamp": "modifyTime"}
		]}`

	var defId string
	t.Run("define", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base, draft)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
		}
		var out api.AddDatasetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.DatasetDefId == "" {
			t.Fatalf("decode: %v %s", err, rec.Body.String())
		}
		defId = out.DatasetDefId
	})

	t.Run("name conflicts", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base, draft)
		if rec.Code != http.StatusConflict {
			t.Fatalf("duplicate name: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.name_conflict")
		rec = doJSON(t, e, http.MethodPost, base, `{"name":"chat_messages"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("built-in name: %d %s", rec.Code, rec.Body.String())
		}
		rec = doJSON(t, e, http.MethodPost, base, `{"name":"prop"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("reserved virtual name: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "request.invalid_field")
	})

	t.Run("bad declaration", func(t *testing.T) {
		// author-mutability without a creator stamp — SDK decl rule.
		rec := doJSON(t, e, http.MethodPost, base,
			`{"name":"broken","fields":[{"key":"x","kind":"string","mutableBy":"author"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad decl: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.decl_invalid")
		// Unknown enum label caught at the boundary.
		rec = doJSON(t, e, http.MethodPost, base, `{"name":"broken2","idRule":"random"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad enum: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "request.invalid_field")
	})

	t.Run("list and discovery", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodGet, base, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var list api.TypeDatasetsListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Datasets) != 1 {
			t.Fatalf("datasets = %+v", list.Datasets)
		}
		def := list.Datasets[0]
		if def.Id != defId || def.Name != "articles" || def.IdRule != "user" ||
			def.DeleteBy != "author" || def.Invalid || len(def.Fields) != 6 {
			t.Errorf("def = %+v", def)
		}

		// Space-level discovery carries the owning type + x-search.
		rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/datasets", "")
		var ds api.DatasetsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range ds.Datasets {
			if s.Name != "articles" {
				continue
			}
			found = true
			if s.TypeId != typeId {
				t.Errorf("typeId = %q, want %q", s.TypeId, typeId)
			}
			var doc struct {
				Search   *struct{ Title, Text string } `json:"x-search"`
				Id       string                        `json:"x-id"`
				DeleteBy string                        `json:"x-delete-by"`
				Required []string                      `json:"required"`
			}
			if err := json.Unmarshal(s.Schema, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Search == nil || doc.Search.Title != "title" || doc.Search.Text != "body" {
				t.Errorf("x-search = %+v", doc.Search)
			}
			if doc.Id != "user" || doc.DeleteBy != "author" || len(doc.Required) != 1 {
				t.Errorf("keywords: %+v", doc)
			}
		}
		if !found {
			t.Fatal("articles missing from discovery")
		}
	})

	upsertBody := `{"objectId":"` + objectId + `","dataset":"articles","records":[
		{"id":"a1","fields":{"title":"Hello","body":"first body","slug":"hello"}},
		{"id":"a2","fields":{"title":"World","body":"second body","slug":"world"}}]}`

	t.Run("upsert create", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert", upsertBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Created != 2 || out.Updated != 0 || out.Skipped != 0 || len(out.Rejections) != 0 {
			t.Fatalf("result = %+v", out)
		}
	})

	t.Run("upsert idempotent rerun", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert", upsertBody)
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Created != 0 || out.Updated != 0 || out.Skipped != 2 {
			t.Fatalf("rerun = %+v", out)
		}
	})

	t.Run("upsert mutable update and immutable rejection", func(t *testing.T) {
		body := `{"objectId":"` + objectId + `","dataset":"articles","records":[
			{"id":"a1","fields":{"title":"Hello v2","body":"first body","slug":"hello"}},
			{"id":"a2","fields":{"title":"World","body":"second body","slug":"changed"}}]}`
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert", body)
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Updated != 1 || len(out.Rejections) != 1 ||
			out.Rejections[0].Code != "upsert.immutable_field" || out.Rejections[0].Id != "a2" {
			t.Fatalf("result = %+v", out)
		}
	})

	t.Run("records carry stamps", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query",
			`{"objectId":"`+objectId+`","dataset":"articles","sort":["id"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Records) != 2 {
			t.Fatalf("records = %+v", out.Records)
		}
		r := out.Records[0]
		if r["id"] != "a1" || r["title"] != "Hello v2" {
			t.Errorf("row = %+v", r)
		}
		if s, _ := r["author"].(string); s == "" {
			t.Error("creator stamp missing")
		}
		if n, _ := r["createdAt"].(float64); n == 0 {
			t.Error("createTime stamp missing")
		}
		if n, _ := r["updatedAt"].(float64); n == 0 {
			t.Error("modifyTime stamp missing")
		}
	})

	t.Run("upsert misuse", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert",
			`{"objectId":"`+objectId+`","dataset":"nope","records":[{"id":"x","fields":{}}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown dataset: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.unknown")

		// chat_messages exists but has auto-derived ids.
		rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert",
			`{"objectId":"`+objectId+`","dataset":"chat_messages","records":[{"id":"x","fields":{}}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("auto-id dataset: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "upsert.requires_user_ids")
	})

	t.Run("patch display leaves", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPatch, base+"/"+defId,
			`{"set":{"displayName":"Posts","search.title":"headline","description":"tmp"}}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
		// Unset round-trip on a mutable leaf.
		rec = doJSON(t, e, http.MethodPatch, base+"/"+defId, `{"unset":["description"]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unset: %d %s", rec.Code, rec.Body.String())
		}
		rec = doJSON(t, e, http.MethodPatch, base+"/"+defId, `{"set":{"idRule":"auto"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("pinned path: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.immutable")
		// The collection name is pinned — the head record's storage
		// "name" slot is a dead lever nothing reads back, so it must
		// reject, not silently no-op.
		rec = doJSON(t, e, http.MethodPatch, base+"/"+defId, `{"set":{"name":"posts"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("name path: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.immutable")

		var list api.TypeDatasetsListResponse
		rec = doJSON(t, e, http.MethodGet, base, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		def := list.Datasets[0]
		if def.Name != "articles" || def.DisplayName != "Posts" ||
			def.Description != "" || def.Search == nil || def.Search.Title != "headline" {
			t.Errorf("patched def = %+v", def)
		}
	})

	t.Run("unknown defId is 404, not a silent no-op", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPatch, base+"/dsdnope",
			`{"set":{"displayName":"X"}}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("patch unknown defId: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "sdk.not_found")
		rec = doJSON(t, e, http.MethodDelete, base+"/dsdnope", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("delete unknown defId: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "sdk.not_found")
	})

	t.Run("non-type typeId is 404 on writes", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+objectId+"/datasets",
			`{"name":"orphaned"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("add dataset on non-type: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "type.not_found")
	})

	t.Run("multi-page upsert", func(t *testing.T) {
		// pageSize 1 forces one change per record; the batch mixes an
		// identical skip (a1 unchanged), a create, and an
		// immutable-field rejection — counters and the rejection's
		// ABSOLUTE index must survive the paging.
		body := `{"objectId":"` + objectId + `","dataset":"articles","pageSize":1,"records":[
			{"id":"a1","fields":{"title":"Hello v2","body":"first body","slug":"hello"}},
			{"id":"p1","fields":{"title":"Paged","body":"fresh","slug":"paged"}},
			{"id":"a2","fields":{"title":"World","body":"second body","slug":"drifted"}}]}`
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/upsert", body)
		var out api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Created != 1 || out.Updated != 0 || out.Skipped != 1 {
			t.Fatalf("counters = %+v", out)
		}
		if len(out.Rejections) != 1 || out.Rejections[0].Index != 2 ||
			out.Rejections[0].Id != "a2" || out.Rejections[0].Code != "upsert.immutable_field" {
			t.Fatalf("rejections = %+v", out.Rejections)
		}
		if len(out.Pages) != 1 {
			t.Fatalf("pages = %+v, want exactly the create page (skip and rejection pages are empty)", out.Pages)
		}
	})

	t.Run("additive field", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base+"/"+defId+"/fields",
			`{"key":"rating","kind":"number","mutableBy":"any"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add field: %d %s", rec.Code, rec.Body.String())
		}
		var out api.AddDatasetFieldResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.FieldDefId == "" {
			t.Fatalf("decode: %v %s", err, rec.Body.String())
		}

		// Additive fields cannot be required (SDK evolution rule).
		rec = doJSON(t, e, http.MethodPost, base+"/"+defId+"/fields",
			`{"key":"later","kind":"string","required":true}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("required additive: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.decl_invalid")

		// Remove it again.
		rec = doJSON(t, e, http.MethodDelete, base+"/"+defId+"/fields/"+out.FieldDefId, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("remove field: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("remove dataset", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodDelete, base+"/"+defId, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
		}
		var list api.TypeDatasetsListResponse
		rec = doJSON(t, e, http.MethodGet, base, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Datasets) != 0 {
			t.Errorf("post-remove list = %+v", list.Datasets)
		}
	})

	t.Run("registered built-in type rejects", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/chat/datasets",
			`{"name":"extra"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("built-in type: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "type.registered")
	})
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v (%s)", err, rec.Body.String())
	}
	if env.Error.Code != code {
		t.Errorf("error code = %q, want %q (message: %s)", env.Error.Code, code, env.Error.Message)
	}
}
