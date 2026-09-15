package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_TypesAndPropertiesFlow mirrors
// any-sync-sdk/sdk_test.go: TestSDK_TypesAndProperties as an HTTP-level
// walkthrough through the live echo router.
func TestServer_TypesAndPropertiesFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// 1. Create a space.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"MovieDB"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	if sp.Id == "" {
		t.Fatal("space id empty")
	}

	// 2. Create a type.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Movie","description":"A film","xKey":"movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	typeId := typeResp.TypeId
	if typeId == "" {
		t.Fatal("typeId empty")
	}

	// 3. Add a property to the type.
	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, typeId),
		`{"name":"Title","kind":"string","xKey":"title"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../types/.../properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var propResp api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &propResp); err != nil {
		t.Fatalf("decode prop: %v", err)
	}
	propId := propResp.PropId
	if propId == "" {
		t.Fatal("propId empty")
	}

	// 4. Create an object bound to the type.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"type":%q}`, typeId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../objects: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	objectId := objResp.ObjectId
	if objectId == "" {
		t.Fatal("objectId empty")
	}

	// 5. Set a base-scope property value.
	setBaseURL := fmt.Sprintf("/v1/spaces/%s/properties/%s/set/%s", sp.Id, objectId, typeId)
	rec = doJSON(t, e, http.MethodPost, setBaseURL,
		fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, propId))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST .../set/:typeId: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var modify api.ModifyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &modify); err != nil {
		t.Fatalf("decode modify: %v", err)
	}
	if modify.VersionId == "" {
		t.Errorf("expected non-empty versionId, got %+v", modify)
	}
	if modify.ChangeId == "" {
		t.Errorf("expected non-empty changeId, got %+v", modify)
	}
	if modify.RecordIds == nil {
		t.Errorf("expected non-nil recordIds, got %+v", modify)
	}

	// 6. Read the property record back.
	rec = doJSON(t, e, http.MethodGet,
		fmt.Sprintf("/v1/spaces/%s/properties/%s", sp.Id, objectId), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../properties/:objectId: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.PropertiesGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}

	// Decode the record and verify the value landed under {typeId}.{propId}.
	var record map[string]any
	if err := json.Unmarshal(got.Record, &record); err != nil {
		t.Fatalf("decode record: %v: %s", err, string(got.Record))
	}
	tnode, ok := record[typeId].(map[string]any)
	if !ok {
		t.Fatalf("record missing %q: %v", typeId, record)
	}
	if v, ok := tnode[propId]; !ok || v != "Casablanca" {
		t.Errorf("record[%s][%s] = %v, want Casablanca", typeId, propId, v)
	}

	// any.type should be the typeId we passed at creation time.
	anyNode, ok := record["any"].(map[string]any)
	if !ok {
		t.Fatalf("record.any missing or wrong shape: %v", record)
	}
	if anyNode["type"] != typeId {
		t.Errorf("record.any.type = %v, want %q", anyNode["type"], typeId)
	}

	// 7. List types — built-in `any` first, then the Movie type.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var typesList api.TypesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typesList); err != nil {
		t.Fatalf("decode types list: %v", err)
	}
	if len(typesList.Types) < 2 {
		t.Fatalf("types list len = %d, want >= 2: %+v", len(typesList.Types), typesList.Types)
	}
	if typesList.Types[0].Id != "any" || !typesList.Types[0].BuiltIn {
		t.Errorf("first type = %+v, want id=any builtIn=true", typesList.Types[0])
	}
	var sawMovie bool
	for _, ti := range typesList.Types {
		if ti.Id == typeId && ti.Name == "Movie" {
			sawMovie = true
			break
		}
	}
	if !sawMovie {
		t.Errorf("Movie type not in list: %+v", typesList.Types)
	}

	// 8. List `any` built-in properties.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/any/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../types/any/properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var anyProps api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &anyProps); err != nil {
		t.Fatalf("decode any props: %v", err)
	}
	wantBuiltins := []string{"name", "description", "icon", "type", "collections"}
	for _, want := range wantBuiltins {
		var seen bool
		for _, p := range anyProps.Properties {
			if p.Id == want || p.Name == want {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("`any` props missing %q: %+v", want, anyProps.Properties)
		}
	}

	// 9. List user-type properties — should contain Title.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+typeId+"/properties", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../types/:id/properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var userProps api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &userProps); err != nil {
		t.Fatalf("decode user props: %v", err)
	}
	var sawTitle bool
	for _, p := range userProps.Properties {
		if p.Id == propId && p.Name == "Title" && p.Kind == api.PropertyKindString {
			sawTitle = true
			break
		}
	}
	if !sawTitle {
		t.Errorf("Title prop missing: %+v", userProps.Properties)
	}

	// 10. Cross-object query for this object id (the per-object
	//     properties dataset went away when storage moved to a single
	//     per-space `objects` collection — see prompt-3.md).
	queryBody := fmt.Sprintf(`{"filter":{"id":%q},"limit":10}`, objectId)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", queryBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST .../objects/query: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var qresp api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &qresp); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qresp.Records) != 1 {
		t.Errorf("expected 1 record from objects/query by id, got %d", len(qresp.Records))
	}

	// 11. Delete the object.
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+sp.Id+"/objects/"+objectId, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE .../objects/:id: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A second delete should map to 404 sdk.not_found.
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+sp.Id+"/objects/"+objectId, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE: status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 404 envelope: %v", err)
	}
	if env.Error.Code != "sdk.not_found" {
		t.Errorf("second DELETE code = %q, want sdk.not_found", env.Error.Code)
	}
}

