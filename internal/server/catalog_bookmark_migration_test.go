package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anyproto/any-sync-sdk/space"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
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

func TestServer_BookmarkMigrationPreservesData(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	d.catalog = catalogForTest(t, legacyBookmarksCatalog)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	sp := createSpaceInfo(t, e, "Bookmark migration")
	old := setupUsecase(t, e, "bookmarks", sp.Id).Bundles[0]
	values := map[string]any{old.Properties["url"]: "https://example.com/keep?q=1", old.Properties["note"]: "A note — сохранить", old.Properties["readerText"]: "# Clipped text\n\n**Keep all of it**", old.Properties["extra"]: "User-added value"}
	payload, _ := json.Marshal(map[string]any{"type": "page", "collections": []string{old.CollectionId}, "initialProperties": map[string]any{"any": map[string]any{"name": "Keep my bookmark"}, old.CollectionId: values}})
	id := mustCreateObject(t, e, sp.Id, string(payload))
	md := "/v1/spaces/" + sp.Id + "/objects/" + id + "/editor/editor_blocks/markdown"
	markdownSet(t, e, md, "Original document body")
	before := objectRow(t, e, sp.Id, id)
	d.catalog = nil
	for attempt := 0; attempt < 2; attempt++ {
		setup := setupUsecase(t, e, "bookmarks", sp.Id)
		var current api.CatalogSetupBundle
		for _, b := range setup.Bundles {
			if b.Id == old.Id {
				current = b
			}
		}
		if current.TypeId != old.CollectionId || current.CollectionId != "" {
			t.Fatalf("migration changed definition identity or kind: %+v", current)
		}
		after := objectRow(t, e, sp.Id, id)
		if rowType(after) != current.TypeId || len(rowCollections(after)) != 0 {
			t.Fatalf("bookmark not retyped: %v", after)
		}
		if !reflect.DeepEqual(before[old.CollectionId], after[current.TypeId]) {
			t.Fatalf("bookmark values changed: before=%v after=%v", before[old.CollectionId], after[current.TypeId])
		}
		if before["id"] != after["id"] {
			t.Fatal("identity changed")
		}
		var body struct {
			Content string `json:"content"`
		}
		decodeGet(t, e, md, &body)
		if body.Content != "Original document body" {
			t.Fatalf("body changed: %q", body.Content)
		}
		markdownSet(t, e, md, "Original document body") // the promoted type still admits document writes.
	}
}

// failBookmarkWrite simulates interruption after a successful member write.
type failBookmarkWrite struct {
	space.Space
	writes int
}

func (s *failBookmarkWrite) Modify(ctx context.Context, b space.ModifyBatch) (space.ModifyResult, error) {
	s.writes++
	if s.writes == 3 {
		return space.ModifyResult{}, errors.New("simulated interruption")
	}
	return s.Space.Modify(ctx, b)
}

