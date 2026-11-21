// Child spaces across peers: a space created under a parent space, with
// its own members and read key, governed from the parent without being
// read. Each peer is its own account unless a test says otherwise.
//
// Gating: runs against the network named by
// ANY_E2E_CHILD_SPACES_NODECONF, and skips when it is unset. The network
// must provide:
//   - a coordinator that accepts child spaces;
//   - a child-spaces limit of 2 to 10 for a new account: the story test
//     creates a second child while the first exists, and the refusals
//     test fills the limit and accepts 1 to 10;
//   - a deletion period short enough for a deletion to become final
//     within three minutes: that is how long the tests wait for a
//     deleted child to leave its parent's list.
//
// Run:
//
//	ANY_E2E_CHILD_SPACES_NODECONF=/path/to/network.yml \
//	  go test ./internal/e2e/ -run TestE2E_MultipeerChildSpaces -count=1 -timeout 60m -v
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerChildSpaces_Story: a writer of the parent creates a
// child, which the parent's owner lists without being a member of it.
// The child's owner adds members of the parent, who read it. The
// parent's owner reads the child's members and removes one: the key
// rotation is pending until a device of the child's owner changes the
// key, and the removed member reads nothing written after it. The
// parent's owner hands the child to another member, then deletes it,
// and its members see it go. A writer of the parent is refused the
// parent-side calls, except the owner change and the delete when it
// owns the child, and a deleted child answers space.deleted.
func TestE2E_MultipeerChildSpaces_Story(t *testing.T) {
	nodeconf := childSpacesNodeconf(t)
	bin := buildBinary(t)
	owner := startPeerConf(t, bin, "parent's owner", nodeconf)
	defer owner.stop(t)
	writer := startPeerConf(t, bin, "parent's writer", nodeconf)
	defer writer.stop(t)
	removed := startPeerConf(t, bin, "member to remove", nodeconf)
	defer removed.stop(t)
	stays := startPeerConf(t, bin, "member who stays", nodeconf)
	defer stays.stop(t)
	ownerId, writerId := accountId(t, owner.base), accountId(t, writer.base)
	removedId, staysId := accountId(t, removed.base), accountId(t, stays.base)

	var parent api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent"}`, http.StatusCreated, &parent)
	joinAs(t, owner, parent.Id, api.SpacePermissionWriter, writer, stays)
	joinAs(t, owner, parent.Id, api.SpacePermissionReader, removed)

	child := createChild(t, writer, parent.Id, "Child")
	waitSpace(t, writer, child.Id, "the creator owns the child", time.Minute, func(si api.SpaceInfo) bool {
		return si.OwnRole == api.SpacePermissionOwner
	})
	waitListed(t, owner, parent.Id, child.Id)
	wantError(t, owner, http.MethodGet, "/v1/spaces/"+child.Id, "", http.StatusNotFound, "space.not_found")

	const beforeText = "Written before the members joined"
	before := createType(t, writer.base, child.Id, beforeText)
	addToSpace(t, writer, child.Id, api.SpacePermissionWriter, removed, stays)
	for _, p := range []*peer{removed, stays} {
		acceptAdd(t, p, child.Id)
		waitNote(t, p, child.Id, before, beforeText)
	}
	waitSpace(t, removed, child.Id, "the joined child names its parent", time.Minute, func(si api.SpaceInfo) bool {
		return si.ParentSpaceId == parent.Id
	})

	waitChildMembers(t, owner, child.Id, "the parent's owner reads the child's members", func(m map[string]api.ChildMember, pending bool) bool {
		return childActiveAs(m, writerId, api.SpacePermissionOwner) &&
			childActiveAs(m, removedId, api.SpacePermissionWriter) &&
			childActiveAs(m, staysId, api.SpacePermissionWriter) && !pending
	})
	if m, _, _ := childMembersOf(t, owner, child.Id); childActive(m, ownerId) {
		t.Errorf("the parent's owner is listed as a member of the child: %+v", m[ownerId])
	}

	// The member who stays is a writer of the parent and of the child.
	t.Run("ParentSideCallsOfAWriter", func(t *testing.T) {
		if _, _, ok := childMembersOf(t, stays, child.Id); !ok {
			t.Errorf("a writer of the parent cannot read the child's members; the read takes no role")
		}
		wantError(t, stays, http.MethodPost, "/v1/children/"+child.Id+"/acl/remove",
			removeBody(removedId), http.StatusForbidden, "space.parent_permission_denied")
		wantError(t, stays, http.MethodPost, "/v1/children/"+child.Id+"/acl/ownership",
			ownershipBody(staysId, api.SpacePermissionWriter), http.StatusForbidden, "space.parent_permission_denied")
		wantError(t, stays, http.MethodDelete, "/v1/children/"+child.Id, "",
			http.StatusForbidden, "space.parent_permission_denied")
		m, _, _ := childMembersOf(t, owner, child.Id)
		if !childActiveAs(m, removedId, api.SpacePermissionWriter) || !childActiveAs(m, writerId, api.SpacePermissionOwner) {
			t.Errorf("the refused calls changed the child: %+v", m)
		}
	})

	keyBefore := waitSpace(t, writer, child.Id, "the push keys are mirrored", time.Minute, func(si api.SpaceInfo) bool {
		return si.Push != nil && si.Push.EncKeyId != ""
	}).Push.EncKeyId

	// No device of the child's owner runs, so nobody changes the key.
	writer.stop(t)
	mustStatus(t, http.MethodPost, owner.base+"/v1/children/"+child.Id+"/acl/remove",
		removeBody(removedId), http.StatusNoContent)
	waitChildMembers(t, owner, child.Id, "the rotation is pending on the parent side", func(m map[string]api.ChildMember, pending bool) bool {
		return !childActive(m, removedId) && pending
	})
	waitRoster(t, stays, child.Id, "the rotation is pending for a member", func(m map[string]api.Member, pending bool) bool {
		return !rosterActive(m, removedId) && pending
	})

	// The device of the child's owner starts again and changes the key
	// by itself.
	restartPeer(t, bin, writer)
	sawPending := false
	waitRoster(t, writer, child.Id, "the child's owner changes the key", func(m map[string]api.Member, pending bool) bool {
		gone := !rosterActive(m, removedId)
		sawPending = sawPending || (gone && pending)
		return gone && !pending
	})
	t.Logf("the child owner's members list read rotationPending true before the key change: %v", sawPending)
	waitChildMembers(t, owner, child.Id, "the rotation ends on the parent side", func(m map[string]api.ChildMember, pending bool) bool {
		return !childActive(m, removedId) && !pending
	})
	waitRoster(t, stays, child.Id, "the rotation ends for a member", func(m map[string]api.Member, pending bool) bool {
		return !rosterActive(m, removedId) && !pending
	})
	waitSpace(t, writer, child.Id, "the push key changes with the read key", time.Minute, func(si api.SpaceInfo) bool {
		return si.Push != nil && si.Push.EncKeyId != "" && si.Push.EncKeyId != keyBefore
	})

	const afterText = "Written after the rotation"
	after := createType(t, writer.base, child.Id, afterText)
	waitNote(t, stays, child.Id, after, afterText)
	neverNote(t, removed, child.Id, after, afterText, 20*time.Second)
	if !readsNote(t, removed, child.Id, before, beforeText) {
		t.Errorf("the removed member no longer reads what it read before the removal")
	}

	// The parent's owner hands the child to the member who stays; the
	// creator keeps writer.
	mustStatus(t, http.MethodPost, owner.base+"/v1/children/"+child.Id+"/acl/ownership",
		ownershipBody(staysId, api.SpacePermissionWriter), http.StatusNoContent)
	waitChildMembers(t, owner, child.Id, "the member who stays owns the child", func(m map[string]api.ChildMember, _ bool) bool {
		return childActiveAs(m, staysId, api.SpacePermissionOwner) && childActiveAs(m, writerId, api.SpacePermissionWriter)
	})
	if !pollUntil(time.Minute, func() bool {
		var me api.Member
		resp, raw := doRequest(t, http.MethodGet, stays.base+"/v1/spaces/"+child.Id+"/members/me", "")
		return resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &me) == nil && me.Permission == api.SpacePermissionOwner
	}) {
		t.Errorf("the new owner's device never reads itself the owner")
	}
	waitSpace(t, writer, child.Id, "the previous owner keeps writer", time.Minute, func(si api.SpaceInfo) bool {
		return si.OwnRole == api.SpacePermissionWriter
	})

	// The child's own owner, a writer of the parent, is accepted on the
	// owner change and the delete, and refused the removal. Its own
	// child takes the calls, since they hand it on and delete it.
	t.Run("ParentSideCallsOfTheChildOwner", func(t *testing.T) {
		own := createChild(t, writer, parent.Id, "Writer's second child")
		waitListed(t, owner, parent.Id, own.Id)
		addToSpace(t, writer, own.Id, api.SpacePermissionWriter, stays)
		waitChildMembers(t, owner, own.Id, "the member is added", func(m map[string]api.ChildMember, _ bool) bool {
			return childActiveAs(m, staysId, api.SpacePermissionWriter)
		})
		path := "/v1/children/" + own.Id

		wantError(t, writer, http.MethodPost, path+"/acl/remove", removeBody(staysId),
			http.StatusForbidden, "space.parent_permission_denied")
		if m, _, _ := childMembersOf(t, owner, own.Id); !childActiveAs(m, staysId, api.SpacePermissionWriter) {
			t.Errorf("the refused removal changed the child: %+v", m)
		}

		wantStatus(t, writer, http.MethodPost, path+"/acl/ownership",
			ownershipBody(staysId, api.SpacePermissionWriter), http.StatusNoContent)
		waitChildMembers(t, owner, own.Id, "the child is handed over by its owner", func(m map[string]api.ChildMember, _ bool) bool {
			return childActiveAs(m, staysId, api.SpacePermissionOwner) && childActiveAs(m, writerId, api.SpacePermissionWriter)
		})

		// The previous owner is a plain writer of the parent and the child.
		wantError(t, writer, http.MethodPost, path+"/acl/ownership", ownershipBody(writerId, api.SpacePermissionWriter),
			http.StatusForbidden, "space.parent_permission_denied")
		wantError(t, writer, http.MethodDelete, path, "", http.StatusForbidden, "space.parent_permission_denied")
		if listed, _, _ := children(t, owner, parent.Id); listed[own.Id] != api.ChildStatusActive {
			t.Errorf("the refused delete changed the child: listed %q", listed[own.Id])
		}

		// The new owner, a writer of the parent too, deletes it.
		wantStatus(t, stays, http.MethodDelete, path, "", http.StatusNoContent)
		if listed, status, code := children(t, owner, parent.Id); status != http.StatusOK ||
			(listed[own.Id] != "" && listed[own.Id] != api.ChildStatusDeletionPending) {
			t.Errorf("children after the child owner's delete: %v (%d %s), want the child gone or deletion_pending", listed, status, code)
		}
	})

	// The parent's owner deletes the child it never read.
	mustStatus(t, http.MethodDelete, owner.base+"/v1/children/"+child.Id, "", http.StatusNoContent)
	if listed, status, code := children(t, owner, parent.Id); status != http.StatusOK ||
		(listed[child.Id] != "" && listed[child.Id] != api.ChildStatusDeletionPending) {
		t.Errorf("children after the delete: %v (%d %s), want the child gone or deletion_pending", listed, status, code)
	}
	t.Run("ParentSideCallsOnTheDeletedChild", func(t *testing.T) {
		parentSideCalls(t, owner, child.Id, writerId, http.StatusConflict, "space.deleted")
		wantStatus(t, owner, http.MethodDelete, "/v1/children/"+child.Id, "", http.StatusNoContent)
	})
	deletedAt := time.Now()
	for _, p := range []*peer{writer, stays} {
		waitSpace(t, p, child.Id, "the deleted child goes", 5*time.Minute, func(si api.SpaceInfo) bool {
			return si.Status == api.SpaceStatusDeleted
		})
		t.Logf("%s read the child deleted %s after the delete", p.name, time.Since(deletedAt).Round(time.Second))
	}
}

