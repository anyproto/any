package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// waitForAuthorized polls GET /v1/auth until an account is open. A
// login is not open until its tech space has synced once, so a peer
// restored from a phrase is usable only past this wait.
func waitForAuthorized(t *testing.T, base string, d time.Duration) api.AuthStatusResponse {
	t.Helper()
	var st api.AuthStatusResponse
	if !pollUntil(d, func() bool {
		st = api.AuthStatusResponse{}
		return tryJSON(t, http.MethodGet, base+"/v1/auth", "", &st) == http.StatusOK && st.Authorized
	}) {
		t.Fatalf("%s: the account never opened; last status %+v login %+v", base, st, st.Login)
	}
	return st
}

// loginStateOf reads the login state recorded under a peer's data dir,
// "" when the account has none.
func loginStateOf(t *testing.T, dataDir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dataDir, "*", "login.json"))
	if len(files) == 0 {
		return ""
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("login state %s: %v", raw, err)
	}
	return env.State
}

// noNetworkConfig is a server config with every node unreachable and
// no local-network discovery: nothing can ever sync.
func noNetworkConfig(t *testing.T) string {
	t.Helper()
	return peerConfigNoP2P(writeDeadNodeconf(t))
}

// startUnauthorized boots an unauthorized server on cfg.
func startUnauthorized(t *testing.T, bin, name, cfg string) *peer {
	t.Helper()
	p := &peer{name: name, addr: freeLoopbackAddr(t), dataDir: t.TempDir()}
	p.base = "http://" + p.addr
	t.Logf("peer %s: addr=%s data=%s", name, p.addr, p.dataDir)
	bootWithConfig(t, bin, p, cfg)
	return p
}

// expectLoginPending asserts p holds a login that waits for its first
// sync: the status says so, and guarded routes refuse.
func expectLoginPending(t *testing.T, p *peer, account, when string) {
	t.Helper()
	var st api.AuthStatusResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/auth", "", http.StatusOK, &st)
	if st.Authorized || st.Login == nil || st.Login.AccountId != account {
		t.Fatalf("%s: %s: auth status %+v login %+v, want a waiting login", p.name, when, st, st.Login)
	}
	var h api.HealthResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/health", "", http.StatusOK, &h)
	if h.Account != "" || h.Login == nil {
		t.Fatalf("%s: %s: health %+v, want a waiting login", p.name, when, h)
	}
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces", "")
	var env api.ErrorEnvelope
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusServiceUnavailable || env.Error.Code != "auth.login_pending" {
		t.Fatalf("%s: %s: GET /v1/spaces: %d %s, want 503 auth.login_pending", p.name, when, resp.StatusCode, raw)
	}
}

// TestE2E_LoginOfflineWaits runs the two account paths with no network
// at all. A created account is open at once and sets up its bao space
// with a bundle. A login with a phrase waits for the account's first
// sync, through a restart, and nothing is served meanwhile.
func TestE2E_LoginOfflineWaits(t *testing.T) {
	if testing.Short() {
		t.Skip("boots servers; rerun without -short")
	}
	bin := buildBinary(t)
	cfg := noNetworkConfig(t)

	// --- Create: usable offline. ---
	creator := startUnauthorized(t, bin, "creator", cfg)
	defer creator.stop(t)
	var created api.AuthResponse
	mustJSON(t, http.MethodPost, creator.base+"/v1/auth", `{}`, http.StatusOK, &created)
	if created.Mnemonic == "" || created.Login != nil {
		t.Fatalf("create: %+v", created)
	}
	if st := waitForAuthorized(t, creator.base, 5*time.Second); st.AccountId != created.AccountId {
		t.Fatalf("create: auth status %+v", st)
	}
	if got := loginStateOf(t, creator.dataDir); got != "" {
		t.Fatalf("a created account recorded login state %q", got)
	}
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, creator.base+"/v1/spaces", `{"name":"Bao","anySpaceType":"bao"}`, http.StatusCreated, &sp)
	if sp.Id == "" || sp.AnySpaceType != "bao" {
		t.Fatalf("create the bao space offline: %+v", sp)
	}
	resp, raw := doRequest(t, http.MethodPost, creator.base+"/v1/spaces/"+sp.Id+"/bundles",
		`{"id":"e2e-offline/v1","name":"Offline","rootType":"page"}`)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("install a bundle offline: %d %s", resp.StatusCode, raw)
	}
	var list api.BundleListResponse
	mustJSON(t, http.MethodGet, creator.base+"/v1/spaces/"+sp.Id+"/bundles", "", http.StatusOK, &list)
	if !list.Synced || len(list.Bundles) != 1 || list.Bundles[0].Id != "e2e-offline/v1" {
		t.Fatalf("bundles of a space created offline: %+v", list)
	}

	// --- Log in: waits. ---
	device := startUnauthorized(t, bin, "device", cfg)
	defer device.stop(t)
	var login api.AuthResponse
	mustJSON(t, http.MethodPost, device.base+"/v1/auth",
		fmt.Sprintf(`{"mnemonic":%q}`, created.Mnemonic), http.StatusOK, &login)
	if login.AccountId != created.AccountId || login.Login == nil {
		t.Fatalf("login with nothing to sync from must wait: %+v", login)
	}
	if got := loginStateOf(t, device.dataDir); got != "pending" {
		t.Fatalf("login state = %q, want pending", got)
	}
	expectLoginPending(t, device, created.AccountId, "after login")

	// The wallet is on disk now; a restart boots it and still waits.
	device.stop(t)
	bootWithConfig(t, bin, device, cfg)
	expectLoginPending(t, device, created.AccountId, "after restart")
}

