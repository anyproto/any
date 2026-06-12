// Multi-device tech-space diagnostic: two `any` servers sharing ONE
// account (same mnemonic, DISTINCT device keys), so both replicate the
// same tech space. Measures how fast tech-space operations done on
// device A become visible on device B WITHOUT any forced sync — space
// create, metadata patch, delete, and a joined space appearing.
// Stream-delivered updates land in ~1-3s; ~30s+ means only the periodic
// headsync (diff) timer is converging the tech space.
//
// IMPORTANT: the second device gets the same mnemonic but a FRESH
// device key (see sameAccountWallet). Copying wallet.key verbatim
// clones the device key too, which makes both servers present the same
// network peerId — any-sync nodes then key streams/subscriptions per
// peer, the two devices fight over one identity, and pushed HeadUpdates
// reach only one of them (the other converges via the ~30s diff timer
// only). `any` currently has no user-facing restore flow that does this
// correctly (`any init` can't take an existing mnemonic), so a verbatim
// wallet.key copy is exactly what a real user would do today — see
// docs/07-roadmap.md.
package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// sameAccountWallet reads a plain (unencrypted) wallet.key, keeps the
// mnemonic — same account — and swaps in a freshly generated device
// key, mirroring what a proper second-device restore flow would do.
// The envelope/payload shapes mirror any-sync-sdk/auth/file.go.
func sameAccountWallet(t *testing.T, walletSrc string) []byte {
	t.Helper()
	raw, err := os.ReadFile(walletSrc)
	if err != nil {
		t.Fatalf("read wallet %s: %v", walletSrc, err)
	}
	var env struct {
		Version int             `json:"version"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse wallet envelope: %v", err)
	}
	if len(env.Payload) == 0 {
		t.Fatalf("wallet %s is encrypted; test needs a plain wallet", walletSrc)
	}
	var p struct {
		Mnemonic  string `json:"mnemonic"`
		DeviceKey string `json:"deviceKey"`
		Index     uint32 `json:"index,omitempty"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("parse wallet payload: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	p.DeviceKey = base64.StdEncoding.EncodeToString(priv)
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(struct {
		Version int             `json:"version"`
		Payload json.RawMessage `json:"payload"`
	}{Version: env.Version, Payload: body})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// startPeerWithWallet is startPeer booting as the same ACCOUNT as the
// wallet at walletSrc but as a distinct DEVICE (fresh device key).
func startPeerWithWallet(t *testing.T, bin, name, walletSrc string) *peer {
	t.Helper()
	dataDir := t.TempDir()
	wallet := sameAccountWallet(t, walletSrc)
	if err := os.WriteFile(filepath.Join(dataDir, "wallet.key"), wallet, 0o600); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	addr := freeLoopbackAddr(t)
	t.Logf("peer %s: addr=%s data=%s (account from %s, fresh device key)", name, addr, dataDir, walletSrc)
	srv := startServer(t, bin, addr, dataDir)
	waitForReady(t, addr, 60*time.Second)
	return &peer{name: name, addr: addr, dataDir: dataDir, base: "http://" + addr, srv: srv}
}

// spaceRow returns the row for id from base's GET /v1/spaces, or nil.
func spaceRow(t *testing.T, base, id string) map[string]any {
	t.Helper()
	var list struct {
		Spaces []map[string]any `json:"spaces"`
	}
	resp, raw := doRequest(t, http.MethodGet, base+"/v1/spaces", "")
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

	devB := startPeerWithWallet(t, bin, "devB", filepath.Join(devA.dataDir, "wallet.key"))
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
