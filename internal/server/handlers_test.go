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

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/localstore"
	"github.com/anyproto/any/internal/push"
)

// stagingPath points at the staging nodeconf fixture at the repo root
// (relative to this package dir, `go test`'s CWD). It's gitignored, not
// checked in — copy your own staging.yml there to run these tests.
const stagingPath = "../../staging.yml"

// newTestDeps boots the server's deps in-process: wallet, SDK and a
// hand-built engine. Returns a teardown that tears the engine down
// (production path). Skips the test if the staging nodeconf isn't
// checked out alongside the repo. Tests that need to trip the engine
// ctx mid-handler can call deps.cancelEngine directly.
func newTestDeps(t testing.TB) (*deps, func()) {
	return newTestDepsCfg(t, nil)
}

// newTestDepsCfg is newTestDeps with a config hook applied before the
// SDK opens — the way tests opt into config-gated services (e.g.
// cfg.Push). Mirrors bootEngine's wiring for the services the mutated
// config enables.
func newTestDepsCfg(t testing.TB, mutate func(*config.Config)) (*deps, func()) {
	t.Helper()
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}

	dataDir := t.TempDir()
	walletPath := filepath.Join(dataDir, "wallet.key")
	provider, _, err := OpenWallet(walletPath, "", "", 0)
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Network.NodeconfPath = stagingPath
	if mutate != nil {
		mutate(&cfg)
	}

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

	// A real engine (no instance lock, no indexer) so the lifecycle
	// paths — gate, per-engine ctx, teardown — run the production
	// code; only the boot itself is hand-built.
	eng := newEngine()
	eng.sdk = sdk
	eng.account = account
	if signKey, err := accountKey(ctx, provider); err == nil {
		eng.signKey = signKey
	}
	eng.chunkers = NewIndexRegistry()
	// Mirror bootEngine: resolve the derived-space registry against
	// the account so the /spaces/derived routes resolve. Pure
	// computation over the account keys — nothing is created.
	if derived, err := resolveDerivedSpaces(ctx, sdk); err == nil {
		eng.derived = derived
	} else {
		t.Logf("resolve derived spaces: %v", err)
	}
	// Mirror bootEngine: the push service exists only when config
	// names a push node.
	if cfg.Push.Active() {
		eng.push = push.New(sdk, dataDir)
		eng.push.Start(eng.ctx)
	}
	// Mirror bootEngine: the local store borrows the SDK's own DB.
	if cfg.Local.Enabled {
		eng.local = localstore.New(sdk.Store())
	}
	d := &deps{
		startedAt: time.Now().UTC(),
		shutdown:  make(chan struct{}, 1),
		root:      dataDir,
		cfg:       cfg,
		runCtx:    context.Background(),
	}
	// Publish without the engine goroutines bootAccount spawns (the
	// process interest and the derived-setup pass) — they reach the
	// network and are not what handler tests exercise.
	d.eng = eng
	d.sdk = eng.sdk
	d.push = eng.push
	d.local = eng.local
	d.account = eng.account
	d.signKey = eng.signKey
	d.derived = eng.derived
	d.shutdownCtx = eng.ctx
	d.ready.Store(true)
	return d, func() {
		d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	}
}

// cancelEngine trips the live engine's context without tearing it
// down — the way tests observe a stream's terminal frame while the
// fixture is still up.
func (d *deps) cancelEngine() {
	d.eng.cancel()
}

// setupSubscribeFixture creates a space + type + object, returning
// (spaceId, typeId, objectId). Shared across handler tests that need
// a baseline space and object to subscribe to.
func setupSubscribeFixture(t *testing.T, e http.Handler) (spaceId, typeId, objectId string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SubTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId = sp.Id

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Movie","description":"film","xKey":"movie"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	typeId = typeResp.TypeId

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects",
		`{"types":["`+typeId+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	objectId = objResp.ObjectId
	return spaceId, typeId, objectId
}

func doJSON(t testing.TB, e http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONH(t, e, method, path, body, nil)
}

// doJSONH is doJSON with extra request headers (the managed-mode
// control token, for one).
func doJSONH(t testing.TB, e http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
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
	// createdAt is stamped on the tech-space row at create time
	// (added-to-account semantics) — only pre-stamp rows are zero.
	if created.CreatedAt.IsZero() {
		t.Error("created.CreatedAt is zero, want stamped time")
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
	if got.CreatedAt.IsZero() {
		t.Error("got.CreatedAt is zero, want stamped time")
	}

	// GET /v1/spaces → list now contains the space.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list2: %v", err)
	}
	if len(list.Spaces) != 1 || list.Spaces[0].Id != created.Id {
		t.Errorf("list = %+v", list.Spaces)
	} else if list.Spaces[0].CreatedAt.IsZero() {
		t.Error("list row CreatedAt is zero, want stamped time")
	}

	// DELETE /v1/spaces/:id → soft delete; row stays in storage with status=deleted.
	rec = doJSON(t, e, http.MethodDelete, "/v1/spaces/"+created.Id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/spaces/:id status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Default list filters to active-only (TEMPORARY workaround), so the
	// soft-deleted space is hidden.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list3: %v", err)
	}
	if len(list.Spaces) != 0 {
		t.Errorf("post-delete default list should be empty (active-only), got = %+v", list.Spaces)
	}

	// ?status=all opts back into the full list, where the deleted row shows.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces?status=all", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list4: %v", err)
	}
	if len(list.Spaces) != 1 || list.Spaces[0].Status != api.SpaceStatusDeleted {
		t.Errorf("post-delete status=all list = %+v", list.Spaces)
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

// TestServer_AccountUpdateMetadata exercises PUT /v1/account/metadata.
// Hits identityRepo on the staging network on the happy path; the
// validation cases avoid the SDK entirely.
func TestServer_AccountUpdateMetadata(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	t.Run("happy path", func(t *testing.T) {
		body := `{"name":"Alice","description":"writer, reader, occasional debugger","iconCid":"bafyfake"}`
		rec := doJSON(t, e, http.MethodPut, "/v1/account/metadata", body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("bad json", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPut, "/v1/account/metadata", "{not json")
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
	})

	t.Run("all fields empty", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPut, "/v1/account/metadata", `{}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.Error.Code != "request.missing_field" {
			t.Errorf("code = %q, want request.missing_field", env.Error.Code)
		}
	})
}

func TestServer_NotImplementedRoutes(t *testing.T) {
	// This test runs without booting the SDK — the 501 handlers don't
	// touch deps.sdk. Skipping the staging precondition lets this run on
	// machines without the test-etc fixture. ready is set by hand so
	// the unauthorized guard doesn't shadow the 501s.
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	d.ready.Store(true)
	e := buildEcho(d)

	cases := []struct{ method, path string }{
		{http.MethodDelete, "/v1/spaces/spc/types/t1"},
		{http.MethodGet, "/v1/spaces/spc/sync-status/peers"},
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
