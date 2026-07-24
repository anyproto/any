package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

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
		if h.Warming {
			t.Error("unauthorized health must report warming=false")
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
	if st.Authorized || st.AccountId != "" || len(st.Accounts) != 0 || st.Warming {
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
	if st.Warming {
		t.Error("synchronous (flag-off) boot must never report warming")
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
