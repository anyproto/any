// Collections surface e2e: a collection is a column group objects are
// filed under (`any.collections`) next to their ONE type
// (`any.type`). Drives the whole surface over real HTTP — create a
// collection with a property, file a typed object under it with an
// initial value, write through set/:collectionId, find it by
// `any.collections`, detach, and the two-surface guards (a collection
// id is not a type id, and the handles never collide).
package e2e

import (
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bin"
	"github.com/anyproto/any/internal/miniapp"
)

func TestE2E_CollectionsSurface(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	binPath := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, binPath, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr
	spaceID := createSpace(t, base, "Collections", "collections surface e2e")
	spaceBase := base + "/v1/spaces/" + spaceID

	// A collection: a handle and columns, no parts, no layout.
	var createdColl api.CollectionsCreateResponse
	mustJSON(t, http.MethodPost, spaceBase+"/collections",
		`{"name":"Wiki","description":"Pages of the wiki","xKey":"wiki"}`,
		http.StatusCreated, &createdColl)
	collID := createdColl.CollectionId
	if collID == "" {
		t.Fatalf("collectionId empty: %+v", createdColl)
	}

	var posProp, folderProp api.AddPropertyResponse
	mustJSON(t, http.MethodPost, spaceBase+"/collections/"+collID+"/properties",
		`{"name":"Position","kind":"string","xKey":"pos"}`, http.StatusCreated, &posProp)
	mustJSON(t, http.MethodPost, spaceBase+"/collections/"+collID+"/properties",
		`{"name":"Folder","kind":"boolean","xKey":"folder"}`, http.StatusCreated, &folderProp)

	t.Run("listed as a collection, never as a type", func(t *testing.T) {
		var colls api.CollectionsListResponse
		mustJSON(t, http.MethodGet, spaceBase+"/collections", "", http.StatusOK, &colls)
		var seen *api.CollectionInfo
		for i, col := range colls.Collections {
			if col.Id == collID {
				seen = &colls.Collections[i]
			}
		}
		if seen == nil {
			t.Fatalf("created collection missing from the listing: %+v", colls.Collections)
		}
		if seen.XKey != "wiki" || seen.Name != "Wiki" || seen.BuiltIn || seen.Hidden {
			t.Errorf("collection info = %+v", *seen)
		}

		// The two built-in collections are registered, hidden, and out
		// of the types surface entirely.
		mustJSON(t, http.MethodGet, spaceBase+"/collections?includeHidden=true", "", http.StatusOK, &colls)
		for _, want := range []string{miniapp.Id, bin.Id} {
			idx := slices.IndexFunc(colls.Collections, func(c api.CollectionInfo) bool { return c.Id == want })
			if idx < 0 {
				t.Errorf("built-in collection %q missing: %+v", want, colls.Collections)
				continue
			}
			if !colls.Collections[idx].BuiltIn || !colls.Collections[idx].Hidden {
				t.Errorf("built-in collection %q = %+v, want builtIn + hidden", want, colls.Collections[idx])
			}
		}
		var types api.TypesListResponse
		mustJSON(t, http.MethodGet, spaceBase+"/types?includeHidden=true", "", http.StatusOK, &types)
		for _, ti := range types.Types {
			if ti.Id == collID || ti.Id == miniapp.Id || ti.Id == bin.Id {
				t.Errorf("collection %q listed as a type: %+v", ti.Id, types.Types)
			}
		}

		// One handle namespace across both surfaces.
		code := mustErrorCode(t, http.MethodPost, spaceBase+"/types",
			`{"name":"Wiki type","xKey":"wiki"}`, http.StatusConflict)
		if code != "type.xkey_conflict" {
			t.Errorf("code = %q, want type.xkey_conflict", code)
		}
	})

	// The object: one type, filed under the collection, with an initial
	// value in each namespace.
	noteType := createType(t, base, spaceID, "Note")
	titleProp := addProperty(t, base, spaceID, noteType, "Title", "string")
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, spaceBase+"/objects",
		`{"type":"`+noteType+`","collections":["`+collID+`"],"initialProperties":{`+
			`"`+noteType+`":{"`+titleProp+`":"Home"},`+
			`"`+collID+`":{"`+posProp.PropId+`":"a0"}}}`,
		http.StatusCreated, &obj)

	t.Run("the row carries one type and the collection", func(t *testing.T) {
		row := membershipRow(t, base, spaceID, obj.ObjectId)
		if row.Any.Type != noteType {
			t.Errorf("any.type = %q, want %q", row.Any.Type, noteType)
		}
		if !slices.Contains(row.Any.Collections, collID) {
			t.Errorf("any.collections = %v, want it to contain %q", row.Any.Collections, collID)
		}
	})

	t.Run("every object has exactly one type", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost, spaceBase+"/objects", `{}`, http.StatusBadRequest)
		if code != "request.missing_field" {
			t.Errorf("create without a type: code = %q, want request.missing_field", code)
		}
		code = mustErrorCode(t, http.MethodPost, spaceBase+"/modify",
			`{"objectId":"`+obj.ObjectId+`","dataset":"objects","records":[{"id":"`+
				obj.ObjectId+`","ops":[{"type":"$unset","path":"any.type"}]}]}`,
			http.StatusBadRequest)
		if code != "membership.type_required" {
			t.Errorf("$unset any.type: code = %q, want membership.type_required", code)
		}
		if row := membershipRow(t, base, spaceID, obj.ObjectId); row.Any.Type != noteType {
			t.Errorf("any.type = %q after the refused unset, want %q", row.Any.Type, noteType)
		}
	})

	t.Run("set under the collection", func(t *testing.T) {
		var res api.ModifyResult
		mustJSON(t, http.MethodPost,
			spaceBase+"/properties/"+obj.ObjectId+"/set/"+collID,
			`{"patch":{"`+folderProp.PropId+`":true}}`, http.StatusOK, &res)
		if res.ChangeId == "" {
			t.Fatalf("collection write minted no change: %+v", res)
		}
		assertProp(t, base, spaceID, obj.ObjectId, collID, posProp.PropId, "a0")
		assertProp(t, base, spaceID, obj.ObjectId, collID, folderProp.PropId, true)
		assertProp(t, base, spaceID, obj.ObjectId, noteType, titleProp, "Home")
	})

	t.Run("found by any.collections, no marker exclusion needed", func(t *testing.T) {
		// The collection's own definition row is `__collection__` in the
		// type slot, so it never matches its own id.
		if ids := queryIds(t, base, spaceID, `{"any.collections":"`+collID+`"}`); !slices.Equal(ids, []string{obj.ObjectId}) {
			t.Errorf("any.collections query = %v, want [%s]", ids, obj.ObjectId)
		}
		if ids := queryIds(t, base, spaceID, `{"any.type":"`+noteType+`"}`); !slices.Equal(ids, []string{obj.ObjectId}) {
			t.Errorf("any.type query = %v, want [%s]", ids, obj.ObjectId)
		}
	})

	t.Run("the two slots refuse each other's ids", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodPost,
			spaceBase+"/properties/"+obj.ObjectId+"/type/"+collID, "", http.StatusBadRequest)
		if code != "type.not_a_type" {
			t.Errorf("setting a collection as the type: code = %q, want type.not_a_type", code)
		}
		code = mustErrorCode(t, http.MethodPost,
			spaceBase+"/properties/"+obj.ObjectId+"/collections/"+noteType, "", http.StatusBadRequest)
		if code != "collection.not_a_collection" {
			t.Errorf("attaching a type as a collection: code = %q, want collection.not_a_collection", code)
		}
	})

	t.Run("detach leaves the values orphan and closes the namespace", func(t *testing.T) {
		var res api.ModifyResult
		mustJSON(t, http.MethodDelete,
			spaceBase+"/properties/"+obj.ObjectId+"/collections/"+collID, "", http.StatusOK, &res)
		row := membershipRow(t, base, spaceID, obj.ObjectId)
		if slices.Contains(row.Any.Collections, collID) {
			t.Errorf("any.collections = %v after detach, want %q gone", row.Any.Collections, collID)
		}
		if ids := queryIds(t, base, spaceID, `{"any.collections":"`+collID+`"}`); len(ids) != 0 {
			t.Errorf("detached object still matches the membership query: %v", ids)
		}
		// Detaching is not a delete: the values stay readable.
		assertProp(t, base, spaceID, obj.ObjectId, collID, posProp.PropId, "a0")
		// But the namespace is no longer writable.
		code := mustErrorCode(t, http.MethodPost,
			spaceBase+"/properties/"+obj.ObjectId+"/set/"+collID,
			`{"patch":{"`+posProp.PropId+`":"a1"}}`, http.StatusBadRequest)
		if code != "dataset.not_declared" {
			t.Errorf("write after detach: code = %q, want dataset.not_declared", code)
		}
	})

	t.Run("a collection id is not a type id", func(t *testing.T) {
		code := mustErrorCode(t, http.MethodGet, spaceBase+"/types/"+collID, "", http.StatusBadRequest)
		if code != "type.not_a_type" {
			t.Errorf("GET /types/:collectionId: code = %q, want type.not_a_type", code)
		}
		code = mustErrorCode(t, http.MethodGet, spaceBase+"/collections/"+noteType, "", http.StatusBadRequest)
		if code != "collection.not_a_collection" {
			t.Errorf("GET /collections/:typeId: code = %q, want collection.not_a_collection", code)
		}
	})

	if out, err := anyStop(t, binPath, dataDir); err != nil {
		t.Fatalf("any stop: %v\n%s", err, out)
	}
	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit cleanly: %v\n%s", err, srv.output())
	}
}

// membershipRow reads one objects row's membership through the query
// surface.
func membershipRow(t *testing.T, base, spaceID, objectID string) binRow {
	t.Helper()
	var qr struct {
		Records []binRow `json:"records"`
	}
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects/query",
		`{"filter":{"id":"`+objectID+`"}}`, http.StatusOK, &qr)
	if len(qr.Records) != 1 {
		t.Fatalf("objects query for %s returned %d rows", objectID, len(qr.Records))
	}
	return qr.Records[0]
}
