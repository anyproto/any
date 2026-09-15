package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bin"
	"github.com/anyproto/any/internal/dataview"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/miniapp"
	"github.com/anyproto/any/internal/page"
)

// typesById reads GET …/types (with the given query string) into a map
// keyed by type id.
func typesById(t *testing.T, e http.Handler, base, q string) map[string]api.TypeInfo {
	t.Helper()
	var list api.TypesListResponse
	decodeGet(t, e, base+"/types"+q, &list)
	out := make(map[string]api.TypeInfo, len(list.Types))
	for _, ti := range list.Types {
		out[ti.Id] = ti
	}
	return out
}

// countOf counts the occurrences of want in types.
func countOf(types []string, want string) int {
	n := 0
	for _, tp := range types {
		if tp == want {
			n++
		}
	}
	return n
}

// propertiesRecord reads an object's raw properties row.
func propertiesRecord(t *testing.T, e http.Handler, spaceId, objectId string) map[string]any {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/properties/"+objectId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get properties: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode properties: %v\nraw=%s", err, rec.Body.String())
	}
	return resp.Record
}

// collectionsById reads GET …/collections (with the given query string)
// into a map keyed by collection id.
func collectionsById(t *testing.T, e http.Handler, base, q string) map[string]api.CollectionInfo {
	t.Helper()
	var list api.CollectionsListResponse
	decodeGet(t, e, base+"/collections"+q, &list)
	out := make(map[string]api.CollectionInfo, len(list.Collections))
	for _, ci := range list.Collections {
		out[ci.Id] = ci
	}
	return out
}

// TestServer_BuiltinTypesHidden pins the listing contract shared by the
// hidden built-in TYPES: out of the default picker listing, present
// with includeHidden as hidden built-ins whose xKey is their id,
// resolvable by GET, and reserved against user types.
func TestServer_BuiltinTypesHidden(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "HiddenBuiltins")
	base := "/v1/spaces/" + sp.Id
	visible := typesById(t, e, base, "")
	all := typesById(t, e, base, "?includeHidden=true")

	for _, id := range []string{page.TypeId, dataview.TypeId} {
		if _, listed := visible[id]; listed {
			t.Errorf("%s listed by default", id)
		}
		ti, ok := all[id]
		if !ok || !ti.Hidden || !ti.BuiltIn || ti.XKey != id {
			t.Errorf("%s includeHidden row = %+v (ok=%v), want hidden built-in with xKey == id", id, ti, ok)
		}
		var got api.TypeInfo
		decodeGet(t, e, base+"/types/"+id, &got)
		if got.Id != id || !got.Hidden {
			t.Errorf("GET %s = %+v", id, got)
		}
		rec := doJSON(t, e, http.MethodPost, base+"/types", `{"name":"Mine","xKey":"`+id+`"}`)
		if rec.Code != http.StatusConflict {
			t.Errorf("user type with xKey %q: %d %s, want 409", id, rec.Code, rec.Body.String())
		} else {
			assertErrorCode(t, rec, "type.xkey_conflict")
		}
		// Registered: no runtime parts.
		rec = doJSON(t, e, http.MethodPost, base+"/types/"+id+"/parts", `{"key":"x","datasets":[{"module":"editor","shared":true}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("add part on %s: %d %s, want 400", id, rec.Code, rec.Body.String())
		} else {
			assertErrorCode(t, rec, "type.registered")
		}
	}
}

// The same contract for the hidden built-in COLLECTIONS: they live on
// the collections surface, the types surface refuses them, and their
// handles are reserved against both.
func TestServer_BuiltinCollectionsHidden(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "HiddenCollections")
	base := "/v1/spaces/" + sp.Id
	visible := collectionsById(t, e, base, "")
	all := collectionsById(t, e, base, "?includeHidden=true")

	for _, id := range []string{miniapp.Id, bin.Id} {
		if _, listed := visible[id]; listed {
			t.Errorf("%s listed by default", id)
		}
		ci, ok := all[id]
		if !ok || !ci.Hidden || !ci.BuiltIn || ci.XKey != id {
			t.Errorf("%s includeHidden row = %+v (ok=%v), want hidden built-in with xKey == id", id, ci, ok)
		}
		var got api.CollectionInfo
		decodeGet(t, e, base+"/collections/"+id, &got)
		if got.Id != id || !got.Hidden {
			t.Errorf("GET %s = %+v", id, got)
		}
		// It is not a type, and it never lands in GET …/types.
		if _, listed := typesById(t, e, base, "?includeHidden=true")[id]; listed {
			t.Errorf("%s listed among the types", id)
		}
		rec := doJSON(t, e, http.MethodGet, base+"/types/"+id, "")
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("GET types/%s: %d %s, want a 4xx", id, rec.Code, rec.Body.String())
		}
		// The handle is reserved on BOTH surfaces.
		for _, path := range []string{base + "/types", base + "/collections"} {
			rec = doJSON(t, e, http.MethodPost, path, `{"name":"Mine","xKey":"`+id+`"}`)
			if rec.Code != http.StatusConflict {
				t.Errorf("POST %s with xKey %q: %d %s, want 409", path, id, rec.Code, rec.Body.String())
			} else {
				assertErrorCode(t, rec, "type.xkey_conflict")
			}
		}
		// Registered: the metadata is statically declared.
		rec = doJSON(t, e, http.MethodPatch, base+"/collections/"+id, `{"name":"Renamed"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("patch %s: %d %s, want 400", id, rec.Code, rec.Body.String())
		} else {
			assertErrorCode(t, rec, "collection.registered")
		}
	}
}

