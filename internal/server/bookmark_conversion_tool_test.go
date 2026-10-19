//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/anyproto/any/internal/api"
	"github.com/labstack/echo/v4"
)

func legacyBookmarkFixture(t *testing.T, count int) (*deps, *echo.Echo, api.SpaceInfo, api.CatalogSetupBundle, []string) {
	t.Helper()
	d := newUnauthorizedDeps(t)
	d.cfg.Network, _ = placeholderNetwork(t)
	d.catalog = catalogForTest(t, legacyBookmarksCatalog)
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	info := createSpaceInfo(t, e, "Explicit conversion")
	old := setupUsecase(t, e, "bookmarks", info.Id).Bundles[0]
	ids := make([]string, count)
	for i := range ids {
		values := map[string]any{old.Properties["url"]: fmt.Sprintf("https://example.com/%d?q=keep", i), old.Properties["note"]: "A note — сохранить", old.Properties["readerText"]: "# Clipped text\n\n**Preserve**", old.Properties["extra"]: "Custom value"}
		body, _ := json.Marshal(map[string]any{"type": "page", "collections": []string{old.CollectionId, "template"}, "initialProperties": map[string]any{"any": map[string]any{"name": "Saved title"}, old.CollectionId: values}})
		ids[i] = mustCreateObject(t, e, info.Id, string(body))
	}
	d.catalog = nil
	return d, e, info, old, ids
}

func runBookmarkConversion(t *testing.T, addr, spaceID string, extra ...string) ([]byte, error) {
	t.Helper()
	args := append([]string{"../../scripts/convert-legacy-bookmarks.py", "--addr", addr, "--space-id", spaceID}, extra...)
	cmd := exec.CommandContext(t.Context(), "python3", args...)
	return cmd.CombinedOutput()
}

func TestServer_BookmarkConversionTool(t *testing.T) {
	_, e, info, old, ids := legacyBookmarkFixture(t, 103)
	server := httptest.NewServer(e)
	defer server.Close()
	before := make(map[string]map[string]any)
	for _, id := range ids {
		before[id] = objectRow(t, e, info.Id, id)
	}
	md := "/v1/spaces/" + info.Id + "/objects/" + ids[0] + "/editor/editor_blocks/markdown"
	markdownSet(t, e, md, "Original document body")
	before[ids[0]] = objectRow(t, e, info.Id, ids[0])
	out, err := runBookmarkConversion(t, server.URL, info.Id)
	if err != nil || !strings.Contains(string(out), `"pending": 103`) {
		t.Fatalf("preview: %s %v", out, err)
	}
	for _, id := range ids {
		if !reflect.DeepEqual(before[id], objectRow(t, e, info.Id, id)) {
			t.Fatal("preview changed a bookmark")
		}
	}
	if rowType(objectRow(t, e, info.Id, old.CollectionId)) != space.CollectionMarker {
		t.Fatal("preview promoted root")
	}
	backup := filepath.Join(t.TempDir(), "before.json")
	out, err = runBookmarkConversion(t, server.URL, info.Id, "--apply", "--backup", backup)
	if err != nil {
		t.Fatalf("apply: %s %v", out, err)
	}
	stat, err := os.Stat(backup)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("private backup: %v %v", stat, err)
	}
	var saved struct {
		Members []map[string]any `json:"members"`
	}
	data, err := os.ReadFile(backup)
	if err != nil || json.Unmarshal(data, &saved) != nil || len(saved.Members) != 103 {
		t.Fatalf("backup incomplete: %v", err)
	}
	for _, row := range saved.Members {
		if !reflect.DeepEqual(before[row["id"].(string)], row) {
			t.Fatal("backup differs from original row")
		}
	}
	for _, id := range ids {
		after := objectRow(t, e, info.Id, id)
		if rowType(after) != old.CollectionId || !reflect.DeepEqual(rowCollections(after), []string{"template"}) {
			t.Fatalf("bad membership: %v", after)
		}
		if !reflect.DeepEqual(before[id][old.CollectionId], after[old.CollectionId]) || before[id]["id"] != after["id"] {
			t.Fatal("identity or values changed")
		}
	}
	var body struct {
		Content string `json:"content"`
	}
	decodeGet(t, e, md, &body)
	if body.Content != "Original document body" {
		t.Fatalf("body changed: %q", body.Content)
	}
	markdownSet(t, e, md, "Original document body")
	setupUsecase(t, e, "bookmarks", info.Id)
	after := objectRow(t, e, info.Id, ids[0])
	out, err = runBookmarkConversion(t, server.URL, info.Id, "--apply", "--backup", filepath.Join(t.TempDir(), "again.json"))
	if err != nil || !strings.Contains(string(out), `"converted": 0`) {
		t.Fatalf("repeat: %s %v", out, err)
	}
	if !reflect.DeepEqual(after, objectRow(t, e, info.Id, ids[0])) {
		t.Fatal("repeat changed bookmark")
	}
}

