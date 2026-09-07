package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// Every field the server serves from a Go declaration — the module and
// static datasets of a space, the account's system datasets, the
// built-in types' properties — carries a description, and a descriptor
// that passes the same gate a client-declared one does. A declaration
// that drifts from the vocabulary fails here the way a client draft
// 400s.
func TestServer_BuiltinDescriptors(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"descriptors"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	spaceId := sp.Id

	// wantSlugs pins descriptors that must be present: the test otherwise
	// validates only what is declared, so a dropped XFormat would pass.
	checkDatasets := func(t *testing.T, path string, wantSlugs map[string]string) {
		rec := doJSON(t, e, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		var resp api.DatasetsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		slugs := map[string]string{}
		for _, ds := range resp.Datasets {
			if isTestSeam(ds.Owners) {
				continue
			}
			var doc struct {
				Properties map[string]struct {
					Type        string          `json:"type"`
					Description string          `json:"description"`
					XFormat     json.RawMessage `json:"x-format"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(ds.Schema, &doc); err != nil {
				t.Fatalf("%s: decode %s: %v", path, ds.Name, err)
			}
			for id, f := range doc.Properties {
				where := ds.Name + "." + id
				slugs[where] = slugOf(f.XFormat)
				if f.Description == "" {
					t.Errorf("%s: no description", where)
				}
				if _, code, reason := validateDescriptor(f.XFormat, f.Type); code != "" {
					t.Errorf("%s: descriptor rejected: %s %s", where, code, reason)
				}
			}
		}
		for field, want := range wantSlugs {
			if got, ok := slugs[field]; !ok {
				t.Errorf("%s: %s not listed", path, field)
			} else if got != want {
				t.Errorf("%s: %s slug %q, want %q", path, field, got, want)
			}
		}
	}
	t.Run("space datasets", func(t *testing.T) {
		checkDatasets(t, "/v1/spaces/"+spaceId+"/datasets", map[string]string{
			"objects.createdAt":       "datetime",
			"chat_messages.createdAt": "datetime",
			"chat_messages.unread":    "checkbox",
			"editor_blocks.text":      "",
			"views.layout":            "",
			"dataviews.name":          "text",
		})
	})
	t.Run("system datasets", func(t *testing.T) {
		checkDatasets(t, "/v1/datasets", map[string]string{
			"spaces.createdAt": "datetime",
			"spaces.derived":   "checkbox",
			"devices.name":     "text",
		})
	})

	t.Run("built-in type properties", func(t *testing.T) {
		seen := 0
		for id, ti := range typesById(t, e, "/v1/spaces/"+spaceId, "?includeHidden=true") {
			if !ti.BuiltIn || isTestSeam([]string{id}) {
				continue
			}
			rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/types/"+id+"/properties", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("properties of %s: %d %s", id, rec.Code, rec.Body.String())
			}
			var resp api.PropertiesListResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("properties of %s: decode: %v", id, err)
			}
			for _, p := range resp.Properties {
				seen++
				where := id + "." + p.Id
				if id == "any" && p.Id == "name" && slugOf(p.XFormat) != "text" {
					t.Errorf("%s: slug %q, want text", where, slugOf(p.XFormat))
				}
				if p.Description == "" {
					t.Errorf("%s: no description", where)
				}
				if _, code, reason := validateDescriptor(p.XFormat, p.Kind); code != "" {
					t.Errorf("%s: descriptor rejected: %s %s", where, code, reason)
				}
			}
		}
		if seen == 0 {
			t.Fatal("no built-in properties listed")
		}
	})
}

// slugOf reads the descriptor's type slug ("" when absent).
func slugOf(raw json.RawMessage) string {
	var xf struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &xf)
	return xf.Type
}

// isTestSeam skips the extraCatalog fixtures registered by other test
// files — declared for structure, not for display.
func isTestSeam(ids []string) bool {
	for _, id := range ids {
		if strings.HasPrefix(id, "testdoc") {
			return true
		}
	}
	return false
}
