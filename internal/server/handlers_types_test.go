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

	// 5. The meta-type's id is reserved the same way — it is a catalog
	//    row (`type`), so a user type cannot claim it.
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"NotMeta","xKey":"type"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("meta-type xKey: status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestServer_MetaTypeCatalogAndXKey pins the SYN-173 wire surface: the
// meta-type is a catalog row with one `xkey` property, a created type's
// xKey round-trips through TypeInfo, and the raw row carries it under
// the meta-type namespace instead of `any`.
func TestServer_MetaTypeCatalogAndXKey(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"MetaDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Movie","xKey":"movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	// Catalog: the meta-type is present and reports its own id as xKey.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode types: %v", err)
	}
	var sawMeta, sawMovie bool
	for _, ti := range list.Types {
		switch ti.Id {
		case "type":
			sawMeta = true
			if !ti.BuiltIn || ti.XKey != "type" {
				t.Errorf("meta-type row = %+v, want builtIn with xKey %q", ti, "type")
			}
		case created.TypeId:
			sawMovie = true
			if ti.XKey != "movie" {
				t.Errorf("created type xKey = %q, want movie", ti.XKey)
			}
		}
	}
	if !sawMeta || !sawMovie {
		t.Fatalf("catalog missing rows: meta=%v movie=%v", sawMeta, sawMovie)
	}

	// The meta-type describes type objects: one `xkey` property.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/type/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("meta-type properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var props api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &props); err != nil {
		t.Fatalf("decode properties: %v", err)
	}
	if len(props.Properties) != 1 || props.Properties[0].Id != "xkey" {
		t.Fatalf("meta-type properties = %+v, want a single xkey entry", props.Properties)
	}

	// Raw row: xkey under the meta-type namespace, name still universal.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/properties/"+created.TypeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read type row: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var row struct {
		Record struct {
			Any  map[string]any `json:"any"`
			Type map[string]any `json:"type"`
		} `json:"record"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if got := row.Record.Type["xkey"]; got != "movie" {
		t.Errorf("record.type.xkey = %v, want movie", got)
	}
	if _, stale := row.Record.Any["xkey"]; stale {
		t.Errorf("record.any.xkey must be gone, row=%+v", row.Record.Any)
	}
	if got := row.Record.Any["name"]; got != "Movie" {
		t.Errorf("record.any.name = %v, want Movie", got)
	}
}

// TestServer_BuiltinTypesReportXKey pins the xKey every built-in
// resolves by. Built-ins carry no stored xKey — the SDK synthesizes
// their TypeInfo — so the handle comes from typeInfoToAPI's id
// backfill, and `nav` from its own short-circuit. A regression here is
// silent: clients resolve types by xKey, so an empty one makes a
// built-in unreachable by name.
func TestServer_BuiltinTypesReportXKey(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"XKeyBuiltins"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// A user type alongside them, to prove the backfill doesn't reach
	// past the built-ins.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Movie","xKey":"movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode types: %v", err)
	}

	builtins := 0
	for _, ti := range list.Types {
		if !ti.BuiltIn {
			if ti.Id != created.TypeId || ti.XKey != "movie" {
				t.Errorf("user type = %+v, want the caller-set xKey", ti)
			}
			continue
		}
		builtins++
		if ti.XKey != ti.Id {
			t.Errorf("built-in %q xKey = %q, want the id", ti.Id, ti.XKey)
		}
	}
	// any + spaceIndex + type + every registered type.
	if builtins < 4 {
		t.Fatalf("only %d built-ins in the catalog, want the full set: %+v", builtins, list.Types)
	}

	// The single-type reads: the shared mapper, plus nav's hardcoded
	// short-circuit, which bypasses it entirely.
	for _, id := range []string{"any", "spaceIndex", "type", "chat", "editor", "page", "nav"} {
		rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+id, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET /types/%s: status=%d body=%s", id, rec.Code, rec.Body.String())
			continue
		}
		var ti api.TypeInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &ti); err != nil {
			t.Errorf("decode %s: %v", id, err)
			continue
		}
		if !ti.BuiltIn || ti.XKey != id {
			t.Errorf("GET /types/%s = %+v, want builtIn with xKey %q", id, ti, id)
		}
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

// TestServer_AddPropertyXKind covers `xKind` on the create path: the
// free-form classification hint rides POST …/properties, round-trips
// through GET …/properties, and stays freely mutable via PATCH.
//
// It exists so a client-side kind marker does not have to be smuggled
// through `xKey`. `xKey` is the stable handle a caller addresses the
// property by; a marker there makes every property of that kind share
// one key, so the two properties below — both multiselects — would be
// indistinguishable to any consumer resolving by xKey.
func TestServer_AddPropertyXKind(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"XKindDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Company","xKey":"company"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	propsURL := "/v1/spaces/" + sp.Id + "/types/" + created.TypeId + "/properties"

	// Two multiselects on one type: distinct slug xKeys, the same marker.
	for _, body := range []string{
		`{"name":"Categories","xKey":"categories","xKind":"tags",` +
			`"kind":"array","format":{"type":"multiselect"}}`,
		`{"name":"Focus","xKey":"focus","xKind":"tags",` +
			`"kind":"array","format":{"type":"multiselect"}}`,
	} {
		rec = doJSON(t, e, http.MethodPost, propsURL, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add property %s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
	}

	// A property that declares no xKind reads back empty — the field is
	// optional, not defaulted from anything.
	rec = doJSON(t, e, http.MethodPost, propsURL, `{"name":"Domain","xKey":"domain","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add plain property: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var plain api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatalf("decode plain prop: %v", err)
	}

	byXKey := func() map[string]api.PropertyDef {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, propsURL, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list properties: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var list api.PropertiesListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode properties: %v", err)
		}
		out := make(map[string]api.PropertyDef, len(list.Properties))
		for _, p := range list.Properties {
			out[p.XKey] = p
		}
		return out
	}

	props := byXKey()
	for _, xkey := range []string{"categories", "focus"} {
		p, ok := props[xkey]
		if !ok {
			t.Fatalf("property %q missing from %v", xkey, props)
		}
		if p.XKind != "tags" {
			t.Errorf("property %q xKind = %q, want tags", xkey, p.XKind)
		}
	}
	if p := props["domain"]; p.XKind != "" {
		t.Errorf("plain property xKind = %q, want empty", p.XKind)
	}
	if props["categories"].Id == props["focus"].Id {
		t.Fatal("the two multiselects collapsed onto one property")
	}

	// xKind stays freely mutable, like name / description / xKey.
	rec = doJSON(t, e, http.MethodPatch, propsURL+"/"+plain.PropId, `{"set":{"xKind":"url"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch xKind: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if p := byXKey()["domain"]; p.XKind != "url" {
		t.Errorf("patched xKind = %q, want url", p.XKind)
	}
}
