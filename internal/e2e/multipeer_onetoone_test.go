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

	// The 1-1's chat: the server arbitrates nothing, so the two
	// clients agree that the INITIATING side registers the bundle and
	// the other adopts it. Bob's Ensure must adopt Alice's root — a
	// second root would split the conversation.
	const bundleId = "general-chat/v1"
	const ensureBody = `{"id":"` + bundleId + `","name":"General","rootTypes":["chat"]}`

	// Even the initiator waits for the registry to converge: a 1-1
	// space is derived on both sides, so its index has to meet the
	// peer's before an install can know whether one already exists.
	var aliceBundle api.BundleEnsureResponse
	if !pollUntilSynced(t, 3*time.Minute, aliceSpace.Id, []*peer{alice, bob}, func() bool {
		aliceBundle = api.BundleEnsureResponse{}
		code := tryJSON(t, http.MethodPost, alice.base+"/v1/spaces/"+aliceSpace.Id+"/bundles",
			ensureBody, &aliceBundle)
		return code == http.StatusOK && aliceBundle.Bundle.RootId != ""
	}) {
		t.Fatalf("initiator never installed the 1-1 chat: %+v", aliceBundle)
	}
	if !aliceBundle.Installed {
		t.Fatalf("initiator adopted instead of installing: %+v", aliceBundle)
	}

	var bobBundle api.BundleEnsureResponse
	if !pollUntilSynced(t, 3*time.Minute, aliceSpace.Id, []*peer{alice, bob}, func() bool {
		bobBundle = api.BundleEnsureResponse{}
		code := tryJSON(t, http.MethodPost, bob.base+"/v1/spaces/"+aliceSpace.Id+"/bundles",
			ensureBody, &bobBundle)
		return code == http.StatusOK && bobBundle.Bundle.RootId != ""
	}) {
		t.Fatalf("bob never resolved the 1-1 chat bundle: %+v", bobBundle)
	}
	if bobBundle.Installed || bobBundle.Bundle.RootId != aliceBundle.Bundle.RootId {
		t.Fatalf("1-1 chat diverged: alice=%q bob=%q (installed=%v)",
			aliceBundle.Bundle.RootId, bobBundle.Bundle.RootId, bobBundle.Installed)
	}

	// Content convergence: Alice writes a chat message in the 1-1 space,
	// Bob (the other writer) reads it back after sync.
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/"+aliceSpace.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	aliceObj := alice.base + "/v1/spaces/" + aliceSpace.Id + "/objects/" + obj.ObjectId
	bobObj := bob.base + "/v1/spaces/" + aliceSpace.Id + "/objects/" + obj.ObjectId

	m1 := sendChat(t, aliceObj, `{"text":"hi from alice"}`)

	var got chatMsg
	if !pollUntilSynced(t, 3*time.Minute, aliceSpace.Id, []*peer{alice, bob}, func() bool {
		got = findMessage(chatMessages(t, bobObj), "hi from alice")
		return got.Id == m1.Id
	}) {
		t.Fatalf("bob never saw alice's message (id=%q)", got.Id)
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
