// Multi-device tech-space diagnostic: two `any` servers sharing ONE
// account (same mnemonic, DISTINCT device keys), so both replicate the
// same tech space. Measures how fast tech-space operations done on
// device A become visible on device B WITHOUT any forced sync — space
// create, metadata patch, delete, and a joined space appearing.
// Stream-delivered updates land in ~1-3s; ~30s+ means only the periodic
// headsync (diff) timer is converging the tech space.
//
// IMPORTANT: the second device gets the same mnemonic but a FRESH
// device key — `any init --mnemonic` (startPeerWithMnemonic) is that
// restore flow. Copying wallet.key verbatim clones the device key too,
// which makes both servers present the same network peerId — any-sync
// nodes then key streams/subscriptions per peer, the two devices fight
// over one identity, and pushed HeadUpdates reach only one of them
// (the other converges via the ~30s diff timer only).
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// walletMnemonic extracts the BIP-39 phrase from a peer's plain wallet
// — the per-account dir (<dataDir>/<id>/wallet.key) or the legacy root
// wallet.key. The envelope/payload shapes mirror
// any-sync-sdk/auth/file.go.
func walletMnemonic(t *testing.T, dataDir string) string {
	t.Helper()
	nested, _ := filepath.Glob(filepath.Join(dataDir, "*", "wallet.key"))
	for _, path := range append(nested, filepath.Join(dataDir, "wallet.key")) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var env struct {
			Payload json.RawMessage `json:"payload,omitempty"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || len(env.Payload) == 0 {
			t.Fatalf("wallet %s is encrypted or malformed; test needs a plain wallet", path)
		}
		var p struct {
			Mnemonic string `json:"mnemonic"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.Mnemonic == "" {
			t.Fatalf("wallet %s has no mnemonic", path)
		}
		return p.Mnemonic
	}
	t.Fatalf("no wallet found under %s", dataDir)
	return ""
}

// startPeerWithMnemonic is startPeer authorizing as the same ACCOUNT
// via `any init --mnemonic` — the real second-device restore flow:
// same phrase derives the same account, the device key is freshly
// generated.
func startPeerWithMnemonic(t *testing.T, bin, name, mnemonic string) *peer {
	t.Helper()
	dataDir := t.TempDir()
	initCmd := exec.Command(bin, "init", "--data-dir", dataDir, "--mnemonic", mnemonic)
	initCmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("any init --mnemonic: %v\n%s", err, out)
	}
	addr := freeLoopbackAddr(t)
	t.Logf("peer %s: addr=%s data=%s (same account, fresh device key)", name, addr, dataDir)
	srv := startServer(t, bin, addr, dataDir)
	waitForReady(t, addr, 60*time.Second)
	return &peer{name: name, addr: addr, dataDir: dataDir, base: "http://" + addr, srv: srv}
}

// spaceRow returns the row for id from base's GET /v1/spaces, or nil.
// Asks for all statuses — the default list hides non-active rows
// (temporary workaround in listSpaces), and the delete step polls for
// status=deleted.
func spaceRow(t *testing.T, base, id string) map[string]any {
	t.Helper()
	var list struct {
		Spaces []map[string]any `json:"spaces"`
	}
	resp, raw := doRequest(t, http.MethodGet, base+"/v1/spaces?status=all", "")
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode space list: %v", err)
	}
	for _, row := range list.Spaces {
		if row["id"] == id {
			return row
		}
	}
	return nil
}

