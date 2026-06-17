// Multi-peer e2e harness: two `any` binaries, distinct data dirs and
// account keys, peer through the staging network. Used for tests where
// a single process can't reproduce the path — invite/join/accept flows
// and CRDT convergence across replicas.
//
// These tests are slow (~60–120s each) because any-sync's headsync poll
// is ~30s; gate behind `-short`.
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

// peer wraps a running `any` server bound to a unique data dir/port.
type peer struct {
	name    string
	addr    string
	dataDir string
	base    string // http://<addr>
	srv     *runningServer
}

func startPeer(t *testing.T, bin, name string) *peer {
	t.Helper()
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	t.Logf("peer %s: addr=%s data=%s", name, addr, dataDir)
	srv := startServer(t, bin, addr, dataDir)
	waitForReady(t, addr, 60*time.Second)
	return &peer{
		name:    name,
		addr:    addr,
		dataDir: dataDir,
		base:    "http://" + addr,
		srv:     srv,
	}
}

func (p *peer) stop(t *testing.T) {
	t.Helper()
	if p.srv != nil {
		p.srv.stop(t)
	}
}

// pollUntil retries fn every 500ms until it returns true or the deadline
// elapses. Returns true on success, false on timeout. Used wherever an
// SDK round-trip needs to settle (headsync ~30s, ACL apply, CRDT merge).
func pollUntil(deadline time.Duration, fn func() bool) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if fn() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// forceSync triggers an immediate head-sync (diff) round on each peer
// for spaceID via POST /v1/spaces/:id/sync (→ Space.SyncHeads), instead
// of waiting on any-sync's ~30s periodic headsync timer. Each call
// blocks server-side until the round completes. Pass peers in
// writer→reader order: the writer pushes its heads to the responsible
// node first, then the reader pulls them back. Best-effort — a transient
// sync error just means the surrounding pollUntilSynced retries on the
// next tick, so non-2xx responses are ignored (the real predicate gates
// success).
func forceSync(t *testing.T, spaceID string, peers ...*peer) {
	t.Helper()
	for _, p := range peers {
		doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+spaceID+"/sync", "")
	}
}

// pollUntilSynced is pollUntil with a forced head-sync round (forceSync)
// on the given peers before each predicate check. This collapses the
// multi-peer convergence wait from "next periodic headsync (~30s)" to
// "as fast as the diff round + predicate settle" — typically a tick or
// two. Peers are synced in slice order; pass writer first, reader last.
//
// IMPORTANT: only use this for predicates that read PER-OBJECT trees
// (chat_messages, editor_blocks). Forcing SyncHeads while converging the
// shared `objects` collection (type/property/value rows) or the derived
// spaceIndex stalls those collections indefinitely on this SDK — use
// plain pollUntil there. See TestE2E_MultipeerCRDTConvergence /
// TestE2E_MultipeerSpaceMetadataSync.
func pollUntilSynced(t *testing.T, deadline time.Duration, spaceID string, peers []*peer, fn func() bool) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		forceSync(t, spaceID, peers...)
		if fn() {
			return true
		}
		if !time.Now().Before(end) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// joinSpace runs the full owner-mints → joiner-joins → owner-accepts
// handshake. Blocks until the joiner's /members/me reports active.
func joinSpace(t *testing.T, owner, joiner *peer, spaceID, permission string) {
	t.Helper()

	var minted api.InviteCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+spaceID+"/invites",
		"", http.StatusCreated, &minted)
	if minted.InviteToken == "" {
		t.Fatalf("invite token empty: %+v", minted)
	}

	var joined api.SpaceInfo
	body, err := json.Marshal(api.SpaceJoinRequest{InviteToken: minted.InviteToken})
	if err != nil {
		t.Fatalf("marshal join body: %v", err)
	}
	// RequestToJoin returns 202 with status="joining" — the actual
	// active flip happens after the owner accepts (polled below).
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/join",
		string(body), http.StatusAccepted, &joined)
	if joined.Id != spaceID {
		t.Fatalf("joiner space id = %q, want %q", joined.Id, spaceID)
	}
	if joined.Status != api.SpaceStatusJoining {
		t.Errorf("joiner space status = %q, want %q", joined.Status, api.SpaceStatusJoining)
	}

	// Owner waits for the join request to land. Plain pollUntil, NOT
	// forced sync: SyncHeads is space-wide and stalls the shared
	// `objects` / spaceIndex collections (see the caveats on
	// TestE2E_MultipeerCRDTConvergence / SpaceMetadataSync), and joinSpace
	// is shared by those tests. The join handshake converges in a couple
	// of seconds on its own anyway — forced sync bought nothing here.
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
		t.Fatalf("owner never saw join request from %s", joiner.addr)
	}

	accept, _ := json.Marshal(api.ACLAcceptRequest{
		RequestRecordId: req.RecordId,
		Permission:      permission,
	})
	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+spaceID+"/acl/accept",
		string(accept), http.StatusNoContent)

	// Joiner waits for the accept to propagate back. Plain pollUntil for
	// the same reason as above.
	if !pollUntil(90*time.Second, func() bool {
		var me api.Member
		mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+spaceID+"/members/me",
			"", http.StatusOK, &me)
		return me.Status == api.MemberStatusActive
	}) {
		t.Fatalf("joiner never reached active status")
	}
}

