package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// favoritesEnsureBody is the canonical favorites/v1 install request
// from the client contract (docs/25-favorites.md): a CREATED root
// (Ensure mints and self-types it) carrying the `entries` part; the
// records live in the namespaced collection `<rootId>_entries`.
const favoritesEnsureBody = `{"id":"favorites/v1","name":"Favorites","parts":[{"key":"entries","datasets":[{
	"key": "entries",
	"idRule": "user",
	"idPattern": "^(any://o/.+|f:[A-Za-z0-9_-]{1,64})$",
	"idMaxLen": 256,
	"dynamic": true,
	"fields": [
		{"key": "parentId", "kind": "string", "required": true, "mutableBy": "any"},
		{"key": "pos", "kind": "string", "required": true, "mutableBy": "any"},
		{"key": "removed", "kind": "boolean", "mutableBy": "any"},
		{"key": "name", "kind": "string", "mutableBy": "any"},
		{"key": "iconCid", "kind": "string", "mutableBy": "any"},
		{"key": "types", "kind": "array", "mutableBy": "any"},
		{"key": "creator", "stamp": "creator"},
		{"key": "createdAt", "stamp": "createTime"},
		{"key": "modifiedAt", "stamp": "modifyTime"}
	]
}]}]}`

// TestServer_FavoritesClientFlow pins the favourites client contract:
// locked reads (synced flag), client-registered install on a created
// root, idempotent re-ensure, the declared record rules — id pattern,
// required parentId/pos, stamps, soft-delete round-trip — and additive
// evolution through the type routes.
func TestServer_FavoritesClientFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)
	base := "/v1/spaces/" + acc.TechSpaceId

	// Locked list: the reply says whether absence is definitive.
	rec := doJSON(t, e, http.MethodGet, base+"/bundles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("bundles list: %d %s", rec.Code, rec.Body.String())
	}
	var list api.BundleListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, b := range list.Bundles {
		if b.Id == "favorites/v1" {
			t.Fatalf("fresh account already has favorites: %+v", b)
		}
	}

	// Client-registered install: created root, minted by Ensure.
	ens := ensureBundle(t, e, acc.TechSpaceId, favoritesEnsureBody)
	if !ens.Installed || ens.Bundle.Derived || ens.Bundle.RootId == "" {
		t.Fatalf("install: %+v", ens)
	}
	root := ens.Bundle.RootId
	again := ensureBundle(t, e, acc.TechSpaceId, favoritesEnsureBody)
	if again.Installed || again.Bundle.RootId != root {
		t.Fatalf("re-ensure must adopt: %+v", again)
	}

	// Locked get: wrapper carries the synced flag.
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/favorites%2Fv1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle get: %d %s", rec.Code, rec.Body.String())
	}
	var got api.BundleGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Bundle.RootId != root {
		t.Fatalf("get: %+v", got)
	}

	// Declaration discoverable under typeId = rootId.
	var defs api.TypeDatasetsListResponse
	decodeGet(t, e, base+"/types/"+root+"/datasets", &defs)
	if len(defs.Datasets) != 1 || defs.Datasets[0].Key != "entries" || defs.Datasets[0].Collection != root+"_entries" {
		t.Fatalf("datasets on root: %+v", defs)
	}
	entries := defs.Datasets[0].Collection

	// Star an item (id = link) and create a folder — one upsert.
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"`+entries+`","records":[
		{"id":"any://o/sp1/obj1","fields":{"parentId":"f:aaa","pos":"a1","name":"Doc","iconCid":"bafyicon","types":["page"]}},
		{"id":"f:aaa","fields":{"parentId":"","pos":"a0","name":"Work"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
	}
	var up api.UpsertResult
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upsert: %v", err)
	}
	if len(up.Rejections) != 0 {
		t.Fatalf("upsert rejections: %+v", up.Rejections)
	}

	// Id pattern and required fields are enforced per record.
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"`+entries+`","records":[
		{"id":"not-a-link","fields":{"parentId":"","pos":"a2"}},
		{"id":"f:no-pos","fields":{"parentId":""}}]}`)
	if rec.Code == http.StatusOK {
		var bad api.UpsertResult
		if err := json.Unmarshal(rec.Body.Bytes(), &bad); err != nil {
			t.Fatalf("decode upsert: %v", err)
		}
		if len(bad.Rejections) != 2 {
			t.Fatalf("want 2 rejections, got %+v", bad.Rejections)
		}
	}

	// Read back: stamps present, client mirror fields intact.
	rec = doJSON(t, e, http.MethodPost, base+"/query",
		`{"objectId":"`+root+`","dataset":"`+entries+`","filter":{"id":"any://o/sp1/obj1"}}`)
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil || len(q.Records) != 1 {
		t.Fatalf("query: %v %s", err, rec.Body.String())
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(q.Records[0], &item); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	for _, k := range []string{"creator", "createdAt", "modifiedAt", "name", "iconCid", "types", "parentId", "pos"} {
		if _, ok := item[k]; !ok {
			t.Fatalf("record misses %q: %s", k, q.Records[0])
		}
	}

	// Stamped fields reject client writes.
	rec = doJSON(t, e, http.MethodPost, base+"/modify", `{"objectId":"`+root+`","dataset":"`+entries+`","records":[
		{"id":"any://o/sp1/obj1","ops":[{"type":"$set","path":"creator","value":"me"}]}]}`)
	if rec.Code == http.StatusOK && !hasRejection(rec.Body.Bytes()) {
		t.Fatalf("client write of a stamped field must not apply: %s", rec.Body.String())
	}

	// Un-star = soft delete; re-star = upsert clears it and re-places.
	rec = doJSON(t, e, http.MethodPost, base+"/modify", `{"objectId":"`+root+`","dataset":"`+entries+`","records":[
		{"id":"any://o/sp1/obj1","ops":[{"type":"$set","path":"removed","value":true}]}]}`)
	if rec.Code != http.StatusOK || hasRejection(rec.Body.Bytes()) {
		t.Fatalf("soft delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query",
		`{"objectId":"`+root+`","dataset":"`+entries+`","filter":{"id":"any://o/sp1/obj1"}}`)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"removed":true`)) {
		t.Fatalf("soft delete not applied: %s", rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"`+entries+`","records":[
		{"id":"any://o/sp1/obj1","fields":{"parentId":"","pos":"a5","removed":false}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-star: %d %s", rec.Code, rec.Body.String())
	}

	// The declaration is the client's: additive evolution through the
	// type routes works (no server-owned reservation).
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+root+"/parts/"+defs.Datasets[0].PartId+"/datasets",
		`{"key":"favorites_meta","idRule":"user","fields":[{"key":"v","kind":"string"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("evolve: %d %s", rec.Code, rec.Body.String())
	}

	// Uninstall = delete the created root; the id then reads as not
	// installed and a fresh install works.
	rec = doJSON(t, e, http.MethodDelete, base+"/objects/"+root, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("uninstall: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/favorites%2Fv1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted winner must read uninstalled: %d %s", rec.Code, rec.Body.String())
	}
	fresh := ensureBundle(t, e, acc.TechSpaceId, favoritesEnsureBody)
	if !fresh.Installed || fresh.Bundle.RootId == root {
		t.Fatalf("reinstall after uninstall: %+v", fresh)
	}
}

// hasRejection reports whether a ModifyResult-shaped body carries a
// non-empty rejections array.
func hasRejection(body []byte) bool {
	var r struct {
		Rejections []json.RawMessage `json:"rejections"`
	}
	_ = json.Unmarshal(body, &r)
	return len(r.Rejections) > 0
}
