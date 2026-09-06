package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_ACLCancelJoin_RowGate pins the account-level route: a
// cancel needs no materialized space, so its refusals come from the
// tech-space row, never from resolveSpace — an unknown id is 404
// space.not_found (not the 409 space.not_accepted a Get-based path
// answers on any non-active row), and a row that is not joining (the
// caller's own active space) is 409 space.join_not_pending. Both are
// decided before any network call. The pending-join happy path needs a
// second account and lives in the multipeer e2e.
func TestServer_ACLCancelJoin_RowGate(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"cancel-join"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/acl/cancel-join", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel on an owned active row: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "space.join_not_pending" {
		t.Errorf("code = %q, want space.join_not_pending", got)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/bafyunknown.space/acl/cancel-join", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel on an unknown id: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "space.not_found" {
		t.Errorf("code = %q, want space.not_found", got)
	}
}