func TestE2E_MultideviceTechSpaceRealtime(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multidevice test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	devA := startPeer(t, bin, "devA")
	defer devA.stop(t)

	// Space created BEFORE device B exists — covers initial tech-space
	// ingestion on a fresh device (setup, generous budget, not a
	// realtime verdict).
	var pre api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces",
		`{"name":"pre-existing"}`, http.StatusCreated, &pre)

	devB := startPeerWithMnemonic(t, bin, "devB", walletMnemonic(t, devA.dataDir))
	defer devB.stop(t)

	// Sanity: same account on both devices.
	var accA, accB map[string]any
	mustJSON(t, http.MethodGet, devA.base+"/v1/account", "", http.StatusOK, &accA)
	mustJSON(t, http.MethodGet, devB.base+"/v1/account", "", http.StatusOK, &accB)
	if accA["id"] != accB["id"] || accA["id"] == "" {
		t.Fatalf("devices have different accounts: A=%v B=%v", accA["id"], accB["id"])
	}

	bootStart := time.Now()
	if !pollUntil(3*time.Minute, func() bool {
		return spaceRow(t, devB.base, pre.Id) != nil
	}) {
		t.Fatalf("setup: devB never ingested pre-existing space %s", pre.Id)
	}
	t.Logf("initial tech-space ingestion on devB: %v after ready", time.Since(bootStart).Round(time.Millisecond))

	type sample struct {
		label   string
		latency time.Duration
		ok      bool
	}
	var samples []sample
	record := func(label string, d time.Duration, ok bool) {
		samples = append(samples, sample{label, d, ok})
		t.Logf("techspace %-28s latency=%v converged=%v", label, d.Round(time.Millisecond), ok)
	}

	// 1. Space created on A → appears on B.
	var s2 api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces",
		`{"name":"created-on-A"}`, http.StatusCreated, &s2)
	d, ok := measureConvergence(realtimeBudget, func() bool {
		return spaceRow(t, devB.base, s2.Id) != nil
	})
	record("create A→B", d, ok)

	// 2. Rename on A → B sees new name. (spaceIndex write + per-device
	// mirror back into each tech-space row.)
	mustStatus(t, http.MethodPatch, devA.base+"/v1/spaces/"+s2.Id,
		`{"name":"renamed-on-A"}`, http.StatusNoContent)
	d, ok = measureConvergence(realtimeBudget, func() bool {
		row := spaceRow(t, devB.base, s2.Id)
		return row != nil && row["name"] == "renamed-on-A"
	})
	record("rename A→B", d, ok)

	// 3. Space created on B → appears on A (reverse direction).
	var s3 api.SpaceInfo
	mustJSON(t, http.MethodPost, devB.base+"/v1/spaces",
		`{"name":"created-on-B"}`, http.StatusCreated, &s3)
	d, ok = measureConvergence(realtimeBudget, func() bool {
		return spaceRow(t, devA.base, s3.Id) != nil
	})
	record("create B→A", d, ok)

	// 4. Delete on A → B sees status=deleted.
	mustStatus(t, http.MethodDelete, devA.base+"/v1/spaces/"+s2.Id, "", http.StatusNoContent)
	d, ok = measureConvergence(realtimeBudget, func() bool {
		row := spaceRow(t, devB.base, s2.Id)
		return row != nil && row["status"] == "deleted"
	})
	record("delete A→B", d, ok)

	// 5. Join visibility: a different account's owner invites this
	// account; device A joins → the joined space must appear in device
	// B's list. Measured from the moment A's membership is active.
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	var shared api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"shared-join"}`, http.StatusCreated, &shared)
	joinSpace(t, owner, devA, shared.Id, api.SpacePermissionWriter)
	d, ok = measureConvergence(realtimeBudget, func() bool {
		return spaceRow(t, devB.base, shared.Id) != nil
	})
	record("join visible A→B", d, ok)

	var slow, failed int
	for _, s := range samples {
		if !s.ok {
			failed++
		} else if s.latency > streamLatencyMax {
			slow++
		}
	}
	if failed > 0 {
		t.Fatalf("%d/%d tech-space ops never converged within %v", failed, len(samples), realtimeBudget)
	}
	if slow > 0 {
		t.Fatalf("%d/%d tech-space ops took >%v to propagate — tech-space realtime (stream) sync is not delivering; convergence relies on the periodic headsync timer", slow, len(samples), streamLatencyMax)
	}
}