func TestServer_BookmarkMigrationResumesAfterInterruption(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	d.catalog = catalogForTest(t, legacyBookmarksCatalog)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	info := createSpaceInfo(t, e, "Resume bookmarks")
	old := setupUsecase(t, e, "bookmarks", info.Id).Bundles[0]
	sp, err := d.sdk.Spaces().Get(context.Background(), info.Id)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 103; i++ {
		id, err := sp.Objects().Create(context.Background(), space.CreateObjectOpts{Type: "page", Collections: []string{old.CollectionId, "template"}, InitialProperties: map[string]map[string]any{old.CollectionId: {old.Properties["url"]: fmt.Sprintf("https://example.com/%d", i), old.Properties["note"]: "retain"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	d.catalog = nil
	cc, err := d.usecaseCatalog()
	if err != nil {
		t.Fatal(err)
	}
	order, _ := cc.order("bookmarks")
	var parts []space.PartDraft
	for _, u := range order {
		for _, b := range u.bundles {
			if b.install.Id == bookmarkBundleID {
				parts = b.install.Parts
			}
		}
	}
	broken := &failBookmarkWrite{Space: sp}
	if err := migrateBookmarkRoot(context.Background(), broken, old.CollectionId, parts); err == nil || !strings.Contains(err.Error(), "simulated interruption") {
		t.Fatalf("expected interruption, got %v", err)
	}
	migrated, err := sp.QueryObjects().Filter(map[string]any{"any.type": old.CollectionId}).Count(context.Background())
	if err != nil || migrated != 1 {
		t.Fatalf("expected one committed bookmark before interruption: %d %v", migrated, err)
	}
	setupUsecase(t, e, "bookmarks", info.Id)
	for _, id := range ids {
		row := objectRow(t, e, info.Id, id)
		if rowType(row) != old.CollectionId || !reflect.DeepEqual(rowCollections(row), []string{"template"}) {
			t.Fatalf("wrong membership after resumed migration: %v", row)
		}
		values, _ := row[old.CollectionId].(map[string]any)
		if values[old.Properties["note"]] != "retain" {
			t.Fatal("note lost")
		}
	}
	setupUsecase(t, e, "bookmarks", info.Id)
	n, err := sp.QueryObjects().Filter(map[string]any{"any.type": old.CollectionId}).Count(context.Background())
	if err != nil || n != len(ids) {
		t.Fatalf("repeat duplicated or lost bookmarks: %d %v", n, err)
	}
}

// editBeforeBookmarkWrite lands a competing write after the migration read,
// immediately before its commit. This is the same ordering as an applied peer edit.
type editBeforeBookmarkWrite struct {
	space.Space
	target string
	edit   []space.Op
	edited bool
}

func (s *editBeforeBookmarkWrite) Modify(ctx context.Context, b space.ModifyBatch) (space.ModifyResult, error) {
	if b.ObjectId == s.target && !s.edited {
		s.edited = true
		res, err := s.Space.Modify(ctx, space.ModifyBatch{ObjectId: s.target, Dataset: objectsDataset, Records: []space.RecordModify{{Id: s.target, Ops: s.edit}}})
		if err != nil || len(res.Rejections) > 0 {
			return res, fmt.Errorf("competing edit: %v %v", err, res.Rejections)
		}
	}
	return s.Space.Modify(ctx, b)
}
func TestServer_BookmarkMigrationConcurrentEdit(t *testing.T) {
	for _, scenario := range []string{"root", "type", "membership"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			d := newUnauthorizedDeps(t)
			d.cfg.Network, _ = placeholderNetwork(t)
			d.catalog = catalogForTest(t, legacyBookmarksCatalog)
			e := buildEcho(d)
			if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			info := createSpaceInfo(t, e, "Concurrent bookmark migration")
			old := setupUsecase(t, e, "bookmarks", info.Id).Bundles[0]
			sp, err := d.sdk.Spaces().Get(ctx, info.Id)
			if err != nil {
				t.Fatal(err)
			}
			id, err := sp.Objects().Create(ctx, space.CreateObjectOpts{Type: "page", Collections: []string{old.CollectionId}, InitialProperties: map[string]map[string]any{old.CollectionId: {old.Properties["url"]: "https://example.com/keep", old.Properties["note"]: "retain"}}})
			if err != nil {
				t.Fatal(err)
			}
			custom, err := sp.Types().Create(ctx, space.TypeCreateParams{Name: "Custom"})
			if err != nil {
				t.Fatal(err)
			}
			target := id
			ops := []space.Op{{Type: space.OpSet, Path: "any.type", Value: custom}}
			switch scenario {
			case "root":
				target = old.CollectionId
				ops = []space.Op{{Type: space.OpSet, Path: "any.name", Value: "My renamed collection"}}
			case "membership":
				ops = []space.Op{{Type: space.OpPull, Path: "any.collections", Value: old.CollectionId}}
			}
			wrapped := &editBeforeBookmarkWrite{Space: sp, target: target, edit: ops}
			err = migrateBookmarkRoot(ctx, wrapped, old.CollectionId, nil)
			if err == nil {
				t.Fatal("stale migration write accepted")
			}
			row := objectRow(t, e, info.Id, id)
			switch scenario {
			case "root":
				root := objectRow(t, e, info.Id, old.CollectionId)
				if rowType(root) != space.CollectionMarker || root["any"].(map[string]any)["name"] != "My renamed collection" {
					t.Fatalf("root edit overwritten: %v", root)
				}
			case "type":
				if rowType(row) != custom {
					t.Fatalf("custom type overwritten: %v", row)
				}
			case "membership":
				if rowType(row) != "page" || len(rowCollections(row)) != 0 {
					t.Fatalf("unfiled object migrated: %v", row)
				}
			}
			values := row[old.CollectionId].(map[string]any)
			if values[old.Properties["url"]] != "https://example.com/keep" || values[old.Properties["note"]] != "retain" {
				t.Fatalf("content lost: %v", values)
			}
		})
	}
}
