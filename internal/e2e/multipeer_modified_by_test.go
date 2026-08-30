// Cross-peer `modifiedBy`: an objects row names the account that
// signed the object's latest synced change, whatever dataset it landed
// on, while `author` stays the account that created it. One peer
// cannot tell the two apart — both stamps are its own identity — so
// the contract only shows up once a second writer is on the space.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// objectRow is the decode target for the derived identity/time stamps
// on one objects-collection row. Comparable, so two peers' rows can be
// checked for convergence with ==.
type objectRow struct {
	Id         string  `json:"id"`
	Author     string  `json:"author"`
	ModifiedBy string  `json:"modifiedBy"`
	ModifiedAt extDate `json:"modifiedAt"`
}

// objectRowOn reads one objects row off a peer. ok=false while the
// peer has not ingested the object yet — the query answers non-200 or
// comes back empty until the shared collection lands, which is "not
// yet", not a failure.
func objectRowOn(t *testing.T, p *peer, spaceId, objectId string) (objectRow, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodPost,
		p.base+"/v1/spaces/"+spaceId+"/objects/query",
		fmt.Sprintf(`{"filter":{"id":%q}}`, objectId))
	if resp.StatusCode != http.StatusOK {
		return objectRow{}, false
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &qr); err != nil || len(qr.Records) == 0 {
		return objectRow{}, false
	}
	var row objectRow
	if err := json.Unmarshal(qr.Records[0], &row); err != nil {
		t.Fatalf("%s: decode objects row: %v (%s)", p.name, err, qr.Records[0])
	}
	return row, row.Id == objectId
}

// peerAccountId reads a peer's own identity off /v1/account.
func peerAccountId(t *testing.T, p *peer) string {
	t.Helper()
	var acct struct {
		Id string `json:"id"`
	}
	mustJSON(t, http.MethodGet, p.base+"/v1/account", "", http.StatusOK, &acct)
	if acct.Id == "" {
		t.Fatalf("%s: GET /v1/account returned no id", p.name)
	}
	return acct.Id
}

// TestE2E_MultipeerModifiedBy pins the whole `modifiedBy` contract
// across replicas: the owner creates an object, the joiner writes to it
// through a synced dataset (chat), and both peers' objects rows
// converge to modifiedBy = joiner / author = owner with modifiedAt at
// the joiner's write; a later owner write flips modifiedBy back on both
// sides. `any` passes the rows through raw, so this is the SDK stamp
// read through the HTTP query surface.
func TestE2E_MultipeerModifiedBy(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"modified-by"}`, http.StatusCreated, &sp)

	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	if obj.ObjectId == "" {
		t.Fatal("owner: no objectId in create response")
	}

	ownerChat := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerChat := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// Both identities are taken from a stamp the peer itself produced
	// (a chat message's `creator`), so the comparisons below cannot be
	// thrown off by an encoding difference. /v1/account reports that
	// same StrKey account id — pinned here because clients read the two
	// together to answer "did I write this last?".
	m1 := sendChat(t, ownerChat, `{"text":"from owner"}`)
	ownerId := m1.Creator
	if ownerId == "" {
		t.Fatalf("owner's message not stamped: %+v", m1)
	}
	if got := peerAccountId(t, owner); got != ownerId {
		t.Errorf("owner /v1/account id = %q, want the stamped identity %q", got, ownerId)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// converge waits until both peers report the identical row and
	// `want` holds, then returns it. Plain pollUntil, not
	// pollUntilSynced: forcing SyncHeads stalls the shared `objects`
	// collection (see the caveat on pollUntilSynced).
	converge := func(what string, budget time.Duration, want func(objectRow) bool) objectRow {
		t.Helper()
		var got objectRow
		if !pollUntil(budget, func() bool {
			a, okA := objectRowOn(t, owner, sp.Id, obj.ObjectId)
			b, okB := objectRowOn(t, joiner, sp.Id, obj.ObjectId)
			if !okA || !okB || a != b || !want(a) {
				return false
			}
			got = a
			return true
		}) {
			a, _ := objectRowOn(t, owner, sp.Id, obj.ObjectId)
			b, _ := objectRowOn(t, joiner, sp.Id, obj.ObjectId)
			t.Fatalf("%s: rows never converged; owner=%+v joiner=%+v", what, a, b)
		}
		return got
	}

	// Nobody but the owner has written yet, so the joiner's first read
	// of the object carries the owner in both stamps.
	initial := converge("initial", 4*time.Minute, func(r objectRow) bool {
		return r.ModifiedBy == ownerId
	})
	if initial.Author != ownerId {
		t.Fatalf("author = %q, want the creating account %q", initial.Author, ownerId)
	}

	// The joiner needs the chat tree locally before it can post into it.
	// Forced sync is safe (and fast) for a per-object tree.
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return findMessage(chatMessages(t, joinerChat), "from owner").Id == m1.Id
	}) {
		t.Fatal("joiner never saw the owner's message")
	}

	// The write under test: a second account writes a dataset of the
	// owner's object. modifiedBy must name that account on BOTH peers,
	// carry the write's time, and leave `author` alone.
	m2 := sendChat(t, joinerChat, `{"text":"from joiner"}`)
	joinerId := m2.Creator
	if joinerId == "" || joinerId == ownerId {
		t.Fatalf("joiner's message not stamped by a second account: %+v", m2)
	}
	if got := peerAccountId(t, joiner); got != joinerId {
		t.Errorf("joiner /v1/account id = %q, want the stamped identity %q", got, joinerId)
	}

	afterJoiner := converge("after the joiner's write", 3*time.Minute, func(r objectRow) bool {
		return r.ModifiedBy == joinerId
	})
	if afterJoiner.Author != ownerId {
		t.Errorf("author = %q after the joiner's write, want the creator %q", afterJoiner.Author, ownerId)
	}
	if at := afterJoiner.ModifiedAt.seconds(); at < m2.CreatedAt {
		t.Errorf("modifiedAt = %d, want the joiner write's time %d or later", at, m2.CreatedAt)
	}
	if at, prev := afterJoiner.ModifiedAt.seconds(), initial.ModifiedAt.seconds(); at <= prev {
		t.Errorf("modifiedAt = %d, want past the create stamp %d", at, prev)
	}

	// And back: the pair follows the latest change whoever signs it.
	// The stamps have second resolution — keep the owner's write in a
	// later second than the joiner's.
	time.Sleep(1100 * time.Millisecond)
	sendChat(t, ownerChat, `{"text":"from owner again"}`)
	afterOwner := converge("after the owner's second write", 3*time.Minute, func(r objectRow) bool {
		return r.ModifiedBy == ownerId
	})
	if afterOwner.Author != ownerId {
		t.Errorf("author = %q, want the creator %q", afterOwner.Author, ownerId)
	}
	if at, prev := afterOwner.ModifiedAt.seconds(), afterJoiner.ModifiedAt.seconds(); at <= prev {
		t.Errorf("modifiedAt = %d, want past the joiner write's %d", at, prev)
	}
}