// TestServer_CrossObjectQueryFlow exercises the per-space objects/query
// endpoint. Mirrors the cross-object section of the SDK's
// TestSDK_TypesAndProperties: two Movie objects, then queries against
// the per-space `objects` collection.
func TestServer_CrossObjectQueryFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Space.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"MovieDB"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	// Type.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Movie","xKey":"movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	typeId := tr.TypeId

	// Property.
	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, typeId),
		`{"name":"Title","kind":"string","xKey":"title"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add prop: %d %s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	propId := pr.PropId

	createObject := func(title string) string {
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
			fmt.Sprintf(`{"type":%q}`, typeId))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
		}
		var o api.ObjectsCreateResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &o)
		rec = doJSON(t, e, http.MethodPost,
			fmt.Sprintf("/v1/spaces/%s/properties/%s/set/%s", sp.Id, o.ObjectId, typeId),
			fmt.Sprintf(`{"patch":{%q:%q}}`, propId, title))
		if rec.Code != http.StatusOK {
			t.Fatalf("set base: %d %s", rec.Code, rec.Body.String())
		}
		return o.ObjectId
	}

	// 4-8 (per prompt): two Movie objects with different titles.
	objId1 := createObject("Casablanca")
	objId2 := createObject("Vertigo")

	queryObjects := func(t *testing.T, body string) []json.RawMessage {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST .../objects/query: %d %s", rec.Code, rec.Body.String())
		}
		var resp api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return resp.Records
	}

	// 9. No filter: at least the two Movie rows. (The SDK may also surface
	//    the type row here per the architecture note in prompt-3.md.)
	all := queryObjects(t, `{"limit":50}`)
	if len(all) < 2 {
		t.Errorf("unfiltered objects/query: %d rows, want >= 2", len(all))
	}

	// 10. Filter by id → exactly one Vertigo row.
	byId := queryObjects(t, fmt.Sprintf(`{"filter":{"id":%q}}`, objId2))
	if len(byId) != 1 {
		t.Fatalf("by-id: %d rows, want 1; bodies=%v", len(byId), byId)
	}
	var idRec map[string]any
	if err := json.Unmarshal(byId[0], &idRec); err != nil {
		t.Fatalf("decode by-id: %v", err)
	}
	gotID, _ := idRec["id"].(string)
	if gotID != objId2 {
		t.Errorf("by-id record id = %v, want %v", gotID, objId2)
	}
	if tn, ok := idRec[typeId].(map[string]any); !ok || tn[propId] != "Vertigo" {
		t.Errorf("by-id record %s.%s = %v, want Vertigo (record=%v)",
			typeId, propId, tn[propId], idRec)
	}

	// 11. Filter by property path → only the Casablanca row.
	byTitle := queryObjects(t, fmt.Sprintf(`{"filter":{%q:%q}}`,
		typeId+"."+propId, "Casablanca"))
	if len(byTitle) != 1 {
		t.Fatalf("by-title: %d rows, want 1; bodies=%v", len(byTitle), byTitle)
	}
	var titleRec map[string]any
	_ = json.Unmarshal(byTitle[0], &titleRec)
	gotTitleID, _ := titleRec["id"].(string)
	if gotTitleID != objId1 {
		t.Errorf("by-title record id = %v, want %v", gotTitleID, objId1)
	}

	// 12. includeTotal drives total + hasNext. The unfiltered set holds
	//     at least the two Movie rows (asserted above), so a limit-1 page
	//     leaves more behind, while a page wide enough to cover everything
	//     reports hasNext=false. Both fields are gated on includeTotal —
	//     omitted (nil pointers) when the caller doesn't ask.
	queryFull := func(t *testing.T, body string) api.QueryResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST .../objects/query: %d %s", rec.Code, rec.Body.String())
		}
		var resp api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		return resp
	}

	// First page of one: more matches remain.
	page := queryFull(t, `{"limit":1,"includeTotal":true}`)
	if page.Total == nil || page.HasNext == nil {
		t.Fatalf("includeTotal page: total=%v hasNext=%v, want both populated", page.Total, page.HasNext)
	}
	if *page.Total < 2 {
		t.Fatalf("includeTotal page: total=%d, want >= 2", *page.Total)
	}
	if len(page.Records) != 1 {
		t.Fatalf("includeTotal page: %d records, want 1", len(page.Records))
	}
	if !*page.HasNext {
		t.Errorf("limit-1 page over %d matches: hasNext=false, want true", *page.Total)
	}

	// A page wide enough to cover every match: nothing left.
	full := queryFull(t, `{"limit":1000,"includeTotal":true}`)
	if full.Total == nil || full.HasNext == nil {
		t.Fatalf("full page: total=%v hasNext=%v, want both populated", full.Total, full.HasNext)
	}
	if len(full.Records) != *full.Total {
		t.Errorf("full page: %d records vs total %d, want equal", len(full.Records), *full.Total)
	}
	if *full.HasNext {
		t.Errorf("page covering all %d matches: hasNext=true, want false", *full.Total)
	}

	// Without includeTotal both fields are omitted from the wire (nil).
	bare := queryFull(t, `{"limit":1}`)
	if bare.Total != nil || bare.HasNext != nil {
		t.Errorf("no includeTotal: total=%v hasNext=%v, want both nil", bare.Total, bare.HasNext)
	}
}

// TestServer_TypeGet_NotFound covers the 404 type.not_found path on
// Types.Get when the id resolves to an object that isn't tagged as a
// type.
func TestServer_TypeGet_NotFound(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"X"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	// Create a regular object — exists but isn't a type.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+obj.ObjectId, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "type.not_found" {
		t.Errorf("code = %q, want type.not_found", env.Error.Code)
	}
}

// TestServer_AddProperty_BadKind asserts the property-kind allowlist is
// enforced server-side with 400 + request.schema rather than reaching
// the SDK.
func TestServer_AddProperty_BadKind(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Need a space + type to exercise the route.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"Demo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &sp)

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"T","xKey":"t"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)

	// kind: "blob" — not in the allowlist.
	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, tr.TypeId),
		`{"name":"X","kind":"blob"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "request.schema" {
		t.Errorf("code = %q, want request.schema", env.Error.Code)
	}
}