// awaitJoinRequest mints an invite on owner, has the joiner post it,
// and blocks until owner sees the pending request via headsync. Returns
// the JoinRequest the owner should act on. Used by the negative-path
// tests (decline, cancel) that don't want the full accept flow.
func awaitJoinRequest(t *testing.T, owner, joiner *peer, spaceID string) api.JoinRequest {
	t.Helper()

	var minted api.InviteCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+spaceID+"/invites",
		"", http.StatusCreated, &minted)
	body, _ := json.Marshal(api.SpaceJoinRequest{InviteToken: minted.InviteToken})
	mustStatus(t, http.MethodPost, joiner.base+"/v1/spaces/join",
		string(body), http.StatusAccepted)

	// Plain pollUntil — see the joinSpace note on why this path avoids
	// forced sync.
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
		t.Fatalf("owner never saw join request")
	}
	return req
}

// TestE2E_MultipeerInviteAccept covers the happy-path invite flow:
// owner mints → joiner joins → owner sees request → owner accepts →
// joiner active → owner /members lists both with the granted permission.
func TestE2E_MultipeerInviteAccept(t *testing.T) {
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
		`{"name":"shared"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	var ml api.MembersListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
		"", http.StatusOK, &ml)
	if len(ml.Members) != 2 {
		t.Fatalf("members = %d, want 2: %+v", len(ml.Members), ml.Members)
	}

	var joinerM api.Member
	for _, m := range ml.Members {
		if m.Permission != api.SpacePermissionOwner {
			joinerM = m
			break
		}
	}
	if joinerM.Permission != api.SpacePermissionWriter {
		t.Errorf("joiner permission = %q, want writer; member=%+v", joinerM.Permission, joinerM)
	}
	if joinerM.Status != api.MemberStatusActive {
		t.Errorf("joiner status = %q, want active", joinerM.Status)
	}
}

// TestE2E_MultipeerCRDTConvergence creates a type, property, object, and
// property value on the owner BEFORE the joiner joins — so when the
// joiner ingests the space, it must process every change in the right
// order (the SDK's DataVersion gate parks ahead-of-schema changes until
// the type definitions catch up). Asserts the joiner eventually reads
// the property value owner wrote.
func TestE2E_MultipeerCRDTConvergence(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	// Owner: full schema + value before joiner sees anything.
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"crdt-convergence"}`, http.StatusCreated, &sp)

	var typeResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/types",
		`{"name":"Movie","xKey":"movie"}`, http.StatusCreated, &typeResp)
	typeID, _ := typeResp["typeId"].(string)
	if typeID == "" {
		t.Fatalf("typeId empty: %+v", typeResp)
	}

	var propResp map[string]any
	mustJSON(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/types/"+typeID+"/properties",
		`{"name":"Title","kind":"string","xKey":"title"}`,
		http.StatusCreated, &propResp)
	propID, _ := propResp["propId"].(string)
	if propID == "" {
		t.Fatalf("propId empty: %+v", propResp)
	}

	var objResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeID), http.StatusCreated, &objResp)
	objectID, _ := objResp["objectId"].(string)
	if objectID == "" {
		t.Fatalf("objectId empty: %+v", objResp)
	}

	mustStatus(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/properties/"+objectID+"/set/"+typeID,
		fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, propID),
		http.StatusOK)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	propsURL := joiner.base + "/v1/spaces/" + sp.Id + "/properties/" + objectID
	objectsQueryURL := joiner.base + "/v1/spaces/" + sp.Id + "/objects/query"
	typesURL := joiner.base + "/v1/spaces/" + sp.Id + "/types"

	// The joiner must pull (a) the type definition, (b) the property
	// definition (under that type), (c) the object, and (d) the
	// property record. Polling all four lets us see *which* step is
	// stalled when we time out.
	// NOTE: plain pollUntil, not pollUntilSynced. Forcing SyncHeads while
	// polling the shared `objects` collection (property values) stalls
	// convergence indefinitely on this SDK — the value never lands on the
	// joiner even after hundreds of forced rounds, whereas reactive sync
	// converges in ~3s. SyncHeads is safe for per-object trees (chat /
	// editor) but not the shared/derived collections; see the same caveat
	// on TestE2E_MultipeerSpaceMetadataSync. (Worth raising on the SDK repo.)
	var lastSeen any
	var lastTypeNames []string
	var lastObjectsCount int
	if !pollUntil(4*time.Minute, func() bool {
		// Probe types — the joiner should eventually list "Movie".
		var typesResp map[string]any
		if resp, raw := doRequest(t, http.MethodGet, typesURL, ""); resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(raw, &typesResp); err == nil {
				if list, _ := typesResp["types"].([]any); list != nil {
					lastTypeNames = lastTypeNames[:0]
					for _, t := range list {
						if m, ok := t.(map[string]any); ok {
							if n, _ := m["name"].(string); n != "" {
								lastTypeNames = append(lastTypeNames, n)
							}
						}
					}
				}
			}
		}
		// Probe objects — the joiner should eventually see the one we created.
		// objects/query response key is `records`.
		var objectsResp map[string]any
		if resp, raw := doRequest(t, http.MethodPost, objectsQueryURL, "{}"); resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(raw, &objectsResp); err == nil {
				if list, _ := objectsResp["records"].([]any); list != nil {
					lastObjectsCount = len(list)
				}
			}
		}
		// And the actual converge check.
		resp, raw := doRequest(t, http.MethodGet, propsURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		record, _ := got["record"].(map[string]any)
		if record == nil {
			return false
		}
		typeNode, _ := record[typeID].(map[string]any)
		if typeNode == nil {
			return false
		}
		v := typeNode[propID]
		if v == "Casablanca" {
			return true
		}
		lastSeen = v
		return false
	}) {
		t.Fatalf("joiner never converged on property value; lastSeen=%v, types=%v, objects=%d",
			lastSeen, lastTypeNames, lastObjectsCount)
	}
}

