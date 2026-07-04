package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerP2P proves the local-network path at the app level:
// two `any` servers on this host discover each other over REAL mDNS
// (the SDK's dnssd driver on the machine's LAN interface), handshake,
// and report the shared space as p2p-connected in both the debug
// surface (/v1/debug/p2p) and sync status (p2p / localPeers).
//
// Gated behind ANY_E2E_P2P=1: it requires a multicast-capable network
// interface (CI loopback won't do) and announces the instances on the
// developer's actual LAN. The space handshake itself still runs
// through staging (invite/join), so the staging fixture is required
// too — the SDK-level e2e covers the fully-offline variant.
func TestE2E_MultipeerP2P(t *testing.T) {
	if os.Getenv("ANY_E2E_P2P") == "" {
		t.Skip("set ANY_E2E_P2P=1 to run the real-mDNS multipeer test")
	}
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

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"lan-shared"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Discovery: each server must see the other as a connected LAN
	// peer that shares the space. The joiner advertises the space via
	// its post-pull re-handshake; the 60s discovery resweep is the
	// slowest fallback path, so allow a bit more than that.
	p2pSees := func(base, spaceId string) bool {
		var st api.P2PStatusResponse
		mustJSON(t, http.MethodGet, base+"/v1/debug/p2p", "", http.StatusOK, &st)
		for _, p := range st.Peers {
			if !p.Connected {
				continue
			}
			for _, id := range p.SpaceIds {
				if id == spaceId {
					return true
				}
			}
		}
		return false
	}
	if !pollUntil(90*time.Second, func() bool {
		return p2pSees(owner.base, sp.Id) && p2pSees(joiner.base, sp.Id)
	}) {
		var so, sj api.P2PStatusResponse
		mustJSON(t, http.MethodGet, owner.base+"/v1/debug/p2p", "", http.StatusOK, &so)
		mustJSON(t, http.MethodGet, joiner.base+"/v1/debug/p2p", "", http.StatusOK, &sj)
		t.Fatalf("peers never saw each other with the shared space over p2p\n owner=%+v\n joiner=%+v", so, sj)
	}

	// Sync status carries the p2p slice on both sides.
	for _, p := range []*peer{owner, joiner} {
		var st api.SpaceSyncStatusResponse
		if !pollUntil(30*time.Second, func() bool {
			mustJSON(t, http.MethodGet, p.base+"/v1/spaces/"+sp.Id+"/sync-status",
				"", http.StatusOK, &st)
			return st.P2P == "connected" && st.LocalPeers >= 1
		}) {
			t.Fatalf("peer %s: sync-status never showed p2p connected; last=%+v", p.name, st)
		}
	}
}

// deadNodeconf is an unreachable any-sync network (loopback ports
// nobody listens on) — the offline fixture. Same shape as the SDK's
// e2e/local.yml.
const deadNodeconf = `id: 64384a038e697b7fce2f447e
networkId: N4N1wDHFpFpovXBqdbq2TDXE9tXdXbtV1eTJFpKJW4YeaJqR
nodes:
  - peerId: 12D3KooWKLCajM89S8unbt3tgGbRLgmiWnFZT3adn9A5pQciBSLa
    addresses:
      - "127.0.0.1:4830"
    types:
      - coordinator
  - peerId: 12D3KooWKnXTtbveMDUFfeSqR5dt9a4JW66tZQXG7C7PdDh3vqGu
    addresses:
      - 127.0.0.1:4430
    types:
      - tree
`

