package e2e

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// childSpacesNodeconfEnv names the nodeconf of a network whose
// coordinator accepts organizations and their child spaces. Unset, the
// child-space tests skip. Set, they run against that network, and a
// network without the feature fails them.
const childSpacesNodeconfEnv = "ANY_E2E_CHILD_SPACES_NODECONF"

// childSpacesNodeconf resolves the nodeconf the child-space tests run
// against, or skips the test when none is named.
func childSpacesNodeconf(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("child-space multipeer test takes minutes; rerun without -short")
	}
	path := os.Getenv(childSpacesNodeconfEnv)
	if path == "" {
		t.Skipf("set %s to the nodeconf of a network whose coordinator accepts organizations and their child spaces", childSpacesNodeconfEnv)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", childSpacesNodeconfEnv, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("%s names %s: %v", childSpacesNodeconfEnv, abs, err)
	}
	return abs
}

// joinAs has each joiner join spaceId on an invite of owner, with perm.
// A space holds one request-to-join invite at a time, so each invite is
// revoked once used.
func joinAs(t *testing.T, owner *peer, spaceId, perm string, joiners ...*peer) {
	t.Helper()
	for _, j := range joiners {
		joinSpace(t, owner, j, spaceId, perm)
		if resp, raw := doRequest(t, http.MethodDelete, owner.base+"/v1/spaces/"+spaceId+"/invites", ""); resp.StatusCode >= 300 {
			t.Fatalf("revoke the invites of %s: %d %s", spaceId, resp.StatusCode, raw)
		}
	}
}

// restartPeer starts p's server again on its data dir and address.
func restartPeer(t *testing.T, bin string, p *peer) {
	t.Helper()
	p.srv = execServer(t, bin, filepath.Join(p.dataDir, "config.yaml"), p.dataDir, p.addr)
	waitForReady(t, p.addr, 60*time.Second)
}

// createOrganization creates on p an organization, the space children
// are created under, and checks the reply reads its type.
func createOrganization(t *testing.T, p *peer, name string) api.SpaceInfo {
	t.Helper()
	body, _ := json.Marshal(api.SpaceCreateRequest{Name: name, SpaceType: space.SpaceTypeOrganization})
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, p.base+"/v1/spaces", string(body), http.StatusCreated, &sp)
	if sp.Id == "" || sp.Type != space.SpaceTypeOrganization || sp.SpaceType != space.SpaceTypeOrganization ||
		sp.ParentSpaceId != "" || sp.Status != api.SpaceStatusActive {
		t.Fatalf("%s: create organization %q = %+v, want an active top-level space of type %s", p.name, name, sp, space.SpaceTypeOrganization)
	}
	return sp
}

// createChild creates a child of parentId on p and checks the reply
// names the parent and reads the type of a child.
func createChild(t *testing.T, p *peer, parentId, name string) api.SpaceInfo {
	t.Helper()
	body, _ := json.Marshal(api.SpaceCreateRequest{Name: name, ParentSpaceId: parentId})
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, p.base+"/v1/spaces", string(body), http.StatusCreated, &sp)
	if sp.Id == "" || sp.ParentSpaceId != parentId || sp.Type != space.SpaceTypeOrganizationSpace || sp.Status != api.SpaceStatusActive {
		t.Fatalf("%s: create child %q of %s = %+v, want an active %s naming its parent", p.name, name, parentId, sp, space.SpaceTypeOrganizationSpace)
	}
	return sp
}

// waitSpace polls p's GET /v1/spaces/:id until ok accepts the row.
func waitSpace(t *testing.T, p *peer, id, what string, within time.Duration, ok func(api.SpaceInfo) bool) api.SpaceInfo {
	t.Helper()
	var (
		last   api.SpaceInfo
		status int
		raw    []byte
	)
	if !pollUntil(within, func() bool {
		var resp *http.Response
		resp, raw = doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+id, "")
		if status = resp.StatusCode; status != http.StatusOK {
			return false
		}
		last = api.SpaceInfo{}
		return json.Unmarshal(raw, &last) == nil && ok(last)
	}) {
		t.Fatalf("%s: %s: space %s: last %d %s", p.name, what, id, status, raw)
	}
	return last
}

