package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_SpaceSync exercises POST /v1/spaces/:id/sync — the wrapper
// for Space.SyncHeads. A freshly created space has responsible nodes, so
// forcing a head-sync round should complete and return 204. The endpoint
// blocks until the round finishes; we only assert the status code, not
// any converged state (single process has nothing to converge with).
func TestServer_SpaceSync(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SyncNow"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/sync", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sync: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("sync: expected empty body, got %s", rec.Body.String())
	}
}
