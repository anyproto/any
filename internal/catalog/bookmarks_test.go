package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anyproto/any/internal/api"
)

func TestCatalog_BookmarksFileIntoNestedFolders(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	u, ok := cat.Get("bookmarks")
	if !ok || len(u.Requires) != 0 || len(u.Bundles) != 3 {
		t.Fatalf("bookmarks usecase: %+v", u)
	}
	order, ok := cat.Order("bookmarks")
	if !ok {
		t.Fatal("bookmarks dependency order missing")
	}
	var bundles []string
	for _, usecase := range order {
		for _, bundle := range usecase.Bundles {
			bundles = append(bundles, bundle.Id)
		}
	}
	if want := []string{"system:bookmark-folder/v1", "system:bookmark/v1", "system:bookmarks/v1"}; !reflect.DeepEqual(bundles, want) {
		t.Fatalf("bookmarks bundles: %v", bundles)
	}
	relationTo := func(t *testing.T, p api.AddPropertyRequest) []string {
		t.Helper()
		var relation struct {
			Type   string `json:"type"`
			Config struct {
				Multiple bool `json:"multiple"`
			} `json:"config"`
			Relation struct {
				TargetTypes []string `json:"targetTypes"`
			} `json:"relation"`
		}
		if err := json.Unmarshal(p.XFormat, &relation); err != nil {
			t.Fatal(err)
		}
		if p.Kind != api.PropertyKindArray || relation.Type != "relation" || relation.Config.Multiple {
			t.Fatalf("%s must be a single relation: %+v", p.XKey, relation)
		}
		return relation.Relation.TargetTypes
	}
	propsOf := func(b api.CatalogBundle) map[string]api.AddPropertyRequest {
		props := map[string]api.AddPropertyRequest{}
		for _, p := range b.Collection.Properties {
			props[p.XKey] = p
		}
		return props
	}
	for i, xKey := range []string{"bookmark_folder", "bookmark"} {
		b := u.Bundles[i]
		if b.Collection == nil || b.Collection.XKey != xKey || b.Type != nil || b.Collection.Meta["defaultType"] != "page" {
			t.Fatalf("%s must be a collection of pages: %+v", xKey, b)
		}
	}
	folder := propsOf(u.Bundles[0])
	if got := relationTo(t, folder["parent"]); !reflect.DeepEqual(got, []string{"bookmark_folder"}) || len(folder) != 2 {
		t.Fatalf("a folder sits inside one other folder: %v %+v", got, folder)
	}
	bookmark := propsOf(u.Bundles[1])
	if got := relationTo(t, bookmark["folder"]); !reflect.DeepEqual(got, []string{"bookmark_folder"}) {
		t.Fatalf("a bookmark sits in one folder: %v", got)
	}
	if bookmark["url"].Kind != api.PropertyKindString || bookmark["archived"].Kind != api.PropertyKindBoolean || bookmark["position"].Kind != api.PropertyKindNumber || len(bookmark) != 4 {
		t.Fatalf("bookmark properties: %+v", bookmark)
	}
	if sidebar := u.Bundles[2]; sidebar.Id != "system:bookmarks/v1" || sidebar.Miniapp == nil || sidebar.RootType != "page" {
		t.Fatalf("bookmarks sidebar: %+v", sidebar)
	}
}
