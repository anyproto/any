// Realtime-sync diagnostic: measures how fast a write on one peer
// becomes visible on another WITHOUT forcing SyncHeads. If any-sync's
// stream-based broadcast (objectsync HeadUpdate over the stream pool)
// works, propagation is ~1-3s; if convergence only happens via the
// periodic headsync (diff) timer, it's ~30s+ per write. The other
// multipeer tests can't tell the difference — they either force diff
// rounds (pollUntilSynced) or poll with multi-minute deadlines.
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

// measureConvergence fast-polls fn (150ms tick) until true or deadline,
// returning the elapsed time and whether it converged.
func measureConvergence(deadline time.Duration, fn func() bool) (time.Duration, bool) {
	start := time.Now()
	end := start.Add(deadline)
	for time.Now().Before(end) {
		if fn() {
			return time.Since(start), true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return time.Since(start), false
}

// realtimeBudget is the per-write convergence deadline. Generous enough
// to capture a periodic-headsync-only delivery (~30-60s) so the test
// reports the actual latency instead of just timing out.
const realtimeBudget = 90 * time.Second

// streamLatencyMax is the verdict threshold: stream-delivered writes
// land in ~1-3s; anything beyond this only made it via the periodic
// diff timer.
const streamLatencyMax = 10 * time.Second

func TestE2E_MultipeerRealtimeSync(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer realtime test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"realtime"}`, http.StatusCreated, &sp)

	// Schema + object for the property-value phase, created pre-join so
	// the joiner ingests it with the space.
	var typeResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/types",
		`{"name":"Doc","xKey":"doc"}`, http.StatusCreated, &typeResp)
	typeID, _ := typeResp["typeId"].(string)
	var propResp map[string]any
	mustJSON(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/types/"+typeID+"/properties",
		`{"name":"Title","kind":"string","xKey":"title"}`, http.StatusCreated, &propResp)
	propID, _ := propResp["propId"].(string)
	var objResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeID), http.StatusCreated, &objResp)
	objectID, _ := objResp["objectId"].(string)
	mustStatus(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/properties/"+objectID+"/set/"+typeID,
		fmt.Sprintf(`{"patch":{%q:"v0"}}`, propID), http.StatusOK)

	// Chat object + seed message, also pre-join.
	var chatObj map[string]any
	chatObj = map[string]any{"objectId": createModuleObject(t, owner.base, sp.Id, "chat")}
	chatObjID, _ := chatObj["objectId"].(string)
	ownerChat := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + chatObjID
	joinerChat := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + chatObjID
	sendChat(t, ownerChat, `{"text":"seed"}`)

	joinSpace(t, owner, joiner, sp.Id, "writer")

	// Setup convergence: joiner must hold both trees before we measure,
	// otherwise the first measurement includes initial space ingestion.
	// Forced sync is fine here for the chat tree (setup, not measured);
	// the property value uses plain pollUntil per the SyncHeads caveat.
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return findMessage(chatMessages(t, joinerChat), "seed").Id != ""
	}) {
		t.Fatalf("setup: joiner never saw seed chat message")
	}
	joinerProps := joiner.base + "/v1/spaces/" + sp.Id + "/properties/" + objectID
	readTitle := func() string {
		resp, raw := doRequest(t, http.MethodGet, joinerProps, "")
		if resp.StatusCode != http.StatusOK {
			return ""
		}
		var got map[string]any
		if json.Unmarshal(raw, &got) != nil {
			return ""
		}
		record, _ := got["record"].(map[string]any)
		typeNode, _ := record[typeID].(map[string]any)
		v, _ := typeNode[propID].(string)
		return v
	}
	if !pollUntil(3*time.Minute, func() bool { return readTitle() == "v0" }) {
		t.Fatalf("setup: joiner never saw initial property value")
	}

	type sample struct {
		label   string
		latency time.Duration
		ok      bool
	}
	var samples []sample
	record := func(label string, d time.Duration, ok bool) {
		samples = append(samples, sample{label, d, ok})
		t.Logf("realtime %-28s latency=%v converged=%v", label, d.Round(time.Millisecond), ok)
	}

	// Phase A: chat messages owner→joiner, three rounds. No forced sync —
	// this is the realtime path under test.
	for i := 1; i <= 3; i++ {
		text := fmt.Sprintf("rt-owner-%d", i)
		sendChat(t, ownerChat, fmt.Sprintf(`{"text":%q}`, text))
		d, ok := measureConvergence(realtimeBudget, func() bool {
			return findMessage(chatMessages(t, joinerChat), text).Id != ""
		})
		record(fmt.Sprintf("chat owner→joiner #%d", i), d, ok)
	}

	// Phase B: reverse direction, joiner→owner.
	sendChat(t, joinerChat, `{"text":"rt-joiner-1"}`)
	d, ok := measureConvergence(realtimeBudget, func() bool {
		return findMessage(chatMessages(t, ownerChat), "rt-joiner-1").Id != ""
	})
	record("chat joiner→owner #1", d, ok)

	// Phase C: shared objects collection — property value update.
	mustStatus(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/properties/"+objectID+"/set/"+typeID,
		fmt.Sprintf(`{"patch":{%q:"v1"}}`, propID), http.StatusOK)
	d, ok = measureConvergence(realtimeBudget, func() bool { return readTitle() == "v1" })
	record("property owner→joiner", d, ok)

	// Verdict: every write must converge, and stream delivery means
	// single-digit seconds. A consistent ~30s+ latency = diffsync-only.
	var slow, failed int
	for _, s := range samples {
		if !s.ok {
			failed++
		} else if s.latency > streamLatencyMax {
			slow++
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d writes never converged within %v — sync broken beyond diffsync", failed, len(samples), realtimeBudget)
	}
	if slow > 0 {
		t.Fatalf("%d/%d writes took >%v to propagate — realtime (stream) sync is not delivering; convergence relies on the periodic headsync timer", slow, len(samples), streamLatencyMax)
	}
}
