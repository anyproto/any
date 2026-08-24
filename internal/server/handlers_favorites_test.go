package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/favorites"
)

// TestServer_FavoritesBuiltinBundle pins the server-owned favourites
// bundle: ensured at boot (idempotent), schema discoverable, the id
// reserved against client ensure, and the declared contract — id
// pattern, required parentId/pos, stamps, soft-delete round-trip —
// enforced on the generic record surface.
func TestServer_FavoritesBuiltinBundle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	// Boot-side ensure, run twice: second pass must adopt, not fork.
	d.ensureBuiltinBundles(ctx)
	d.ensureBuiltinBundles(ctx)

	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)
	base := "/v1/spaces/" + acc.TechSpaceId

	var list api.BundleListResponse
	decodeGet(t, e, base+"/bundles", &list)
	root := ""
	for _, b := range list.Bundles {
		if b.Id == favorites.BundleId {
			if !b.Derived || b.RootId == "" {
				t.Fatalf("favorites bundle: %+v", b)
			}
			root = b.RootId
		}
	}
	if root == "" {
		t.Fatalf("favorites/v1 not installed: %+v", list.Bundles)
	}

	// Client ensure of the server-owned id is refused — racing the
	// boot pass could pin a divergent declaration forever.
	rec := doJSON(t, e, http.MethodPost, base+"/bundles",
		`{"id":"favorites/v1","derived":true,"datasets":[{"name":"entries","idRule":"user","fields":[{"key":"x","kind":"string"}]}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("client ensure of a built-in id: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "bundle.reserved" {
		t.Fatalf("code %q", env.Error.Code)
	}

	// Declaration discoverable under typeId = rootId.
	var defs api.TypeDatasetsListResponse
	decodeGet(t, e, base+"/types/"+root+"/datasets", &defs)
	if len(defs.Datasets) != 1 || defs.Datasets[0].Name != favorites.Dataset {
		t.Fatalf("datasets on root: %+v", defs)
	}

	// Star an item (id = link) and create a folder — one upsert.
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"entries","records":[
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
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"entries","records":[
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
		`{"objectId":"`+root+`","dataset":"entries","filter":{"id":"any://o/sp1/obj1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(q.Records) != 1 {
		t.Fatalf("records: %d", len(q.Records))
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
	rec = doJSON(t, e, http.MethodPost, base+"/modify", `{"objectId":"`+root+`","dataset":"entries","records":[
		{"id":"any://o/sp1/obj1","ops":[{"type":"$set","path":"creator","value":"me"}]}]}`)
	if rec.Code == http.StatusOK && !hasRejection(rec.Body.Bytes()) {
		t.Fatalf("client write of a stamped field must not apply: %s", rec.Body.String())
	}

	// Un-star = soft delete; re-star = upsert clears it and re-places.
	rec = doJSON(t, e, http.MethodPost, base+"/modify", `{"objectId":"`+root+`","dataset":"entries","records":[
		{"id":"any://o/sp1/obj1","ops":[{"type":"$set","path":"removed","value":true}]}]}`)
	if rec.Code != http.StatusOK || hasRejection(rec.Body.Bytes()) {
		t.Fatalf("soft delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query",
		`{"objectId":"`+root+`","dataset":"entries","filter":{"id":"any://o/sp1/obj1"}}`)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"removed":true`)) {
		t.Fatalf("soft delete not applied: %s", rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/upsert", `{"objectId":"`+root+`","dataset":"entries","records":[
		{"id":"any://o/sp1/obj1","fields":{"parentId":"","pos":"a5","removed":false}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-star: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, base+"/query",
		`{"objectId":"`+root+`","dataset":"entries","filter":{"id":"any://o/sp1/obj1"}}`)
	var q2 api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q2); err != nil || len(q2.Records) != 1 {
		t.Fatalf("re-read: %v %s", err, rec.Body.String())
	}
	var row struct {
		Removed  bool   `json:"removed"`
		ParentId string `json:"parentId"`
		Pos      string `json:"pos"`
	}
	if err := json.Unmarshal(q2.Records[0], &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if row.Removed || row.ParentId != "" || row.Pos != "a5" {
		t.Fatalf("re-star state: %+v", row)
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