// TestE2E_MultipeerSpaceMetadataSync verifies the per-space spaceIndex
// path: owner PATCHes name/description/icon after the joiner is active,
// joiner eventually reads the new values from GET /v1/spaces/:id. The
// CRDT-replicated spaceIndex object is written on owner; both peers'
// indexer hooks mirror the converged state back into their own
// tech-space rows, so the joiner's plain `GET /v1/spaces/:id` is what
// the assertion polls.
func TestE2E_MultipeerSpaceMetadataSync(t *testing.T) {
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
		`{"name":"orig-name","description":"orig-desc"}`,
		http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Sanity: pre-patch, joiner sees the original name. The initial
	// metadata is set during Create and rides the same spaceIndex
	// path, so it may take a beat to mirror on the joiner side after
	// the join completes.
	// NOTE: plain pollUntil, not pollUntilSynced. Like the property
	// convergence test, forcing SyncHeads stalls the spaceIndex-mirrored
	// metadata path — the joiner never observes the value. Reactive sync
	// handles it in ~3s. (Worth raising on the SDK repo.)
	if !pollUntil(60*time.Second, func() bool {
		var got api.SpaceInfo
		mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id,
			"", http.StatusOK, &got)
		return got.Name == "orig-name"
	}) {
		var got api.SpaceInfo
		mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id,
			"", http.StatusOK, &got)
		t.Fatalf("joiner never saw original name; last=%+v", got)
	}

	// Owner renames + bumps description + sets an icon.
	patch, _ := json.Marshal(api.SpaceUpdateRequest{
		Name:        ptr("renamed-by-owner"),
		Description: ptr("new-desc"),
		IconCID:     ptr("bafyiconcidplaceholder"),
	})
	mustStatus(t, http.MethodPatch, owner.base+"/v1/spaces/"+sp.Id,
		string(patch), http.StatusNoContent)

	// Joiner polls the same endpoint the CLI / UI would read. Plain
	// pollUntil — see the spaceIndex caveat on the orig-name poll above.
	var last api.SpaceInfo
	if !pollUntil(90*time.Second, func() bool {
		mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id,
			"", http.StatusOK, &last)
		return last.Name == "renamed-by-owner" &&
			last.Description == "new-desc" &&
			last.IconCID == "bafyiconcidplaceholder"
	}) {
		t.Fatalf("joiner never converged on patched metadata; last=%+v", last)
	}

	// And the row should show up in joiner's GET /v1/spaces list too,
	// not just the by-id lookup.
	var list api.SpaceListResponse
	mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces", "",
		http.StatusOK, &list)
	var found bool
	for _, row := range list.Spaces {
		if row.Id == sp.Id {
			found = true
			if row.Name != "renamed-by-owner" {
				t.Errorf("list row name = %q, want renamed-by-owner; row=%+v", row.Name, row)
			}
			break
		}
	}
	if !found {
		t.Errorf("joiner list missing space %s: %+v", sp.Id, list.Spaces)
	}
}