func TestServer_BookmarkConversionStopsAndResumes(t *testing.T) {
	for _, scenario := range []string{"interruption", "root", "type", "membership"} {
		t.Run(scenario, func(t *testing.T) {
			d, e, info, old, ids := legacyBookmarkFixture(t, 3)
			sp, err := d.sdk.Spaces().Get(context.Background(), info.Id)
			if err != nil {
				t.Fatal(err)
			}
			custom, err := sp.Types().Create(context.Background(), space.TypeCreateParams{Name: "Custom"})
			if err != nil {
				t.Fatal(err)
			}
			var fired atomic.Bool
			var writes atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/modify") {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "read", 500)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var batch struct {
						ObjectID string `json:"objectId"`
					}
					if json.Unmarshal(raw, &batch) != nil {
						http.Error(w, "json", 500)
						return
					}
					target := ids[0]
					if scenario == "root" {
						target = old.CollectionId
					}
					if scenario == "interruption" {
						if writes.Add(1) == 3 && fired.CompareAndSwap(false, true) {
							http.Error(w, "interrupted", 503)
							return
						}
					} else if batch.ObjectID == target && fired.CompareAndSwap(false, true) {
						ops := []space.Op{{Type: space.OpSet, Path: "any.type", Value: custom}}
						if scenario == "root" {
							ops = []space.Op{{Type: space.OpSet, Path: "any.name", Value: "User renamed collection"}}
						}
						if scenario == "membership" {
							ops = []space.Op{{Type: space.OpPull, Path: "any.collections", Value: old.CollectionId}}
						}
						result, err := sp.Modify(r.Context(), space.ModifyBatch{ObjectId: target, Dataset: "objects", Records: []space.RecordModify{{Id: target, Ops: ops}}})
						if err != nil || len(result.Rejections) > 0 {
							http.Error(w, "fixture edit failed", 500)
							return
						}
					}
				}
				e.ServeHTTP(w, r)
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			out, err := runBookmarkConversion(t, server.URL, info.Id, "--apply", "--backup", filepath.Join(t.TempDir(), "before.json"))
			if err == nil || !fired.Load() {
				t.Fatalf("expected controlled refusal: %s %v", out, err)
			}
			switch scenario {
			case "interruption":
				out, err = runBookmarkConversion(t, server.URL, info.Id, "--apply", "--backup", filepath.Join(t.TempDir(), "resume.json"))
				if err != nil {
					t.Fatalf("resume: %s %v", out, err)
				}
				for _, id := range ids {
					if rowType(objectRow(t, e, info.Id, id)) != old.CollectionId {
						t.Fatal("resume missed member")
					}
				}
			case "root":
				root := objectRow(t, e, info.Id, old.CollectionId)
				if rowType(root) != space.CollectionMarker || root["any"].(map[string]any)["name"] != "User renamed collection" {
					t.Fatal("root edit overwritten")
				}
			case "type":
				if rowType(objectRow(t, e, info.Id, ids[0])) != custom {
					t.Fatal("custom type overwritten")
				}
			case "membership":
				row := objectRow(t, e, info.Id, ids[0])
				if rowType(row) != "page" || !reflect.DeepEqual(rowCollections(row), []string{"template"}) {
					t.Fatal("removed membership restored")
				}
			}
		})
	}
}

func TestBookmarkConversionToolUnit(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "python3", "../../scripts/test_convert_legacy_bookmarks.py").CombinedOutput()
	if err != nil {
		t.Fatalf("conversion tool unit tests: %s %v", out, err)
	}
}
