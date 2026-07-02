package e2e

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerIdentitiesDirectory verifies the account-global
// identities directory (SDK.Identities(), GET /v1/identities) populates
// across peers once they share a space. The member watcher seeds a
// directory row from each member's ACL metadata symkey — so cross-peer
// PRESENCE is deterministic after membership converges, independent of
// identityRepo. The profile NAME is resolved from identityRepo in the
// background, so it's checked best-effort (logged, not asserted).
//
// Hard assertions:
//   - each peer's directory contains the OTHER peer's identity,
//   - that row's spaceIds includes the shared space,
//   - GET /v1/identities/:identity round-trips and never leaks symKey,
//   - GET on an unknown identity is 404 identity.not_found.
func TestE2E_MultipeerIdentitiesDirectory(t *testing.T) {
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

	// Publish profiles BEFORE the space/ACL records are written so each
	// party's ACL metadata carries its name — gives the background
	// identityRepo resolution something to surface for the name check.
	mustStatus(t, http.MethodPut, owner.base+"/v1/account/metadata",
		`{"name":"Owner Alice"}`, http.StatusNoContent)
	mustStatus(t, http.MethodPut, joiner.base+"/v1/account/metadata",
		`{"name":"Joiner Bob"}`, http.StatusNoContent)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"directory-shared"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Pull the canonical account identities (the row keys the directory
	// uses) off the owner's members list — both members are present once
	// the join is accepted.
	var ml api.MembersListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
		"", http.StatusOK, &ml)
	if len(ml.Members) != 2 {
		t.Fatalf("members = %d, want 2: %+v", len(ml.Members), ml.Members)
	}
	var ownerID, joinerID string
	for _, m := range ml.Members {
		if m.Permission == api.SpacePermissionOwner {
			ownerID = m.Identity
		} else {
			joinerID = m.Identity
		}
	}
	if ownerID == "" || joinerID == "" {
		t.Fatalf("could not resolve both identities: owner=%q joiner=%q", ownerID, joinerID)
	}

	// Touching /members started each watcher (the owner's above; the
	// joiner's below), which is what seeds the directory rows.
	mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id+"/members", "", http.StatusOK, &ml)

	// Each peer's directory should contain the other, with the shared
	// space in spaceIds. Plain pollUntil — the directory is account-local
	// (written from the already-synced ACL), so no forced head-sync.
	var ownerSeesJoiner, joinerSeesOwner api.IdentityInfo
	if !pollUntil(120*time.Second, func() bool {
		e1, ok1 := directoryEntry(t, owner, joinerID)
		e2, ok2 := directoryEntry(t, joiner, ownerID)
		if ok1 && ok2 {
			ownerSeesJoiner, joinerSeesOwner = e1, e2
			return true
		}
		return false
	}) {
		t.Fatalf("directories never converged: owner→joiner and joiner→owner not both present")
	}

	if !containsStr(ownerSeesJoiner.SpaceIds, sp.Id) {
		t.Errorf("owner's joiner entry spaceIds = %v, want to include %q", ownerSeesJoiner.SpaceIds, sp.Id)
	}
	if !containsStr(joinerSeesOwner.SpaceIds, sp.Id) {
		t.Errorf("joiner's owner entry spaceIds = %v, want to include %q", joinerSeesOwner.SpaceIds, sp.Id)
	}

	// GET one entry by id: round-trips and must never leak the synced
	// symKey (the public shape has no such field; assert on the raw body
	// too as a belt-and-suspenders check against future regressions).
	resp, raw := doRequest(t, http.MethodGet, owner.base+"/v1/identities/"+joinerID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/identities/:id status=%d body=%s", resp.StatusCode, raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "symkey") {
		t.Errorf("directory entry leaked symKey: %s", raw)
	}

	// Unknown identity → 404 identity.not_found.
	var env api.ErrorEnvelope
	mustJSON(t, http.MethodGet, owner.base+"/v1/identities/A00neverEncountered",
		"", http.StatusNotFound, &env)
	if env.Error.Code != "identity.not_found" {
		t.Errorf("unknown-identity code = %q, want identity.not_found", env.Error.Code)
	}

	// Best-effort name resolution (identityRepo, background). Logged, not
	// asserted — failure here would be an SDK/network timing issue, not a
	// wrapper bug, and the presence assertions above already cover the
	// wrapping contract.
	if pollUntil(60*time.Second, func() bool {
		e, ok := directoryEntry(t, owner, joinerID)
		return ok && e.Name != ""
	}) {
		e, _ := directoryEntry(t, owner, joinerID)
		if e.Name != "Joiner Bob" {
			t.Logf("owner resolved joiner name = %q, expected %q", e.Name, "Joiner Bob")
		} else {
			t.Logf("owner resolved joiner profile name: %q", e.Name)
		}
	} else {
		t.Logf("joiner profile name not resolved within 60s (identityRepo timing); presence verified")
	}
}

// directoryEntry fetches one peer's identities directory and returns the
// entry matching identity, ok=false when absent.
func directoryEntry(t *testing.T, p *peer, identity string) (api.IdentityInfo, bool) {
	t.Helper()
	var list api.IdentitiesListResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/identities", "", http.StatusOK, &list)
	for _, e := range list.Identities {
		if e.Identity == identity {
			return e, true
		}
	}
	return api.IdentityInfo{}, false
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
