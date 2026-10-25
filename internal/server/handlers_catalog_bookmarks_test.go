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
	want := []string{"system:bookmark-folder/v1", "system:bookmark/v1", "system:bookmarks/v1"}
	if got := bundleIds(res); !slices.Equal(got, want) {
		t.Fatalf("setup order = %v, want %v", got, want)
	}
	byId := map[string]api.CatalogSetupBundle{}
	for _, b := range res.Bundles {
		byId[b.Id] = b
		if !b.Installed || b.Usecase != "bookmarks" || b.Bundle.RootId == "" {
			t.Fatalf("fresh bundle: %+v", b)
		}
	}
	bookmark, app := byId["system:bookmark/v1"], byId["system:bookmarks/v1"]
	if bookmark.CollectionId != bookmark.Bundle.RootId || bookmark.TypeId != "" ||
		rowType(objectRow(t, e, sp.Id, bookmark.CollectionId)) != "__collection__" {
		t.Fatalf("bookmark definition: %+v", bookmark)
	}
	var info api.CollectionInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+bookmark.CollectionId, &info)
	if info.XKey != "bookmark" || info.Meta["defaultType"] != "page" {
		t.Fatalf("bookmark collection info: %+v", info)
	}
	for _, key := range []string{"url", "folder", "archived"} {
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
	for _, ti := range types.Types {
		if ti.XKey == "bookmark" || ti.Id == bookmark.CollectionId || ti.Id == app.Bundle.RootId {
			t.Fatalf("bookmarks must not declare a type: %+v", ti)
		}
	}

	// A repeated setup adopts the same definitions and app root.
	for _, usecase := range []string{"bookmarks"} {
		again := setupUsecase(t, e, usecase, sp.Id)
		if !slices.Equal(bundleIds(again), want) {
			t.Fatalf("%s repeated setup order: %v", usecase, bundleIds(again))
		}
		for _, b := range again.Bundles {
			first := byId[b.Id]
			if b.Installed || b.Bundle.RootId != first.Bundle.RootId || b.CollectionId != first.CollectionId || b.TypeId != first.TypeId {
				t.Fatalf("%s repeated setup changed %s: %+v", usecase, b.Id, b)
			}
		}
	}
	// Folders and bookmarks are pages, each filed under its collection with
	// the values keyed by that collection.
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
	folder := byId["system:bookmark-folder/v1"]
	if folder.Properties["parent"] == "" {
		t.Fatal("folder parent property unresolved")
	}
	workId := create(folder.CollectionId, "Work", map[string]any{})
	designId := create(folder.CollectionId, "Design system", map[string]any{
		folder.Properties["parent"]: []any{anyuri.BuildObject(workId)},
	})
	values := map[string]any{
		bookmark.Properties["url"]:      "https://example.com/article",
		bookmark.Properties["folder"]:   []any{anyuri.BuildObject(designId)},
		bookmark.Properties["archived"]: false,
	}
	id := create(bookmark.CollectionId, "An article", values)
	row = objectRow(t, e, sp.Id, id)
	stored, _ := row[bookmark.CollectionId].(map[string]any)
	if rowType(row) != page.TypeId || !slices.Equal(rowCollections(row), []string{bookmark.CollectionId}) {
		t.Fatalf("bookmark membership: %v", row)
	}
	for key, wantValue := range values {
		if !reflect.DeepEqual(stored[key], wantValue) {
			t.Fatalf("bookmark property %s = %v, want %v", key, stored[key], wantValue)
		}
	}
	body, err := json.Marshal(map[string]any{"filter": map[string]any{"any.collections": bookmark.CollectionId}})
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
