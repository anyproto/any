package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// objectMembers reads an object's one type and its collections through
// the properties GET.
func objectMembers(t *testing.T, e http.Handler, spaceId, objectId string) (typeId string, collections []string) {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/properties/"+objectId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get properties: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Record struct {
			Any struct {
				Type        string   `json:"type"`
				Collections []string `json:"collections"`
			} `json:"any"`
		} `json:"record"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode properties: %v\nraw=%s", err, rec.Body.String())
	}
	return resp.Record.Any.Type, resp.Record.Any.Collections
}

// objectCollections is the collections half of objectMembers.
func objectCollections(t *testing.T, e http.Handler, spaceId, objectId string) []string {
	t.Helper()
	_, cols := objectMembers(t, e, spaceId, objectId)
	return cols
}

// mustCreateCollection creates a user collection and returns its id.
func mustCreateCollection(t *testing.T, e http.Handler, spaceId, body string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/collections", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var out api.CollectionsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.CollectionId == "" {
		t.Fatalf("decode collection: %v %s", err, rec.Body.String())
	}
	return out.CollectionId
}

// TestServer_PropertiesSetType exercises the one-type slot on an
// existing object: setting replaces, a second type replaces the first,
// and unsetting clears it while the old type's records stay as orphan
// data — retyping is not a delete.
func TestServer_PropertiesSetType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "SetTypeTest")

	obj := mustCreateObject(t, e, sp.Id, `{}`)
	base := "/v1/spaces/" + sp.Id + "/properties/" + obj
	if got, _ := objectMembers(t, e, sp.Id, obj); got != "" {
		t.Fatalf("fresh object already typed: %q", got)
	}

	// Set twice — $set, so the second call is a no-op.
	for i := range 2 {
		rec := doJSON(t, e, http.MethodPost, base+"/type/"+dataview.TypeId, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("set type #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
		if res := decodeModifyResult(t, rec.Body.Bytes()); res.VersionId == "" {
			t.Errorf("set type #%d: empty versionId in %+v", i+1, res)
		}
	}
	if got, _ := objectMembers(t, e, sp.Id, obj); got != dataview.TypeId {
		t.Fatalf("any.type = %q, want %s", got, dataview.TypeId)
	}

	// Records on the type's datasets survive a retype as orphan data.
	ensureDataview(t, e, sp.Id, obj, "default", `{"name": "Table", "pos": "a0"}`)
	createView(t, e, sp.Id, obj, "default", defaultViewPayload)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Task","xKey":"task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var task api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &task)

	if rec = doJSON(t, e, http.MethodPost, base+"/type/"+task.TypeId, ""); rec.Code != http.StatusOK {
		t.Fatalf("retype: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := objectMembers(t, e, sp.Id, obj); got != task.TypeId {
		t.Fatalf("retype left any.type = %q", got)
	}
	if got := len(listViews(t, e, sp.Id, obj)); got != 1 {
		t.Errorf("view count after retype = %d, want 1 (records are read-tolerant orphans)", got)
	}

	// Unset twice — idempotent.
	for i := range 2 {
		rec = doJSON(t, e, http.MethodDelete, base+"/type", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("unset type #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if got, _ := objectMembers(t, e, sp.Id, obj); got != "" {
		t.Fatalf("any.type after unset = %q", got)
	}
}

// TestServer_PropertiesCollectionBinding: a collection is $addToSet, so
// attach is idempotent and detach removes only that membership; values
// in the collection's namespace survive it.
func TestServer_PropertiesCollectionBinding(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CollectionBinding")

	shelf := mustCreateCollection(t, e, sp.Id, `{"name":"Shelf","xKey":"shelf"}`)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/collections/"+shelf+"/properties",
		`{"xKey":"note","name":"Note","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add collection property: %d %s", rec.Code, rec.Body.String())
	}
	var prop api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &prop)

	obj := mustCreateObject(t, e, sp.Id, `{}`)
	url := "/v1/spaces/" + sp.Id + "/properties/" + obj + "/collections/" + shelf

	for i := range 2 {
		if rec = doJSON(t, e, http.MethodPost, url, ""); rec.Code != http.StatusOK {
			t.Fatalf("attach #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	cols := objectCollections(t, e, sp.Id, obj)
	if countOf(cols, shelf) != 1 {
		t.Fatalf("collections = %v, want %s once (attach is idempotent)", cols, shelf)
	}

	// The membership admits writes to the collection's columns.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/properties/"+obj+"/set/"+shelf,
		`{"patch":{"`+prop.PropId+`":"kept"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set a collection property: %d %s", rec.Code, rec.Body.String())
	}

	for i := range 2 {
		if rec = doJSON(t, e, http.MethodDelete, url, ""); rec.Code != http.StatusOK {
			t.Fatalf("detach #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if cols = objectCollections(t, e, sp.Id, obj); slices.Contains(cols, shelf) {
		t.Errorf("still filed after detach: %v", cols)
	}
	// Detaching is not a delete: the values stay as orphan data.
	if row := propertiesRecord(t, e, sp.Id, obj); row[shelf] == nil {
		t.Errorf("detach dropped the namespace: %v", row)
	}
}

// The two slots refuse each other's ids: a collection cannot be the
// object's type and a type cannot be a collection it is filed under.
func TestServer_PropertiesSlotsAreTyped(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "SlotGuards")

	shelf := mustCreateCollection(t, e, sp.Id, `{"name":"Shelf","xKey":"shelf"}`)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Task","xKey":"task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var task api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &task)

	obj := mustCreateObject(t, e, sp.Id, `{}`)
	base := "/v1/spaces/" + sp.Id + "/properties/" + obj

	for name, tc := range map[string]struct {
		method, path string
		status       int
		code         string
	}{
		"collection in the type slot": {http.MethodPost, base + "/type/" + shelf, http.StatusBadRequest, "type.not_a_type"},
		"type in the collection slot": {http.MethodPost, base + "/collections/" + task.TypeId, http.StatusBadRequest, "collection.not_a_collection"},
		"unknown type":                {http.MethodPost, base + "/type/no_such_type", http.StatusNotFound, "type.not_found"},
		"unknown collection":          {http.MethodPost, base + "/collections/no_such_collection", http.StatusNotFound, "collection.not_found"},
	} {
		rec = doJSON(t, e, tc.method, tc.path, "")
		if rec.Code != tc.status {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body.String(), tc.status)
			continue
		}
		if got := errEnvCode(t, rec.Body.Bytes()); got != tc.code {
			t.Errorf("%s: code = %q, want %q", name, got, tc.code)
		}
	}
	// Nothing refused reached the row.
	if tp, cols := objectMembers(t, e, sp.Id, obj); tp != "" || len(cols) != 0 {
		t.Fatalf("a refused id reached the row: type=%q collections=%v", tp, cols)
	}

	// Detach is the repair path for an id that is already on the row, so
	// it pre-flights nothing.
	if rec = doJSON(t, e, http.MethodDelete, base+"/collections/no_such_collection", ""); rec.Code != http.StatusOK {
		t.Errorf("detach of an unknown collection: %d %s, want 200 (repair path)", rec.Code, rec.Body.String())
	}
}

// A definition object is an object: a dataview can be hosted on one,
// which is what "views on a type" relies on.
func TestServer_PropertiesDataviewHostsADefinition(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "TypeHost")

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Task","xKey":"task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	dv := mustCreateObject(t, e, sp.Id, fmt.Sprintf(`{"type":%q,"initialProperties":{%q:{%q:%q}}}`,
		dataview.TypeId, dataview.TypeId, dataview.PropHost, typeResp.TypeId))
	ensureDataview(t, e, sp.Id, dv, "default", `{"name": "Table", "pos": "a0"}`)
	createView(t, e, sp.Id, dv, "default", defaultViewPayload)
	if v := getView(t, e, sp.Id, dv, "default"); v.Name != "All" {
		t.Errorf("view on a type's dataview round-tripped wrong: %+v", v)
	}
	if row := propertiesRecord(t, e, sp.Id, dv); row[dataview.TypeId].(map[string]any)[dataview.PropHost] != typeResp.TypeId {
		t.Errorf("host not stored: %v", row[dataview.TypeId])
	}
}

// TestServer_PropertiesSetTypeUnknownObject: the object must exist —
// the write reads its row first, so an unknown id is a 404, not a
// silently-created object.
func TestServer_PropertiesSetTypeUnknownObject(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "AttachErr")

	rec := doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/type/%s", sp.Id, "not-an-object", dataview.TypeId), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("set type on a bogus object id: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "object.not_found" {
		t.Errorf("code = %q, want object.not_found", got)
	}
}
