package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerCancelJoin covers the joiner-withdraws path and the
// re-request after it:
//
//   - owner mints → joiner joins → joiner POST …/acl/cancel-join → 204
//     (the pending join is the only state the route applies to, and the
//     one resolveSpace refuses — the route must not go through it);
//   - the joiner's row reads `deleted` on return, a second cancel is 409
//     space.join_not_pending, and the owner's /members/requests drains;
//   - the joiner re-posts the SAME token → 202 joining; the owner sees a
//     fresh request, accepts it, and the joiner reaches active.
func TestE2E_MultipeerCancelJoin(t *testing.T) {
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
		`{"name":"cancel-join"}`, http.StatusCreated, &sp)

	// Nothing to withdraw yet: the joiner has no row (404, never the
	// 409 space.not_accepted a resolveSpace path would answer), the
	// owner's own row is active.
	resp, raw := doRequest(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/acl/cancel-join", "")
	if resp.StatusCode != http.StatusNotFound || errorCode(t, raw) != "space.not_found" {
		t.Fatalf("cancel with no row: %d %s, want 404 space.not_found", resp.StatusCode, raw)
	}
	resp, raw = doRequest(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/acl/cancel-join", "")
	if resp.StatusCode != http.StatusConflict || errorCode(t, raw) != "space.join_not_pending" {
		t.Fatalf("cancel on an owned row: %d %s, want 409 space.join_not_pending", resp.StatusCode, raw)
	}

	// One invite for both rounds — a space holds one active invite, and
	// the withdrawn request must not consume it.
	var minted api.InviteCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/invites",
		"", http.StatusCreated, &minted)
	first := requestJoin(t, owner, joiner, sp.Id, minted.InviteToken)

	// Withdraw. The end state is observable on return.
	mustStatus(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/acl/cancel-join",
		"", http.StatusNoContent)
	var row api.SpaceInfo
	mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id, "", http.StatusOK, &row)
	if row.Status != api.SpaceStatusDeleted {
		t.Errorf("joiner row after cancel: status=%q, want %q", row.Status, api.SpaceStatusDeleted)
	}
	resp, raw = doRequest(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/acl/cancel-join", "")
	if resp.StatusCode != http.StatusConflict || errorCode(t, raw) != "space.join_not_pending" {
		t.Fatalf("second cancel: %d %s, want 409 space.join_not_pending", resp.StatusCode, raw)
	}

	// The owner's pending list drains once the cancel record pulls.
	if !pollUntil(60*time.Second, func() bool {
		var reqs api.JoinRequestsResponse
		mustJSON(t, http.MethodGet,
			owner.base+"/v1/spaces/"+sp.Id+"/members/requests", "",
			http.StatusOK, &reqs)
		return len(reqs.Requests) == 0
	}) {
		t.Fatalf("owner request list did not drain after the joiner's cancel")
	}

	// Re-request with the same token: the ended row revives to joining
	// and the owner sees a fresh ACL request (never the withdrawn id).
	second := requestJoin(t, owner, joiner, sp.Id, minted.InviteToken)
	if second.RecordId == first.RecordId {
		t.Fatalf("re-request reused the withdrawn record id %s", first.RecordId)
	}

	accept, _ := json.Marshal(api.ACLAcceptRequest{
		RequestRecordId: second.RecordId,
		Permission:      api.SpacePermissionWriter,
	})
	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/acl/accept",
		string(accept), http.StatusNoContent)

	if !pollUntil(90*time.Second, func() bool {
		resp, raw := doRequest(t, http.MethodGet,
			joiner.base+"/v1/spaces/"+sp.Id+"/members/me", "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var me api.Member
		if err := json.Unmarshal(raw, &me); err != nil {
			t.Fatalf("decode members/me: %v (body %s)", err, raw)
		}
		return me.Status == api.MemberStatusActive
	}) {
		t.Fatalf("joiner never reached active after the re-requested join was accepted")
	}
}

// requestJoin posts token from the joiner (202, row `joining`) and
// blocks until the owner sees the pending request. Plain pollUntil —
// see the joinSpace note on why this path avoids forced sync.
func requestJoin(t *testing.T, owner, joiner *peer, spaceID, token string) api.JoinRequest {
	t.Helper()
	body, _ := json.Marshal(api.SpaceJoinRequest{InviteToken: token})
	var joined api.SpaceInfo
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/join",
		string(body), http.StatusAccepted, &joined)
	if joined.Status != api.SpaceStatusJoining {
		t.Errorf("joiner space status = %q, want %q", joined.Status, api.SpaceStatusJoining)
	}
	var req api.JoinRequest
	if !pollUntil(90*time.Second, func() bool {
		var resp api.JoinRequestsResponse
		mustJSON(t, http.MethodGet,
			owner.base+"/v1/spaces/"+spaceID+"/members/requests", "",
			http.StatusOK, &resp)
		if len(resp.Requests) > 0 {
			req = resp.Requests[0]
			return true
		}
		return false
	}) {
		t.Fatalf("owner never saw the join request")
	}
	return req
}

// errorCode decodes the uniform error envelope's code from a non-2xx
// body.
func errorCode(t *testing.T, raw []byte) string {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, raw)
	}
	return env.Error.Code
}
