package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bin"
	"github.com/anyproto/any/internal/page"
)

// markdownOf reads an object's canonical body through the markdown
// bridge.
func markdownOf(t *testing.T, e http.Handler, spaceId, objectId string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+objectId+"/editor/editor_blocks/markdown", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET markdown: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Content
}

// TestServer_TemplateCopy pins the templates contract over HTTP
// (docs/31-templates.md): the registered `template` collection, a copy
// through `from` that drops the marker and stamps any.template, the
// plain duplicate and save-as-template, the typed refusals, and the
// exclusion clause ordinary lists carry.
func TestServer_TemplateCopy(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "Templates")
	base := "/v1/spaces/" + sp.Id
	tplId := space.TemplateCollectionId

	// The marker: hidden, built-in, xKey == id, two columns, reserved.
	if _, ok := collectionsById(t, e, base, "")[tplId]; ok {
		t.Errorf("%s in the default collection listing", tplId)
	}
	ci, ok := collectionsById(t, e, base, "?includeHidden=true")[tplId]
	if !ok || !ci.Hidden || !ci.BuiltIn || ci.XKey != tplId {
		t.Fatalf("%s includeHidden row = %+v (ok=%v), want hidden built-in with xKey == id", tplId, ci, ok)
	}
	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/collections/"+tplId+"/properties", &props)
	kinds := map[string]string{}
	for _, p := range props.Properties {
		kinds[p.Id] = p.Kind
	}
	if kinds["title"] != api.PropertyKindString || kinds["version"] != api.PropertyKindNumber || len(kinds) != 2 {
		t.Fatalf("template properties = %+v", props.Properties)
	}
	if rec := doJSON(t, e, http.MethodPost, base+"/collections", `{"name":"Mine","xKey":"`+tplId+`"}`); rec.Code != http.StatusConflict {
		t.Errorf("user collection claiming the id: %d %s, want 409", rec.Code, rec.Body.String())
	}

	// A user collection with a column, so a collection-owned value
	// travels with the copy.
	coll := mustCreateCollection(t, e, sp.Id, `{"name":"Reviews","xKey":"reviews"}`)
	rec := doJSON(t, e, http.MethodPost, base+"/collections/"+coll+"/properties", `{"name":"Mood","xKey":"mood","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add column: %d %s", rec.Code, rec.Body.String())
	}
	var prop struct {
		PropId string `json:"propId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prop); err != nil {
		t.Fatal(err)
	}

	tpl := mustCreateObject(t, e, sp.Id, fmt.Sprintf(`{"type":"%s","collections":["%s","%s"],"initialProperties":{
		"any":{"name":"Weekly review","description":"the ritual","tags":["ops"]},
		"%s":{"title":"Weekly","version":2},
		"%s":{"%s":"calm"}}}`, page.TypeId, tplId, coll, tplId, coll, prop.PropId))
	rec = doJSON(t, e, http.MethodPut, base+"/objects/"+tpl+"/editor/editor_blocks/markdown",
		`{"content":"## What went well\n\n- item one\n- item two\n\n## What to change"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT template body: %d %s", rec.Code, rec.Body.String())
	}
	body := markdownOf(t, e, sp.Id, tpl)

	// Create from the template: the request's name wins, the rest is
	// the template's, the marker is gone, the stamp is on.
	obj := mustCreateObject(t, e, sp.Id, `{"from":"`+tpl+`","initialProperties":{"any":{"name":"Weekly review — 13 Oct"}}}`)
	row := propertiesRecord(t, e, sp.Id, obj)
	anyGroup, _ := row["any"].(map[string]any)
	if anyGroup["type"] != page.TypeId || anyGroup["name"] != "Weekly review — 13 Oct" || anyGroup["description"] != "the ritual" {
		t.Errorf("copied any group = %v", anyGroup)
	}
	if cols := objectCollections(t, e, sp.Id, obj); len(cols) != 1 || cols[0] != coll {
		t.Errorf("copy collections = %v, want [%s] (marker dropped)", cols, coll)
	}
	if _, has := row[tplId]; has {
		t.Errorf("copy carries a %s namespace: %v", tplId, row[tplId])
	}
	if group, _ := row[coll].(map[string]any); group[prop.PropId] != "calm" {
		t.Errorf("collection value not copied: %v", row[coll])
	}
	stamp, _ := anyGroup["template"].(map[string]any)
	if stamp["id"] != tpl || stamp["version"] != float64(2) {
		t.Errorf("any.template = %v, want {id: %s, version: 2}", anyGroup["template"], tpl)
	}
	if got := markdownOf(t, e, sp.Id, obj); got != body {
		t.Errorf("copied body = %q, want %q", got, body)
	}

	// The template keeps changing; the copy does not follow.
	rec = doJSON(t, e, http.MethodPut, base+"/objects/"+tpl+"/editor/editor_blocks/markdown", `{"content":"## Changed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT template body again: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, fmt.Sprintf("%s/properties/%s/set/%s", base, tpl, tplId), `{"patch":{"version":3}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bump version: %d %s", rec.Code, rec.Body.String())
	}
	if got := markdownOf(t, e, sp.Id, obj); got != body {
		t.Errorf("copy followed the template: %q", got)
	}

	// Lists: templates are members of their type and collections, so
	// the bin clause grows by one id; the picker query finds them.
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
	if got := query(`{"any.collections":"` + coll + `"}`); len(got) != 2 {
		t.Errorf("collection members = %d, want 2 (template included)", len(got))
	}
	if got := query(`{"$and":[{"any.collections":"` + coll + `"},{"any.collections":{"$nin":["` + bin.Id + `","` + tplId + `"]}}]}`); len(got) != 1 {
		t.Errorf("members minus templates = %d, want 1", len(got))
	}
	if got := query(`{"$and":[{"any.collections":"` + tplId + `"},{"any.type":"` + page.TypeId + `"}]}`); len(got) != 1 {
		t.Errorf("templates of the type = %d, want 1", len(got))
	}
	if got := query(`{"any.template.id":"` + tpl + `","any.template.version":{"$lt":3}}`); len(got) != 1 {
		t.Errorf("outdated objects = %d, want 1", len(got))
	}

	// Duplicate: a plain object copies as it is, no stamp; and save as
	// template files the copy under the marker.
	dup := mustCreateObject(t, e, sp.Id, `{"from":"`+obj+`"}`)
	dupRow := propertiesRecord(t, e, sp.Id, dup)
	dupAny, _ := dupRow["any"].(map[string]any)
	if dupAny["name"] != "Weekly review — 13 Oct" || dupAny["template"] != nil {
		t.Errorf("duplicate any group = %v, want the name and no stamp", dupAny)
	}
	if cols := objectCollections(t, e, sp.Id, dup); len(cols) != 1 || cols[0] != coll {
		t.Errorf("duplicate collections = %v", cols)
	}
	saved := mustCreateObject(t, e, sp.Id, fmt.Sprintf(`{"from":"%s","collections":["%s"],"initialProperties":{"%s":{"title":"From an object","version":1}}}`, obj, tplId, tplId))
	savedRow := propertiesRecord(t, e, sp.Id, saved)
	if group, _ := savedRow[tplId].(map[string]any); group["title"] != "From an object" {
		t.Errorf("saved template namespace = %v", savedRow[tplId])
	}
	if cols := objectCollections(t, e, sp.Id, saved); len(cols) != 2 {
		t.Errorf("saved template collections = %v, want the source's plus the marker", cols)
	}
	if got := markdownOf(t, e, sp.Id, saved); got != body {
		t.Errorf("saved template body = %q", got)
	}

	// Refusals.
	if rec := doJSON(t, e, http.MethodDelete, base+"/objects/"+dup, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete duplicate: %d %s", rec.Code, rec.Body.String())
	}
	cases := []struct {
		name, body, code string
		status           int
	}{
		{"type differs", `{"from":"` + tpl + `","type":"dataview"}`, "template.type_mismatch", http.StatusBadRequest},
		{"unknown source", `{"from":"bafyunknown"}`, "object.not_found", http.StatusNotFound},
		{"deleted source", `{"from":"` + dup + `"}`, "object.deleted", http.StatusGone},
		{"serialized nil", `{"from":"null"}`, "object.id_required", http.StatusBadRequest},
		{"wrong shape", `{"from":["x"]}`, "request.schema", http.StatusBadRequest},
		{"definition row", `{"from":"` + coll + `"}`, "membership.meta_type", http.StatusBadRequest},
		{"nothing", `{}`, "request.missing_field", http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := doJSON(t, e, http.MethodPost, base+"/objects", c.body)
		if rec.Code != c.status {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, rec.Body.String(), c.status)
			continue
		}
		assertErrorCode(t, rec, c.code)
	}
	// The same type is fine.
	if rec := doJSON(t, e, http.MethodPost, base+"/objects", `{"from":"`+tpl+`","type":"`+page.TypeId+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("same type: %d %s", rec.Code, rec.Body.String())
	}
}