// TestE2E_LoginOpensAfterFirstSync is the login path with a network.
// Device B logs in with A's phrase and opens once the account's tech
// space has synced; from then on it starts at once, with no network
// either. The space A created for the bao use is in B's list with that
// use. On the space it downloaded, a bundle read answers only as a
// definitive one, and B adopts A's install instead of making its own.
// (That the calls are refused before the registry arrives is pinned
// offline, in the server's TestBundles_RefusedOnDownloadedSpace.)
func TestE2E_LoginOpensAfterFirstSync(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test; rerun without -short")
	}
	bin := buildBinary(t)

	devA := startPeer(t, bin, "devA")
	defer devA.stop(t)
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces", `{"name":"login-e2e","anySpaceType":"bao"}`, http.StatusCreated, &sp)
	const install = `{"id":"e2e-login/v1","name":"Login","rootType":"page"}`
	var onA api.BundleEnsureResponse
	resp, raw := doRequest(t, http.MethodPost, devA.base+"/v1/spaces/"+sp.Id+"/bundles", install)
	if (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) || json.Unmarshal(raw, &onA) != nil || !onA.Installed {
		t.Fatalf("devA install: %d %s", resp.StatusCode, raw)
	}
	forceSync(t, sp.Id, devA)

	// startPeerWithMnemonic returns past the login's first sync.
	devB := startPeerWithMnemonic(t, bin, "devB", walletMnemonic(t, devA.dataDir))
	defer func() { devB.stop(t) }()
	if got := loginStateOf(t, devB.dataDir); got != "complete" {
		t.Fatalf("devB login state = %q, want complete", got)
	}

	// The space is in B's list with the use A created it for, whether
	// or not the space itself has loaded.
	var rows api.SpaceListResponse
	mustJSON(t, http.MethodGet, devB.base+"/v1/spaces", "", http.StatusOK, &rows)
	found := false
	for _, row := range rows.Spaces {
		if row.Id == sp.Id {
			found = row.AnySpaceType == "bao"
		}
	}
	if !found {
		t.Fatalf("devB does not find the bao space by its use after the login: %+v", rows.Spaces)
	}

	// The space arrives as a row; its registry never answers as a
	// provisional read. A registry that is here can still lack A's row
	// for a moment: B may sync from a node A's write has not reached.
	bundlesURL := devB.base + "/v1/spaces/" + sp.Id + "/bundles"
	var list api.BundleListResponse
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{devA}, func() bool {
		resp, raw := doRequest(t, http.MethodGet, bundlesURL, "")
		switch resp.StatusCode {
		case http.StatusOK:
			list = api.BundleListResponse{}
			if err := json.Unmarshal(raw, &list); err != nil || !list.Synced {
				t.Fatalf("devB bundles: a read that answers must be definitive: %v %s", err, raw)
			}
			return len(list.Bundles) == 1
		case http.StatusConflict, http.StatusNotFound:
			// 409 bundle.not_ready while the registry has not arrived;
			// 404 while the row itself has not.
			return false
		}
		t.Fatalf("devB bundles: %d %s", resp.StatusCode, raw)
		return false
	}) {
		t.Fatalf("devB never read the space's registry; last %+v", list)
	}

	// B adopts A's install: no second root.
	var onB api.BundleEnsureResponse
	if !pollUntil(2*time.Minute, func() bool {
		onB = api.BundleEnsureResponse{}
		return tryJSON(t, http.MethodPost, bundlesURL, install, &onB) == http.StatusOK
	}) {
		t.Fatal("devB ensure never answered 200")
	}
	if onB.Installed || onB.Bundle.RootId != onA.Bundle.RootId || len(onB.Bundle.Losers) != 0 {
		t.Fatalf("devB must adopt devA's root %s: %+v", onA.Bundle.RootId, onB)
	}

	// A completed login needs no network to start.
	restore := restartOffline(t, bin, devB)
	defer restore()
	if st := waitForAuthorized(t, devB.base, 30*time.Second); st.Login != nil {
		t.Fatalf("devB offline restart: %+v", st)
	}
	assertOffline(t, devB, sp.Id)
}