// TestE2E_MultipeerChildSpaces_Refusals: the account limits carry the
// child-spaces limit. The children list of a parent created a moment
// ago answers 409 until the parent is on the network, and neither 404
// nor 5xx. A reader's child is refused for the role, stays a usable
// local space, answers the refusal's code on the routes that share it
// and on its children list, and shows the refusal on a second device of
// the account. A writer and a reader cannot list the children. The
// children of the owner's parents fill its limit; the next child is
// refused for the limit and accepted by retry-push once a place frees
// up. The parent-side routes answer their codes for an unknown child, a
// top-level space and an id that is not a space id.
func TestE2E_MultipeerChildSpaces_Refusals(t *testing.T) {
	nodeconf := childSpacesNodeconf(t)
	bin := buildBinary(t)
	owner := startPeerConf(t, bin, "parent's owner", nodeconf)
	defer owner.stop(t)
	writer := startPeerConf(t, bin, "parent's writer", nodeconf)
	defer writer.stop(t)
	reader := startPeerConf(t, bin, "parent's reader", nodeconf)
	defer reader.stop(t)
	readerB := startPeerWithMnemonicConf(t, bin, "reader's second device", walletMnemonic(t, reader.dataDir), nodeconf)
	defer readerB.stop(t)
	writerId := accountId(t, writer.base)

	var limits api.AccountLimitsResponse
	if !pollUntil(time.Minute, func() bool {
		limits = api.AccountLimitsResponse{}
		mustJSON(t, http.MethodGet, owner.base+"/v1/account/limits", "", http.StatusOK, &limits)
		return limits.ChildSpaces != nil
	}) {
		t.Fatalf("GET /v1/account/limits carries no childSpaces: %+v", limits)
	}
	limit := int(limits.ChildSpaces.Limit)
	t.Logf("childSpaces: %+v", *limits.ChildSpaces)
	if limit < 1 || limit > 10 {
		t.Fatalf("childSpaces.limit = %d; the test fills the limit and expects 1 to 10", limit)
	}
	if limits.ChildSpaces.FetchedAt.IsZero() {
		t.Errorf("childSpaces.fetchedAt is zero")
	}

	// A parent is not on the network at once: until it is pushed the
	// coordinator does not know it, then its ACL is still on the way.
	// The account holds it all along, so the list never answers 404.
	t.Run("ChildrenOfAParentJustCreated", func(t *testing.T) {
		var fresh api.SpaceInfo
		mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent created a moment ago"}`, http.StatusCreated, &fresh)
		var seen []string
		for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(50 * time.Millisecond) {
			resp, raw := doRequest(t, http.MethodGet, owner.base+"/v1/spaces/"+fresh.Id+"/children", "")
			answer := fmt.Sprint(resp.StatusCode)
			if resp.StatusCode != http.StatusOK {
				answer += " " + errorCode(t, raw)
			}
			if len(seen) == 0 || seen[len(seen)-1] != answer {
				seen = append(seen, answer)
			}
			switch answer {
			case "200":
				t.Logf("the children list of a parent just created answered %v", seen)
				return
			case "409 space.not_pushed", "409 space.parent_not_available":
			default:
				t.Fatalf("children of a parent just created: %d %s after %v, want 409 space.not_pushed, then 409 space.parent_not_available, then 200",
					resp.StatusCode, raw, seen)
			}
			if time.Now().After(deadline) {
				t.Fatalf("children of a parent just created never answered 200: %v", seen)
			}
		}
	})

	var parent1, parent2 api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent 1"}`, http.StatusCreated, &parent1)
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent 2"}`, http.StatusCreated, &parent2)
	joinAs(t, owner, parent1.Id, api.SpacePermissionWriter, writer)
	joinAs(t, owner, parent1.Id, api.SpacePermissionReader, reader)

	// A reader's child: refused for the role, and usable locally.
	readersChild := createChild(t, reader, parent1.Id, "Reader's child")
	refused := waitSpace(t, reader, readersChild.Id, "the reader's child is refused", 90*time.Second, func(si api.SpaceInfo) bool {
		return si.Refusal != ""
	})
	if refused.Refusal != api.SpaceRefusalParentPermissionDenied || refused.Status != api.SpaceStatusActive || refused.ParentSpaceId != parent1.Id {
		t.Errorf("the reader's child = %+v, want an active child of %s refused with parent_permission_denied", refused, parent1.Id)
	}
	const refusedText = "Written while refused"
	refusedNote := createType(t, reader.base, readersChild.Id, refusedText)
	if !readsNote(t, reader, readersChild.Id, refusedNote, refusedText) {
		t.Errorf("the refused child is not readable locally")
	}
	wantError(t, reader, http.MethodPost, "/v1/spaces/"+readersChild.Id+"/retry-push", "",
		http.StatusForbidden, "space.parent_permission_denied")
	if row := getSpace(t, reader, readersChild.Id); row.Refusal != api.SpaceRefusalParentPermissionDenied {
		t.Errorf("refusal after a refused retry = %q, want parent_permission_denied", row.Refusal)
	}

	// A refused child is on no node: the routes that share it and its
	// children list answer the code of its refusal at once, with no wait
	// for a push.
	t.Run("SharingARefusedChild", func(t *testing.T) {
		sharingRefused(t, reader, readersChild.Id, writerId, http.StatusForbidden, "space.parent_permission_denied")
		wantError(t, reader, http.MethodGet, "/v1/spaces/"+readersChild.Id+"/children", "",
			http.StatusForbidden, "space.parent_permission_denied")
	})

	t.Run("RefusalOnTheSecondDevice", func(t *testing.T) {
		var row map[string]any
		if !pollUntil(90*time.Second, func() bool {
			r, ok := rawSpaceRow(t, readerB, readersChild.Id)
			row = r
			return ok && r["refusal"] == api.SpaceRefusalParentPermissionDenied
		}) {
			t.Fatalf("the second device never read the refusal; raw row %+v", row)
		}
		if row["parentSpaceId"] != parent1.Id {
			t.Errorf("second device: parentSpaceId = %v, want %s", row["parentSpaceId"], parent1.Id)
		}
		start := time.Now()
		var list api.SpaceListResponse
		mustJSON(t, http.MethodGet, readerB.base+"/v1/spaces?status=all", "", http.StatusOK, &list)
		t.Logf("GET /v1/spaces on the second device took %s", time.Since(start).Round(time.Millisecond))
		found := false
		for _, si := range list.Spaces {
			if si.Id == readersChild.Id {
				found = true
				if si.Refusal != api.SpaceRefusalParentPermissionDenied || si.ParentSpaceId != parent1.Id {
					t.Errorf("second device: space list row = %+v, want parent %s and refusal parent_permission_denied", si, parent1.Id)
				}
			}
		}
		if !found {
			t.Errorf("second device: the space list has no row of the refused child")
		}
		// The second device may hold a copy, synced from the first over
		// the local network; then it asks the coordinator itself.
		resp, raw := doRequest(t, http.MethodPost, readerB.base+"/v1/spaces/"+readersChild.Id+"/retry-push", "")
		switch code := errorCode(t, raw); {
		case resp.StatusCode == http.StatusConflict && code == "space.not_stored":
		case resp.StatusCode == http.StatusForbidden && code == "space.parent_permission_denied":
			t.Logf("the second device holds a copy of the refused child")
		default:
			t.Errorf("retry-push on the second device: %d %s, want 409 space.not_stored, or 403 space.parent_permission_denied from a device that holds a copy", resp.StatusCode, raw)
		}
	})

	t.Run("ChildrenListRefused", func(t *testing.T) {
		for _, p := range []*peer{writer, reader} {
			wantError(t, p, http.MethodGet, "/v1/spaces/"+parent1.Id+"/children", "",
				http.StatusForbidden, "space.parent_permission_denied")
		}
	})

	// The children of the owner's two parents fill its limit; the
	// writer's child counts against the owner.
	type made struct {
		by         *peer
		parent, id string
	}
	filled := []made{{writer, parent1.Id, createChild(t, writer, parent1.Id, "Writer's child").Id}}
	for i := 1; i < limit; i++ {
		parent := []string{parent2.Id, parent1.Id}[(i-1)%2]
		filled = append(filled, made{owner, parent, createChild(t, owner, parent, fmt.Sprintf("Owner's child %d", i)).Id})
	}
	for _, c := range filled {
		waitListed(t, owner, c.parent, c.id)
		if row := waitSpace(t, c.by, c.id, "the accepted child's row", time.Minute, func(api.SpaceInfo) bool { return true }); row.Refusal != "" {
			t.Errorf("%s: accepted child %s reads refusal %q", c.by.name, c.id, row.Refusal)
		}
	}
	if listed, _, _ := children(t, owner, parent1.Id); listed[readersChild.Id] != "" {
		t.Errorf("the refused child is listed as %s", listed[readersChild.Id])
	}

	over := createChild(t, owner, parent2.Id, "Over the limit")
	overRow := waitSpace(t, owner, over.Id, "the child over the limit is refused", 90*time.Second, func(si api.SpaceInfo) bool {
		return si.Refusal != ""
	})
	if overRow.Refusal != api.SpaceRefusalChildLimitReached || overRow.Status != api.SpaceStatusActive {
		t.Errorf("the child over the limit = %+v, want an active space refused with child_limit_reached", overRow)
	}
	wantError(t, owner, http.MethodPost, "/v1/spaces/"+over.Id+"/retry-push", "",
		http.StatusConflict, "space.child_limit_reached")
	sharingRefused(t, owner, over.Id, writerId, http.StatusConflict, "space.child_limit_reached")
	wantError(t, owner, http.MethodGet, "/v1/spaces/"+over.Id+"/children", "",
		http.StatusConflict, "space.child_limit_reached")
	if listed, _, _ := children(t, owner, parent2.Id); listed[over.Id] != "" {
		t.Errorf("the child over the limit is listed as %s", listed[over.Id])
	}
	const overText = "Written over the limit"
	overNote := createType(t, owner.base, over.Id, overText)

	// A place frees up once a child's deletion is final.
	freed := filled[0]
	mustStatus(t, http.MethodDelete, owner.base+"/v1/children/"+freed.id, "", http.StatusNoContent)
	waitChildren(t, owner, freed.parent, "the freed child's deletion is final", 3*time.Minute, func(m map[string]string) bool {
		_, listed := m[freed.id]
		return !listed
	})
	mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+over.Id+"/retry-push", "", http.StatusNoContent)
	if row := getSpace(t, owner, over.Id); row.Refusal != "" {
		t.Errorf("refusal after an accepted retry = %q, want none", row.Refusal)
	}
	waitListed(t, owner, parent2.Id, over.Id)
	if !readsNote(t, owner, over.Id, overNote, overText) {
		t.Errorf("the accepted child lost what was written while it was refused")
	}

	t.Run("UnknownChild", func(t *testing.T) {
		unknown := unknownSpaceId(t, parent1.Id)
		wantError(t, owner, http.MethodGet, "/v1/spaces/"+unknown+"/children", "", http.StatusNotFound, "space.not_found")
		parentSideCalls(t, owner, unknown, writerId, http.StatusNotFound, "space.not_found")
		wantError(t, owner, http.MethodDelete, "/v1/children/"+unknown, "", http.StatusNotFound, "space.not_found")
	})

	t.Run("NotAChild", func(t *testing.T) {
		parentSideCalls(t, owner, parent1.Id, writerId, http.StatusConflict, "space.not_child")
		wantError(t, owner, http.MethodPost, "/v1/spaces/"+parent1.Id+"/retry-push", "", http.StatusConflict, "space.not_child")
		wantError(t, owner, http.MethodDelete, "/v1/children/"+parent1.Id, "", http.StatusConflict, "space.not_child")
		if row := getSpace(t, owner, parent1.Id); row.Status != api.SpaceStatusActive {
			t.Errorf("the parent after the refused calls = %+v", row)
		}
	})

	t.Run("NotASpaceId", func(t *testing.T) {
		wantError(t, owner, http.MethodGet, "/v1/children/not-a-space-id/members", "", http.StatusBadRequest, "request.invalid_field")
		wantError(t, owner, http.MethodGet, "/v1/spaces/not-a-space-id/children", "", http.StatusBadRequest, "request.invalid_field")
	})
}

