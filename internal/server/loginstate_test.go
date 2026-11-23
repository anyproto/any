package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/labstack/echo/v4"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// offlineDeps is newUnauthorizedDeps on the placeholder network: it
// boots and serves, and nothing ever syncs.
func offlineDeps(t *testing.T) (*deps, *echo.Echo) {
	t.Helper()
	d := newUnauthorizedDeps(t)
	network, _ := placeholderNetwork(t)
	d.cfg.Network = network
	return d, buildEcho(d)
}

func authStatusOf(t *testing.T, e http.Handler) api.AuthStatusResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/auth", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var st api.AuthStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func postAuth(t *testing.T, e http.Handler, body string) api.AuthResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/auth", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// waitAuthorized polls GET /v1/auth until the account is open.
func waitAuthorized(t *testing.T, e http.Handler, account string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		st := authStatusOf(t, e)
		if st.Authorized && st.AccountId == account && st.Login == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("account %s never opened: %+v", account, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readLoginState(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(loginStatePath(dir))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var env loginStateEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("login state: %v (%s)", err, raw)
	}
	return env.State
}

// A created account is open at once with no network: nothing waits, no
// login state is written, and a derived space, its bundles registry and
// an install all work.
func TestLogin_CreatedAccountOpensOffline(t *testing.T) {
	d, e := offlineDeps(t)

	created := postAuth(t, e, `{}`)
	if created.Mnemonic == "" || !created.Created || created.Login != nil {
		t.Fatalf("generate reply: %+v", created)
	}
	st := authStatusOf(t, e)
	if !st.Authorized || st.AccountId != created.AccountId || st.Login != nil {
		t.Fatalf("auth status: %+v", st)
	}
	if got := readLoginState(t, config.AccountDir(d.root, created.AccountId)); got != "" {
		t.Fatalf("a created account wrote login state %q", got)
	}

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("derive bao offline: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil || sp.Id == "" {
		t.Fatalf("derive bao: %v %s", err, rec.Body.String())
	}

	// The registry of a space created here is here: the read answers,
	// and answers definitively.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/bundles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list bundles offline: %d %s", rec.Code, rec.Body.String())
	}
	var list api.BundleListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || !list.Synced || len(list.Bundles) != 0 {
		t.Fatalf("list bundles offline: %v %s", err, rec.Body.String())
	}

	// Both root strategies install offline.
	for _, body := range []string{
		`{"id":"offline-created/v1","name":"Created","rootType":"page"}`,
		`{"id":"offline-derived/v1","name":"Derived","rootType":"page","derived":true}`,
	} {
		rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", body)
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("install %s offline: %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// The tech space's registry is the account's own.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+d.sdk.TechSpaceId()+"/bundles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list tech bundles offline: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || !list.Synced {
		t.Fatalf("list tech bundles offline: %v %s", err, rec.Body.String())
	}
}

// Logging in to an existing account keeps the server closed until the
// tech space has synced once: the reply and the status say so, guarded
// routes refuse, the state survives a restart, and the first sync opens
// the account for good.
func TestLogin_WaitsForFirstSync(t *testing.T) {
	phrase, account := otherMnemonic(t)
	d, e := offlineDeps(t)
	synced := make(chan struct{})
	d.firstSync = func(ctx context.Context, _ *anysyncsdk.SDK) error {
		select {
		case <-synced:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	body := `{"mnemonic":` + strconv.Quote(phrase) + `}`
	dir := config.AccountDir(d.root, account)

	login := postAuth(t, e, body)
	if login.AccountId != account || !login.Created || login.Login == nil || login.Login.AccountId != account {
		t.Fatalf("login reply: %+v", login)
	}
	if got := readLoginState(t, dir); got != loginStatePending {
		t.Fatalf("login state = %q, want pending", got)
	}
	expectPending := func(when string) {
		t.Helper()
		st := authStatusOf(t, e)
		if st.Authorized || st.AccountId != "" || st.Login == nil || st.Login.AccountId != account {
			t.Fatalf("%s: auth status: %+v", when, st)
		}
		rec := doJSON(t, e, http.MethodGet, "/v1/health", "")
		var h api.HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if h.Account != "" || h.Login == nil || h.Login.AccountId != account {
			t.Fatalf("%s: health: %s", when, rec.Body.String())
		}
		for _, route := range []string{"/v1/spaces", "/v1/spaces/derived", "/v1/account"} {
			rec := doJSON(t, e, http.MethodGet, route, "")
			if rec.Code != http.StatusServiceUnavailable || errEnvCode(t, rec.Body.Bytes()) != "auth.login_pending" {
				t.Fatalf("%s: GET %s: %d %s, want 503 auth.login_pending", when, route, rec.Code, rec.Body.String())
			}
		}
	}
	expectPending("after login")

	// The same phrase again names the waiting account: nothing boots.
	again := postAuth(t, e, body)
	if !again.AlreadyAuthorized || again.Login == nil {
		t.Fatalf("second login: %+v", again)
	}

	// A restart: the keys are here now, and the login still waits.
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	if st := authStatusOf(t, e); st.Authorized || st.Login != nil {
		t.Fatalf("after teardown: %+v", st)
	}
	identity, err := ResolveIdentity(d.cfg, d.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.bootAccount(identity, fileCredential(d.cfg, identity.WalletPath, walletSeed{})); err != nil {
		t.Fatal(err)
	}
	expectPending("after restart")

	// The first sync opens the account and records it.
	close(synced)
	waitAuthorized(t, e, account)
	if got := readLoginState(t, dir); got != loginStateComplete {
		t.Fatalf("login state = %q, want complete", got)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces after the first sync: %d %s", rec.Code, rec.Body.String())
	}

	// From here every start is immediate, with no sync to wait for.
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	d.firstSync = func(context.Context, *anysyncsdk.SDK) error {
		t.Error("a completed login waited for a sync again")
		return nil
	}
	if _, err := d.bootAccount(identity, fileCredential(d.cfg, identity.WalletPath, walletSeed{})); err != nil {
		t.Fatal(err)
	}
	if st := authStatusOf(t, e); !st.Authorized || st.AccountId != account || st.Login != nil {
		t.Fatalf("restart after a completed login: %+v", st)
	}
}

// A teardown ends a waiting login cleanly, and the engine never opens
// behind it.
func TestLogin_TeardownWhileWaiting(t *testing.T) {
	phrase, _ := otherMnemonic(t)
	d, e := offlineDeps(t)
	if login := postAuth(t, e, `{"mnemonic":`+strconv.Quote(phrase)+`}`); login.Login == nil {
		t.Fatalf("login reply: %+v", login)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("teardown of a waiting login did not return")
	}
	if st := authStatusOf(t, e); st.Authorized || st.Login != nil {
		t.Fatalf("after teardown: %+v", st)
	}
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/spaces after teardown: %d %s, want 401", rec.Code, rec.Body.String())
	}
}

// A phrase is a login only on a device that holds nothing of the
// account: with the wallet or the SDK data already here, the account
// opens at once.
func TestLogin_PhraseOnDeviceWithAccountOpensAtOnce(t *testing.T) {
	d, e := offlineDeps(t)
	created := postAuth(t, e, `{}`)
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)

	again := postAuth(t, e, `{"mnemonic":`+strconv.Quote(created.Mnemonic)+`}`)
	if again.AccountId != created.AccountId || again.Created || again.Login != nil {
		t.Fatalf("phrase on a device with the wallet: %+v", again)
	}
	if st := authStatusOf(t, e); !st.Authorized || st.Login != nil {
		t.Fatalf("auth status: %+v", st)
	}
	if got := readLoginState(t, config.AccountDir(d.root, created.AccountId)); got != "" {
		t.Fatalf("login state %q written for an account that was created here", got)
	}
}

func TestMarkLoginPending(t *testing.T) {
	newDir := func() (dir, key string) {
		dir = filepath.Join(t.TempDir(), "account")
		return dir, filepath.Join(dir, "wallet.key")
	}

	// A device with nothing of the account: marked, and the dir is made.
	dir, key := newDir()
	if err := MarkLoginPending(dir, key); err != nil {
		t.Fatal(err)
	}
	if !LoginPending(dir) || readLoginState(t, dir) != loginStatePending {
		t.Fatalf("empty dir not marked pending")
	}

	// A key of either custody, SDK data or an existing state each mean
	// "not a login".
	for name, prepare := range map[string]func(dir, key string){
		"key":        func(_, key string) { writeTestFile(t, key) },
		"device key": func(dir, _ string) { writeTestFile(t, deviceKeyPath(dir)) },
		"wallet": func(dir, _ string) {
			writeTestFile(t, config.WalletPath(config.Config{}, dir))
		},
		"sdk data": func(dir, _ string) { writeTestFile(t, filepath.Join(sdkDataDir(dir), "sdk.db")) },
		"complete": func(dir, _ string) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := writeLoginState(dir, loginStateComplete); err != nil {
				t.Fatal(err)
			}
		},
	} {
		dir, key := newDir()
		prepare(dir, key)
		if name == "wallet" {
			// The managed custody asks about its own key file.
			key = deviceKeyPath(dir)
		}
		if err := MarkLoginPending(dir, key); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if LoginPending(dir) {
			t.Fatalf("%s: marked pending", name)
		}
	}

	// No file reads as complete; one that cannot be read as pending.
	dir, _ = newDir()
	if LoginPending(dir) {
		t.Fatal("a dir without login state reads as pending")
	}
	writeTestFile(t, loginStatePath(dir))
	if !LoginPending(dir) {
		t.Fatal("an unreadable login state must read as pending")
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The CLI restore path: `any init --mnemonic` marks the login before it
// writes the wallet, and the server's startup boot then waits.
func TestLogin_InitThenRunWaits(t *testing.T) {
	phrase, account := otherMnemonic(t)
	d, e := offlineDeps(t)
	dir := config.AccountDir(d.root, account)
	walletPath := config.WalletPath(config.Config{}, dir)
	if err := MarkLoginPending(dir, walletPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenWallet(walletPath, "", phrase, auth.DefaultAccountIndex); err != nil {
		t.Fatal(err)
	}

	identity, err := ResolveIdentity(d.cfg, d.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.bootAccount(identity, fileCredential(d.cfg, identity.WalletPath, walletSeed{})); err != nil {
		t.Fatal(err)
	}
	st := authStatusOf(t, e)
	if st.Authorized || st.Login == nil || st.Login.AccountId != account {
		t.Fatalf("startup boot after init --mnemonic: %+v", st)
	}
}

// A space this device downloaded answers no bundle call until its
// registry has arrived — for its owner and for a derived install too.
// A row naming the account's derived space, with no index behind it, is
// that state: what another device's create leaves here before the space
// has synced.
func TestBundles_RefusedOnDownloadedSpace(t *testing.T) {
	d, e := offlineDeps(t)
	postAuth(t, e, `{}`)

	var derived struct {
		Spaces []struct {
			Name    string `json:"name"`
			SpaceId string `json:"spaceId"`
		} `json:"spaces"`
	}
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/derived", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &derived); err != nil || len(derived.Spaces) == 0 {
		t.Fatalf("derived registry: %v %s", err, rec.Body.String())
	}
	spaceId := derived.Spaces[0].SpaceId
	if err := d.sdk.Spaces().Track(context.Background(), spaceId); err != nil {
		t.Fatal(err)
	}
	// The space exists as far as this device knows: the call opens it
	// and creates nothing.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/derived/"+derived.Spaces[0].Name, "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("open the downloaded derived space: %d %s", rec.Code, rec.Body.String())
	}

	bundles := "/v1/spaces/" + spaceId + "/bundles"
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, bundles, ""},
		{http.MethodGet, bundles + "/x%2Fv1", ""},
		{http.MethodPost, bundles, `{"id":"x/v1","name":"X","rootType":"page"}`},
		{http.MethodPost, bundles, `{"id":"x/v1","name":"X","rootType":"page","derived":true}`},
		{http.MethodDelete, bundles + "/x%2Fv1", ""},
		{http.MethodPost, bundles + "/x%2Fv1/children", `{"seed":"s","type":"page"}`},
		{http.MethodPost, bundles + "/x%2Fv1/resolve", `{"loserRootId":"root"}`},
		{http.MethodPost, "/v1/catalog/general-chat/setup", `{"spaceId":` + strconv.Quote(spaceId) + `}`},
	} {
		rec := doJSON(t, e, call.method, call.path, call.body)
		if rec.Code != http.StatusConflict || errEnvCode(t, rec.Body.Bytes()) != api.ErrBundleNotReady {
			t.Errorf("%s %s %s: %d %s, want 409 bundle.not_ready", call.method, call.path, call.body, rec.Code, rec.Body.String())
		}
	}
}
