// Multipeer coverage for 1-1 profile-name resolution in BOTH
// directions. Identity profiles are encrypted with a per-account
// metadata symkey; for a 1-1 the coordinator inbox carries that key one
// way only (initiator → acceptor), and the immutable ACL distributes
// none. Each participant publishes its own symkey inside the 1-1 (the
// `identityKeys` rows on the space's index object), so once the space is
// active on both sides each directory learns the other's key and
// resolves the peer's profile name.
//
// The Alice→Bob direction has no path other than those rows. Bob→Alice
// could also ride the inbox, so this test deliberately does NOT use the
// inbox (Bob learns of the request out-of-band via register-incoming)
// and asserts both directions.
//
// Mirrors TestE2E_MultipeerOneToOne: gated on the staging fixture +
// -short, a couple of minutes wall clock.
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_MultipeerOneToOneNames(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer one-to-one name test takes ~2-3 min; rerun without -short")
	}

	const (
		aliceName = "Alice Initiator"
		bobName   = "Bob Acceptor"
		// What Bob's app knows about Alice out-of-band, before any
		// profile is decrypted. Deliberately different from aliceName so
		// the space-row assertion below proves the RESOLVED profile won,
		// not the hint.
		displayHint = "alice (out-of-band hint)"
	)

	bin := buildBinary(t)
	alice := startPeer(t, bin, "alice")
	defer alice.stop(t)
	bob := startPeer(t, bin, "bob")
	defer bob.stop(t)

	// Publish both profiles BEFORE the 1-1 exists: the profile bytes go
	// to identityRepo, so they're fetchable the moment a peer holds the
	// matching symkey. Publishing first removes "the profile wasn't
	// pushed yet" as an explanation for a name that never resolves.
	mustStatus(t, http.MethodPut, alice.base+"/v1/account/metadata",
		`{"name":"`+aliceName+`"}`, http.StatusNoContent)
	mustStatus(t, http.MethodPut, bob.base+"/v1/account/metadata",
		`{"name":"`+bobName+`"}`, http.StatusNoContent)

	var accA, accB api.AccountResponse
	mustJSON(t, http.MethodGet, alice.base+"/v1/account", "", http.StatusOK, &accA)
	mustJSON(t, http.MethodGet, bob.base+"/v1/account", "", http.StatusOK, &accB)
	if accA.Id == "" || accB.Id == "" {
		t.Fatalf("empty account ids: A=%q B=%q", accA.Id, accB.Id)
	}

	// Before the 1-1 the two accounts have never met: neither directory
	// knows the other. Pins the starting state so a name found later
	// can't be something the harness handed over.
	if _, ok := identityByID(t, alice, accB.Id); ok {
		t.Fatalf("alice already knows %s before the 1-1", accB.Id)
	}
	if _, ok := identityByID(t, bob, accA.Id); ok {
		t.Fatalf("bob already knows %s before the 1-1", accA.Id)
	}

	// Alice initiates → active immediately (implicit self-approval).
	var aliceSpace api.SpaceInfo
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/one-to-one",
		`{"otherIdentity":"`+accB.Id+`"}`, http.StatusCreated, &aliceSpace)
	if aliceSpace.Id == "" {
		t.Fatalf("alice: empty 1-1 space id")
	}

	// Bob learns of the request through his own channel, not the
	// coordinator inbox: register-incoming seeds a device-local pending
	// row carrying only the display hint.
	mustStatus(t, http.MethodPost, bob.base+"/v1/spaces/one-to-one/register-incoming",
		`{"peerIdentity":"`+accA.Id+`","displayHint":{"name":"`+displayHint+`"}}`,
		http.StatusNoContent)

	var bobSpace api.SpaceInfo
	mustJSON(t, http.MethodPost, bob.base+"/v1/spaces/"+aliceSpace.Id+"/one-to-one/accept",
		"", http.StatusOK, &bobSpace)
	if bobSpace.Id != aliceSpace.Id {
		t.Fatalf("bob accepted id = %q, want %q (symmetric derivation)", bobSpace.Id, aliceSpace.Id)
	}
	if bobSpace.Status != api.SpaceStatusActive {
		t.Fatalf("bob: status after accept = %q, want active", bobSpace.Status)
	}

	// Both sides are active, so both have published their symkey row on
	// the space's index object. Poll until each directory carries the
	// other's NAME — that needs the key row to cross AND the identityRepo
	// profile fetch to land. Head-sync is forced on both peers each tick
	// so the key rows don't wait on any-sync's ~30s periodic poll.
	start := time.Now()
	var (
		aliceSeesBob, bobSeesAlice     api.IdentityInfo
		aliceSeesBobAt, bobSeesAliceAt time.Duration
	)
	resolved := pollUntilSynced(t, 120*time.Second, aliceSpace.Id, []*peer{alice, bob}, func() bool {
		if aliceSeesBobAt == 0 {
			if e, ok := identityByID(t, alice, accB.Id); ok && e.Name != "" {
				aliceSeesBob, aliceSeesBobAt = e, time.Since(start)
				t.Logf("alice resolved bob's name %q after %s", e.Name, aliceSeesBobAt.Round(time.Second))
			}
		}
		if bobSeesAliceAt == 0 {
			if e, ok := identityByID(t, bob, accA.Id); ok && e.Name != "" {
				bobSeesAlice, bobSeesAliceAt = e, time.Since(start)
				t.Logf("bob resolved alice's name %q after %s", e.Name, bobSeesAliceAt.Round(time.Second))
			}
		}
		return aliceSeesBobAt != 0 && bobSeesAliceAt != 0
	})
	if !resolved {
		t.Fatalf("names never resolved in both directions within 120s: alice→bob=%v bob→alice=%v",
			aliceSeesBobAt != 0, bobSeesAliceAt != 0)
	}

	if aliceSeesBob.Name != bobName {
		t.Errorf("alice's directory name for bob = %q, want %q", aliceSeesBob.Name, bobName)
	}
	if bobSeesAlice.Name != aliceName {
		t.Errorf("bob's directory name for alice = %q, want %q", bobSeesAlice.Name, aliceName)
	}
	if !containsStr(aliceSeesBob.SpaceIds, aliceSpace.Id) {
		t.Errorf("alice's bob entry spaceIds = %v, want to include the 1-1 %q",
			aliceSeesBob.SpaceIds, aliceSpace.Id)
	}
	if !containsStr(bobSeesAlice.SpaceIds, aliceSpace.Id) {
		t.Errorf("bob's alice entry spaceIds = %v, want to include the 1-1 %q",
			bobSeesAlice.SpaceIds, aliceSpace.Id)
	}

	// The 1-1 row has no space-set name: the SDK renders the friend's
	// resolved profile onto it (on Bob's side that means overriding the
	// out-of-band display hint). `author` is the OTHER participant — a
	// 1-1's ACL owner is a synthetic key nobody holds.
	checkRow(t, alice, aliceSpace.Id, bobName, accB.Id)
	checkRow(t, bob, aliceSpace.Id, aliceName, accA.Id)
}