// TestE2E_MultipeerChildSpaces_Membership: only members of the parent
// become members of a child. An account outside the parent is refused
// on a direct add, with nobody of the batch added, and on a join, with
// no row written; the acceptance of a member who left the parent after
// asking is refused. A guest key of the child's owner gives the
// outsider read-only access. The parent-side routes change nothing in
// the parent, a top-level space the outsider does not hold.
func TestE2E_MultipeerChildSpaces_Membership(t *testing.T) {
	nodeconf := childSpacesNodeconf(t)
	bin := buildBinary(t)
	owner := startPeerConf(t, bin, "parent's owner", nodeconf)
	defer owner.stop(t)
	writer := startPeerConf(t, bin, "parent's writer", nodeconf)
	defer writer.stop(t)
	leaver := startPeerConf(t, bin, "member who leaves the parent", nodeconf)
	defer leaver.stop(t)
	outsider := startPeerConf(t, bin, "outsider", nodeconf)
	defer outsider.stop(t)
	leaverId, outsiderId := accountId(t, leaver.base), accountId(t, outsider.base)

	var parent api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent"}`, http.StatusCreated, &parent)
	joinAs(t, owner, parent.Id, api.SpacePermissionWriter, writer)
	joinAs(t, owner, parent.Id, api.SpacePermissionReader, leaver)
	child := createChild(t, writer, parent.Id, "Child")
	waitListed(t, owner, parent.Id, child.Id)
	const noteText = "For the members of the child"
	note := createType(t, writer.base, child.Id, noteText)

	notMember := func(t *testing.T, identities ...string) {
		t.Helper()
		listed, _, ok := roster(t, writer, child.Id)
		onNodes, _, okNodes := childMembersOf(t, owner, child.Id)
		if !ok || !okNodes {
			t.Fatalf("members of the child unreadable: roster %v, parent side %v", ok, okNodes)
		}
		for _, id := range identities {
			if rosterActive(listed, id) || childActive(onNodes, id) {
				t.Errorf("%s is a member of the child: roster %+v, parent side %+v", id, listed[id], onNodes[id])
			}
		}
	}

	t.Run("DirectAddOfAnOutsider", func(t *testing.T) {
		body, _ := json.Marshal(api.ACLAddRequest{Accounts: []api.ACLAddAccount{
			{Identity: leaverId, Permission: api.SpacePermissionWriter},
			{Identity: outsiderId, Permission: api.SpacePermissionWriter},
		}})
		wantError(t, writer, http.MethodPost, "/v1/spaces/"+child.Id+"/acl/add", string(body),
			http.StatusConflict, "acl.not_parent_member")
		notMember(t, leaverId, outsiderId)
	})

	var invite api.InviteCreateResponse
	mustJSON(t, http.MethodPost, writer.base+"/v1/spaces/"+child.Id+"/invites", "", http.StatusCreated, &invite)
	joinBody, _ := json.Marshal(api.SpaceJoinRequest{InviteToken: invite.InviteToken})

	t.Run("JoinOfAnOutsider", func(t *testing.T) {
		wantError(t, outsider, http.MethodPost, "/v1/spaces/join", string(joinBody),
			http.StatusConflict, "acl.not_parent_member")
		wantError(t, outsider, http.MethodGet, "/v1/spaces/"+child.Id, "", http.StatusNotFound, "space.not_found")
		var reqs api.JoinRequestsResponse
		mustJSON(t, http.MethodGet, writer.base+"/v1/spaces/"+child.Id+"/members/requests", "", http.StatusOK, &reqs)
		for _, r := range reqs.Requests {
			if r.Identity == outsiderId {
				t.Errorf("the refused join is a request of the child: %+v", r)
			}
		}
	})

	t.Run("AcceptAfterTheJoinerLeftTheParent", func(t *testing.T) {
		mustStatus(t, http.MethodPost, leaver.base+"/v1/spaces/join", string(joinBody), http.StatusAccepted)
		var req api.JoinRequest
		if !pollUntil(90*time.Second, func() bool {
			var reqs api.JoinRequestsResponse
			mustJSON(t, http.MethodGet, writer.base+"/v1/spaces/"+child.Id+"/members/requests", "", http.StatusOK, &reqs)
			for _, r := range reqs.Requests {
				if r.Identity == leaverId {
					req = r
					return true
				}
			}
			return false
		}) {
			t.Fatalf("the child's owner never saw the join request")
		}
		mustStatus(t, http.MethodPost, owner.base+"/v1/spaces/"+parent.Id+"/acl/remove",
			removeBody(leaverId), http.StatusNoContent)
		accept, _ := json.Marshal(api.ACLAcceptRequest{RequestRecordId: req.RecordId, Permission: api.SpacePermissionWriter})
		wantError(t, writer, http.MethodPost, "/v1/spaces/"+child.Id+"/acl/accept", string(accept),
			http.StatusConflict, "acl.not_parent_member")
		notMember(t, leaverId)
	})

	t.Run("GuestKeyForAnOutsider", func(t *testing.T) {
		var guest api.InviteCreateResponse
		mustJSON(t, http.MethodPost, writer.base+"/v1/spaces/"+child.Id+"/guest-key", "", http.StatusCreated, &guest)
		body, _ := json.Marshal(api.SpaceJoinRequest{InviteToken: guest.InviteToken})
		resp, raw := doRequest(t, http.MethodPost, outsider.base+"/v1/spaces/join", string(body))
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
			t.Fatalf("guest join: %d %s", resp.StatusCode, raw)
		}
		waitSpace(t, outsider, child.Id, "the guest's copy is active", 2*time.Minute, func(si api.SpaceInfo) bool {
			return si.Status == api.SpaceStatusActive && si.OwnRole == api.SpacePermissionGuest
		})
		waitNote(t, outsider, child.Id, note, noteText)
		wantError(t, outsider, http.MethodPost, "/v1/spaces/"+child.Id+"/types",
			`{"name":"Written by a guest","xKey":"written_by_a_guest"}`, http.StatusForbidden, "space.read_only")
		waitSpace(t, outsider, child.Id, "the guest's row names the parent", time.Minute, func(si api.SpaceInfo) bool {
			return si.ParentSpaceId == parent.Id
		})
	})

	// The parent is a top-level space the outsider does not hold: the
	// coordinator shows it has no parent, and nothing there changes.
	t.Run("TopLevelSpaceOfAnotherAccount", func(t *testing.T) {
		ownerId, writerId := accountId(t, owner.base), accountId(t, writer.base)
		path := "/v1/children/" + parent.Id
		wantError(t, outsider, http.MethodDelete, path, "", http.StatusConflict, "space.not_child")
		for _, member := range []string{ownerId, writerId} {
			wantError(t, outsider, http.MethodPost, path+"/acl/remove", removeBody(member),
				http.StatusForbidden, "acl.forbidden")
		}
		wantError(t, outsider, http.MethodPost, path+"/acl/ownership", ownershipBody(writerId, api.SpacePermissionWriter),
			http.StatusForbidden, "acl.forbidden")
		wantError(t, outsider, http.MethodPost, path+"/acl/ownership", ownershipBody(outsiderId, api.SpacePermissionWriter),
			http.StatusNotFound, "members.not_found")
		listed, _, ok := roster(t, owner, parent.Id)
		if !ok || listed[ownerId].Permission != api.SpacePermissionOwner || !rosterActive(listed, writerId) {
			t.Errorf("the parent's members after the refused calls: readable %v, owner %+v, writer %+v", ok, listed[ownerId], listed[writerId])
		}
		if row := getSpace(t, owner, parent.Id); row.Status != api.SpaceStatusActive {
			t.Errorf("the parent after the refused calls = %+v", row)
		}
	})
}

