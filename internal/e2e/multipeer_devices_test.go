// Multi-device devices-registry e2e: two `any` servers
// sharing ONE account (same mnemonic, distinct device keys) exercise
// the tech-space `devices` dataset end-to-end — boot self-registration
// converging across devices, CONCURRENT active claims resolving to the
// same winner on both readers (the core claim of the election design),
// one device handing the role to the other by peer id, the role
// following the target's installed app rather than the claimer's, and
// row pruning — including a pruned device unable to move the role.
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

// rowOn returns peer's row in base's registry.
func rowOn(t *testing.T, base, peer string) (api.DeviceInfo, bool) {
	t.Helper()
	for _, dev := range devicesOn(t, base).Devices {
		if dev.PeerId == peer {
			return dev, true
		}
	}
	return api.DeviceInfo{}, false
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
	// devB sends `"peerId": null`, which must be the same self claim.
	var wg sync.WaitGroup
	for base, body := range map[string]string{
		devA.base: `{"app":"bao"}`,
		devB.base: `{"app":"bao","peerId":null}`,
	} {
		wg.Add(1)
		go func(base, body string) {
			defer wg.Done()
			// Off the test goroutine: t.Fatalf would only Goexit this worker
			// and let the test run on half-failed — use tryRequest + Errorf.
			resp, raw, err := tryRequest(http.MethodPost, base+"/v1/devices/activate", body)
			if err != nil {
				t.Errorf("POST /v1/devices/activate on %s: %v", base, err)
				return
			}
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("POST /v1/devices/activate on %s: status %d: %s", base, resp.StatusCode, raw)
			}
		}(base, body)
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

	winnerBase, loserBase, loser := devA.base, devB.base, selfB
	if winner == selfB {
		winnerBase, loserBase, loser = devB.base, devA.base, selfA
	}
	claimOn := func(base, peer string) api.DeviceActiveClaim {
		dev, _ := rowOn(t, base, peer)
		return dev.ActiveClaims["bao"]
	}
	bothActive := func(want string) bool {
		return devicesOn(t, devA.base).Active["bao"] == want &&
			devicesOn(t, devB.base).Active["bao"] == want
	}
	// activate posts body and checks the status, the error code and
	// details.peerId (wantPeer; "" = absent).
	activate := func(base, body string, wantStatus int, wantCode, wantPeer string) {
		t.Helper()
		resp, raw := doRequest(t, http.MethodPost, base+"/v1/devices/activate", body)
		if resp.StatusCode != wantStatus {
			t.Errorf("activate %s on %s: status %d, want %d: %s", body, base, resp.StatusCode, wantStatus, raw)
			return
		}
		if wantCode == "" {
			return
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Errorf("activate %s on %s: decode error envelope: %v: %s", body, base, err, raw)
			return
		}
		if env.Error.Code != wantCode {
			t.Errorf("activate %s on %s: code %q, want %q", body, base, env.Error.Code, wantCode)
		}
		got, present := env.Error.Details["peerId"]
		if wantPeer == "" && present || wantPeer != "" && got != wantPeer {
			t.Errorf("activate %s on %s: details.peerId %v, want %q", body, base, got, wantPeer)
		}
	}

	hasAppOn := func(base, peer string) bool {
		dev, ok := rowOn(t, base, peer)
		_, has := dev.Apps["bao"]
		return ok && has
	}
	// setApp installs or uninstalls bao on base's own row and waits until
	// both readers see the change.
	setApp := func(base, peer string, installed bool) {
		t.Helper()
		body := `{"apps":{"bao":null}}`
		if installed {
			body = `{"apps":{"bao":{}}}`
		}
		mustStatus(t, http.MethodPut, base+"/v1/devices/me", body, http.StatusNoContent)
		if !pollUntil(3*time.Minute, func() bool {
			return hasAppOn(devA.base, peer) == installed && hasAppOn(devB.base, peer) == installed
		}) {
			t.Fatalf("bao installed=%v on %s never reached both readers", installed, peer)
		}
	}
	// handOff claims bao for target from base and waits until both
	// readers elect want.
	handOff := func(base, target, want string) {
		t.Helper()
		mustStatus(t, http.MethodPost, base+"/v1/devices/activate",
			`{"app":"bao","peerId":"`+target+`"}`, http.StatusNoContent)
		if !pollUntil(3*time.Minute, func() bool { return bothActive(want) }) {
			t.Fatalf("hand-off to %s never elected %s: A=%v B=%v", target, want,
				devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
		}
	}

	// Remote switch: the winner hands the role to the other device by
	// naming its peer id. The claim lands on the winner's own row with
	// the target; the target's row is never written.
	loserClaim := claimOn(winnerBase, loser)
	handOff(winnerBase, loser, loser)
	for _, base := range []string{devA.base, devB.base} {
		if got := claimOn(base, winner); got.Target != loser {
			t.Errorf("on %s the claimer's claim = %+v, want target %s", base, got, loser)
		}
		if got := claimOn(base, loser); got != loserClaim {
			t.Errorf("on %s the target's claim = %+v, want it untouched (%+v)", base, got, loserClaim)
		}
	}

	// The loser hands back, then the winner hands to it again: now the
	// loser's own claim names the winner, so the loser holds the role
	// only through the winner's claim. The winner uninstalls bao and the
	// role stays put — the claimer needs no app.
	handOff(loserBase, winner, winner)
	handOff(winnerBase, loser, loser)
	setApp(winnerBase, winner, false)
	if !bothActive(loser) {
		t.Errorf("claimer's uninstall moved the role: A=%v B=%v",
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}

	// Fallback: the winner reinstalls; then the target uninstalls, so the
	// best claim (winner → loser) dangles and the lower one (loser →
	// winner) decides.
	setApp(winnerBase, winner, true)
	if !bothActive(loser) {
		t.Errorf("claimer's reinstall moved the role: A=%v B=%v",
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}
	setApp(loserBase, loser, false)
	if !pollUntil(3*time.Minute, func() bool { return bothActive(winner) }) {
		t.Fatalf("a dangling best claim never fell back to %s: A=%v B=%v", winner,
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}

	activate(winnerBase, `{"app":"notinstalled","peerId":"`+loser+`"}`, http.StatusConflict, "device.app_not_installed", loser)
	activate(winnerBase, `{"app":"bao","peerId":"`+loser+`"}`, http.StatusConflict, "device.app_not_installed", loser)
	activate(loserBase, `{"app":"bao","peerId":"`+loser+`"}`, http.StatusConflict, "device.app_not_installed", loser)
	activate(winnerBase, `{"app":"bao","peerId":"nonexistent-peer"}`, http.StatusNotFound, "device.not_found", "nonexistent-peer")
	activate(winnerBase, `{"app":"bao","peerId":""}`, http.StatusBadRequest, "request.invalid_field", "")

	// The winner uninstalls too: no claim names a device that has bao, so
	// no device is active — with no un-claim write anywhere.
	setApp(winnerBase, winner, false)
	if !pollUntil(3*time.Minute, func() bool { return bothActive("") }) {
		t.Fatalf("uninstalling everywhere left a winner: A=%v B=%v",
			devicesOn(t, devA.base).Active, devicesOn(t, devB.base).Active)
	}

	// devA carries bao again so a claim for it passes the write-time
	// checks below.
	setApp(devA.base, selfA, true)

	// Prune devB's row from devA; the registry drops to one row on both
	// peers (sticky tombstone — devB stays unlisted even though its
	// server is still running).
	mustStatus(t, http.MethodDelete, devA.base+"/v1/devices/"+selfB, "", http.StatusNoContent)
	if !pollUntil(3*time.Minute, func() bool {
		_, onA := rowOn(t, devA.base, selfB)
		_, onB := rowOn(t, devB.base, selfB)
		return !onA && !onB
	}) {
		t.Fatalf("pruned device row still listed: A=%+v", devicesOn(t, devA.base).Devices)
	}

	// Error contract spot-checks.
	resp, _ := doRequest(t, http.MethodDelete, devA.base+"/v1/devices/nonexistent-peer", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE unknown device: status %d, want 404", resp.StatusCode)
	}
	activate(devA.base, `{"app":"bao","peerId":"`+selfB+`"}`, http.StatusNotFound, "device.not_found", selfB)
	// A pruned device can't move the role, and learns it was pruned
	// whatever it names; the error is about this device, not the target.
	activate(devB.base, `{"app":"bao","peerId":"`+selfA+`"}`, http.StatusConflict, "device.pruned", "")
	activate(devB.base, `{"app":"notinstalled","peerId":"`+selfA+`"}`, http.StatusConflict, "device.pruned", "")
	activate(devB.base, `{"app":"bao"}`, http.StatusConflict, "device.pruned", "")
	activate(devA.base, `{}`, http.StatusBadRequest, "request.missing_field", "")
	resp, _ = doRequest(t, http.MethodPut, devA.base+"/v1/devices/me", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty self-update: status %d, want 400", resp.StatusCode)
	}
}
