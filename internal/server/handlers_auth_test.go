package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
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
	shutdownCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := &deps{
		startedAt:      time.Now().UTC(),
		shutdown:       make(chan struct{}, 1),
		chunkers:       NewIndexRegistry(),
		shutdownCtx:    shutdownCtx,
		cancelShutdown: cancel,
		streamsWG:      &sync.WaitGroup{},
		root:           cfg.DataDir,
		cfg:            cfg,
		runCtx:         context.Background(),
	}
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

	for _, tc := range []struct {
		body, code string
		status     int
	}{
		{`{"mnemonic":"definitely not a valid phrase"}`, "auth.bad_mnemonic", http.StatusBadRequest},
		{`{"mnemonic":"x y","accountId":"Azz"}`, "request.invalid_field", http.StatusBadRequest},
		{`{"accountId":"Azz","index":1}`, "request.invalid_field", http.StatusBadRequest},
		{`{"index":1}`, "request.invalid_field", http.StatusBadRequest},
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

// TestAuth_BootViaHTTP exercises the full deferred-boot path: generate
// an account over POST /v1/auth, then use the SDK-backed surface.
// Needs the staging nodeconf like every other SDK-booting test here.
func TestAuth_BootViaHTTP(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}

	d := newUnauthorizedDeps(t)
	e := buildEcho(d)
	defer func() {
		d.cancelShutdown()
		d.streamsWG.Wait()
		d.closeEngine(logger.NewNamed("test"))
	}()

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

// newReadyDepsForVerify fakes the ONE thing POST /v1/auth/verify reads:
// a server that is authorized and knows its account id. No SDK is
// opened — the handler derives from the request and compares strings,
// so a real engine would only make the test slow and network-bound.
func newReadyDepsForVerify(t *testing.T, account string) *deps {
	t.Helper()
	d := newUnauthorizedDeps(t)
	d.account = account
	d.ready.Store(true)
	return d
}

func TestAuthVerify_MatchesOnlyTheRunningAccount(t *testing.T) {
	mine, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	account, err := auth.AccountId(mine, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := buildEcho(newReadyDepsForVerify(t, account))

	verify := func(body string) api.AuthVerifyResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, "/v1/auth/verify", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("verify: want 200, got %d %s", rec.Code, rec.Body.String())
		}
		var resp api.AuthVerifyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		// The reply is one bit. Echoing any account id — the running
		// one, or the id derived from the submitted phrase — would turn
		// this into a phrase→account oracle, so assert on the wire text.
		if body := rec.Body.String(); strings.Contains(body, "account") ||
			strings.Contains(body, account) {
			t.Fatalf("verify reply leaks an account id: %s", body)
		}
		return resp
	}

	if !verify(`{"mnemonic":"` + mine + `"}`).Matches {
		t.Error("the running account's own phrase must match")
	}
	if verify(`{"mnemonic":"` + other + `"}`).Matches {
		t.Error("a different account's phrase must not match")
	}
	// Right phrase, wrong derivation index — a different account, so a
	// mismatch (the client must send the index a restore would use).
	if verify(`{"mnemonic":"` + mine + `","index":1}`).Matches {
		t.Error("index 1 derives another account — must not match")
	}
}

func TestAuthVerify_RejectsBadInput(t *testing.T) {
	mine, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	account, err := auth.AccountId(mine, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := buildEcho(newReadyDepsForVerify(t, account))

	for _, tc := range []struct {
		name, body, code string
	}{
		{"not a phrase", `{"mnemonic":"definitely not a bip39 phrase"}`, "auth.bad_mnemonic"},
		{"empty", `{"mnemonic":""}`, "request.missing_field"},
		{"absent", `{}`, "request.missing_field"},
		{"unknown field", `{"mnemonic":"` + mine + `","matches":true}`, "request.unknown_field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, "/v1/auth/verify", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
			}
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q (%s)", env.Error.Code, tc.code, rec.Body.String())
			}
		})
	}
}

// An unauthorized server has nothing to verify against: the route is
// deliberately outside the guard's exemption list, so it 401s like every
// other non-meta route rather than answering with a bare `false`.
func TestAuthVerify_UnauthorizedIsGuarded(t *testing.T) {
	mine, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	e := buildEcho(newUnauthorizedDeps(t))

	rec := doJSON(t, e, http.MethodPost, "/v1/auth/verify", `{"mnemonic":"`+mine+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "auth.required" {
		t.Fatalf("code = %q", env.Error.Code)
	}
}
