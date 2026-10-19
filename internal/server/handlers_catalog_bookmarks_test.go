package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/page"
)

const legacyBookmarksCatalog = `usecases:
  - id: bookmarks
    name: Bookmarks
    bundles:
      - id: system:bookmark/v1
        name: Bookmarks
        collection:
          xKey: bookmark
          properties:
            - { xKey: url, name: URL, kind: string, xFormat: { type: url } }
            - { xKey: note, name: Note, kind: string, xFormat: { type: longtext } }
            - { xKey: readerText, name: Reader text, kind: string, xFormat: { type: longtext } }
            - { xKey: extra, name: Custom field, kind: string }
`

func TestServer_CatalogSetupBookmarks(t *testing.T) {
	// Exercise the real SDK on the offline fixture, without a staging dependency.
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("authenticate: %d %s", rec.Code, rec.Body.String())
	}
	sp := createSpaceInfo(t, e, "CatalogBookmarks")
	res := setupUsecase(t, e, "bookmarks", sp.Id)
	want := []string{"system:task/v1", "system:project/v2", "system:area/v2", "system:tasks/v1", "system:bookmark/v1", "system:bookmarks/v1"}
	if got := bundleIds(res); !slices.Equal(got, want) {
		t.Fatalf("setup order = %v, want %v", got, want)
	}
	byId := map[string]api.CatalogSetupBundle{}
	for i, b := range res.Bundles {
		byId[b.Id] = b
		usecase := "tasks"
		if i >= 4 {
			usecase = "bookmarks"
		}
		if !b.Installed || b.Usecase != usecase || b.Bundle.RootId == "" {
			t.Fatalf("fresh bundle: %+v", b)
		}
	}
	bookmark, app := byId["system:bookmark/v1"], byId["system:bookmarks/v1"]
	if bookmark.TypeId != bookmark.Bundle.RootId || bookmark.CollectionId != "" ||
		rowType(objectRow(t, e, sp.Id, bookmark.TypeId)) != "__type__" {
		t.Fatalf("bookmark definition: %+v", bookmark)
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+bookmark.TypeId, &info)
	if info.XKey != "bookmark" {
		t.Fatalf("bookmark type info: %+v", info)
	}
	for _, key := range []string{"url", "purpose", "tags", "contexts", "inbox", "archived", "completed", "note", "readerText", "summary"} {
		if bookmark.Properties[key] == "" {
			t.Fatalf("bookmark property %s unresolved: %+v", key, bookmark.Properties)
		}
	}
	row := objectRow(t, e, sp.Id, app.Bundle.RootId)
	ma, _ := row["miniapp"].(map[string]any)
	if app.TypeId != "" || app.CollectionId != "" || rowType(row) != page.TypeId ||
		!slices.Contains(rowCollections(row), "miniapp") || ma["bundle"] != app.Id {
		t.Fatalf("bookmarks app root: bundle=%+v row=%v", app, row)
	}

	// Both entry points adopt the same shared definitions and app roots.
	for _, usecase := range []string{"tasks", "bookmarks"} {
		again := setupUsecase(t, e, usecase, sp.Id)
		expected := want
		if usecase == "tasks" {
			expected = want[:4]
		}
		if !slices.Equal(bundleIds(again), expected) {
			t.Fatalf("%s repeated setup order: %v", usecase, bundleIds(again))
		}
		for _, b := range again.Bundles {
			first := byId[b.Id]
			if b.Installed || b.Bundle.RootId != first.Bundle.RootId || b.CollectionId != first.CollectionId || b.TypeId != first.TypeId {
				t.Fatalf("%s repeated setup changed %s: %+v", usecase, b.Id, b)
			}
		}
	}
	// Projects and areas remain pages in their shared collections;
	// a bookmark below is created with its own type.
	create := func(collectionId string, name string, values map[string]any) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"type":              page.TypeId,
			"collections":       []string{collectionId},
			"initialProperties": map[string]any{"any": map[string]any{"name": name}, collectionId: values},
		})
		if err != nil {
			t.Fatal(err)
		}
		return mustCreateObject(t, e, sp.Id, string(body))
	}
	area, project := byId["system:area/v2"], byId["system:project/v2"]
	if project.Properties["parent"] == "" {
		t.Fatal("project parent property unresolved")
	}
	areaId := create(area.CollectionId, "Learning", map[string]any{})
	projectId := create(project.CollectionId, "Reading", map[string]any{
		project.Properties["parent"]: []string{anyuri.BuildObject(areaId)},
	})
	values := map[string]any{
		bookmark.Properties["url"]:       "https://example.com/article",
		bookmark.Properties["purpose"]:   []any{"read"},
		bookmark.Properties["contexts"]:  []any{anyuri.BuildObject(areaId), anyuri.BuildObject(projectId)},
		bookmark.Properties["inbox"]:     false,
		bookmark.Properties["archived"]:  false,
		bookmark.Properties["completed"]: true,
	}
	body, _ := json.Marshal(map[string]any{"type": bookmark.TypeId, "initialProperties": map[string]any{"any": map[string]any{"name": "An article"}, bookmark.TypeId: values}})
	id := mustCreateObject(t, e, sp.Id, string(body))
	row = objectRow(t, e, sp.Id, id)
	stored, _ := row[bookmark.TypeId].(map[string]any)
	if rowType(row) != bookmark.TypeId || len(rowCollections(row)) != 0 {
		t.Fatalf("bookmark membership: %v", row)
	}
	for key, wantValue := range values {
		if !reflect.DeepEqual(stored[key], wantValue) {
			t.Fatalf("bookmark property %s = %v, want %v", key, stored[key], wantValue)
		}
	}
	body, err := json.Marshal(map[string]any{"filter": map[string]any{"any.type": bookmark.TypeId}})
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query", string(body))
	var query api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &query); rec.Code != http.StatusOK || err != nil || len(query.Records) != 1 {
		t.Fatalf("query bookmarks: %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	var found map[string]any
	if err := json.Unmarshal(query.Records[0], &found); err != nil || found["id"] != id {
		t.Fatalf("query returned another object: %s (%v)", query.Records[0], err)
	}
}

// Catalog setup never converts an installed definition or its members.
func TestServer_CatalogSetupDoesNotConvertLegacyBookmarks(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	d.catalog = catalogForTest(t, legacyBookmarksCatalog)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	sp := createSpaceInfo(t, e, "Legacy bookmarks")
	old := setupUsecase(t, e, "bookmarks", sp.Id).Bundles[0]
	id := mustCreateObject(t, e, sp.Id, `{"type":"page","collections":["`+old.CollectionId+`"],"initialProperties":{"`+old.CollectionId+`":{"`+old.Properties["url"]+`":"https://example.com/keep","`+old.Properties["note"]+`":"Keep note"}}}`)
	before := objectRow(t, e, sp.Id, id)
	d.catalog = nil
	for attempt := 0; attempt < 2; attempt++ {
		rec := doJSON(t, e, http.MethodPost, "/v1/catalog/bookmarks/setup", `{"spaceId":"`+sp.Id+`"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected definition conflict without conversion: %d %s", rec.Code, rec.Body.String())
		}
		var envelope api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.Code != "catalog.definition_conflict" {
			t.Fatal(rec.Body.String())
		}
		root := objectRow(t, e, sp.Id, old.CollectionId)
		if rowType(root) != "__collection__" {
			t.Fatalf("setup converted definition: %v", root)
		}
		after := objectRow(t, e, sp.Id, id)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("setup changed legacy bookmark: before=%v after=%v", before, after)
		}
	}
}
