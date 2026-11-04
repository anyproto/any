package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// overviewBundle reads the Overview's registry row on p.
func overviewBundle(t *testing.T, p *peer, spaceId string) (api.BundleGetResponse, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+spaceId+"/bundles/"+overviewBundlePath, "")
	if resp.StatusCode != http.StatusOK {
		return api.BundleGetResponse{}, false
	}
	var got api.BundleGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: decode bundle: %v body=%s", p.name, err, raw)
	}
	return got, true
}

// TestE2E_MultideviceOverviewFork forks the Overview: two devices of the
// owner's account set it up while apart, each writes entries, and after
// sync the client merge carries the loser's entries the winner lacks
// into the winner.
func TestE2E_MultideviceOverviewFork(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multidevice overview test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	devA := startPeer(t, bin, "devA")
	defer devA.stop(t)
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces", `{"name":"overview-fork"}`, http.StatusCreated, &sp)

	devB := startPeerWithMnemonic(t, bin, "devB", walletMnemonic(t, devA.dataDir))
	defer devB.stop(t)
	// devB holds the space as its owner, with a converged empty registry.
	if !pollUntil(3*time.Minute, func() bool {
		resp, raw := doRequest(t, http.MethodGet, devB.base+"/v1/spaces/"+sp.Id+"/members/me", "")
		var me api.Member
		if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &me) != nil || me.Permission != api.SpacePermissionOwner {
			return false
		}
		var list api.BundleListResponse
		resp, raw = doRequest(t, http.MethodGet, devB.base+"/v1/spaces/"+sp.Id+"/bundles", "")
		return resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &list) == nil && list.Synced && len(list.Bundles) == 0
	}) {
		t.Fatal("devB never opened the space as its owner")
	}

	restore := restartOffline(t, bin, devB)
	assertOffline(t, devB, sp.Id)

	onA := overviewSetup(t, devA, sp.Id)
	if !onA.Installed || onA.Bundle.Derived {
		t.Fatalf("devA setup: %+v", onA)
	}
	rootA := onA.Bundle.RootId
	aWrote := mustEntries(t, `{
		"settings": {"opensTo":"overview"},
		"w:chat":   {"pos":"PPPO","layout":"messages"},
		"s:recent": {"limit":5},
		"w:tasks":  {"pos":"PPPN","removed":false},
		"w:recent": {"pos":"PPPL","removed":true}}`)
	upsertEntries(t, devA, sp.Id, rootA, recordsJSON(t, aWrote))

	start := time.Now()
	onB := overviewSetup(t, devB, sp.Id)
	t.Logf("devB offline setup: %v installed=%v root=%s", time.Since(start).Round(time.Millisecond), onB.Installed, onB.Bundle.RootId)
	if !onB.Installed || onB.Bundle.RootId == rootA {
		t.Fatalf("devB offline setup must mint its own root: %+v", onB)
	}
	rootB := onB.Bundle.RootId
	bWrote := mustEntries(t, `{
		"settings":  {"opensTo":"last","overviewHidden":true},
		"w:chat":    {"pos":"PPPM","layout":"compact"},
		"s:bin":     {"removed":true},
		"f:bfolder": {"name":"B folder","parentId":"","pos":"a0"}}`)
	upsertEntries(t, devB, sp.Id, rootB, recordsJSON(t, bWrote))
	assertOffline(t, devB, sp.Id)
	if b, _ := overviewBundle(t, devA, sp.Id); len(b.Bundle.Roots) > 1 {
		t.Fatalf("devA saw devB's install while apart: %+v", b.Bundle)
	}

	restore()

	// One winner and one loser, the same on both devices.
	var bA, bB api.BundleGetResponse
	start = time.Now()
	if !pollUntil(3*time.Minute, func() bool {
		var okA, okB bool
		bA, okA = overviewBundle(t, devA, sp.Id)
		bB, okB = overviewBundle(t, devB, sp.Id)
		return okA && okB && len(bA.Bundle.Losers) == 1 && len(bB.Bundle.Losers) == 1 &&
			bA.Bundle.RootId == bB.Bundle.RootId && bA.Bundle.Losers[0] == bB.Bundle.Losers[0]
	}) {
		t.Fatalf("the registry never named one loser on both devices:\n devA=%+v\n devB=%+v", bA, bB)
	}
	winner, loser := bA.Bundle.RootId, bA.Bundle.Losers[0]
	t.Logf("fork visible %v after reconnect: winner=%s loser=%s roots=%v synced=%v/%v",
		time.Since(start).Round(time.Millisecond), winner, loser, bA.Bundle.Roots, bA.Synced, bB.Synced)
	if !slices.Contains(bA.Bundle.Roots, rootA) || !slices.Contains(bA.Bundle.Roots, rootB) || winner == loser {
		t.Fatalf("roots: %+v (devA minted %s, devB %s)", bA.Bundle, rootA, rootB)
	}
	won, lost := aWrote, bWrote
	if winner == rootB {
		won, lost = bWrote, aWrote
	}

	// A setup on either device now adopts the winner.
	for _, p := range []*peer{devA, devB} {
		if s := overviewSetup(t, p, sp.Id); s.Installed || s.Bundle.RootId != winner || !slices.Equal(s.Bundle.Losers, []string{loser}) {
			t.Fatalf("%s: setup after the fork: %+v", p.name, s)
		}
	}

	// The merge, on devA: once both trees have arrived, every loser record
	// whose id the winner lacks is upserted into the winner as it is.
	var onWinner, onLoser entries
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{devB, devA}, func() bool {
		var okW, okL bool
		onWinner, okW = overviewEntries(t, devA, sp.Id, winner)
		onLoser, okL = overviewEntries(t, devA, sp.Id, loser)
		return okW && okL && containsEntries(onWinner, won) && containsEntries(onLoser, lost)
	}) {
		t.Fatalf("devA never held both trees:\n winner=%v\n loser=%v", onWinner, onLoser)
	}
	merge := entries{}
	for id, fields := range onLoser {
		if _, held := onWinner[id]; !held {
			merge[id] = fields
		}
	}
	up := upsertEntries(t, devA, sp.Id, winner, recordsJSON(t, merge))
	t.Logf("merge upserted %d loser-only records (created=%d updated=%d skipped=%d)", len(merge), up.Created, up.Updated, up.Skipped)

	// Resolve inside the grace window: refused, the loser stays listed.
	resp, raw := doRequest(t, http.MethodPost, devA.base+"/v1/spaces/"+sp.Id+"/bundles/"+overviewBundlePath+"/resolve",
		`{"loserRootId":"`+loser+`"}`)
	t.Logf("resolve: %d %s", resp.StatusCode, raw)
	if resp.StatusCode != http.StatusConflict || errorCode(t, raw) != "bundle.loser_not_ready" {
		t.Fatalf("resolve inside the grace window: %d %s", resp.StatusCode, raw)
	}
	if b, _ := overviewBundle(t, devA, sp.Id); !slices.Equal(b.Bundle.Losers, []string{loser}) || b.Bundle.RootId != winner {
		t.Fatalf("after a refused resolve: %+v", b.Bundle)
	}

	// The winner holds the union on both devices, its own values kept.
	want := entries{}
	for id, fields := range lost {
		want[id] = fields
	}
	for id, fields := range won {
		want[id] = fields
	}
	var gotA, gotB entries
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{devA, devB}, func() bool {
		gotA, _ = overviewEntries(t, devA, sp.Id, winner)
		gotB, _ = overviewEntries(t, devB, sp.Id, winner)
		return len(gotA) == len(want) && len(gotB) == len(want) && containsEntries(gotA, want) && containsEntries(gotB, want)
	}) {
		t.Fatalf("the winner never held the union:\n want=%v\n devA=%v\n devB=%v", want, gotA, gotB)
	}
}
