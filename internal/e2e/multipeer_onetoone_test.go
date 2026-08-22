// Multipeer 1-1 (direct) space coverage. The single-process tests in
// internal/server/handlers_onetoone_test.go only prove the 400 validation
// branches; what needs two replicas is the symmetric derivation (both
// peers land on the same spaceId from each other's identity), the
// approve flow (register-incoming → pending → accept), and that the
// derived space actually syncs content across the two writers.
//
// Mirrors TestE2E_MultipeerChat: gated on the staging fixture + -short,
// ~2-3 min wall clock (any-sync headsync poll is ~30s).
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_MultipeerOneToOne(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer one-to-one test takes ~2-3 min; rerun without -short")
	}

	bin := buildBinary(t)
	alice := startPeer(t, bin, "alice")
	defer alice.stop(t)
	bob := startPeer(t, bin, "bob")
	defer bob.stop(t)

	var accA, accB api.AccountResponse
	mustJSON(t, http.MethodGet, alice.base+"/v1/account", "", http.StatusOK, &accA)
	mustJSON(t, http.MethodGet, bob.base+"/v1/account", "", http.StatusOK, &accB)
	if accA.Id == "" || accB.Id == "" {
		t.Fatalf("empty account ids: A=%q B=%q", accA.Id, accB.Id)
	}

	// Alice initiates → active immediately (implicit self-approval).
	var aliceSpace api.SpaceInfo
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/one-to-one",
		`{"otherIdentity":"`+accB.Id+`"}`, http.StatusCreated, &aliceSpace)
	if aliceSpace.Id == "" {
		t.Fatalf("alice: empty 1-1 space id")
	}
	if aliceSpace.SpaceType != space.SpaceTypeOneToOne {
		t.Errorf("alice: spaceType = %q, want %q", aliceSpace.SpaceType, space.SpaceTypeOneToOne)
	}
	if aliceSpace.Status != api.SpaceStatusActive {
		t.Errorf("alice: status = %q, want active", aliceSpace.Status)
	}

	// Bob registers the incoming request out-of-band → a device-local
	// pending row keyed by the symmetrically-derived id. Proves
	// derivation is order-independent: Bob derives the SAME id from
	// Alice's identity that Alice derived from Bob's.
	mustStatus(t, http.MethodPost, bob.base+"/v1/spaces/one-to-one/register-incoming",
		`{"peerIdentity":"`+accA.Id+`","displayHint":{"name":"Alice"}}`, http.StatusNoContent)

	var pending api.SpaceListResponse
	mustJSON(t, http.MethodGet, bob.base+"/v1/spaces?status=one_to_one_pending",
		"", http.StatusOK, &pending)
	if !hasSpace(pending.Spaces, aliceSpace.Id, api.SpaceStatusOneToOnePending) {
		t.Fatalf("bob: pending list missing %s as one_to_one_pending: %+v", aliceSpace.Id, pending.Spaces)
	}

	// Bob accepts → materialized + active, same id.
	var bobSpace api.SpaceInfo
	mustJSON(t, http.MethodPost, bob.base+"/v1/spaces/"+aliceSpace.Id+"/one-to-one/accept",
		"", http.StatusOK, &bobSpace)
	if bobSpace.Id != aliceSpace.Id {
		t.Fatalf("bob accepted id = %q, want %q (symmetric derivation)", bobSpace.Id, aliceSpace.Id)
	}
	if bobSpace.Status != api.SpaceStatusActive {
		t.Errorf("bob: status after accept = %q, want active", bobSpace.Status)
	}

	// The 1-1's chat, on a DERIVED root. Both participants are
	// writers and the ACL owner is a synthetic key nobody holds, so
	// neither side can ever claim "nobody else could have installed" —
	// a created root leaves both refused until the registry converges,
	// which never happens while they are apart. A derived root is the
	// same id on both sides by construction, so each installs its own
	// copy immediately and they meet on the one object.
	const bundleId = "general-chat/v1"
	const ensureBody = `{"id":"` + bundleId + `","name":"General","rootTypes":["chat"],"derived":true}`

	// FIRST attempt, both sides, no convergence polling: that is the
	// contract — a derived install never waits and never 409s.
	var aliceBundle api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/"+aliceSpace.Id+"/bundles",
		ensureBody, http.StatusOK, &aliceBundle)
	if aliceBundle.Bundle.RootId == "" || !aliceBundle.Bundle.Derived {
		t.Fatalf("initiator did not install a derived chat: %+v", aliceBundle)
	}

	var bobBundle api.BundleEnsureResponse
	mustJSON(t, http.MethodPost, bob.base+"/v1/spaces/"+aliceSpace.Id+"/bundles",
		ensureBody, http.StatusOK, &bobBundle)
	if bobBundle.Bundle.RootId != aliceBundle.Bundle.RootId {
		t.Fatalf("1-1 chat forked: alice=%q bob=%q",
			aliceBundle.Bundle.RootId, bobBundle.Bundle.RootId)
	}
	if !bobBundle.Bundle.Derived {
		t.Fatalf("peer did not resolve the chat as derived: %+v", bobBundle.Bundle)
	}
	if len(aliceBundle.Bundle.Losers) != 0 || len(bobBundle.Bundle.Losers) != 0 {
		t.Fatalf("derived install produced losers: alice=%v bob=%v",
			aliceBundle.Bundle.Losers, bobBundle.Bundle.Losers)
	}

	// Content convergence ON THE SHARED ROOT: each side materialized
	// its own copy of the same object, so the two histories merge.
	chatObj := "/v1/spaces/" + aliceSpace.Id + "/objects/" + aliceBundle.Bundle.RootId
	aliceObj := alice.base + chatObj
	bobObj := bob.base + chatObj

	m1 := sendChat(t, aliceObj, `{"text":"hi from alice"}`)
	m2 := sendChat(t, bobObj, `{"text":"hi from bob"}`)

	var got, back chatMsg
	if !pollUntilSynced(t, 3*time.Minute, aliceSpace.Id, []*peer{alice, bob}, func() bool {
		got = findMessage(chatMessages(t, bobObj), "hi from alice")
		back = findMessage(chatMessages(t, aliceObj), "hi from bob")
		return got.Id == m1.Id && back.Id == m2.Id
	}) {
		t.Fatalf("derived chat never merged: bob saw %q, alice saw %q", got.Id, back.Id)
	}
}

func hasSpace(spaces []api.SpaceInfo, id, status string) bool {
	for _, s := range spaces {
		if s.Id == id && s.Status == status {
			return true
		}
	}
	return false
}
