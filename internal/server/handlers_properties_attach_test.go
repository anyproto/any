package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/dataview"
)

// objectTypes reads an object's any.types through the properties GET.
func objectTypes(t *testing.T, e http.Handler, spaceId, objectId string) []string {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/properties/"+objectId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get properties: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Record struct {
			Any struct {
				Types []string `json:"types"`
			} `json:"any"`
		} `json:"record"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode properties: %v\nraw=%s", err, rec.Body.String())
	}
	return resp.Record.Any.Types
}

func hasType(types []string, want string) bool {
	for _, tp := range types {
		if tp == want {
			return true
		}
	}
	return false
}

// TestServer_PropertiesAttachDetach exercises runtime type binding on
// an existing object: attach is idempotent, detach removes the binding,
// and detaching leaves the type's records as orphan data rather than
// deleting them.
func TestServer_PropertiesAttachDetach(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"AttachTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}

	attachURL := fmt.Sprintf("/v1/spaces/%s/properties/%s/attach/%s", sp.Id, obj.ObjectId, dataview.TypeId)
	detachURL := fmt.Sprintf("/v1/spaces/%s/properties/%s/detach/%s", sp.Id, obj.ObjectId, dataview.TypeId)

	if types := objectTypes(t, e, sp.Id, obj.ObjectId); hasType(types, dataview.TypeId) {
		t.Fatalf("fresh object already carries %s: %v", dataview.TypeId, types)
	}

	// Attach twice — $addToSet, so the second call is a no-op.
	for i := range 2 {
		rec = doJSON(t, e, http.MethodPost, attachURL, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("attach #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
		if res := decodeModifyResult(t, rec.Body.Bytes()); res.VersionId == "" {
			t.Errorf("attach #%d: empty versionId in %+v", i+1, res)
		}
	}
	types := objectTypes(t, e, sp.Id, obj.ObjectId)
	if !hasType(types, dataview.TypeId) {
		t.Fatalf("type not attached: %v", types)
	}
	count := 0
	for _, tp := range types {
		if tp == dataview.TypeId {
			count++
		}
	}
	if count != 1 {
		t.Errorf("type listed %d times, want 1 (attach must be idempotent): %v", count, types)
	}

	// A record on the now-attached datasets survives detach as orphan
	// data — detaching a type is not a delete.
	ensureDataview(t, e, sp.Id, obj.ObjectId, "default", `{"name": "Table", "pos": "a0"}`)
	createView(t, e, sp.Id, obj.ObjectId, "default", defaultViewPayload)

	rec = doJSON(t, e, http.MethodPost, detachURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detach: %d %s", rec.Code, rec.Body.String())
	}
	if types := objectTypes(t, e, sp.Id, obj.ObjectId); hasType(types, dataview.TypeId) {
		t.Errorf("type still attached after detach: %v", types)
	}
	if got := len(listViews(t, e, sp.Id, obj.ObjectId)); got != 1 {
		t.Errorf("view count after detach = %d, want 1 (records are read-tolerant orphans)", got)
	}

	// Detach again — $pull, idempotent.
	rec = doJSON(t, e, http.MethodPost, detachURL, "")
	if rec.Code != http.StatusOK {
		t.Errorf("second detach: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_PropertiesAttachToTypeObject: a type object is an object,
// so views can be hosted on one. This is what "views on a type" relies
// on — AttachType has no meta-type guard.
func TestServer_PropertiesAttachToTypeObject(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"TypeHost"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Task","xKey":"task"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/attach/%s", sp.Id, typeResp.TypeId, dataview.TypeId), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("attach to type object: %d %s", rec.Code, rec.Body.String())
	}

	ensureDataview(t, e, sp.Id, typeResp.TypeId, "default", `{"name": "Table", "pos": "a0"}`)
	createView(t, e, sp.Id, typeResp.TypeId, "default", defaultViewPayload)
	v := getView(t, e, sp.Id, typeResp.TypeId, "default")
	if v.Name != "All" {
		t.Errorf("view on type object round-tripped wrong: %+v", v)
	}
}

// TestServer_PropertiesAttachUnknownObject: the object must exist —
// attach reads its row first, so an unknown id is a 404, not a
// silently-created object.
func TestServer_PropertiesAttachUnknownObject(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"AttachErr"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/properties/%s/attach/%s", sp.Id, "not-an-object", dataview.TypeId), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("attach to a bogus object id: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "object.not_found" {
		t.Errorf("code = %q, want object.not_found", got)
	}
}
