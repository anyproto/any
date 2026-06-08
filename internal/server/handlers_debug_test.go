package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_DebugFlow mirrors any-sync-sdk/debug_api_test.go at the
// HTTP level: create a space + type + property + object, do two
// property writes, then hit /debug/objects/:id and /debug. We assert
// shape, not exact tree-walk results — the SDK test already covers
// those substantively.
func TestServer_DebugFlow(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Bring up a space + type + property + object so the per-object
	// debug walk has more than just the root change to look at.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"DebugDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Note"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../types: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost,
		fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, typeResp.TypeId),
		`{"name":"Title","kind":"string","xKey":"title"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var propResp api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &propResp); err != nil {
		t.Fatalf("decode prop: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeResp.TypeId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../objects: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}

	// Two writes on top of the root change.
	setBaseURL := fmt.Sprintf("/v1/spaces/%s/properties/%s/base/%s",
		sp.Id, objResp.ObjectId, typeResp.TypeId)
	for _, val := range []string{"first", "second"} {
		body := fmt.Sprintf(`{"patch":{%q:%q}}`, propResp.PropId, val)
		rec = doJSON(t, e, http.MethodPost, setBaseURL, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("SetBase %s: status=%d body=%s", val, rec.Code, rec.Body.String())
		}
	}

	// Per-object debug snapshot.
	rec = doJSON(t, e, http.MethodGet,
		fmt.Sprintf("/v1/spaces/%s/debug/objects/%s", sp.Id, objResp.ObjectId), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../debug/objects/:id: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var od api.ObjectDebugResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &od); err != nil {
		t.Fatalf("decode object debug: %v", err)
	}
	if od.ObjectId != objResp.ObjectId {
		t.Errorf("objectId = %q, want %q", od.ObjectId, objResp.ObjectId)
	}
	if od.HeadsCount != 1 {
		t.Errorf("headsCount = %d, want 1 (single-writer)", od.HeadsCount)
	}
	if od.BranchCount != 0 {
		t.Errorf("branchCount = %d, want 0 (linear)", od.BranchCount)
	}
	if od.TreeLen < 3 {
		t.Errorf("treeLen = %d, want >= 3 (root + 2 writes)", od.TreeLen)
	}
	if od.Snapshots < 1 {
		t.Errorf("snapshots = %d, want >= 1 (root)", od.Snapshots)
	}
	if len(od.Heads) != 1 {
		t.Errorf("heads len = %d, want 1", len(od.Heads))
	}
	if od.LatestVersionId == "" {
		t.Error("latestVersionId empty; head must carry an OrderId")
	}
	if od.MaxAddSeq == 0 {
		t.Error("maxAddSeq = 0; controller must have applied at least one change")
	}
	switch od.SyncState {
	case "unknown", "syncing", "synced", "offline":
		// All valid for a freshly-created local object.
	default:
		t.Errorf("syncState = %q, want one of unknown/syncing/synced/offline", od.SyncState)
	}
	if od.Pending == nil {
		t.Error("pending must be [] not null")
	}

	// Space-level debug snapshot. Peers may be empty — no diff round
	// has necessarily run yet in this short-lived local test.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/debug", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../debug: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sd api.SpaceDebugResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sd); err != nil {
		t.Fatalf("decode space debug: %v", err)
	}
	if sd.SpaceId != sp.Id {
		t.Errorf("spaceId = %q, want %q", sd.SpaceId, sp.Id)
	}
	if sd.Peers == nil {
		t.Error("peers must be [] not null")
	}
	for _, p := range sd.Peers {
		if p.PeerId == "" {
			t.Error("peer row missing peerId")
		}
	}

	// Empty objectId → 404 from echo's router (no matching route for
	// .../debug/objects/). Spot-check 404 for an unknown object id —
	// the SDK errors with "load <id>: not found".
	rec = doJSON(t, e, http.MethodGet,
		"/v1/spaces/"+sp.Id+"/debug/objects/does-not-exist", "")
	if rec.Code < 400 {
		t.Errorf("unknown objectId status=%d, want >= 400; body=%s", rec.Code, rec.Body.String())
	}
}
