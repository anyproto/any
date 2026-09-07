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

	checkDatasets := func(t *testing.T, path string, wantFields ...string) {
		rec := doJSON(t, e, http.MethodGet, path, "")
		seen := map[string]bool{}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		for _, ds := range datasetsIn(t, rec.Body.Bytes()) {
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
				seen[where] = true
				if f.Description == "" {
					t.Errorf("%s: no description", where)
				}
				if _, code, reason := validateDescriptor(f.XFormat, f.Type); code != "" {
					t.Errorf("%s: descriptor rejected: %s %s", where, code, reason)
				}
			}
		}
		for _, w := range wantFields {
			if !seen[w] {
				t.Errorf("%s: %s not listed", path, w)
			}
		}
	}
	t.Run("space datasets", func(t *testing.T) {
		checkDatasets(t, "/v1/spaces/"+spaceId+"/datasets",
			"objects.name", "chat_messages.createdAt", "editor_blocks.text", "views.layout")
	})
	t.Run("system datasets", func(t *testing.T) {
		checkDatasets(t, "/v1/datasets", "spaces.createdAt", "devices.name")
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
			var defs []api.PropertyDef
			decodeArrayField(t, rec.Body.Bytes(), &defs)
			for _, p := range defs {
				seen++
				where := id + "." + p.Id
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

// datasetsIn extracts the dataset list from a datasets response
// without binding to the envelope key.
func datasetsIn(t *testing.T, body []byte) []api.DatasetSchema {
	t.Helper()
	var out []api.DatasetSchema
	decodeArrayField(t, body, &out)
	return out
}

// decodeArrayField decodes the one array-valued member of a JSON
// object envelope into out.
func decodeArrayField(t *testing.T, body []byte, out any) {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, body)
	}
	for _, raw := range env {
		if len(raw) > 0 && raw[0] == '[' {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("decode list: %v\n%s", err, raw)
			}
			return
		}
	}
	t.Fatalf("no list in envelope: %s", body)
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