// checkRow asserts one peer's 1-1 row reports the friend as author and,
// best-effort, carries the friend's resolved name. The name is rendered
// from the identities directory at read time but the underlying write is
// asynchronous, so it gets its own short poll and is LOGGED rather than
// failed — the directory assertions above are the hard contract.
func checkRow(t *testing.T, p *peer, spaceID, wantName, wantAuthor string) {
	t.Helper()
	var row api.SpaceInfo
	named := pollUntil(30*time.Second, func() bool {
		mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+spaceID, "", http.StatusOK, &row)
		return row.Name == wantName
	})
	if !named {
		t.Logf("%s: 1-1 row name = %q, want %q (not populated within 30s of the directory resolving)",
			p.name, row.Name, wantName)
	}
	if row.Author != wantAuthor {
		t.Errorf("%s: 1-1 row author = %q, want the friend %q", p.name, row.Author, wantAuthor)
	}
}

// identityByID reads one entry off a peer's identities directory through
// GET /v1/identities/:identity. ok=false on 404 identity.not_found — the
// "encountered but unknown" answer the directory gives before a peer's
// key has arrived.
func identityByID(t *testing.T, p *peer, identity string) (api.IdentityInfo, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/identities/"+identity, "")
	switch resp.StatusCode {
	case http.StatusOK:
		var info api.IdentityInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("%s: decode identity %s: %v body=%s", p.name, identity, err, raw)
		}
		return info, true
	case http.StatusNotFound:
		return api.IdentityInfo{}, false
	default:
		t.Fatalf("%s: GET /v1/identities/%s: status=%d body=%s", p.name, identity, resp.StatusCode, raw)
		return api.IdentityInfo{}, false
	}
}
