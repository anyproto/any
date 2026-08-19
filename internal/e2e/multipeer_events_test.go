package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerEvents drives a space-scope event across two peers:
// the joiner subscribes `scope=space&type=test.*`, the owner publishes,
// the joiner receives with the owner's verified identity (self=false)
// while the owner's own subscriber sees the loopback (self=true).
// Events are fire-and-forget with no replay, and subscribe-side
// interest propagation to peers is asynchronous — so the owner
// publishes in a retry loop until the joiner observes one.
func TestE2E_MultipeerEvents(t *testing.T) {
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

	var ownerAcc api.AccountResponse
	mustJSON(t, http.MethodGet, owner.base+"/v1/account", "", http.StatusOK, &ownerAcc)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"events"}`, http.StatusCreated, &sp)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	subURL := "/v1/events/subscribe?scope=space&spaceId=" + sp.Id + "&type=test.*"
	jFrames, jErrc := openSSE(ctx, t, http.MethodGet, joiner.base+subURL, "")
	waitSSE(t, jFrames, jErrc, "ready", 15*time.Second)
	oFrames, oErrc := openSSE(ctx, t, http.MethodGet, owner.base+subURL, "")
	waitSSE(t, oFrames, oErrc, "ready", 15*time.Second)

	// Publish until the joiner sees one — the joiner's interest reaches
	// the owner asynchronously and earlier publishes are dropped by
	// design.
	var got api.Event
	received := false
	for i := 0; i < 40 && !received; i++ {
		body := fmt.Sprintf(`{"type":"test.ping","scope":"space","spaceId":%q,"target":"t1","data":{"seq":%d}}`,
			sp.Id, i)
		var pub api.EventPublishResponse
		mustJSON(t, http.MethodPost, owner.base+"/v1/events", body, http.StatusOK, &pub)

		select {
		case f, ok := <-jFrames:
			if !ok {
				t.Fatal("joiner SSE stream closed")
			}
			if f.Event != "event" {
				continue
			}
			if err := json.Unmarshal(f.Data, &got); err != nil {
				t.Fatalf("decode joiner event: %v (%s)", err, f.Data)
			}
			received = true
		case err := <-jErrc:
			t.Fatalf("joiner SSE error: %v", err)
		case <-time.After(2 * time.Second):
		}
	}
	if !received {
		t.Fatal("joiner never received a space-scope event")
	}
	if got.Type != "test.ping" || got.Scope != api.EventScopeSpace || got.SpaceId != sp.Id || got.Target != "t1" {
		t.Errorf("joiner event = %+v", got)
	}
	if got.Sender == nil || got.Sender.Identity != ownerAcc.Id || got.Sender.Self {
		t.Errorf("joiner sender = %+v, want identity=%s self=false", got.Sender, ownerAcc.Id)
	}

	// The owner's own subscriber saw at least one of the same publishes
	// via the synchronous loopback, stamped self=true.
	of := waitSSE(t, oFrames, oErrc, "event", 15*time.Second)
	var oev api.Event
	if err := json.Unmarshal(of.Data, &oev); err != nil {
		t.Fatalf("decode owner event: %v", err)
	}
	if oev.Sender == nil || !oev.Sender.Self || oev.Sender.Identity != ownerAcc.Id {
		t.Errorf("owner loopback sender = %+v, want self=true", oev.Sender)
	}
}