// startPeerOffline boots an UNAUTHORIZED `any` server against the dead
// nodeconf, then onboards it via POST /v1/auth with the given mnemonic
// (the auth path always generates a FRESH device key, so several
// devices of one account get distinct peer ids).
//
// Deliberately does NOT go through startServerInit — that helper
// overwrites config.yaml with the staging nodeconf, and this test must
// stay genuinely offline.
func startPeerOffline(t *testing.T, bin, name, nodeconfPath, mnemonic string) *peer {
	t.Helper()
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	cfgPath := filepath.Join(dataDir, "config.yaml")
	cfgBody := fmt.Sprintf("network:\n  nodeconfPath: %s\nindex:\n  embedder: none\n", nodeconfPath)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Logf("peer %s: addr=%s data=%s (offline)", name, addr, dataDir)
	srv := execServer(t, bin, cfgPath, dataDir, addr)
	waitForReady(t, addr, 60*time.Second)

	body, _ := json.Marshal(api.AuthRequest{Mnemonic: mnemonic})
	var resp api.AuthResponse
	mustJSON(t, http.MethodPost, "http://"+addr+"/v1/auth", string(body), http.StatusOK, &resp)
	if resp.AccountId == "" {
		t.Fatalf("peer %s: auth returned empty accountId", name)
	}
	return &peer{name: name, addr: addr, dataDir: dataDir, base: "http://" + addr, srv: srv}
}

// TestE2E_MultipeerP2PColdRestore proves account restore over pure
// LAN: device A onboards from a mnemonic OFFLINE (dead nodeconf),
// creates a space; then device B — empty data dir, same mnemonic —
// onboards and must converge the space list and content from A alone,
// over real mDNS discovery, with no coordinator or sync node ever
// reachable. Gated like TestE2E_MultipeerP2P (needs a multicast-capable
// interface); no staging fixture required.
func TestE2E_MultipeerP2PColdRestore(t *testing.T) {
	if os.Getenv("ANY_E2E_P2P") == "" {
		t.Skip("set ANY_E2E_P2P=1 to run the real-mDNS multipeer test")
	}
	if testing.Short() {
		t.Skip("multipeer test takes ~60s; rerun without -short")
	}

	nodeconfPath := filepath.Join(t.TempDir(), "dead-nodeconf.yml")
	if err := os.WriteFile(nodeconfPath, []byte(deadNodeconf), 0o600); err != nil {
		t.Fatal(err)
	}
	mnemonic, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}

	bin := buildBinary(t)
	devA := startPeerOffline(t, bin, "devA", nodeconfPath, mnemonic)
	defer devA.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces",
		`{"name":"cold-restore"}`, http.StatusCreated, &sp)

	// Device B: brand-new device of the same account, fully offline.
	devB := startPeerOffline(t, bin, "devB", nodeconfPath, mnemonic)
	defer devB.stop(t)

	// Distinct peer ids are a hard requirement — p2p filters "self" by
	// peerId, so devices sharing one can never pair.
	var pa, pb api.P2PStatusResponse
	mustJSON(t, http.MethodGet, devA.base+"/v1/debug/p2p", "", http.StatusOK, &pa)
	mustJSON(t, http.MethodGet, devB.base+"/v1/debug/p2p", "", http.StatusOK, &pb)
	if pa.PeerId == "" || pa.PeerId == pb.PeerId {
		t.Fatalf("devices must have distinct non-empty peer ids: A=%q B=%q", pa.PeerId, pb.PeerId)
	}

	// Cold restore: B must learn the space over the LAN alone.
	if !pollUntil(120*time.Second, func() bool {
		var list api.SpaceListResponse
		mustJSON(t, http.MethodGet, devB.base+"/v1/spaces", "", http.StatusOK, &list)
		for _, s := range list.Spaces {
			if s.Id == sp.Id {
				return true
			}
		}
		return false
	}) {
		mustJSON(t, http.MethodGet, devB.base+"/v1/debug/p2p", "", http.StatusOK, &pb)
		t.Fatalf("device B never cold-restored the space over p2p; p2p=%+v", pb)
	}

	// And the restored space must report the p2p connection.
	var st api.SpaceSyncStatusResponse
	if !pollUntil(30*time.Second, func() bool {
		mustJSON(t, http.MethodGet, devB.base+"/v1/spaces/"+sp.Id+"/sync-status",
			"", http.StatusOK, &st)
		return st.P2P == "connected" && st.LocalPeers >= 1 && st.NetworkPeers == 0
	}) {
		t.Fatalf("device B: sync-status never showed offline p2p convergence; last=%+v", st)
	}
}
