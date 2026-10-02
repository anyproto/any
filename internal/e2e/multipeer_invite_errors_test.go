package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerInviteRefusals covers the two invite refusals that
// carry a typed 4xx: a join with an invite the owner revoked is
// 410 invite.revoked and posts no request, and a mint by a non-owner
// member is 403 acl.forbidden (the coordinator's make-shareable is
// owner-only).
func TestE2E_MultipeerInviteRefusals(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes ~60s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"invite-refusals"}`, http.StatusCreated, &sp)

	var minted api.InviteCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/invites",
		"", http.StatusCreated, &minted)
	var invs api.InvitesListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/invites",
		"", http.StatusOK, &invs)
	if len(invs.Invites) != 1 {
		t.Fatalf("invites = %+v, want one", invs.Invites)
	}
	mustStatus(t, http.MethodDelete,
		owner.base+"/v1/spaces/"+sp.Id+"/invites/"+invs.Invites[0].RecordId,
		"", http.StatusNoContent)

	body, _ := json.Marshal(api.SpaceJoinRequest{InviteToken: minted.InviteToken})
	var env api.ErrorEnvelope
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/join",
		string(body), http.StatusGone, &env)
	if env.Error.Code != "invite.revoked" {
		t.Fatalf("join with a revoked invite: code = %q, want invite.revoked (%+v)", env.Error.Code, env.Error)
	}
	if env.Error.Details["spaceId"] != sp.Id {
		t.Errorf("details.spaceId = %v, want %s", env.Error.Details["spaceId"], sp.Id)
	}

	// The refused join posted nothing: the owner sees no request.
	if pollUntil(15*time.Second, func() bool {
		var resp api.JoinRequestsResponse
		mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members/requests",
			"", http.StatusOK, &resp)
		return len(resp.Requests) > 0
	}) {
		t.Fatalf("owner saw a join request from a revoked invite")
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	if code := mustErrorCode(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/invites",
		"", http.StatusForbidden); code != "acl.forbidden" {
		t.Fatalf("non-owner invite create: code = %q, want acl.forbidden", code)
	}
}
