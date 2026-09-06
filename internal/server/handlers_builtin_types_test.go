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

// TestServer_BuiltinTypesHidden pins the listing contract shared by the
// hidden built-ins: out of the default picker listing, present
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

	for _, id := range []string{page.TypeId, miniapp.TypeId, bin.TypeId, dataview.TypeId} {
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
	obj := mustCreateObject(t, e, sp.Id, `{"types":["`+page.TypeId+`"],"initialProperties":{"any":{"name":"Doc"}}}`)
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

	// Attaching page later admits the same writes.
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+bare+"/attach/"+page.TypeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("attach page: %d %s", rec.Code, rec.Body.String())
	}
	blocksCreate(t, e, base+"/objects/"+bare, `{"type":"paragraph","text":"now"}`)

	// page and a user document type on one object share the canonical
	// collection: one body, whichever type's part admitted the write,
	// and detaching page leaves the body to the other type.
	userDoc := installModuleType(t, e, sp.Id, editor.Module)
	both := mustCreateObject(t, e, sp.Id, `{"types":["`+page.TypeId+`","`+userDoc+`"]}`)
	bothBase := base + "/objects/" + both
	blocksCreate(t, e, bothBase, `{"type":"paragraph","text":"one"}`)
	blocksCreate(t, e, bothBase, `{"type":"paragraph","text":"two"}`)
	if got := len(blocksList(t, e, bothBase).Records); got != 2 {
		t.Errorf("blocks on a page+user-typed object = %d, want 2 in one body", got)
	}
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+both+"/detach/"+page.TypeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detach page: %d %s", rec.Code, rec.Body.String())
	}
	if got := getMarkdown(t, e, bothBase+"/editor/editor_blocks/markdown"); got != "one\n\ntwo" {
		t.Errorf("body after detaching page = %q, want it intact under the user type", got)
	}
}

