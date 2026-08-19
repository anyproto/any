// Multi-device devices-registry e2e (SYN-165): two `any` servers
// sharing ONE account (same mnemonic, distinct device keys) exercise
// the tech-space `devices` dataset end-to-end — boot self-registration
// converging across devices, CONCURRENT active claims resolving to the
// same winner on both readers (the core claim of the election design),
// the winner losing the role by uninstalling the app, and row pruning.
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// devicesOn fetches base's GET /v1/devices.
func devicesOn(t *testing.T, base string) api.DevicesListResponse {
	t.Helper()
	var out api.DevicesListResponse
	resp, raw := doRequest(t, http.MethodGet, base+"/v1/devices", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/devices: status %d: %s", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode devices list: %v", err)
	}
	return out
}

func TestE2E_MultideviceDevicesElection(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multidevice test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	devA := startPeer(t, bin, "devA")
	defer devA.stop(t)
	devB := startPeerWithMnemonic(t, bin, "devB", walletMnemonic(t, devA.dataDir))
	defer devB.stop(t)

	selfA := devicesOn(t, devA.base).Self
	selfB := devicesOn(t, devB.base).Self
	if selfA == "" || selfB == "" || selfA == selfB {
		t.Fatalf("bad self peer ids: A=%q B=%q (must be distinct and non-empty)", selfA, selfB)
	}

	// Boot self-registration: each device upserted its own row at engine
	// start; the rows CRDT-replicate through the tech space, so both
	// peers converge on the same two-row registry.
	bothRows := func(base string) bool {
		list := devicesOn(t, base)
		seen := map[string]bool{}
		for _, dev := range list.Devices {
			seen[dev.PeerId] = true
			if dev.OS == "" || dev.Version == "" {
				return false
			}
		}
		return seen[selfA] && seen[selfB]
	}
	if !pollUntil(3*time.Minute, func() bool { return bothRows(devA.base) && bothRows(devB.base) }) {
		t.Fatalf("device rows never converged: A=%+v B=%+v",
			devicesOn(t, devA.base).Devices, devicesOn(t, devB.base).Devices)
	}

	// Concurrent claims — the survivable "bug-shaped state" the design
	// must resolve deterministically: both devices claim `bao` at once;
	// after convergence BOTH readers must elect the SAME winner.
	var wg sync.WaitGroup
	for _, base := range []string{devA.base, devB.base} {
		wg.Add(1)
		go func(base string) {
			defer wg.Done()
			// Off the test goroutine: t.Fatalf would only Goexit this worker
			// and let the test run on half-failed — use tryRequest + Errorf.
			resp, raw, err := tryRequest(http.MethodPost, base+"/v1/devices/activate", `{"app":"bao"}`)
			if err != nil {
				t.Errorf("POST /v1/devices/activate on %s: %v", base, err)
				return
			}
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("POST /v1/devices/activate on %s: status %d: %s", base, resp.StatusCode, raw)
			}
		}(base)
	}
	wg.Wait()
	if t.Failed() {
		return // don't wait out the convergence polls on a failed claim
	}

	claimsOf := func(list api.DevicesListResponse) int {
		n := 0
		for _, dev := range list.Devices {
			if _, ok := dev.ActiveClaims["bao"]; ok {
				n++
			}
		}
		return n
	}
	var winner string
	if !pollUntil(3*time.Minute, func() bool {
		la, lb := devicesOn(t, devA.base), devicesOn(t, devB.base)
		// Converged = both peers see both claims and agree on a winner.
		if claimsOf(la) != 2 || claimsOf(lb) != 2 {
			return false
		}
		winner = la.Active["bao"]
		return winner != "" && lb.Active["bao"] == winner
	}) {
		t.Fatalf("concurrent claims never converged to one winner: A=%v B=%v",
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}
	if winner != selfA && winner != selfB {
		t.Fatalf("winner %q is neither device (A=%q B=%q)", winner, selfA, selfB)
	}
	t.Logf("concurrent claims converged: winner=%s", winner)

	// The winner uninstalls bao: its (higher) claim dangles and must
	// stop counting — the loser's surviving claim wins on both readers,
	// with no un-claim write anywhere.
	winnerBase, loser := devA.base, selfB
	if winner == selfB {
		winnerBase, loser = devB.base, selfA
	}
	resp, raw := doRequest(t, http.MethodPut, winnerBase+"/v1/devices/me", `{"apps":{"bao":null}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /v1/devices/me (uninstall): status %d: %s", resp.StatusCode, raw)
	}
	if !pollUntil(3*time.Minute, func() bool {
		return devicesOn(t, devA.base).Active["bao"] == loser &&
			devicesOn(t, devB.base).Active["bao"] == loser
	}) {
		t.Fatalf("winner's uninstall never moved the role: A=%v B=%v",
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}

	// Prune devB's row from devA; the registry drops to one row on both
	// peers (sticky tombstone — devB stays unlisted even though its
	// server is still running).
	resp, raw = doRequest(t, http.MethodDelete, devA.base+"/v1/devices/"+selfB, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/devices/%s: status %d: %s", selfB, resp.StatusCode, raw)
	}
	if !pollUntil(3*time.Minute, func() bool {
		for _, dev := range devicesOn(t, devA.base).Devices {
			if dev.PeerId == selfB {
				return false
			}
		}
		for _, dev := range devicesOn(t, devB.base).Devices {
			if dev.PeerId == selfB {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("pruned device row still listed: A=%+v", devicesOn(t, devA.base).Devices)
	}

	// Error contract spot-checks.
	resp, _ = doRequest(t, http.MethodDelete, devA.base+"/v1/devices/nonexistent-peer", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE unknown device: status %d, want 404", resp.StatusCode)
	}
	resp, _ = doRequest(t, http.MethodPost, devA.base+"/v1/devices/activate", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("activate without app: status %d, want 400", resp.StatusCode)
	}
	resp, _ = doRequest(t, http.MethodPut, devA.base+"/v1/devices/me", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty self-update: status %d, want 400", resp.StatusCode)
	}
}
