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

// TestE2E_MultipeerProcesses drives the process helper across two
// peers over a shared space: the owner registers a space-scope
// process and heartbeats progress; the joiner — holding the space's
// event interest via an SSE subscription — materializes it in
// GET /v1/processes from the broadcasts alone; the joiner cancels,
// the owner receives process.cancel addressed at its identity and
// finishes with status cancelled, which the joiner observes as the
// terminal state. Broadcasts are fire-and-forget and interest
// propagation is asynchronous, so every cross-peer step polls.
func TestE2E_MultipeerProcesses(t *testing.T) {
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
		`{"name":"processes"}`, http.StatusCreated, &sp)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// The joiner's process visibility rides its space event interest;
	// the owner's cancel channel is its own process.cancel subscription.
	jURL := "/v1/events/subscribe?scope=space&spaceId=" + sp.Id + "&type=process.*"
	jFrames, jErrc := openSSE(ctx, t, http.MethodGet, joiner.base+jURL, "")
	waitSSE(t, jFrames, jErrc, "ready", 15*time.Second)
	oURL := "/v1/events/subscribe?scope=space&spaceId=" + sp.Id + "&type=process.cancel"
	oFrames, oErrc := openSSE(ctx, t, http.MethodGet, owner.base+oURL, "")
	waitSSE(t, oFrames, oErrc, "ready", 15*time.Second)

	mustJSON(t, http.MethodPost, owner.base+"/v1/processes",
		fmt.Sprintf(`{"id":"job1","kind":"e2e.demo","title":"Demo","scope":"space","spaceId":%q,"target":"obj1"}`, sp.Id),
		http.StatusOK, nil)

	// Heartbeat progress until the joiner's view materializes the row —
	// every frame carries the full descriptor, so any one suffices.
	var jp api.Process
	materialized := false
	for i := 0; i < 40 && !materialized; i++ {
		mustJSON(t, http.MethodPost, owner.base+"/v1/processes/job1/progress",
			fmt.Sprintf(`{"done":%d,"total":40,"message":"tick"}`, i), http.StatusOK, nil)
		var list api.ProcessListResponse
		mustJSON(t, http.MethodGet, joiner.base+"/v1/processes", "", http.StatusOK, &list)
		for _, p := range list.Processes {
			if p.Id == "job1" && p.Identity == ownerAcc.Id {
				jp, materialized = p, true
			}
		}
		if !materialized {
			time.Sleep(2 * time.Second)
		}
	}
	if !materialized {
		t.Fatal("joiner never materialized the owner's process")
	}
	if jp.Kind != "e2e.demo" || jp.Title != "Demo" || jp.Target != "obj1" ||
		jp.State != api.ProcessStateRunning || jp.Self || jp.SpaceId != sp.Id {
		t.Errorf("joiner view = %+v", jp)
	}

	// Joiner cancels until the owner's cancel subscription hears it.
	var cancelEv api.Event
	heard := false
	for i := 0; i < 40 && !heard; i++ {
		mustJSON(t, http.MethodPost, joiner.base+"/v1/processes/job1/cancel", "{}", http.StatusOK, nil)
		select {
		case f, ok := <-oFrames:
			if !ok {
				t.Fatal("owner SSE stream closed")
			}
			if f.Event != "event" {
				continue
			}
			if err := json.Unmarshal(f.Data, &cancelEv); err != nil {
				t.Fatalf("decode cancel event: %v (%s)", err, f.Data)
			}
			heard = true
		case err := <-oErrc:
			t.Fatalf("owner SSE error: %v", err)
		case <-time.After(2 * time.Second):
		}
	}
	if !heard {
		t.Fatal("owner never received process.cancel")
	}
	if cancelEv.Target != "job1" || cancelEv.Sender == nil || cancelEv.Sender.Self {
		t.Errorf("cancel event = %+v, want target job1 from the joiner", cancelEv)
	}
	var cd struct {
		Identity string `json:"identity"`
	}
	if err := json.Unmarshal(cancelEv.Data, &cd); err != nil || cd.Identity != ownerAcc.Id {
		t.Errorf("cancel data = %s (err %v), want identity %s", cancelEv.Data, err, ownerAcc.Id)
	}

	// Owner reacts: finish cancelled, until the joiner sees the
	// terminal state (the row stays live for re-finish meanwhile).
	terminal := false
	for i := 0; i < 40 && !terminal; i++ {
		mustJSON(t, http.MethodPost, owner.base+"/v1/processes/job1/finish",
			`{"status":"cancelled"}`, http.StatusOK, nil)
		var list api.ProcessListResponse
		mustJSON(t, http.MethodGet, joiner.base+"/v1/processes", "", http.StatusOK, &list)
		for _, p := range list.Processes {
			if p.Id == "job1" && p.State == api.ProcessStateCancelled {
				terminal = true
			}
		}
		if !terminal {
			time.Sleep(2 * time.Second)
		}
	}
	if !terminal {
		t.Fatal("joiner never observed the cancelled terminal state")
	}
}
