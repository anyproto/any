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
	//    (built-ins carry xKey "" but resolve by id, e.g. "data_view").
	rec = doJSON(t, e, http.MethodPost, typesURL, `{"name":"NotChat","xKey":"data_view"}`)
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

// TestServer_MetaTypeCatalogAndXKey pins the meta-type wire surface: the
// meta-type is a catalog row with the `xkey` / `weight` / `layout`
// properties, a created type's
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

	// The meta-type describes type objects: the `xkey` handle, the
	// rendering pair `weight` / `layout`, the `hidden` flag and the
	// `meta` bag.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/type/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("meta-type properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var props api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &props); err != nil {
		t.Fatalf("decode properties: %v", err)
	}
	metaKinds := map[string]string{}
	for _, p := range props.Properties {
		metaKinds[p.Id] = p.Kind
	}
	if len(metaKinds) != 5 || metaKinds["xkey"] != "string" || metaKinds["weight"] != "number" || metaKinds["layout"] != "object" ||
		metaKinds["hidden"] != "boolean" || metaKinds["meta"] != "object" {
		t.Fatalf("meta-type properties = %+v, want xkey/weight/layout/hidden/meta", props.Properties)
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
	for _, id := range []string{"any", "spaceIndex", "type", "data_view", "nav"} {
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

// TestServer_NoBuiltinContentTypes pins that documents and chats are
// not server types any more: `page`, `editor` and `chat` are absent
// from the catalog, unresolvable by id, and their names are free for
// client-registered types (the well-known bundles claim them); the
// editor and chat collections come from a type's part declaring the
// module instead.
func TestServer_NoBuiltinContentTypes(t *testing.T) {
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

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode types: %v", err)
	}
	for _, ti := range list.Types {
		switch ti.Id {
		case "page", "editor", "chat":
			t.Errorf("%s listed as a type: %+v", ti.Id, ti)
		}
	}
	for _, id := range []string{"page", "editor", "chat"} {
		rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+id, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET type %s: status=%d, want 404; body=%s", id, rec.Code, rec.Body.String())
		}
	}

	// The names are ordinary user xKeys now; the type declares the
	// editor module through a part and its objects hold a body.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Page","xKey":"page","weight":10}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("user type with xKey page: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	mustAddPart(t, e, sp.Id, created.TypeId, `{"key":"body","datasets":[{"module":"editor","shared":true}]}`)
	obj := mustCreateObject(t, e, sp.Id, `{"types":["`+created.TypeId+`"],"initialProperties":{"any":{"name":"My page","tags":["draft","idea"]}}}`)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/"+obj+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"body"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("block on the page: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"any.types":"`+created.TypeId+`","any.tags":"draft"},"limit":10}`)
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
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error.Code
}

// TestServer_TypeHiddenAndMeta pins the two type flags: hidden types
// stay out of the default listing and come back with includeHidden
// (GET by id always resolves them; a bundle's self-typed root is
// hidden when its install asks); meta is a per-key scalar bag — create
// takes it whole, PATCH sets and unsets per key, bad keys and values
// are refused at the boundary.
func TestServer_TypeHiddenAndMeta(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "TypeFlags")
	base := "/v1/spaces/" + sp.Id
	rec := doJSON(t, e, http.MethodPost, base+"/types",
		`{"name":"Draft","xKey":"draft","hidden":true,"meta":{"index":"none","rank":3,"beta":true}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	listIds := func(q string) map[string]api.TypeInfo {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, base+"/types"+q, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s: %d %s", q, rec.Code, rec.Body.String())
		}
		var list api.TypesListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		out := map[string]api.TypeInfo{}
		for _, ti := range list.Types {
			out[ti.Id] = ti
		}
		return out
	}
	if _, listed := listIds("")[created.TypeId]; listed {
		t.Error("hidden type listed by default")
	}
	ti, listed := listIds("?includeHidden=true")[created.TypeId]
	if !listed || !ti.Hidden || ti.Meta["index"] != "none" || ti.Meta["rank"] != float64(3) || ti.Meta["beta"] != true {
		t.Fatalf("includeHidden row = %+v (listed=%v)", ti, listed)
	}
	rec = doJSON(t, e, http.MethodGet, base+"/types/"+created.TypeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get hidden type: %d %s", rec.Code, rec.Body.String())
	}

	// Per-key patch: set one, unset one, leave one; unhide.
	rec = doJSON(t, e, http.MethodPatch, base+"/types/"+created.TypeId,
		`{"hidden":false,"meta":{"index":"basic","beta":null}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	ti, listed = listIds("")[created.TypeId]
	if !listed || ti.Hidden {
		t.Fatalf("unhidden type must list by default: %+v listed=%v", ti, listed)
	}
	if ti.Meta["index"] != "basic" || ti.Meta["rank"] != float64(3) || len(ti.Meta) != 2 {
		t.Errorf("meta after per-key patch = %v, want index=basic rank=3", ti.Meta)
	}
	for _, body := range []string{`{"meta":{"a.b":"x"}}`, `{"meta":{"$x":"y"}}`, `{"meta":{"obj":{"k":1}}}`, `{"meta":{"arr":[1]}}`} {
		rec = doJSON(t, e, http.MethodPatch, base+"/types/"+created.TypeId, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("patch %s: %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}

	// A bundle root with parts is hidden when the install asks for it
	// (a records host) and listed otherwise (a type objects carry).
	res := ensureBundle(t, e, sp.Id, `{"id":"notes/v1","name":"Notes","hidden":true,"parts":[{"key":"entries","datasets":[{"key":"entries","idRule":"user","fields":[{"key":"title","kind":"string"}]}]}]}`)
	if _, listed := listIds("")[res.Bundle.RootId]; listed {
		t.Error("hidden bundle root listed as a pickable type")
	}
	if root, ok := listIds("?includeHidden=true")[res.Bundle.RootId]; !ok || !root.Hidden {
		t.Errorf("bundle root = %+v (ok=%v), want hidden", root, ok)
	}
	page := ensureBundle(t, e, sp.Id, `{"id":"page-test/v1","name":"Page","layout":{"type":"page"},"parts":[{"key":"body","datasets":[{"module":"editor","shared":true}]}]}`)
	if root, ok := listIds("")[page.Bundle.RootId]; !ok || root.Hidden {
		t.Errorf("declared type root = %+v (ok=%v), want listed", root, ok)
	}
}