// getSpace reads p's GET /v1/spaces/:id once.
func getSpace(t *testing.T, p *peer, id string) api.SpaceInfo {
	t.Helper()
	var si api.SpaceInfo
	mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+id, "", http.StatusOK, &si)
	return si
}

// rawSpaceRow returns p's raw space-list row of id, read with
// POST /v1/spaces/query, which opens no space.
func rawSpaceRow(t *testing.T, p *peer, id string) (map[string]any, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"filter": map[string]any{"id": id}})
	var resp struct {
		Records []map[string]any `json:"records"`
	}
	mustJSON(t, http.MethodPost, p.base+"/v1/spaces/query", string(body), http.StatusOK, &resp)
	for _, r := range resp.Records {
		if r["id"] == id {
			return r, true
		}
	}
	return nil, false
}

// children returns the children of parentId as the coordinator answers
// p, by id, or the status and error code of a refusal.
func children(t *testing.T, p *peer, parentId string) (map[string]string, int, string) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+parentId+"/children", "")
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, errorCode(t, raw)
	}
	var list api.SpaceChildrenResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode children of %s: %v (%s)", parentId, err, raw)
	}
	out := make(map[string]string, len(list.Children))
	for _, c := range list.Children {
		out[c.Id] = c.Status
	}
	return out, http.StatusOK, ""
}

// waitChildren waits until the children of parentId as p reads them
// satisfy ok. A refusal of the read is waited out too.
func waitChildren(t *testing.T, p *peer, parentId, what string, within time.Duration, ok func(map[string]string) bool) map[string]string {
	t.Helper()
	var (
		last   map[string]string
		status int
		code   string
	)
	if !pollUntil(within, func() bool {
		last, status, code = children(t, p, parentId)
		return status == http.StatusOK && ok(last)
	}) {
		t.Fatalf("%s: %s: children of %s are %v (last %d %s)", p.name, what, parentId, last, status, code)
	}
	return last
}

// waitListed waits until p lists childId as an active child of
// parentId: the coordinator accepted it.
func waitListed(t *testing.T, p *peer, parentId, childId string) {
	t.Helper()
	waitChildren(t, p, parentId, "child "+childId+" accepted", 90*time.Second, func(m map[string]string) bool {
		return m[childId] == api.ChildStatusActive
	})
}

// childMembersOf reads the accounts of childId from the parent side on
// p, by identity.
func childMembersOf(t *testing.T, p *peer, childId string) (map[string]api.ChildMember, bool, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/children/"+childId+"/members", "")
	if resp.StatusCode != http.StatusOK {
		return nil, false, false
	}
	var r api.ChildMembersResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode members of %s: %v (%s)", childId, err, raw)
	}
	out := make(map[string]api.ChildMember, len(r.Members))
	for _, m := range r.Members {
		out[m.Identity] = m
	}
	return out, r.RotationPending, true
}

// waitChildMembers waits until the parent-side read of childId on p
// satisfies ok.
func waitChildMembers(t *testing.T, p *peer, childId, what string, ok func(map[string]api.ChildMember, bool) bool) {
	t.Helper()
	var (
		last    map[string]api.ChildMember
		pending bool
	)
	if !pollUntil(90*time.Second, func() bool {
		var read bool
		last, pending, read = childMembersOf(t, p, childId)
		return read && ok(last, pending)
	}) {
		t.Fatalf("%s: %s: members of %s are %+v, rotationPending %v", p.name, what, childId, last, pending)
	}
}

