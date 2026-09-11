package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

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

	for _, body := range []string{"{", `{"version":2,"networkId":"N1"}`, `{"version":1}`} {
		if err := os.WriteFile(networkPinPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := checkNetworkPin(dir, "N1"); !errors.Is(err, errNetworkPinCorrupt) {
			t.Errorf("pin %q: err %v, want errNetworkPinCorrupt", body, err)
		}
	}
}

// TestAuth_NetworkPin: the first boot pins the account to its network;
// a boot under another network is refused before anything opens and
// leaves the dir as it was; a dir without a pin adopts the network it
// boots with.
func TestAuth_NetworkPin(t *testing.T) {
	placeholder := string(config.NodeconfPlaceholder())
	networkId, err := config.NetworkId([]byte(placeholder))
	if err != nil {
		t.Fatal(err)
	}
	d := newUnauthorizedDeps(t)
	d.cfg.Network = config.Network{Nodeconf: placeholder}
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

	selectAccount := `{"accountId":"` + resp.AccountId + `"}`
	d.cfg.Network = config.Network{Nodeconf: "networkId: Nother"}
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", selectAccount)
	expectAuthError(t, rec, http.StatusConflict, "auth.network_mismatch", "")
	var env struct {
		Error struct {
			Details struct{ Pinned, Configured string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Details.Pinned != networkId || env.Error.Details.Configured != "Nother" {
		t.Fatalf("mismatch details: %+v", env.Error.Details)
	}
	if d.accountID() != "" {
		t.Fatal("a refused network must leave the server unauthorized")
	}
	if after, err := os.ReadFile(networkPinPath(dir)); err != nil || string(after) != string(pin) {
		t.Fatalf("refusal touched the pin: %q, %v", after, err)
	}
	if _, err := os.Stat(config.WalletPath(config.Config{}, dir)); err != nil {
		t.Fatalf("refusal touched the wallet: %v", err)
	}

	// An account dir from before pins adopts the network it boots with.
	if err := os.Remove(networkPinPath(dir)); err != nil {
		t.Fatal(err)
	}
	d.cfg.Network = config.Network{Nodeconf: placeholder}
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", selectAccount)
	if rec.Code != http.StatusOK {
		t.Fatalf("boot of an unpinned dir: %d %s", rec.Code, rec.Body.String())
	}
	if pinned, err := checkNetworkPin(dir, networkId); !pinned || err != nil {
		t.Fatalf("unpinned dir must adopt %s: pinned=%v err=%v", networkId, pinned, err)
	}
}
