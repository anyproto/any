package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_TypeCreate_XKeyValidation covers the server-side xKey rule
// for POST /v1/spaces/:spaceId/types: an xKey is required (it's the only
// human handle a type resolves by besides its CID) and must be unique
// within the space — colliding with an existing type's xKey OR its id.
func TestServer_TypeCreate_XKeyValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"XKeyDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	typesURL := "/v1/spaces/" + sp.Id + "/types"

	// 1. Name-only create is rejected.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Pages"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("name-only create: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_required" {
		t.Errorf("name-only code = %q, want type.xkey_required", code)
	}

	// 2. With an xKey it succeeds.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Pages","xKey":"pages"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with xKey: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Re-using the same xKey collides — even with a different name.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"Other","xKey":"pages"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate xKey: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_conflict" {
		t.Errorf("duplicate xKey code = %q, want type.xkey_conflict", code)
	}

	// 4. An xKey shadowing a built-in type's literal id also collides
	//    (built-ins carry xKey "" but resolve by id, e.g. "chat").
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"NotChat","xKey":"chat"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("builtin-id xKey: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_conflict" {
		t.Errorf("builtin-id xKey code = %q, want type.xkey_conflict", code)
	}
}

// TestServer_TypeProperties_NotFound covers the existence check on
// GET /v1/spaces/:spaceId/types/:typeId/properties: an unknown typeId
// is a typed 404, never a silent 200 [] (the SDK's Properties returns
// an empty slice for unknown ids, indistinguishable from "type exists,
// no properties yet" — the server disambiguates).
func TestServer_TypeProperties_NotFound(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"PropsDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// 1. Unknown type id → 404 type.not_found.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/bafynope/properties", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown type: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.not_found" {
		t.Errorf("unknown type code = %q, want type.not_found", code)
	}

	// 2. A real type with no properties yet still answers 200 [].
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Pages","xKey":"pages"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+created.TypeId+"/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty type: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var props api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &props); err != nil {
		t.Fatalf("decode properties: %v", err)
	}
	if len(props.Properties) != 0 {
		t.Errorf("empty type properties = %v, want []", props.Properties)
	}
}

// TestServer_BuiltinPageType covers the built-in `page` marker type:
// it is present in every space (registered, not created), resolves by
// its literal id with an empty property list, accepts objects typed
// under it, and its id is fenced off from user xKeys.
func TestServer_BuiltinPageType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"PageDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// 1. GET /types lists page with BuiltIn=true.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode types: %v", err)
	}
	var found *api.TypeInfo
	for i := range list.Types {
		if list.Types[i].Id == "page" {
			found = &list.Types[i]
		}
	}
	if found == nil {
		t.Fatalf("page type missing from list: %s", rec.Body.String())
	}
	if !found.BuiltIn || found.Name != "Page" {
		t.Errorf("page entry = %+v, want BuiltIn=true Name=Page", *found)
	}

	// 2. Resolves by literal id; declares no properties.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/page", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get page type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/page/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("page properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var props api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &props); err != nil {
		t.Fatalf("decode properties: %v", err)
	}
	if len(props.Properties) != 0 {
		t.Errorf("page properties = %v, want []", props.Properties)
	}

	// 3. Objects can be created typed page; labels ride the built-in
	// `any.tags` property (page itself declares none).
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types":["page"],"initialProperties":{"any":{"name":"My page","tags":["draft","idea"]}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page object: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"any.types":"page","any.tags":"draft"},"limit":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query pages by tag: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var qresp api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &qresp); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qresp.Records) != 1 {
		t.Errorf("pages tagged draft = %d records, want 1; body=%s", len(qresp.Records), rec.Body.String())
	}

	// 4. The literal id is fenced off from user xKeys.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"NotPage","xKey":"page"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("xKey page: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code != "type.xkey_conflict" {
		t.Errorf("xKey page code = %q, want type.xkey_conflict", code)
	}
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error.Code
}