// roster reads GET /v1/spaces/:id/members on p, by identity.
func roster(t *testing.T, p *peer, spaceId string) (map[string]api.Member, bool, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+spaceId+"/members", "")
	if resp.StatusCode != http.StatusOK {
		return nil, false, false
	}
	var r api.MembersListResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode members of %s: %v (%s)", spaceId, err, raw)
	}
	out := make(map[string]api.Member, len(r.Members))
	for _, m := range r.Members {
		out[m.Identity] = m
	}
	return out, r.RotationPending, true
}

// waitRoster waits until p's members list of spaceId satisfies ok.
func waitRoster(t *testing.T, p *peer, spaceId, what string, ok func(map[string]api.Member, bool) bool) {
	t.Helper()
	var (
		last    map[string]api.Member
		pending bool
	)
	if !pollUntil(90*time.Second, func() bool {
		var read bool
		last, pending, read = roster(t, p, spaceId)
		return read && ok(last, pending)
	}) {
		t.Fatalf("%s: %s: members of %s are %+v, rotationPending %v", p.name, what, spaceId, last, pending)
	}
}

// childActiveAs reports that identity is an active member of the
// parent-side read with permission perm.
func childActiveAs(m map[string]api.ChildMember, identity, perm string) bool {
	acc, ok := m[identity]
	return ok && acc.Status == api.MemberStatusActive && acc.Permission == perm
}

// childActive reports that identity is an active member of the
// parent-side read, with any permission.
func childActive(m map[string]api.ChildMember, identity string) bool {
	acc, ok := m[identity]
	return ok && acc.Status == api.MemberStatusActive
}

// rosterActive reports that identity is an active member of a members
// list, with any permission.
func rosterActive(m map[string]api.Member, identity string) bool {
	acc, ok := m[identity]
	return ok && acc.Status == api.MemberStatusActive
}

// addToSpace adds members to spaceId by identity with perm, in one
// record written by adder.
func addToSpace(t *testing.T, adder *peer, spaceId, perm string, members ...*peer) {
	t.Helper()
	var req api.ACLAddRequest
	for _, m := range members {
		req.Accounts = append(req.Accounts, api.ACLAddAccount{Identity: accountId(t, m.base), Permission: perm})
	}
	body, _ := json.Marshal(req)
	mustStatus(t, http.MethodPost, adder.base+"/v1/spaces/"+spaceId+"/acl/add", string(body), http.StatusNoContent)
}

// acceptAdd accepts on p the direct-add invite to spaceId once it
// arrives, and waits for the space to be active there.
func acceptAdd(t *testing.T, p *peer, spaceId string) {
	t.Helper()
	waitSpace(t, p, spaceId, "the direct-add invite arrives", 2*time.Minute, func(si api.SpaceInfo) bool {
		return si.Status == api.SpaceStatusInvitePending
	})
	resp, raw := doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+spaceId+"/invite/accept", "")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s: accept invite to %s: %d %s", p.name, spaceId, resp.StatusCode, raw)
	}
	waitSpace(t, p, spaceId, "the accepted space is active", 2*time.Minute, func(si api.SpaceInfo) bool {
		return si.Status == api.SpaceStatusActive
	})
}

// readsNote reports whether p reads the type noteId in spaceId with
// the name text. A type stands for any content: it is encrypted with
// the space's read key.
func readsNote(t *testing.T, p *peer, spaceId, noteId, text string) bool {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+spaceId+"/types/"+noteId, "")
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var info api.TypeInfo
	return json.Unmarshal(raw, &info) == nil && info.Name == text
}

// waitNote waits until p reads the note.
func waitNote(t *testing.T, p *peer, spaceId, noteId, text string) {
	t.Helper()
	if !pollUntil(90*time.Second, func() bool { return readsNote(t, p, spaceId, noteId, text) }) {
		t.Fatalf("%s never read %q in %s", p.name, text, spaceId)
	}
}

