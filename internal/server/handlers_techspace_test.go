package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

const scratchDatasets = `[{
	"name": "entries",
	"idRule": "user",
	"idPattern": "^(any://o/.+|f:[A-Za-z0-9_-]{1,64})$",
	"idMaxLen": 256,
	"fields": [
		{"key": "title", "kind": "string", "required": true},
		{"key": "parentId", "kind": "string", "mutableBy": "any"},
		{"key": "creator", "stamp": "creator"},
		{"key": "createdAt", "stamp": "createTime"}
	]
}]`

// TestServer_TechSpace pins the tech space as a :spaceId: reachable
// for bundles, type reads, dataset declarations and records on bundle
// roots; every other per-space family refused with space.unsupported;
// the index object readable through the allowlist only.
func TestServer_TechSpace(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)
	if acc.TechSpaceId == "" {
		t.Fatal("account must expose techSpaceId")
	}
	tech := acc.TechSpaceId
	base := "/v1/spaces/" + tech

	var info api.SpaceInfo
	decodeGet(t, e, base, &info)
	if info.Id != tech || info.SpaceType != "any.techspace" {
		t.Fatalf("tech space info: %+v", info)
	}
	if info.SpaceIndexObjectId == "" {
		t.Fatal("tech space info must carry the index object id")
	}

	// Never in the list.
	var list api.SpaceListResponse
	decodeGet(t, e, "/v1/spaces", &list)
	for _, si := range list.Spaces {
		if si.Id == tech {
			t.Fatal("tech space listed")
		}
	}

	// Refused families: one code.
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, base + "/objects", `{}`},
		{http.MethodGet, base + "/members", ""},
		{http.MethodPatch, base, `{"name":"x"}`},
		{http.MethodDelete, base, ""},
		{http.MethodPost, base + "/types", `{"name":"T"}`},
		{http.MethodGet, base + "/files", ""},
		{http.MethodPost, base + "/search", `{"query":"x"}`},
		{http.MethodPatch, base + "/settings", `{"notifyMode":"all"}`},
	} {
		rec := doJSON(t, e, rt.method, rt.path, rt.body)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: want 405, got %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != codeSpaceUnsupported {
			t.Fatalf("%s %s: code %q", rt.method, rt.path, env.Error.Code)
		}
	}

	// Bundles: derived-only with datasets.
	rec := doJSON(t, e, http.MethodPost, base+"/bundles", `{"id":"notes/v1","derived":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ensure without datasets: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/bundles", `{"id":"notes/v1","derived":true,"rootTypes":["any"],"datasets":`+scratchDatasets+`}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rootTypes on the tech space: %d %s", rec.Code, rec.Body.String())
	}
	ens := ensureBundle(t, e, tech, `{"id":"notes/v1","name":"Favorites","derived":true,"datasets":`+scratchDatasets+`}`)
	if !ens.Installed || !ens.Bundle.Derived || ens.Bundle.RootId == "" {
		t.Fatalf("ensure: %+v", ens)
	}
	root := ens.Bundle.RootId
	again := ensureBundle(t, e, tech, `{"id":"notes/v1","derived":true,"datasets":`+scratchDatasets+`}`)
	if again.Installed || again.Bundle.RootId != root {
		t.Fatalf("re-ensure must adopt: %+v", again)
	}

	// The root is a type implementing itself; the declaration is
	// discoverable under typeId = rootId.
	var obj api.ObjectGetResponse
	decodeGet(t, e, base+"/objects/"+root, &obj)
	var row struct {
		Any struct {
			Types []string `json:"types"`
		} `json:"any"`
	}
	if err := json.Unmarshal(obj.Record, &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	var marker, self bool
	for _, ty := range row.Any.Types {
		marker = marker || ty == "__type__"
		self = self || ty == root
	}
	if !marker || !self {
		t.Fatalf("root types %v: want __type__ and %s", row.Any.Types, root)
	}
	var defs api.TypeDatasetsListResponse
	decodeGet(t, e, base+"/types/"+root+"/datasets", &defs)
	if len(defs.Datasets) != 1 || defs.Datasets[0].Name != "entries" {
		t.Fatalf("datasets on root: %+v", defs)
	}

	// Records through the generic path.
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"entries","records":[
		{"id":"any://o/one","fields":{"title":"One"}},
		{"id":"f:folder","fields":{"title":"Folder"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+root+`","dataset":"entries"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(q.Records) != 2 {
		t.Fatalf("query records: %d", len(q.Records))
	}

	// Evolution through the type routes on the root.
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+root+"/datasets",
		`{"name":"tags","idRule":"user","fields":[{"key":"label","kind":"string","required":true}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset on root: %d %s", rec.Code, rec.Body.String())
	}
	// …but not on the index object.
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+info.SpaceIndexObjectId+"/datasets",
		`{"name":"nope","idRule":"user","fields":[{"key":"label","kind":"string"}]}`)
	if rec.Code == http.StatusCreated {
		t.Fatalf("dataset declared on the index object: %d %s", rec.Code, rec.Body.String())
	}

	// Index object reads: allowlist, guest keys stripped; writes refused.
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("index spaces query: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"identities"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("index identities query must be refused: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"bundles"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("index bundles query: %d %s", rec.Code, rec.Body.String())
	}
	// The withheld guest/invite key fields are neither returned nor
	// filterable (a filter on withheld data is a byte oracle).
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces"}`)
	for _, leak := range []string{`"guestKey"`, `"issuedInviteKeys"`, `"issuedGuestKey"`} {
		if bytes.Contains(rec.Body.Bytes(), []byte(leak)) {
			t.Fatalf("index spaces rows leak %s: %s", leak, rec.Body.String())
		}
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces","filter":{"issuedInviteKeys.member":{"$regex":"^A"}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("filter on withheld field: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces","sort":["-guestKey"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sort on withheld field: %d %s", rec.Code, rec.Body.String())
	}
	// Projection is the third way to name a field, and answers the same
	// refusal — not a 200 with an unexplained hole in the records.
	rec = doJSON(t, e, http.MethodPost, base+"/query", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces","projection":{"guestKey":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("projection on withheld field: %d %s", rec.Code, rec.Body.String())
	}
	// No generic writes to the index tree, whatever the dataset name;
	// no aggregate on a stripped dataset; no write sinks anywhere.
	rec = doJSON(t, e, http.MethodPost, base+"/delete-records", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"entries","recordIds":["x"]}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete-records on the index: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/aggregate", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces","pipeline":[{"$count":"n"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("aggregate on the spaces dataset: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/aggregate", `{"objectId":"`+root+`","dataset":"entries","pipeline":[{"$merge":{"into":"x"}}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("$merge sink must be refused: %d %s", rec.Code, rec.Body.String())
	}
	// A typo'd dataset is a client mistake, not an unsupported surface.
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"entrees","records":[{"id":"f:x","fields":{"title":"t"}}]}`)
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("typo dataset must not read as unsupported: %d %s", rec.Code, rec.Body.String())
	}
	// Loser resolution and phase-2 children are off the tech surface.
	// Resolve is reachable (created installs can fork); the winner is
	// refused as a loser by the verdict, not by the route guard.
	rec = doJSON(t, e, http.MethodPost, base+"/bundles/notes%2Fv1/resolve", `{"loserRootId":"`+root+`"}`)
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("resolve on tech must be routed: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/bundles/notes%2Fv1/children", `{"seed":"s1"}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("children on tech: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/modify", `{"objectId":"`+info.SpaceIndexObjectId+`","dataset":"spaces","records":[{"id":"x","ops":[{"type":"$set","path":"name","value":"nope"}]}]}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("index spaces modify must be refused: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown object: 404; lifecycle refused.
	rec = doJSON(t, e, http.MethodGet, base+"/objects/bafy-no-such", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown object: %d %s", rec.Code, rec.Body.String())
	}
	// DELETE is routed (uninstall of created bundle roots), but a
	// DERIVED root refuses at the object layer — never a 204.
	rec = doJSON(t, e, http.MethodDelete, base+"/objects/"+root, "")
	if rec.Code == http.StatusNoContent || rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("derived root delete: %d %s", rec.Code, rec.Body.String())
	}

	// Sync-status and debug work.
	rec = doJSON(t, e, http.MethodGet, base+"/sync-status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync-status: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, base+"/debug", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("debug: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_ObjectGet pins the single-object read in a regular space:
// the row for a live object, 404 for an unknown id, 410 once deleted.
func TestServer_ObjectGet(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _, objectId := setupSubscribeFixture(t, e)
	var obj api.ObjectGetResponse
	decodeGet(t, e, "/v1/spaces/"+spaceId+"/objects/"+objectId, &obj)
	if obj.ObjectId != objectId || len(obj.Record) == 0 {
		t.Fatalf("object get: %+v", obj)
	}
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/bafy-no-such", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+objectId, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+objectId, "")
	if rec.Code != http.StatusGone {
		t.Fatalf("deleted: want 410, got %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != codeObjectDeleted {
		t.Fatalf("deleted code: %q", env.Error.Code)
	}
}
