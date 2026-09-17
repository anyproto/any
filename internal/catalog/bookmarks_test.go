package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anyproto/any/internal/api"
)

func TestCatalog_BookmarksReuseSharedContexts(t *testing.T) {
	cat, problems := Load(Embedded(), knownTypes)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	u, ok := cat.Get("bookmarks")
	if !ok || !reflect.DeepEqual(u.Requires, []string{"tasks"}) || len(u.Bundles) != 2 {
		t.Fatalf("bookmarks usecase: %+v", u)
	}
	item := u.Bundles[0]
	if item.Id != "system:bookmark/v1" || item.Type == nil || item.Type.XKey != "bookmark" ||
		item.Collection != nil || item.RootType != "" || item.Miniapp != nil ||
		len(item.Parts) != 0 || item.Hidden || item.Derived {
		t.Fatalf("bookmark must declare a listed type: %+v", item)
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
	if want := []string{"system:task/v1", "system:project/v1", "system:area/v1", "system:tasks/v1", "system:bookmark/v1", "system:bookmarks/v1"}; !reflect.DeepEqual(bundles, want) {
		t.Fatalf("bookmarks must adopt the shared task definitions before its own bundles: %v", bundles)
	}
	props := map[string]api.AddPropertyRequest{}
	for _, p := range item.Type.Properties {
		props[p.XKey] = p
	}
	for _, key := range []string{"inbox", "archived", "completed"} {
		if props[key].Kind != api.PropertyKindBoolean {
			t.Errorf("%s must be an independent boolean", key)
		}
	}
	if _, ok := props["favorite"]; ok {
		t.Error("bookmarks use native Any favorites, not a bookmark-specific property")
	}
	for _, key := range []string{"purpose", "tags"} {
		t.Run(key, func(t *testing.T) {
			var choice struct {
				Type    string                     `json:"type"`
				Options map[string]json.RawMessage `json:"options"`
				Config  struct {
					Multiple *bool `json:"multiple"`
				} `json:"config"`
			}
			if err := json.Unmarshal(props[key].XFormat, &choice); err != nil {
				t.Fatal(err)
			}
			if props[key].Kind != api.PropertyKindArray || choice.Type != "choice" {
				t.Fatalf("%s must be a native choice property: %+v", key, props[key])
			}
			if choice.Config.Multiple == nil || *choice.Config.Multiple != (key == "tags") {
				t.Fatalf("%s choice multiplicity: %+v", key, choice.Config)
			}
			if key == "purpose" {
				want := []string{"read", "buy", "inspiration", "try"}
				if len(choice.Options) != len(want) {
					t.Fatalf("purpose must seed four stable option keys: %v", choice.Options)
				}
				for _, option := range want {
					if _, ok := choice.Options[option]; !ok {
						t.Errorf("purpose option %q missing", option)
					}
				}
			} else if choice.Options == nil || len(choice.Options) != 0 {
				t.Fatalf("tags must start with an empty editable options map: %v", choice.Options)
			}
		})
	}
	var relation struct {
		Type   string `json:"type"`
		Config struct {
			Multiple bool `json:"multiple"`
		} `json:"config"`
		Relation struct {
			TargetTypes []string `json:"targetTypes"`
		} `json:"relation"`
	}
	if err := json.Unmarshal(props["contexts"].XFormat, &relation); err != nil {
		t.Fatal(err)
	}
	if props["contexts"].Kind != api.PropertyKindArray || relation.Type != "relation" ||
		!relation.Config.Multiple || !reflect.DeepEqual(relation.Relation.TargetTypes, []string{"project", "area"}) {
		t.Fatalf("bookmark context relation: %+v", relation)
	}
	if props["url"].Kind != api.PropertyKindString || len(props) != 10 {
		t.Fatalf("URL bookmark properties: %+v", props)
	}
	if sidebar := u.Bundles[1]; sidebar.Id != "system:bookmarks/v1" || sidebar.Miniapp == nil ||
		sidebar.RootType != "page" || sidebar.Type != nil || sidebar.Collection != nil ||
		len(sidebar.Parts) != 0 || sidebar.Hidden || sidebar.Derived {
		t.Fatalf("bookmarks sidebar: %+v", sidebar)
	}
}