// neverNote checks for a while that p does not read the note.
func neverNote(t *testing.T, p *peer, spaceId, noteId, text string, within time.Duration) {
	t.Helper()
	if pollUntil(within, func() bool { return readsNote(t, p, spaceId, noteId, text) }) {
		t.Errorf("%s read %q in %s", p.name, text, spaceId)
	}
}

// wantError checks that method path on p answers status with code.
func wantError(t *testing.T, p *peer, method, path, body string, status int, code string) {
	t.Helper()
	resp, raw := doRequest(t, method, p.base+path, body)
	if resp.StatusCode != status {
		t.Errorf("%s: %s %s: %d %s, want %d %s", p.name, method, path, resp.StatusCode, raw, status, code)
		return
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Error.Code != code {
		t.Errorf("%s: %s %s: body %s, want code %s", p.name, method, path, raw, code)
	}
}

// sharingRefused checks that the calls that share the refused child
// spaceId answer on p at once: invite create and direct add with status
// and code, the guest key with the code of a space with no guest role.
func sharingRefused(t *testing.T, p *peer, spaceId, identity string, status int, code string) {
	t.Helper()
	add, _ := json.Marshal(api.ACLAddRequest{Accounts: []api.ACLAddAccount{{Identity: identity, Permission: api.SpacePermissionWriter}}})
	for _, call := range []struct {
		path, body string
		status     int
		code       string
	}{
		{"/invites", "", status, code},
		{"/guest-key", "", http.StatusConflict, "acl.no_guest_role"},
		{"/acl/add", string(add), status, code},
	} {
		start := time.Now()
		wantError(t, p, http.MethodPost, "/v1/spaces/"+spaceId+call.path, call.body, call.status, call.code)
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("%s: POST %s on the refused child took %s, want its answer at once", p.name, call.path, took.Round(time.Second))
		}
	}
}

// wantStatus checks that method path on p answers status.
func wantStatus(t *testing.T, p *peer, method, path, body string, status int) {
	t.Helper()
	resp, raw := doRequest(t, method, p.base+path, body)
	if resp.StatusCode != status {
		t.Errorf("%s: %s %s: %d %s, want %d", p.name, method, path, resp.StatusCode, raw, status)
	}
}

// removeBody and ownershipBody are the bodies of the ACL routes.
func removeBody(identities ...string) string {
	b, _ := json.Marshal(api.ACLRemoveRequest{Identities: identities})
	return string(b)
}

func ownershipBody(newOwner, oldOwnerPerm string) string {
	b, _ := json.Marshal(api.ACLOwnershipRequest{NewOwner: newOwner, OldOwnerPerm: oldOwnerPerm})
	return string(b)
}

// parentSideCalls checks the members read, a removal naming identity and
// an owner change to identity on childId answer status with code.
func parentSideCalls(t *testing.T, p *peer, childId, identity string, status int, code string) {
	t.Helper()
	wantError(t, p, http.MethodGet, "/v1/children/"+childId+"/members", "", status, code)
	wantError(t, p, http.MethodPost, "/v1/children/"+childId+"/acl/remove", removeBody(identity), status, code)
	wantError(t, p, http.MethodPost, "/v1/children/"+childId+"/acl/ownership",
		ownershipBody(identity, api.SpacePermissionWriter), status, code)
}

// unknownSpaceId returns a well-formed space id the network does not
// know, with the replication key of like.
func unknownSpaceId(t *testing.T, like string) string {
	t.Helper()
	dot := strings.LastIndexByte(like, '.')
	if dot <= 0 {
		t.Fatalf("%q is not a space id", like)
	}
	// A CIDv1 of a dag-cbor sha2-256 digest, in base32 with the "b"
	// multibase prefix, like the cid of every space id.
	raw := append([]byte{0x01, 0x71, 0x12, 0x20}, make([]byte, 32)...)
	if _, err := rand.Read(raw[4:]); err != nil {
		t.Fatal(err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return "b" + strings.ToLower(enc.EncodeToString(raw)) + like[dot:]
}
