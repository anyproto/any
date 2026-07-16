package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// getSpaceInfo fetches GET /v1/spaces/:id and decodes the SpaceInfo.
func getSpaceInfo(t *testing.T, e http.Handler, id string) api.SpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET space: %d %s", rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestSpaceSettings_PatchAndReadBack(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId := mustCreateSpaceForSettings(t, e)

	// Fresh space: no settings.
	if info := getSpaceInfo(t, e, spaceId); info.Settings != nil {
		t.Errorf("fresh settings = %+v, want absent", info.Settings)
	}

	// Set two keys of different scalar kinds.
	rec := doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/settings",
		`{"set":{"notifyMode":"mentions","pinned":true,"order":3}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH settings: %d %s", rec.Code, rec.Body.String())
	}
	info := getSpaceInfo(t, e, spaceId)
	if info.Settings["notifyMode"] != "mentions" || info.Settings["pinned"] != true {
		t.Errorf("settings after set = %+v", info.Settings)
	}
	if n, ok := info.Settings["order"].(float64); !ok || n != 3 {
		t.Errorf("numeric setting = %#v, want 3 (JSON number)", info.Settings["order"])
	}

	// Per-key patch: overwrite one key, unset another, in one change.
	rec = doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/settings",
		`{"set":{"notifyMode":"none"},"unset":["pinned"]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH settings 2: %d %s", rec.Code, rec.Body.String())
	}
	info = getSpaceInfo(t, e, spaceId)
	if info.Settings["notifyMode"] != "none" {
		t.Errorf("notifyMode = %v, want none", info.Settings["notifyMode"])
	}
	if _, present := info.Settings["pinned"]; present {
		t.Errorf("pinned should be unset: %+v", info.Settings)
	}
	if info.Settings["order"] == nil {
		t.Errorf("untouched key must survive the patch: %+v", info.Settings)
	}

	// Settings ride the list rows too.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	var list api.SpaceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Spaces) != 1 || list.Spaces[0].Settings["notifyMode"] != "none" {
		t.Errorf("list rows should carry settings: %+v", list.Spaces)
	}
}

func TestSpaceSettings_Validation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId := mustCreateSpaceForSettings(t, e)

	cases := []struct {
		name, body, code string
	}{
		{"bad json", "{not json", "request.bad_json"},
		{"empty patch", `{}`, "request.missing_field"},
		{"empty sets", `{"set":{},"unset":[]}`, "request.missing_field"},
		{"dotted set key", `{"set":{"a.b":1}}`, "request.invalid_field"},
		{"empty unset key", `{"unset":[""]}`, "request.invalid_field"},
		{"overlap", `{"set":{"k":1},"unset":["k"]}`, "request.invalid_field"},
		{"object value", `{"set":{"k":{"nested":true}}}`, "request.invalid_field"},
		{"array value", `{"set":{"k":[1,2]}}`, "request.invalid_field"},
		{"null value", `{"set":{"k":null}}`, "request.invalid_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/settings", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != tc.code {
				t.Errorf("code = %q, want %q", apiErr.Code, tc.code)
			}
		})
	}

	// Unknown space → 404 (after body validation).
	rec := doJSON(t, e, http.MethodPatch, "/v1/spaces/does-not-exist/settings",
		`{"set":{"notifyMode":"all"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown space: %d %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != "space.not_found" {
		t.Errorf("code = %q, want space.not_found", apiErr.Code)
	}

	// Bad body on an unknown space still 400s first (validation before
	// space lookup).
	rec = doJSON(t, e, http.MethodPatch, "/v1/spaces/does-not-exist/settings", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("body validation should precede space lookup: %d %s", rec.Code, rec.Body.String())
	}
}

// mustCreateSpaceForSettings mirrors handlers_indexer_test.go's
// mustCreateSpace, which is compiled only under the fts/vector build
// tags — settings tests must not depend on those.
func mustCreateSpaceForSettings(t *testing.T, e http.Handler) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SettingsTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	return sp.Id
}
