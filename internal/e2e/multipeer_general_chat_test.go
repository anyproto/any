package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerGeneralChat proves the general-chat contract across
// two peers: the joiner resolves the SAME generalChatObjectId off its
// own GET /v1/spaces/:id (deterministic derive — no id exchange, no
// remote tree fetch required to start writing), and messages sent by
// both peers into that id converge. This is the scenario the derived
// general chat exists for: each peer materializes an identical tree
// root locally from the fixed seed, so a chat write never depends on
// having synced another peer's chat tree first.
func TestE2E_MultipeerGeneralChat(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer general chat test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"general-chat"}`, http.StatusCreated, &sp)
	if sp.GeneralChatObjectId == "" {
		t.Fatalf("owner create response missing generalChatObjectId: %+v", sp)
	}

	// Owner speaks first, before the joiner exists in the space.
	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + sp.GeneralChatObjectId
	m1 := sendChat(t, ownerBase, `{"text":"hello from owner"}`)
	if m1.Id == "" {
		t.Fatalf("m1 not stamped: %+v", m1)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// The joiner resolves the id from its OWN single-space response —
	// no out-of-band exchange. Poll: right after join the space handle
	// may not be materialized yet and the field is best-effort.
	var joinerInfo api.SpaceInfo
	if !pollUntil(2*time.Minute, func() bool {
		joinerInfo = api.SpaceInfo{}
		mustJSON(t, http.MethodGet, joiner.base+"/v1/spaces/"+sp.Id,
			"", http.StatusOK, &joinerInfo)
		return joinerInfo.GeneralChatObjectId != ""
	}) {
		t.Fatalf("joiner never resolved generalChatObjectId: %+v", joinerInfo)
	}
	if joinerInfo.GeneralChatObjectId != sp.GeneralChatObjectId {
		t.Fatalf("general chat id diverged: owner=%q joiner=%q",
			sp.GeneralChatObjectId, joinerInfo.GeneralChatObjectId)
	}

	// The joiner writes immediately into its locally-derived tree —
	// this must not depend on the owner's tree replica having synced.
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + joinerInfo.GeneralChatObjectId
	m2 := sendChat(t, joinerBase, `{"text":"hello from joiner"}`)
	if m2.Id == "" {
		t.Fatalf("m2 not stamped: %+v", m2)
	}

	// Both messages converge on both peers (locally-derived roots are
	// byte-identical, so the two replicas merge into one tree).
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		onOwner := chatMessages(t, ownerBase)
		onJoiner := chatMessages(t, joinerBase)
		return findById(onOwner, m2.Id).Id == m2.Id &&
			findById(onJoiner, m1.Id).Id == m1.Id
	}) {
		t.Fatalf("general chat did not converge: owner has m2=%v, joiner has m1=%v",
			findById(chatMessages(t, ownerBase), m2.Id).Id != "",
			findById(chatMessages(t, joinerBase), m1.Id).Id != "")
	}
}
