package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/config"
)

// newUnauthorizedDeps builds deps the way server.Run does when no
// identity resolves: no engine, ready=false.
func newUnauthorizedDeps(t *testing.T) *deps {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Index.Enabled = false // never spawn embedder/model downloads in tests
	cfg.Network.NodeconfPath = stagingPath
	d := &deps{
		startedAt: time.Now().UTC(),
		shutdown:  make(chan struct{}, 1),
		root:      cfg.DataDir,
		cfg:       cfg,
		runCtx:    context.Background(),
	}
	// Whatever a test boots is torn down with it.
	t.Cleanup(func() { d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown) })
	return d
}

func TestAuth_UnauthorizedGuard(t *testing.T) {
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	// Meta endpoints stay reachable.
	if rec := doJSON(t, e, http.MethodGet, "/v1/health", ""); rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	} else {
		var h api.HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if h.Account != "" {
			t.Errorf("unauthorized health must report empty account, got %q", h.Account)
		}
		// bootstrapping must be present and false while unauthorized.
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if v, ok := raw["bootstrapping"]; !ok || v != false {
			t.Errorf("unauthorized health must carry bootstrapping=false, got %v (present=%v)", v, ok)
		}
	}

	// SDK-backed routes are rejected with auth.required.
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/v1/spaces"},
		{http.MethodGet, "/v1/account"},
		{http.MethodGet, "/v1/datasets"},
		{http.MethodPost, "/v1/spaces/query"},
	} {
		rec := doJSON(t, e, probe.method, probe.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d %s", probe.method, probe.path, rec.Code, rec.Body.String())
			continue
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != "auth.required" {
			t.Errorf("%s %s: code = %q", probe.method, probe.path, env.Error.Code)
		}
	}

	// Unknown /v1 paths still 404 while unauthorized — the guard must
	// not mask them as auth.required.
	if rec := doJSON(t, e, http.MethodGet, "/v1/does-not-exist", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuth_StatusAndValidation(t *testing.T) {
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodGet, "/v1/auth", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var st api.AuthStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Authorized || st.AccountId != "" || len(st.Accounts) != 0 {
		t.Fatalf("fresh root status: %+v", st)
	}
	// Standalone: the mode is reported and every capability bit is off.
	if st.Mode != config.ModeStandalone || st.Capabilities != (api.AuthCapabilities{}) {
		t.Fatalf("standalone status mode/capabilities: %+v", st)
	}

	for _, tc := range []struct {
		body, code string
		status     int
	}{
		{`{"mnemonic":"definitely not a valid phrase"}`, "auth.bad_mnemonic", http.StatusBadRequest},
		{`{"mnemonic":"x y","accountId":"Azz"}`, "request.invalid_field", http.StatusBadRequest},
		{`{"accountId":"Azz","index":1}`, "request.invalid_field", http.StatusBadRequest},
		{`{"index":1}`, "request.invalid_field", http.StatusBadRequest},
		// Explicit 0 is distinguishable from omitted (pointer field) and
		// equally invalid without a mnemonic.
		{`{"index":0}`, "request.invalid_field", http.StatusBadRequest},
		{`{"accountId":"Azz"}`, "auth.account_not_found", http.StatusNotFound},
	} {
		rec := doJSON(t, e, http.MethodPost, "/v1/auth", tc.body)
		if rec.Code != tc.status {
			t.Errorf("POST %s: want %d, got %d %s", tc.body, tc.status, rec.Code, rec.Body.String())
			continue
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != tc.code {
			t.Errorf("POST %s: code = %q want %q", tc.body, env.Error.Code, tc.code)
		}
	}
}

// TestAuth_StatusManaged pins the managed-mode status shape: the mode
// string, every capability bit on, and an EMPTY accounts list even when
// a standalone wallet sits under the same root — a managed server never
// boots from one, so it must not advertise it.
func TestAuth_StatusManaged(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	touchWallet(t, config.AccountDir(d.root, "Aleftover"))
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodGet, "/v1/auth", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var st api.AuthStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Authorized || st.Mode != config.ModeManaged {
		t.Fatalf("managed status: %+v", st)
	}
	if st.Capabilities != (api.AuthCapabilities{Deauthorize: true, SwitchAccount: true, Shutdown: true}) {
		t.Fatalf("managed capabilities: %+v", st.Capabilities)
	}
	if len(st.Accounts) != 0 {
		t.Fatalf("managed status must not list on-disk wallets: %+v", st.Accounts)
	}
}

// TestAuth_BootViaHTTP exercises the full deferred-boot path: generate
// an account over POST /v1/auth, then use the SDK-backed surface.
// Needs the staging nodeconf like every other SDK-booting test here.
func TestAuth_BootViaHTTP(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}

	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AccountId == "" || !resp.Created || resp.Mnemonic == "" {
		t.Fatalf("generate reply: %+v", resp)
	}
	// A generated account derives at the any default index, not
	// anytype's index 0.
	if id1, err := auth.AccountId(resp.Mnemonic, auth.DefaultAccountIndex); err != nil || id1 != resp.AccountId {
		t.Fatalf("generated account %q is not the index-%d derivation %q (err %v)",
			resp.AccountId, auth.DefaultAccountIndex, id1, err)
	}
	if id0, err := auth.AccountId(resp.Mnemonic, 0); err != nil || id0 == resp.AccountId {
		t.Fatalf("generated account must differ from the index-0 (anytype) derivation (err %v)", err)
	}

	// The engine is live: guard open, account surface working.
	rec = doJSON(t, e, http.MethodGet, "/v1/account", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/account after auth: %d %s", rec.Code, rec.Body.String())
	}
	var acct api.AccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &acct); err != nil {
		t.Fatal(err)
	}
	if acct.Id != resp.AccountId {
		t.Fatalf("account id %q != auth reply %q", acct.Id, resp.AccountId)
	}

	// Status reflects the booted account and lists its wallet dir.
	rec = doJSON(t, e, http.MethodGet, "/v1/auth", "")
	var st api.AuthStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Authorized || st.AccountId != resp.AccountId {
		t.Fatalf("status after boot: %+v", st)
	}
	if len(st.Accounts) != 1 || st.Accounts[0].Id != resp.AccountId || st.Accounts[0].Default {
		t.Fatalf("accounts after boot: %+v", st.Accounts)
	}

	// Double-auth is rejected.
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second POST /v1/auth: want 409, got %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "auth.already_authorized" {
		t.Fatalf("second POST code = %q", env.Error.Code)
	}
}

// TestAuth_TeardownResetsDeps drives an in-place teardown against a
// live engine: both stream families receive closed{deauthorized}, the
// guard answers 401, the engine fields are cleared, GET /v1/auth keeps
// answering while the drain runs, and the same process boots again.
func TestAuth_TeardownResetsDeps(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	first, err := cl.Authorize(ctx, api.AuthRequest{})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	// One stream per family: the callback-fed status driver and the
	// mailbox-fed query driver end through different code paths.
	pump := func(ch chan client.SSEFrame) func(client.SSEFrame) error {
		return func(f client.SSEFrame) error {
			select {
			case ch <- f:
			case <-ctx.Done():
			}
			return nil
		}
	}
	statusFrames := make(chan client.SSEFrame, 16)
	go func() { _ = cl.StreamSyncStatusAccount(ctx, pump(statusFrames)) }()
	queryFrames := make(chan client.SSEFrame, 16)
	go func() { _ = cl.StreamSpaceListQuerySubscribe(ctx, []byte(`{}`), pump(queryFrames)) }()
	if got := waitFrame(t, statusFrames, 10*time.Second); got.Event != "ready" {
		t.Fatalf("status stream first frame = %q", got.Event)
	}
	if got := waitFrame(t, queryFrames, 10*time.Second); got.Event != "ready" {
		t.Fatalf("query stream first frame = %q", got.Event)
	}
	if got := waitFrame(t, queryFrames, 10*time.Second); got.Event != "snapshot" {
		t.Fatalf("query stream second frame = %q", got.Event)
	}

	torn := make(chan struct{})
	go func() {
		d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)
		close(torn)
	}()
	// The exempt status route must keep answering while the drain
	// runs — it reads through the gate, never through authMu.
	statusDone := make(chan error, 1)
	go func() {
		_, err := cl.AuthStatus(ctx)
		statusDone <- err
	}()
	select {
	case err := <-statusDone:
		if err != nil {
			t.Fatalf("auth status during teardown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GET /v1/auth blocked behind the teardown")
	}
	select {
	case <-torn:
	case <-time.After(30 * time.Second):
		t.Fatal("teardown did not complete")
	}

	for name, ch := range map[string]chan client.SSEFrame{"status": statusFrames, "query": queryFrames} {
		got := waitFrame(t, ch, 10*time.Second)
		if got.Event != "closed" {
			t.Fatalf("%s stream terminal frame = %q (data=%s)", name, got.Event, got.Data)
		}
		var closed api.SubscribeClosed
		if err := json.Unmarshal(got.Data, &closed); err != nil {
			t.Fatalf("%s decode closed: %v", name, err)
		}
		if closed.Reason != api.SubscribeClosedDeauthorized {
			t.Fatalf("%s closed reason = %q, want deauthorized", name, closed.Reason)
		}
	}

	if d.ready.Load() || d.eng != nil || d.sdk != nil || d.shutdownCtx != nil || d.account != "" {
		t.Fatalf("deps not reset after teardown: ready=%v eng=%v sdk=%v", d.ready.Load(), d.eng, d.sdk)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after teardown GET /v1/spaces: %d %s", rec.Code, rec.Body.String())
	}
	st, err := cl.AuthStatus(ctx)
	if err != nil || st.Authorized || st.AccountId != "" {
		t.Fatalf("status after teardown: %+v err=%v", st, err)
	}

	// The same process boots again — the fresh gate, ctx and engine
	// are what the switch path relies on.
	second, err := cl.Authorize(ctx, api.AuthRequest{Mnemonic: first.Mnemonic})
	if err != nil {
		t.Fatalf("re-authorize: %v", err)
	}
	if second.AccountId != first.AccountId || second.Created {
		t.Fatalf("re-authorize reply: %+v (first %+v)", second, first)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/account", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/account after re-boot: %d %s", rec.Code, rec.Body.String())
	}
}

// Authorized health: bootstrapping tracks the SDK's background boot
// pass. Mid-pass true is timing-dependent (not cheaply fakeable — the
// channel is SDK-internal), so pin the deterministic settled state:
// false once BootstrapDone closes.
func TestHealth_BootstrappingFalseAfterBootPass(t *testing.T) {
	d, closeFn := newTestDeps(t)
	defer closeFn()
	e := buildEcho(d)

	select {
	case <-d.sdk.BootstrapDone():
	case <-time.After(30 * time.Second):
		t.Fatal("SDK boot pass did not finish in 30s")
	}
	rec := doJSON(t, e, http.MethodGet, "/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
	var h api.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Bootstrapping {
		t.Error("bootstrapping must be false after BootstrapDone")
	}
	if h.Account == "" {
		t.Error("authorized health must report the account id")
	}
}
