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
	props := map[string]api.AddPropertyRequest{}
	for _, b := range u.Bundles {
		if b.Type != nil && b.Type.XKey != "bookmark" {
			t.Fatal("bookmarks declares only its own item type and reuses shared project and area definitions")
		}
		if b.Id == "system:bookmark/v1" {
			for _, p := range b.Type.Properties {
				props[p.XKey] = p
			}
		}
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
	if !relation.Config.Multiple || !reflect.DeepEqual(relation.Relation.TargetTypes, []string{"project", "area"}) {
		t.Fatalf("bookmark context relation: %+v", relation)
	}
	if props["url"].Kind != api.PropertyKindString || len(props) != 10 {
		t.Fatalf("URL bookmark properties: %+v", props)
	}
	if sidebar := u.Bundles[1]; sidebar.Id != "system:bookmarks/v1" || sidebar.Miniapp == nil {
		t.Fatalf("bookmarks sidebar: %+v", sidebar)
	}
}
