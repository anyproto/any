package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// otherNetwork names a network no test account is pinned to. Nothing
// boots on it: every use expects the pin to refuse first.
var otherNetwork = config.Network{Nodeconf: "networkId: Nother"}

// placeholderNetwork is the bootable-but-offline network and its id.
func placeholderNetwork(t *testing.T) (config.Network, string) {
	t.Helper()
	raw := config.NodeconfPlaceholder()
	id, err := config.NodeconfNetworkId(raw)
	if err != nil {
		t.Fatal(err)
	}
	return config.Network{Nodeconf: string(raw)}, id
}

// expectMismatchDetails asserts the ids a 409 auth.network_mismatch names.
func expectMismatchDetails(t *testing.T, body []byte, pinned, configured string) {
	t.Helper()
	var env struct {
		Error struct {
			Details struct{ Pinned, Configured string }
		}
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Details.Pinned != pinned || env.Error.Details.Configured != configured {
		t.Fatalf("mismatch details %+v, want pinned %s configured %s", env.Error.Details, pinned, configured)
	}
}

func TestNetworkPin(t *testing.T) {
	dir := t.TempDir()
	if pinned, err := checkNetworkPin(dir, "N1"); pinned || err != nil {
		t.Fatalf("unpinned dir: pinned=%v err=%v", pinned, err)
	}
	if err := writeNetworkPin(dir, "N1"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(networkPinPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("pin mode %v, want 0600", st.Mode().Perm())
	}
	if pinned, err := checkNetworkPin(dir, "N1"); !pinned || err != nil {
		t.Fatalf("same network: pinned=%v err=%v", pinned, err)
	}
	_, err = checkNetworkPin(dir, "N2")
	var mismatch *ErrNetworkMismatch
	if !errors.As(err, &mismatch) || mismatch.Pinned != "N1" || mismatch.Configured != "N2" {
		t.Fatalf("other network: %v", err)
	}

	// A later envelope version keeps networkId and stays readable.
	if err := os.WriteFile(networkPinPath(dir), []byte(`{"version":2,"networkId":"N1","since":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if pinned, err := checkNetworkPin(dir, "N1"); !pinned || err != nil {
		t.Fatalf("later version: pinned=%v err=%v", pinned, err)
	}

	for _, body := range []string{"{", `{"version":0,"networkId":"N1"}`, `{"version":1}`} {
		if err := os.WriteFile(networkPinPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := checkNetworkPin(dir, "N1"); !errors.Is(err, errNetworkPinCorrupt) {
			t.Errorf("pin %q: err %v, want errNetworkPinCorrupt", body, err)
		}
	}
}

// TestAuth_NetworkPin: the first boot pins the account to its network;
// a boot under another network is refused without touching the dir; an
// unreadable pin is refused with its own code and never rewritten;
// removing the pin re-pins to the network the next boot runs on.
func TestAuth_NetworkPin(t *testing.T) {
	network, networkId := placeholderNetwork(t)
	d := newUnauthorizedDeps(t)
	d.cfg.Network = network
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	dir := config.AccountDir(d.root, resp.AccountId)
	if pinned, err := checkNetworkPin(dir, networkId); !pinned || err != nil {
		t.Fatalf("first boot must pin %s: pinned=%v err=%v", networkId, pinned, err)
	}
	pin, err := os.ReadFile(networkPinPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)

	// The lock rewrites server.pid on every acquire; a refusal must not
	// get that far.
	pidPath := filepath.Join(dir, "server.pid")
	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}
	selectAccount := `{"accountId":"` + resp.AccountId + `"}`
	d.cfg.Network = otherNetwork
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", selectAccount)
	expectAuthError(t, rec, http.StatusConflict, "auth.network_mismatch", "")
	expectMismatchDetails(t, rec.Body.Bytes(), networkId, "Nother")
	if d.accountID() != "" {
		t.Fatal("a refused network must leave the server unauthorized")
	}
	if after, err := os.ReadFile(networkPinPath(dir)); err != nil || string(after) != string(pin) {
		t.Fatalf("refusal touched the pin: %q, %v", after, err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("refusal took the account lock (server.pid stat err %v)", err)
	}

	if err := os.WriteFile(networkPinPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.cfg.Network = network
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", selectAccount)
	expectAuthError(t, rec, http.StatusInternalServerError, "auth.network_pin_corrupt", "")
	if after, err := os.ReadFile(networkPinPath(dir)); err != nil || string(after) != "{" {
		t.Fatalf("an unreadable pin must never be rewritten: %q, %v", after, err)
	}

	if err := os.Remove(networkPinPath(dir)); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", selectAccount)
	if rec.Code != http.StatusOK {
		t.Fatalf("boot of an unpinned dir: %d %s", rec.Code, rec.Body.String())
	}
	if pinned, err := checkNetworkPin(dir, networkId); !pinned || err != nil {
		t.Fatalf("unpinned dir must adopt %s: pinned=%v err=%v", networkId, pinned, err)
	}
}

// TestAuth_NetworkPinSwitch: a managed replace to an account pinned to
// another network is refused while the running account stays up, and
// the target's dir is left alone.
func TestAuth_NetworkPinSwitch(t *testing.T) {
	network, _ := placeholderNetwork(t)
	d := newUnauthorizedDeps(t)
	d.cfg.Network = network
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	e := buildEcho(d)
	tok := map[string]string{api.ControlTokenHeader: "tok"}

	rec := doJSONH(t, e, http.MethodPost, "/v1/auth", `{}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("managed generate: %d %s", rec.Code, rec.Body.String())
	}
	var first api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	mnemonic, other := otherMnemonic(t)
	otherDir := config.AccountDir(d.root, other)
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeNetworkPin(otherDir, "Nother"); err != nil {
		t.Fatal(err)
	}
	rec = doJSONH(t, e, http.MethodPost, "/v1/auth", `{"mnemonic":`+strconv.Quote(mnemonic)+`,"replace":true}`, tok)
	expectAuthError(t, rec, http.StatusConflict, "auth.network_mismatch", other)
	if got := d.accountID(); got != first.AccountId {
		t.Fatalf("running account after a refused switch = %q, want %q", got, first.AccountId)
	}
	if _, err := os.Stat(deviceKeyPath(otherDir)); !os.IsNotExist(err) {
		t.Fatalf("refused switch minted the target's device key (stat err %v)", err)
	}
}

// TestRun_NetworkPin: `any run` refuses to start an account pinned to
// another network and names both ids.
func TestRun_NetworkPin(t *testing.T) {
	network, networkId := placeholderNetwork(t)
	d := newUnauthorizedDeps(t)
	d.cfg.Network = network
	e := buildEcho(d)
	if rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)

	cfg := d.cfg
	cfg.Network = otherNetwork
	cfg.Listen.Addr = "127.0.0.1:0"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := Run(ctx, cfg)
	var mismatch *ErrNetworkMismatch
	if !errors.As(err, &mismatch) || mismatch.Pinned != networkId || mismatch.Configured != "Nother" {
		t.Fatalf("Run on another network: %v", err)
	}
	if !strings.Contains(err.Error(), networkId) || !strings.Contains(err.Error(), "Nother") {
		t.Fatalf("startup error must name both networks: %v", err)
	}
}
