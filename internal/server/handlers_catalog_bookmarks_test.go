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
	want := []string{"system:task/v1", "system:project/v1", "system:area/v1", "system:tasks/v1", "system:bookmark/v1", "system:bookmarks/v1"}
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
	var types api.TypesListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types", &types)
	definitions := 0
	for _, info := range types.Types {
		if info.XKey == "bookmark" {
			definitions++
			if info.Id != bookmark.TypeId {
				t.Fatalf("bookmark handle resolves to another type: %+v", info)
			}
		}
	}
	if definitions != 1 {
		t.Fatalf("bookmark type definitions = %d, want 1", definitions)
	}
	var collections api.CollectionsListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections", &collections)
	for _, info := range collections.Collections {
		if info.XKey == "bookmark" || info.Id == bookmark.TypeId || info.Id == app.Bundle.RootId {
			t.Fatalf("bookmarks must not declare a collection: %+v", info)
		}
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
			if b.Installed || b.Bundle.RootId != byId[b.Id].Bundle.RootId || b.TypeId != byId[b.Id].TypeId {
				t.Fatalf("%s repeated setup changed %s: %+v", usecase, b.Id, b)
			}
		}
	}
	create := func(typeId string, props map[string]map[string]any) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"type": typeId, "initialProperties": props})
		if err != nil {
			t.Fatal(err)
		}
		return mustCreateObject(t, e, sp.Id, string(body))
	}
	area, project := byId["system:area/v1"], byId["system:project/v1"]
	areaId := create(area.TypeId, map[string]map[string]any{"any": {"name": "Learning"}})
	if project.Properties["parent"] == "" {
		t.Fatal("project parent property unresolved")
	}
	projectId := create(project.TypeId, map[string]map[string]any{
		"any":          {"name": "Reading"},
		project.TypeId: {project.Properties["parent"]: []string{anyuri.BuildObject(areaId)}},
	})
	values := map[string]any{
		bookmark.Properties["url"]:       "https://example.com/article",
		bookmark.Properties["purpose"]:   []any{"read"},
		bookmark.Properties["contexts"]:  []any{anyuri.BuildObject(areaId), anyuri.BuildObject(projectId)},
		bookmark.Properties["inbox"]:     false,
		bookmark.Properties["archived"]:  false,
		bookmark.Properties["completed"]: true,
	}
	id := create(bookmark.TypeId, map[string]map[string]any{"any": {"name": "An article"}, bookmark.TypeId: values})
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
