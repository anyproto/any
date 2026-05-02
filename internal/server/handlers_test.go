package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// stagingPath points at the SDK's shared test-etc fixture relative to
// this test file. Mirrors the SDK's sdk_test.go and anysyncx app_test.go.
const stagingPath = "../../../test-etc/staging.yml"

// newTestDeps boots the server's deps in-process: wallet, SDK, fake
// shutdown channel. Returns a teardown that closes the SDK. Skips the
// test if the staging nodeconf isn't checked out alongside the repo.
func newTestDeps(t *testing.T) (*deps, func()) {
	t.Helper()
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}

	dataDir := t.TempDir()
	walletPath := filepath.Join(dataDir, "wallet.key")
	provider, _, err := OpenWallet(walletPath, "")
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Network.NodeconfPath = stagingPath

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sdk, err := OpenSDK(ctx, cfg, dataDir, provider)
	if err != nil {
		t.Fatalf("OpenSDK: %v", err)
	}

	account, err := AccountID(ctx, provider)
	if err != nil {
		_ = sdk.Close()
		t.Fatalf("AccountID: %v", err)
	}

	d := &deps{
		account:   account,
		startedAt: time.Now().UTC(),
		shutdown:  make(chan struct{}, 1),
		sdk:       sdk,
	}
	return d, func() {
		if err := sdk.Close(); err != nil {
			t.Logf("sdk close: %v", err)
		}
	}
}

func doJSON(t *testing.T, e http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec
}

func TestServer_AccountAndSpaceLifecycle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// GET /v1/account → real id from SDK.
	rec := doJSON(t, e, http.MethodGet, "/v1/account", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/account status = %d body=%s", rec.Code, rec.Body.String())
	}
	var acc api.AccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil {
		t.Fatalf("unmarshal account: %v", err)
	}
	if acc.Id == "" {
		t.Fatal("account id should be non-empty")
	}

	// GET /v1/spaces → empty list initially.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list api.SpaceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Spaces) != 0 {
		t.Fatalf("expected empty list, got %v", list.Spaces)
	}

	// POST /v1/spaces → creates a space.
	body := `{"name":"Demo","description":"smoke"}`
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.Id == "" {
		t.Fatal("created space id should be non-empty")
	}
	if created.Name != "Demo" {
		t.Errorf("name = %q, want Demo", created.Name)
	}
	if created.Status != api.SpaceStatusActive {
		t.Errorf("status = %q, want active", created.Status)
	}

	// GET /v1/spaces/:id → same row.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+created.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces/:id status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.Id != created.Id {
		t.Errorf("got.Id = %q, want %q", got.Id, created.Id)
	}

	// GET /v1/spaces → list now contains the space.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list2: %v", err)
	}
	if len(list.Spaces) != 1 || list.Spaces[0].Id != created.Id {
		t.Errorf("list = %+v", list.Spaces)
	}

	// DELETE /v1/spaces/:id → soft delete in the SDK index. Per PR-016
	// the server hides soft-deleted spaces from the API: the list no
	// longer contains it, and GET /:id returns 404 space.not_found.
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+created.Id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/spaces/:id status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list3: %v", err)
	}
	if len(list.Spaces) != 0 {
		t.Errorf("post-delete list = %+v, want empty (deleted spaces should be hidden)", list.Spaces)
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+created.Id, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/spaces/:id after delete: status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Error.Code != "space.not_found" {
		t.Errorf("error code = %q, want space.not_found", env.Error.Code)
	}
}

func TestServer_SpaceCreate_BadJSON(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.Error.Code != "request.bad_json" {
		t.Errorf("code = %q, want request.bad_json", env.Error.Code)
	}
}

func TestServer_NotImplementedRoutes(t *testing.T) {
	// This test runs without booting the SDK — the 501 handlers don't
	// touch deps.sdk. Skipping the staging precondition lets this run on
	// machines without the test-etc fixture.
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	e := buildEcho(d)

	cases := []struct{ method, path string }{
		{http.MethodPut, "/v1/account/metadata"},
		{http.MethodPost, "/v1/spaces/join"},
		{http.MethodPost, "/v1/spaces/derive"},
		{http.MethodPost, "/v1/spaces/one-to-one"},
		{http.MethodDelete, "/v1/spaces/spc/types/t1"},
		{http.MethodDelete, "/v1/spaces/spc/types/t1/properties/p1"},
		{http.MethodPatch, "/v1/spaces/spc/types/t1/properties/p1"},
		{http.MethodPost, "/v1/spaces/spc/properties/o1/account/t1"},
		{http.MethodGet, "/v1/spaces/spc/members"},
		{http.MethodPost, "/v1/spaces/spc/acl/invite"},
		{http.MethodGet, "/v1/spaces/spc/sync-status"},
	}
	for _, tc := range cases {
		rec := doJSON(t, e, tc.method, tc.path, "{}")
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status = %d, want 501; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
			continue
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Errorf("%s %s: unmarshal: %v", tc.method, tc.path, err)
			continue
		}
		if env.Error.Code != "sdk.not_implemented" {
			t.Errorf("%s %s: code = %q, want sdk.not_implemented", tc.method, tc.path, env.Error.Code)
		}
	}
}
