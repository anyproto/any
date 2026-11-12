package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// nodeAddress matches one entry of a nodeconf `addresses` list.
var nodeAddress = regexp.MustCompile(`(?m)^(\s*- )(?:quic://)?[^\s:/]+:\d+\s*$`)

// unreachableNetwork points a test config at a copy of the staging
// nodeconf whose every node address is a closed local port, with the
// direct layers off: no node and no peer answers.
func unreachableNetwork(t testing.TB) func(*config.Config) {
	t.Helper()
	raw, err := os.ReadFile(stagingPath)
	if err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	path := filepath.Join(t.TempDir(), "nodeconf.yml")
	if err := os.WriteFile(path, nodeAddress.ReplaceAll(raw, []byte("${1}127.0.0.1:1")), 0o600); err != nil {
		t.Fatalf("write nodeconf: %v", err)
	}
	return func(cfg *config.Config) {
		cfg.Network.NodeconfPath = path
		off := false
		cfg.P2P.Enabled = &off
	}
}

// setCatalogSetupTimeout shortens the setup bound for one test.
func setCatalogSetupTimeout(t testing.TB, d time.Duration) {
	t.Helper()
	prev := catalogSetupTimeout
	catalogSetupTimeout = d
	t.Cleanup(func() { catalogSetupTimeout = prev })
}

// TestServer_CatalogSetupDerivedSpaceWithoutStorage pins setup on a
// derived space another device of the account created: here it is a row
// with no storage. With no node reachable the space opens from its seed
// and the setup lands.
func TestServer_CatalogSetupDerivedSpaceWithoutStorage(t *testing.T) {
	d, teardown := newTestDepsCfg(t, unreachableNetwork(t))
	defer teardown()
	e := buildEcho(d)
	setCatalogSetupTimeout(t, 30*time.Second)

	bao, ok := d.derivedSpaceByName("bao")
	if !ok {
		t.Fatal("no bao entry in the derived-space registry")
	}
	// The row as it syncs in from the other device.
	if err := d.sdk.Spaces().Track(context.Background(), bao.SpaceId); err != nil {
		t.Fatalf("track: %v", err)
	}
	var listed api.DerivedSpaceListResponse
	decodeGet(t, e, "/v1/spaces/derived", &listed)
	if len(listed.Spaces) == 0 || listed.Spaces[0].SpaceId != bao.SpaceId || !listed.Spaces[0].Created {
		t.Fatalf("derived listing: %+v", listed.Spaces)
	}

	res := setupUsecase(t, e, "general-chat", bao.SpaceId)
	if len(res.Bundles) == 0 || !res.Bundles[0].Bundle.Derived || res.Bundles[0].Bundle.RootId == "" {
		t.Fatalf("setup reply: %+v", res.Bundles)
	}
}

// TestServer_CatalogSetupDeadline pins the setup's bound: an install
// waiting on the account store's write lock answers
// `503 catalog.setup_timeout` naming the step, and the next call lands
// once the lock is free.
func TestServer_CatalogSetupDeadline(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogDeadline")

	prev := catalogSetupTimeout
	setCatalogSetupTimeout(t, 2*time.Second)

	tx, err := d.sdk.Store().WriteTx(context.Background())
	if err != nil {
		t.Fatalf("hold write tx: %v", err)
	}
	// Released on its own later, so a handler that waits the lock out
	// fails the assertions below instead of hanging the test.
	release := time.AfterFunc(20*time.Second, func() { _ = tx.Rollback() })
	start := time.Now()
	rec := doJSON(t, e, http.MethodPost, "/v1/catalog/general-chat/setup", `{"spaceId":"`+sp.Id+`"}`)
	took := time.Since(start)
	if release.Stop() {
		_ = tx.Rollback()
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup under a held write lock: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Error.Code != api.ErrCatalogSetupTimeout || env.Error.Details["step"] != setupStepInstall ||
		env.Error.Details["spaceId"] != sp.Id || env.Error.Details["usecaseId"] != "general-chat" {
		t.Fatalf("timeout reply: %+v", env.Error)
	}
	if took > 10*time.Second {
		t.Fatalf("timeout reply took %s, want about %s", took, catalogSetupTimeout)
	}

	catalogSetupTimeout = prev
	res := setupUsecase(t, e, "general-chat", sp.Id)
	if len(res.Bundles) == 0 || !res.Bundles[0].Bundle.Derived || res.Bundles[0].Bundle.RootId == "" {
		t.Fatalf("setup after the lock is free: %+v", res.Bundles)
	}
}