func ptr[T any](v T) *T { return &v }

// TestE2E_MultipeerDecline covers the owner-rejects-join path:
// owner mints → joiner joins → owner declines → owner /members/requests
// drains and the joiner does not appear as an active member.
func TestE2E_MultipeerDecline(t *testing.T) {
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
		`{"name":"decline"}`, http.StatusCreated, &sp)

	req := awaitJoinRequest(t, owner, joiner, sp.Id)

	decline, _ := json.Marshal(api.ACLDeclineRequest{Identity: req.Identity})
	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/acl/decline",
		string(decline), http.StatusNoContent)

	// Pending request list should drain (decline is local-immediate on
	// owner; consensus push happens but owner-side reflection is
	// already up-to-date).
	if !pollUntil(60*time.Second, func() bool {
		var resp api.JoinRequestsResponse
		mustJSON(t, http.MethodGet,
			owner.base+"/v1/spaces/"+sp.Id+"/members/requests", "",
			http.StatusOK, &resp)
		return len(resp.Requests) == 0
	}) {
		t.Fatalf("owner request list did not drain after decline")
	}

	var ml api.MembersListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
		"", http.StatusOK, &ml)
	for _, m := range ml.Members {
		if m.Permission != api.SpacePermissionOwner && m.Status == api.MemberStatusActive {
			t.Errorf("non-owner active after decline: %+v", m)
		}
	}
}

// TestE2E_MultipeerStopSharing exercises the cleanup path: after a
// successful join, owner /acl/stop-sharing → invites empty, only owner
// remains in members. Validates the multi-step rotation (revoke
// invites + remove non-owners + rotate read key) round-trips through
// the HTTP layer.
func TestE2E_MultipeerStopSharing(t *testing.T) {
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
		`{"name":"stopsharing"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Confirm precondition: 2 members + 1 invite (joinSpace minted one).
	var preInvites api.InvitesListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/invites",
		"", http.StatusOK, &preInvites)
	if len(preInvites.Invites) == 0 {
		t.Fatalf("expected at least one active invite before stop-sharing")
	}

	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/acl/stop-sharing",
		"", http.StatusNoContent)

	// Invites drained.
	if !pollUntil(60*time.Second, func() bool {
		var resp api.InvitesListResponse
		mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/invites",
			"", http.StatusOK, &resp)
		return len(resp.Invites) == 0
	}) {
		t.Fatalf("invites did not drain after stop-sharing")
	}

	// StopSharing drops non-owners — but they may still surface in
	// /members with Status="removed" rather than being deleted from
	// the row set. The contract we assert: exactly one active member
	// (the owner), regardless of how removed entries are rendered.
	var lastMembers []api.Member
	if !pollUntil(60*time.Second, func() bool {
		var ml api.MembersListResponse
		mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
			"", http.StatusOK, &ml)
		lastMembers = ml.Members
		actives := 0
		var ownerOk bool
		for _, m := range ml.Members {
			if m.Status == api.MemberStatusActive {
				actives++
				if m.Permission == api.SpacePermissionOwner {
					ownerOk = true
				}
			}
		}
		return actives == 1 && ownerOk
	}) {
		t.Fatalf("active membership did not collapse to owner-only after stop-sharing; members=%+v", lastMembers)
	}
}

// TestE2E_MultipeerPermissionChange flips the joiner from writer to
// reader after they're already active. Asserts owner's /members
// reflects the new permission. (The follow-on assertion that joiner
// can no longer write is deferred — it'd need cross-peer object data
// sync, which the SDK doesn't ship today; see CRDTConvergence skip.)
func TestE2E_MultipeerPermissionChange(t *testing.T) {
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
		`{"name":"perm-change"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Look up joiner identity from the post-join member list.
	var ml api.MembersListResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
		"", http.StatusOK, &ml)
	var joinerId string
	for _, m := range ml.Members {
		if m.Permission != api.SpacePermissionOwner {
			joinerId = m.Identity
		}
	}
	if joinerId == "" {
		t.Fatalf("could not find joiner in members list: %+v", ml.Members)
	}

	body, _ := json.Marshal(api.ACLChangePermissionsRequest{
		Changes: []api.ACLPermissionChange{{
			Identity:   joinerId,
			Permission: api.SpacePermissionReader,
		}},
	})
	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/acl/permissions",
		string(body), http.StatusNoContent)

	if !pollUntil(60*time.Second, func() bool {
		var ml api.MembersListResponse
		mustJSON(t, http.MethodGet, owner.base+"/v1/spaces/"+sp.Id+"/members",
			"", http.StatusOK, &ml)
		for _, m := range ml.Members {
			if m.Identity == joinerId && m.Permission == api.SpacePermissionReader {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("joiner permission did not flip to reader on owner")
	}
}
