package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_Backlinks covers the reverse-reference read: objects
// linking to a target through a relation property show up in the
// target's /backlinks, with the declaring (typeId, propId) pair;
// non-referencing objects and unknown targets yield empty lists.
func TestServer_Backlinks(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"BacklinksDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Doc","xKey":"doc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types/"+tr.TypeId+"/properties",
		`{"name":"Related","xKey":"related","kind":"array","xFormat":{"type":"relation","config":{"multiple":true}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create relation prop: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatalf("decode prop: %v", err)
	}

	createObject := func(body string) string {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create object: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp api.ObjectsCreateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode object: %v", err)
		}
		return resp.ObjectId
	}

	target := createObject(`{"types":["` + tr.TypeId + `"]}`)
	refA := createObject(`{"types":["` + tr.TypeId + `"],"initialProperties":{"` +
		tr.TypeId + `":{"` + pr.PropId + `":["any://` + target + `"]}}}`)
	refB := createObject(`{"types":["` + tr.TypeId + `"],"initialProperties":{"` +
		tr.TypeId + `":{"` + pr.PropId + `":["any://elsewhere","any://` + target + `"]}}}`)
	// Links elsewhere only — must not show up.
	createObject(`{"types":["` + tr.TypeId + `"],"initialProperties":{"` +
		tr.TypeId + `":{"` + pr.PropId + `":["any://elsewhere"]}}}`)

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/objects/"+target+"/backlinks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET backlinks: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.BacklinksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode backlinks: %v", err)
	}
	if len(resp.Backlinks) != 2 {
		t.Fatalf("backlinks = %+v, want exactly refs from %s and %s", resp.Backlinks, refA, refB)
	}
	got := map[string]bool{}
	for _, b := range resp.Backlinks {
		got[b.ObjectId] = true
		if b.TypeId != tr.TypeId || b.PropId != pr.PropId {
			t.Errorf("backlink %+v: want typeId=%s propId=%s", b, tr.TypeId, pr.PropId)
		}
	}
	if !got[refA] || !got[refB] {
		t.Errorf("backlink objectIds = %v, want {%s, %s}", got, refA, refB)
	}

	// An id nobody references — including one that doesn't exist —
	// yields an empty (non-null) list.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/objects/nonexistent/backlinks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET backlinks (unknown): status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !json.Valid([]byte(body)) {
		t.Fatalf("invalid JSON: %s", body)
	}
	resp = api.BacklinksResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode backlinks: %v", err)
	}
	if resp.Backlinks == nil || len(resp.Backlinks) != 0 {
		t.Errorf("unknown target: backlinks = %+v, want []", resp.Backlinks)
	}
}
