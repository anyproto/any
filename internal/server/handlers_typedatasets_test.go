package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		Search:   &api.DatasetSearchFields{Title: "title", Text: api.SearchText{"body"}, Scope: "news"},
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
	if draft.Search == nil || draft.Search.Title != "title" ||
		len(draft.Search.Text) != 1 || draft.Search.Text[0] != "body" ||
		draft.Search.Scope != "news" {
		t.Errorf("search not mapped: %+v", draft.Search)
	}

	// A multi-key text mapping rides through as the key list.
	req.Search = &api.DatasetSearchFields{Text: api.SearchText{"body", "notes"}}
	multi, code, _ := datasetDraftFromAPI(req)
	if code != "" || len(multi.Search.Text) != 2 || multi.Search.Text[1] != "notes" {
		t.Errorf("multi-key text not mapped: code=%q search=%+v", code, multi.Search)
	}
	if len(draft.Fields) != 2 || draft.Fields[0].Kind != space.PropertyKindString ||
		!draft.Fields[0].Required || draft.Fields[0].MutableBy != space.MutableByAuthor ||
		draft.Fields[1].Stamp != space.StampCreator {
		t.Errorf("fields not mapped: %+v", draft.Fields)
	}

	// A non-slug scope is caught at the boundary.
	req.Search = &api.DatasetSearchFields{Text: api.SearchText{"body"}, Scope: "Not A Slug"}
	if _, code, _ := datasetDraftFromAPI(req); code != "request.invalid_field" {
		t.Errorf("bad search.scope: code=%q", code)
	}
	req.Search = nil

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
		"search": {"title": "title", "text": "body", "scope": "news"},
		"fields": [
			{"key": "title", "kind": "string", "required": true, "mutableBy": "author"},
			{"key": "body", "kind": "string", "mutableBy": "author"},
			{"key": "slug", "kind": "string", "description": "URL slug", "xFormat": {"type": "text", "icon": "link"}},
			{"key": "tags", "kind": "array", "mutableBy": "any",
			 "shape": {"kind": "array", "items": {"kind": "string"}},
			 "xFormat": {"type": "choice", "config": {"multiple": true}}},
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
			def.DeleteBy != "author" || def.Invalid || len(def.Fields) != 7 {
			t.Errorf("def = %+v", def)
		}
		// The descriptive slice and the full shape read back.
		byKey := map[string]api.DatasetFieldDef{}
		for _, f := range def.Fields {
			byKey[f.Key] = f
		}
		if f := byKey["slug"]; f.Description != "URL slug" || string(f.XFormat) != `{"icon":"link","type":"text"}` {
			t.Errorf("slug field = %+v", f)
		}
		if f := byKey["tags"]; f.Kind != "array" || f.Shape == nil || f.Shape.Items == nil || f.Shape.Items.Kind != "string" ||
			string(f.XFormat) != `{"config":{"multiple":true},"type":"choice"}` {
			t.Errorf("tags field = %+v (shape %+v)", f, f.Shape)
		}
		if f := byKey["body"]; f.XFormat != nil || f.Shape != nil || f.Description != "" {
			t.Errorf("bare field must read back without descriptor/shape: %+v", f)
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
				Search   *struct{ Title, Text, Scope string } `json:"x-search"`
				Id       string                               `json:"x-id"`
				DeleteBy string                               `json:"x-delete-by"`
				Required []string                             `json:"required"`
			}
			if err := json.Unmarshal(s.Schema, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Search == nil || doc.Search.Title != "title" || doc.Search.Text != "body" ||
				doc.Search.Scope != "news" {
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
		// Time stamps are instants: `{"$date": "<RFC 3339>"}` on the
		// wire, not epoch numbers.
		for field, label := range map[string]string{"createdAt": "createTime", "updatedAt": "modifyTime"} {
			obj, _ := r[field].(map[string]any)
			iso, _ := obj["$date"].(string)
			if iso == "" {
				t.Errorf("%s stamp missing or not a $date instant: %#v", label, r[field])
				continue
			}
			if _, err := time.Parse(time.RFC3339, iso); err != nil {
				t.Errorf("%s stamp %q is not RFC 3339: %v", label, iso, err)
			}
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
			`{"set":{"displayName":"Posts","search.title":"headline","search.scope":"press","description":"tmp"}}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
		// A non-slug scope value rejects before the SDK write.
		rec = doJSON(t, e, http.MethodPatch, base+"/"+defId, `{"set":{"search.scope":"Not A Slug"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad scope value: %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "request.invalid_field")
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
			def.Description != "" || def.Search == nil || def.Search.Title != "headline" ||
			def.Search.Scope != "press" {
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

// TestTypeDatasets_SearchTextMultiField covers the string-or-array
// search.text leaf: array declaration, wire read-back on
// both surfaces, drift-patch of the array leaf, single-element
// canonicalization, and the invalid-array rejects.
func TestTypeDatasets_SearchTextMultiField(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, _ := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/types/" + typeId + "/datasets"

	rec := doJSON(t, e, http.MethodPost, base, `{
		"name": "emails", "idRule": "user",
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
	defId := added.DatasetDefId

	readText := func(t *testing.T) api.SearchText {
		t.Helper()
		var list api.TypeDatasetsListResponse
		rec := doJSON(t, e, http.MethodGet, base, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		for _, def := range list.Datasets {
			if def.Id == defId {
				if def.Search == nil {
					t.Fatal("search mapping missing")
				}
				return def.Search.Text
			}
		}
		t.Fatal("definition missing from list")
		return nil
	}
	// discoveryText returns the RAW x-search.text of the discovery doc,
	// so the canonical wire form (bare string vs array) is observable.
	discoveryText := func(t *testing.T) json.RawMessage {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/datasets", "")
		var ds api.DatasetsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
			t.Fatal(err)
		}
		for _, s := range ds.Datasets {
			if s.Name != "emails" {
				continue
			}
			var doc struct {
				Search struct {
					Text json.RawMessage `json:"text"`
				} `json:"x-search"`
			}
			if err := json.Unmarshal(s.Schema, &doc); err != nil {
				t.Fatal(err)
			}
			return doc.Search.Text
		}
		t.Fatal("emails missing from discovery")
		return nil
	}

	t.Run("array declaration reads back", func(t *testing.T) {
		got := readText(t)
		if len(got) != 2 || got[0] != "body" || got[1] != "notes" {
			t.Errorf("text = %v", got)
		}
		if raw := string(discoveryText(t)); raw != `["body","notes"]` {
			t.Errorf("discovery x-search.text = %s, want the array form", raw)
		}
	})

	t.Run("drift-patch of the array leaf", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPatch, base+"/"+defId,
			`{"set":{"search.text":["notes","subject"]}}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
		got := readText(t)
		if len(got) != 2 || got[0] != "notes" || got[1] != "subject" {
			t.Errorf("patched text = %v", got)
		}
	})

	t.Run("single-element patch canonicalizes", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPatch, base+"/"+defId,
			`{"set":{"search.text":["body"]}}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
		got := readText(t)
		if len(got) != 1 || got[0] != "body" {
			t.Errorf("patched text = %v", got)
		}
		if raw := string(discoveryText(t)); raw != `"body"` {
			t.Errorf("discovery x-search.text = %s, want the bare string", raw)
		}
	})

	t.Run("patch rejects invalid text values", func(t *testing.T) {
		for _, bad := range []string{`[]`, `[""]`, `["body","body"]`, `[42]`, `42`} {
			rec := doJSON(t, e, http.MethodPatch, base+"/"+defId,
				`{"set":{"search.text":`+bad+`}}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("patch %s: %d %s", bad, rec.Code, rec.Body.String())
			}
			assertErrorCode(t, rec, "request.invalid_field")
		}
	})

	t.Run("declaration rejects invalid arrays", func(t *testing.T) {
		for _, bad := range []string{`[]`, `[""]`, `["a","a"]`} {
			rec := doJSON(t, e, http.MethodPost, base,
				`{"name":"badtext","search":{"text":`+bad+`}}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("declare %s: %d %s", bad, rec.Code, rec.Body.String())
			}
			assertErrorCode(t, rec, "dataset.decl_invalid")
		}
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

// TestTypeDatasets_FieldPatch covers PATCH …/fields/:fieldId: the
// display pair and every xFormat path mutate under the property PATCH
// rules, the behavioral declaration is pinned, the slug stays within
// the field's kind, and a field id off another dataset is a 404.
func TestTypeDatasets_FieldPatch(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, _ := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/types/" + typeId + "/datasets"

	rec := doJSON(t, e, http.MethodPost, base, `{"name":"notes","fields":[
		{"key":"title","kind":"string","xFormat":{"type":"text","icon":"heading"}},
		{"key":"stage","kind":"array","mutableBy":"any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var added api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, e, http.MethodPost, base, `{"name":"other","fields":[{"key":"x","kind":"string"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add other dataset: %d %s", rec.Code, rec.Body.String())
	}
	// A field draft's descriptor is validated like a property's — against
	// the wire kind, the shape's kind, or the kind a stamp implies.
	for name, tc := range map[string]struct {
		body string
		code string
	}{
		"slug/kind mismatch":    {`{"name":"bad1","fields":[{"key":"x","kind":"string","xFormat":{"type":"choice"}}]}`, "property.format_invalid"},
		"stamped field slug":    {`{"name":"bad2","fields":[{"key":"author","stamp":"creator","xFormat":{"type":"date"}}]}`, "property.format_invalid"},
		"descriptor not object": {`{"name":"bad3","fields":[{"key":"x","kind":"string","xFormat":"email"}]}`, "request.invalid_field"},
		"reserved key":          {`{"name":"bad4","fields":[{"key":"x","kind":"string","xFormat":{"type":"text","validate":{}}}]}`, "property.format_invalid"},
	} {
		rec := doJSON(t, e, http.MethodPost, base, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, tc.code)
	}
	// A stamped field with a slug that fits the implied kind is fine.
	rec = doJSON(t, e, http.MethodPost, base, `{"name":"stamped","fields":[{"key":"createdAt","stamp":"createTime","xFormat":{"type":"datetime"}}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("stamped datetime slug: %d %s", rec.Code, rec.Body.String())
	}
	var other api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}

	fields := func() map[string]api.DatasetFieldDef {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, base, "")
		var list api.TypeDatasetsListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		out := map[string]api.DatasetFieldDef{}
		for _, def := range list.Datasets {
			if def.Id != added.DatasetDefId {
				continue
			}
			for _, f := range def.Fields {
				out[f.Key] = f
			}
		}
		return out
	}
	title := fields()["title"]
	stage := fields()["stage"]
	fieldURL := base + "/" + added.DatasetDefId + "/fields/"

	// Display pair + descriptor leaves, a vendor subtree, an unset.
	rec = doJSON(t, e, http.MethodPatch, fieldURL+title.Id,
		`{"set":{"name":"Title","description":"Headline","xFormat.icon":"title","xFormat.config.maxLen":120,"xFormat.acme.widget":"compact"},"unset":["xFormat.type"]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch field: %d %s", rec.Code, rec.Body.String())
	}
	if f := fields()["title"]; f.Name != "Title" || f.Description != "Headline" ||
		string(f.XFormat) != `{"acme":{"widget":"compact"},"config":{"maxLen":120},"icon":"title"}` {
		t.Errorf("patched field = %+v xFormat=%s", f, f.XFormat)
	}
	// A descriptor grows onto a bare field; the slug must fit the kind.
	rec = doJSON(t, e, http.MethodPatch, fieldURL+stage.Id, `{"set":{"xFormat.type":"choice","xFormat.options.lead.name":"Lead"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("grow descriptor: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPatch, fieldURL+stage.Id, `{"set":{"xFormat.type":"text"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-kind slug: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "property.format_invalid")
	// Containers are unset-only; the declaration is pinned.
	rec = doJSON(t, e, http.MethodPatch, fieldURL+stage.Id, `{"set":{"xFormat.options.lead":{"name":"X"}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("object set: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "request.invalid_field")
	for _, body := range []string{`{"set":{"kind":"string"}}`, `{"set":{"required":true}}`, `{"unset":["key"]}`} {
		rec = doJSON(t, e, http.MethodPatch, fieldURL+stage.Id, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("pinned %s: %d %s", body, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "dataset.immutable")
	}
	rec = doJSON(t, e, http.MethodPatch, fieldURL+stage.Id, `{"unset":["xFormat.options.lead"]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unset option: %d %s", rec.Code, rec.Body.String())
	}
	// A per-path unset removes the option, not its parent container.
	if f := fields()["stage"]; string(f.XFormat) != `{"options":{},"type":"choice"}` {
		t.Errorf("stage xFormat = %s", f.XFormat)
	}
	// A field id that belongs to another dataset (or nothing) is 404.
	rec = doJSON(t, e, http.MethodPatch, base+"/"+other.DatasetDefId+"/fields/"+title.Id, `{"set":{"name":"X"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("field off another dataset: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "sdk.not_found")
	rec = doJSON(t, e, http.MethodPatch, fieldURL+"nope", `{"set":{"name":"X"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown field: %d %s", rec.Code, rec.Body.String())
	}
}