// TestServer_PageType pins the built-in document type: one static part
// sharing the editor's canonical collection, so an object carrying
// `page` holds `editor_blocks` — writable through the editor routes,
// listed among the collection's owners — while a bare object does not.
func TestServer_PageType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "PageType")
	base := "/v1/spaces/" + sp.Id

	var parts api.TypePartsListResponse
	decodeGet(t, e, base+"/types/"+page.TypeId+"/parts", &parts)
	if len(parts.Parts) != 1 {
		t.Fatalf("parts = %+v, want one", parts.Parts)
	}
	body := parts.Parts[0]
	if body.Id != page.PartBody || body.Key != page.PartBody || string(body.UI) != `{"type":"document"}` || body.Hidden {
		t.Errorf("body part = %+v", body)
	}
	if len(body.Datasets) != 1 || body.Datasets[0].Collection != editor.Dataset || body.Datasets[0].Module != editor.Module || !body.Datasets[0].Shared {
		t.Errorf("body datasets = %+v", body.Datasets)
	}
	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/types/"+page.TypeId+"/properties", &props)
	if len(props.Properties) != 0 {
		t.Errorf("page properties = %+v, want none", props.Properties)
	}

	// Discovery: page owns the canonical editor collection in every space.
	var ds api.DatasetsResponse
	decodeGet(t, e, base+"/datasets", &ds)
	var owners []string
	for _, s := range ds.Datasets {
		if s.Name == editor.Dataset {
			owners = s.Owners
		}
	}
	if !slices.Contains(owners, page.TypeId) {
		t.Errorf("%s owners = %v, want %s among them", editor.Dataset, owners, page.TypeId)
	}

	// The write gate follows the declaration.
	obj := mustCreateObject(t, e, sp.Id, `{"type":"`+page.TypeId+`","initialProperties":{"any":{"name":"Doc"}}}`)
	objBase := base + "/objects/" + obj
	b := blocksCreate(t, e, objBase, `{"type":"paragraph","text":"hello"}`)
	if b.Text != "hello" {
		t.Errorf("block = %+v", b)
	}
	if resp := appendMarkdown(t, e, objBase, "second"); len(resp.Inserted) != 1 {
		t.Errorf("append = %+v, want one inserted", resp)
	}
	if got := getMarkdown(t, e, objBase+"/editor/editor_blocks/markdown"); got != "hello\n\nsecond" {
		t.Errorf("markdown = %q", got)
	}

	bare := mustCreateObject(t, e, sp.Id, `{}`)
	rec := doJSON(t, e, http.MethodPost, base+"/objects/"+bare+"/editor/editor_blocks/blocks", `{"type":"paragraph","text":"no"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("block write without page: %d %s, want 400", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "dataset.not_declared")

	// Setting page later admits the same writes.
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+bare+"/type/"+page.TypeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("set page: %d %s", rec.Code, rec.Body.String())
	}
	bareBase := base + "/objects/" + bare
	blocksCreate(t, e, bareBase, `{"type":"paragraph","text":"now"}`)

	// page and a user document type share the canonical collection, so
	// retyping from one to the other keeps the SAME body: whichever
	// type's part admits the write, there is one editor_blocks.
	userDoc := installModuleType(t, e, sp.Id, editor.Module)
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+bare+"/type/"+userDoc, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("retype to the user document type: %d %s", rec.Code, rec.Body.String())
	}
	blocksCreate(t, e, bareBase, `{"type":"paragraph","text":"then"}`)
	if got := len(blocksList(t, e, bareBase).Records); got != 2 {
		t.Errorf("blocks after the retype = %d, want 2 in one body", got)
	}
	if got := getMarkdown(t, e, bareBase+"/editor/editor_blocks/markdown"); got != "now\n\nthen" {
		t.Errorf("body after the retype = %q, want it intact under the user type", got)
	}
}

// TestServer_MiniappType pins the built-in mini-app marker — a
// COLLECTION: `bundle` (string), `pos` (string) and `hidden` (bool),
// writable on a member through the generic properties surface and
// refused elsewhere.
func TestServer_MiniappType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "MiniappType")
	base := "/v1/spaces/" + sp.Id

	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/collections/"+miniapp.Id+"/properties", &props)
	kinds := map[string]string{}
	for _, p := range props.Properties {
		kinds[p.Id] = string(p.Kind)
	}
	want := map[string]string{miniapp.PropBundle: api.PropertyKindString, miniapp.PropPos: api.PropertyKindString, miniapp.PropHidden: api.PropertyKindBoolean}
	if len(kinds) != len(want) {
		t.Fatalf("miniapp properties = %+v", props.Properties)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Fatalf("miniapp property %s kind = %q, want %q", id, kinds[id], k)
		}
	}
	// A collection has no parts, so the type route refuses the id.
	if rec := doJSON(t, e, http.MethodGet, base+"/types/"+miniapp.Id+"/parts", ""); rec.Code == http.StatusOK {
		t.Errorf("miniapp answered the type parts route: %s", rec.Body.String())
	}

	obj := mustCreateObject(t, e, sp.Id, `{"collections":["`+miniapp.Id+`"]}`)
	setURL := fmt.Sprintf("%s/properties/%s/set/%s", base, obj, miniapp.Id)
	rec := doJSON(t, e, http.MethodPost, setURL, `{"patch":{"bundle":"system:wiki/v1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set bundle: %d %s", rec.Code, rec.Body.String())
	}
	row := propertiesRecord(t, e, sp.Id, obj)
	ns, _ := row[miniapp.Id].(map[string]any)
	if ns[miniapp.PropBundle] != "system:wiki/v1" {
		t.Errorf("miniapp namespace = %v", row[miniapp.Id])
	}
	if rec := doJSON(t, e, http.MethodPost, setURL, `{"patch":{"bundle":42}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("number into a string property: %d %s, want 400", rec.Code, rec.Body.String())
	} else {
		assertErrorCode(t, rec, "property.kind_mismatch")
	}
	if rec := doJSON(t, e, http.MethodPost, setURL, `{"patch":{"other":"x"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("undeclared property: %d %s, want 400", rec.Code, rec.Body.String())
	}

	bare := mustCreateObject(t, e, sp.Id, `{}`)
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/set/%s", base, bare, miniapp.Id), `{"patch":{"bundle":"system:wiki/v1"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("set on a non-member: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestServer_BinMoveRestore pins move-to-bin over the plain collection
// routes: the POST files the object under `bin` AND stamps movedAt /
// movedBy in one change, the DELETE removes it and clears both, and the
// stamps name this account and a fresh instant every time.
func TestServer_BinMoveRestore(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BinType")
	base := "/v1/spaces/" + sp.Id
	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)

	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/collections/"+bin.Id+"/properties", &props)
	kinds := map[string]string{}
	for _, p := range props.Properties {
		kinds[p.Id] = p.Kind
	}
	if kinds[bin.PropMovedAt] != api.PropertyKindDatetime || kinds[bin.PropMovedBy] != api.PropertyKindString || len(kinds) != 2 {
		t.Fatalf("bin properties = %+v", props.Properties)
	}

	obj := mustCreateObject(t, e, sp.Id, `{"initialProperties":{"any":{"name":"Trash me"}}}`)
	binURL := fmt.Sprintf("%s/properties/%s/collections/%s", base, obj, bin.Id)

	// stamps reads the bin namespace: (movedAt, movedBy, present). A
	// restored row carries NO `bin` key at all — an empty `bin: {}` would
	// read as a member to a client testing the key.
	stamps := func() (time.Time, string, bool) {
		t.Helper()
		row := propertiesRecord(t, e, sp.Id, obj)
		nsRaw, hasNs := row[bin.Id]
		if !hasNs {
			return time.Time{}, "", false
		}
		ns, _ := nsRaw.(map[string]any)
		at, hasAt := ns[bin.PropMovedAt].(map[string]any)
		by, hasBy := ns[bin.PropMovedBy].(string)
		if !hasAt || !hasBy {
			t.Fatalf("bin namespace present but not fully stamped: %v", nsRaw)
		}
		s, _ := at["$date"].(string)
		ts, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatalf("movedAt = %v, want {\"$date\": <RFC 3339>}: %v", at, err)
		}
		return ts, by, true
	}

	before := time.Now().Add(-time.Minute)
	rec := doJSON(t, e, http.MethodPost, binURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("move to bin: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if res.ChangeId == "" || res.VersionId == "" {
		t.Errorf("move result = %+v, want a change", res)
	}
	if cols := objectCollections(t, e, sp.Id, obj); !slices.Contains(cols, bin.Id) {
		t.Fatalf("bin not attached: %v", cols)
	}
	first, by, ok := stamps()
	if !ok {
		t.Fatal("bin stamps absent after move")
	}
	if by != acc.Id {
		t.Errorf("movedBy = %q, want this account %q", by, acc.Id)
	}
	if first.Before(before) || first.After(time.Now().Add(time.Minute)) {
		t.Errorf("movedAt = %v, want about now", first)
	}
	// One change stamped the row: modifiedAt names the same instant.
	row := propertiesRecord(t, e, sp.Id, obj)
	if mod, _ := row["modifiedAt"].(map[string]any); mod == nil {
		t.Errorf("row lacks modifiedAt after the move: %v", row)
	}

	// Members are what clients filter out of ordinary lists.
	query := func(filter string) []json.RawMessage {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, base+"/objects/query", `{"filter":`+filter+`,"limit":10}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("objects query: %d %s", rec.Code, rec.Body.String())
		}
		var resp api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Records
	}
	if got := query(`{"any.collections":"` + bin.Id + `"}`); len(got) != 1 {
		t.Errorf("members of bin = %d, want 1", len(got))
	}
	if got := query(`{"id":"` + obj + `","any.collections":{"$nin":["` + bin.Id + `"]}}`); len(got) != 0 {
		t.Errorf("$nin bin still lists the binned object: %s", got)
	}

	// Idempotent: a second move keeps one membership entry and re-stamps.
	rec = doJSON(t, e, http.MethodPost, binURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second move: %d %s", rec.Code, rec.Body.String())
	}
	if cols := objectCollections(t, e, sp.Id, obj); countOf(cols, bin.Id) != 1 {
		t.Errorf("bin listed %d times after the second move: %v", countOf(cols, bin.Id), cols)
	}
	second, _, ok := stamps()
	if !ok || second.Before(first) {
		t.Errorf("second move stamps = (%v, %v), want a fresh instant >= %v", second, ok, first)
	}

	// Restore: the membership goes, and so do both stamps.
	rec = doJSON(t, e, http.MethodDelete, binURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	if cols := objectCollections(t, e, sp.Id, obj); slices.Contains(cols, bin.Id) {
		t.Errorf("bin still attached after restore: %v", cols)
	}
	if _, _, ok := stamps(); ok {
		t.Errorf("stamps survive the restore: %v", propertiesRecord(t, e, sp.Id, obj)[bin.Id])
	}
	if got := query(`{"id":"` + obj + `","any.collections":{"$nin":["` + bin.Id + `"]}}`); len(got) != 1 {
		t.Errorf("restored object missing from the $nin list")
	}
	// Restore is idempotent too.
	if rec := doJSON(t, e, http.MethodDelete, binURL, ""); rec.Code != http.StatusOK {
		t.Errorf("second restore: %d %s", rec.Code, rec.Body.String())
	}

	// Moving again stamps afresh — nothing stale leaks from the first move.
	rec = doJSON(t, e, http.MethodPost, binURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("third move: %d %s", rec.Code, rec.Body.String())
	}
	if third, by, ok := stamps(); !ok || by != acc.Id || third.Before(second) {
		t.Errorf("third move stamps = (%v, %q, %v)", third, by, ok)
	}

	// Unknown object: the generic 404, no partial write.
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/collections/%s", base, "not-an-object", bin.Id), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("move a bogus id: %d %s, want 404", rec.Code, rec.Body.String())
	}

	// Restore on an object that was never binned is a no-op 200 and
	// leaves no `bin` key behind.
	never := mustCreateObject(t, e, sp.Id, `{}`)
	rec = doJSON(t, e, http.MethodDelete, fmt.Sprintf("%s/properties/%s/collections/%s", base, never, bin.Id), "")
	if rec.Code != http.StatusOK {
		t.Errorf("restore a never-binned object: %d %s", rec.Code, rec.Body.String())
	}
	if row := propertiesRecord(t, e, sp.Id, never); row[bin.Id] != nil || slices.Contains(objectCollections(t, e, sp.Id, never), bin.Id) {
		t.Errorf("never-binned object gained a bin namespace: %v", row[bin.Id])
	}
}