// TestServer_MiniappType pins the built-in mini-app marker: one string
// property `bundle`, writable on a carrier through the generic
// properties surface and refused elsewhere.
func TestServer_MiniappType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "MiniappType")
	base := "/v1/spaces/" + sp.Id

	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/types/"+miniapp.TypeId+"/properties", &props)
	if len(props.Properties) != 1 || props.Properties[0].Id != miniapp.PropBundle || props.Properties[0].Kind != api.PropertyKindString {
		t.Fatalf("miniapp properties = %+v", props.Properties)
	}
	var parts api.TypePartsListResponse
	decodeGet(t, e, base+"/types/"+miniapp.TypeId+"/parts", &parts)
	if len(parts.Parts) != 0 {
		t.Errorf("miniapp parts = %+v, want none", parts.Parts)
	}

	obj := mustCreateObject(t, e, sp.Id, `{"types":["`+miniapp.TypeId+`"]}`)
	setURL := fmt.Sprintf("%s/properties/%s/set/%s", base, obj, miniapp.TypeId)
	rec := doJSON(t, e, http.MethodPost, setURL, `{"patch":{"bundle":"system:wiki/v1"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set bundle: %d %s", rec.Code, rec.Body.String())
	}
	row := propertiesRecord(t, e, sp.Id, obj)
	ns, _ := row[miniapp.TypeId].(map[string]any)
	if ns[miniapp.PropBundle] != "system:wiki/v1" {
		t.Errorf("miniapp namespace = %v", row[miniapp.TypeId])
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
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/set/%s", base, bare, miniapp.TypeId), `{"patch":{"bundle":"system:wiki/v1"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("set on a non-carrier: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestServer_BinMoveRestore pins move-to-bin over the plain attach /
// detach routes: attach adds the type AND stamps movedAt / movedBy in
// one change, detach removes the type and clears both, and the stamps
// name this account and a fresh instant every time.
func TestServer_BinMoveRestore(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BinType")
	base := "/v1/spaces/" + sp.Id
	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)

	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/types/"+bin.TypeId+"/properties", &props)
	kinds := map[string]string{}
	for _, p := range props.Properties {
		kinds[p.Id] = p.Kind
	}
	if kinds[bin.PropMovedAt] != api.PropertyKindDatetime || kinds[bin.PropMovedBy] != api.PropertyKindString || len(kinds) != 2 {
		t.Fatalf("bin properties = %+v", props.Properties)
	}

	obj := mustCreateObject(t, e, sp.Id, `{"initialProperties":{"any":{"name":"Trash me"}}}`)
	attachURL := fmt.Sprintf("%s/properties/%s/attach/%s", base, obj, bin.TypeId)
	detachURL := fmt.Sprintf("%s/properties/%s/detach/%s", base, obj, bin.TypeId)

	// stamps reads the bin namespace: (movedAt, movedBy, present). A
	// restored row carries NO `bin` key at all — an empty `bin: {}` would
	// read as a carrier to a client testing the key.
	stamps := func() (time.Time, string, bool) {
		t.Helper()
		row := propertiesRecord(t, e, sp.Id, obj)
		nsRaw, hasNs := row[bin.TypeId]
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
	rec := doJSON(t, e, http.MethodPost, attachURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("move to bin: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if res.ChangeId == "" || res.VersionId == "" {
		t.Errorf("move result = %+v, want a change", res)
	}
	if types := objectTypes(t, e, sp.Id, obj); !hasType(types, bin.TypeId) {
		t.Fatalf("bin not attached: %v", types)
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

	// Carriers are what clients filter out of ordinary lists.
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
	if got := query(`{"any.types":"` + bin.TypeId + `"}`); len(got) != 1 {
		t.Errorf("carriers of bin = %d, want 1", len(got))
	}
	if got := query(`{"id":"` + obj + `","any.types":{"$nin":["` + bin.TypeId + `"]}}`); len(got) != 0 {
		t.Errorf("$nin bin still lists the binned object: %s", got)
	}

	// Idempotent: a second move keeps one membership entry and re-stamps.
	rec = doJSON(t, e, http.MethodPost, attachURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second move: %d %s", rec.Code, rec.Body.String())
	}
	if types := objectTypes(t, e, sp.Id, obj); countOf(types, bin.TypeId) != 1 {
		t.Errorf("bin listed %d times after the second move: %v", countOf(types, bin.TypeId), types)
	}
	second, _, ok := stamps()
	if !ok || second.Before(first) {
		t.Errorf("second move stamps = (%v, %v), want a fresh instant >= %v", second, ok, first)
	}

	// Restore: the type goes, and so do both stamps.
	rec = doJSON(t, e, http.MethodPost, detachURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	if types := objectTypes(t, e, sp.Id, obj); hasType(types, bin.TypeId) {
		t.Errorf("bin still attached after restore: %v", types)
	}
	if _, _, ok := stamps(); ok {
		t.Errorf("stamps survive the restore: %v", propertiesRecord(t, e, sp.Id, obj)[bin.TypeId])
	}
	if got := query(`{"id":"` + obj + `","any.types":{"$nin":["` + bin.TypeId + `"]}}`); len(got) != 1 {
		t.Errorf("restored object missing from the $nin list")
	}
	// Restore is idempotent too.
	if rec := doJSON(t, e, http.MethodPost, detachURL, ""); rec.Code != http.StatusOK {
		t.Errorf("second restore: %d %s", rec.Code, rec.Body.String())
	}

	// Moving again stamps afresh — nothing stale leaks from the first move.
	rec = doJSON(t, e, http.MethodPost, attachURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("third move: %d %s", rec.Code, rec.Body.String())
	}
	if third, by, ok := stamps(); !ok || by != acc.Id || third.Before(second) {
		t.Errorf("third move stamps = (%v, %q, %v)", third, by, ok)
	}

	// Unknown object: the generic 404, no partial write.
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/attach/%s", base, "not-an-object", bin.TypeId), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("move a bogus id: %d %s, want 404", rec.Code, rec.Body.String())
	}

	// Restore on an object that was never binned is a no-op 200 and
	// leaves no `bin` key behind.
	never := mustCreateObject(t, e, sp.Id, `{}`)
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/detach/%s", base, never, bin.TypeId), "")
	if rec.Code != http.StatusOK {
		t.Errorf("restore a never-binned object: %d %s", rec.Code, rec.Body.String())
	}
	if row := propertiesRecord(t, e, sp.Id, never); row[bin.TypeId] != nil || hasType(objectTypes(t, e, sp.Id, never), bin.TypeId) {
		t.Errorf("never-binned object gained a bin namespace: %v", row[bin.TypeId])
	}
}