// TestE2E_MultipeerChildSpaces_Deleting: a member of the child, and the
// parent's owner once a member of it, remove their own copy and the
// child lives on for the others. The parent's delete answers
// space.has_children and changes nothing while a child exists, one the
// owner is no member of or one in its own list; once the parent's owner
// deleted the child and the list is empty, the parent's delete goes
// through and its members see it go.
func TestE2E_MultipeerChildSpaces_Deleting(t *testing.T) {
	nodeconf := childSpacesNodeconf(t)
	bin := buildBinary(t)
	owner := startPeerConf(t, bin, "parent's owner", nodeconf)
	defer owner.stop(t)
	writer := startPeerConf(t, bin, "parent's writer", nodeconf)
	defer writer.stop(t)
	member := startPeerConf(t, bin, "member of the child", nodeconf)
	defer member.stop(t)

	var parent api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"Parent"}`, http.StatusCreated, &parent)
	joinAs(t, owner, parent.Id, api.SpacePermissionWriter, writer)
	joinAs(t, owner, parent.Id, api.SpacePermissionReader, member)
	child := createChild(t, writer, parent.Id, "Child")
	waitListed(t, owner, parent.Id, child.Id)
	addToSpace(t, writer, child.Id, api.SpacePermissionWriter, member)
	acceptAdd(t, member, child.Id)

	childAlive := func(t *testing.T) {
		t.Helper()
		if listed, status, code := children(t, owner, parent.Id); status != http.StatusOK || listed[child.Id] != api.ChildStatusActive {
			t.Errorf("children of the parent = %v (%d %s), want the child active", listed, status, code)
		}
		if row := getSpace(t, writer, child.Id); row.Status != api.SpaceStatusActive {
			t.Errorf("the child's owner reads the child as %+v", row)
		}
		createType(t, writer.base, child.Id, fmt.Sprintf("Written at %s", time.Now().Format(time.RFC3339Nano)))
	}
	parentUnchanged := func(t *testing.T) {
		t.Helper()
		if row := getSpace(t, owner, parent.Id); row.Status != api.SpaceStatusActive || row.Refusal != "" {
			t.Errorf("the parent after the refused delete = %+v, want active with no refusal", row)
		}
		if row := getSpace(t, writer, parent.Id); row.Status != api.SpaceStatusActive {
			t.Errorf("a member reads the parent as %+v", row)
		}
		childAlive(t)
	}

	t.Run("MemberRemovesItsCopy", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, member.base+"/v1/spaces/"+child.Id, "", http.StatusNoContent)
		waitSpace(t, member, child.Id, "the member's copy goes", time.Minute, func(si api.SpaceInfo) bool {
			return si.Status == api.SpaceStatusDeleted
		})
		childAlive(t)
	})

	t.Run("ParentWithAChildTheOwnerIsNoMemberOf", func(t *testing.T) {
		wantError(t, owner, http.MethodDelete, "/v1/spaces/"+parent.Id, "", http.StatusConflict, "space.has_children")
		parentUnchanged(t)
	})

	addToSpace(t, writer, child.Id, api.SpacePermissionWriter, owner)
	acceptAdd(t, owner, child.Id)
	waitSpace(t, owner, child.Id, "the parent's owner's copy names the parent", time.Minute, func(si api.SpaceInfo) bool {
		return si.ParentSpaceId == parent.Id
	})

	t.Run("ParentWithAChildInTheOwnList", func(t *testing.T) {
		wantError(t, owner, http.MethodDelete, "/v1/spaces/"+parent.Id, "", http.StatusConflict, "space.has_children")
		parentUnchanged(t)
	})

	t.Run("ParentOwnerRemovesItsCopy", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, owner.base+"/v1/spaces/"+child.Id, "", http.StatusNoContent)
		waitSpace(t, owner, child.Id, "the parent's owner's copy goes", time.Minute, func(si api.SpaceInfo) bool {
			return si.Status == api.SpaceStatusDeleted
		})
		childAlive(t)
		wantError(t, owner, http.MethodDelete, "/v1/spaces/"+parent.Id, "", http.StatusConflict, "space.has_children")
		parentUnchanged(t)
	})

	mustStatus(t, http.MethodDelete, owner.base+"/v1/children/"+child.Id, "", http.StatusNoContent)

	// The coordinator makes a deletion final on a pass of its own; a
	// parent delete sent while the child is listed as deletion_pending
	// is refused.
	parentDeleted := false
	t.Run("ChildDeletionNotFinal", func(t *testing.T) {
		listed, _, _ := children(t, owner, parent.Id)
		if listed[child.Id] != api.ChildStatusDeletionPending {
			t.Skipf("the child's deletion was final before the check: %v", listed)
		}
		resp, raw := doRequest(t, http.MethodDelete, owner.base+"/v1/spaces/"+parent.Id, "")
		if resp.StatusCode == http.StatusNoContent {
			parentDeleted = true
		}
		if resp.StatusCode != http.StatusConflict || errorCode(t, raw) != "space.has_children" {
			t.Fatalf("delete of the parent while its child is listed deletion_pending: %d %s, want 409 space.has_children", resp.StatusCode, raw)
		}
		if row := getSpace(t, owner, parent.Id); row.Status != api.SpaceStatusActive {
			t.Errorf("the parent after the refused delete = %+v", row)
		}
	})

	if !parentDeleted {
		waitChildren(t, owner, parent.Id, "the parent lists no child", 3*time.Minute, func(m map[string]string) bool {
			return len(m) == 0
		})
		mustStatus(t, http.MethodDelete, owner.base+"/v1/spaces/"+parent.Id, "", http.StatusNoContent)
	}
	if row := getSpace(t, owner, parent.Id); row.Status != api.SpaceStatusDeleted || row.Refusal != "" {
		t.Errorf("the deleted parent = %+v, want deleted with no refusal", row)
	}
	deletedAt := time.Now()
	waitSpace(t, writer, parent.Id, "the member sees the parent go", 5*time.Minute, func(si api.SpaceInfo) bool {
		return si.Status == api.SpaceStatusDeleted
	})
	t.Logf("the member read the parent deleted %s after the delete", time.Since(deletedAt).Round(time.Second))
	wantError(t, owner, http.MethodGet, "/v1/spaces/"+parent.Id+"/children", "", http.StatusConflict, "space.deleted")
}
