package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
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

// setDuration sets a package-level bound for one test.
func setDuration(t testing.TB, v *time.Duration, d time.Duration) {
	t.Helper()
	prev := *v
	*v = d
	t.Cleanup(func() { *v = prev })
}

// wantSetupTimeout checks a setup reply is the timeout at step.
func wantSetupTimeout(t *testing.T, rec *httptest.ResponseRecorder, spaceId, step string) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Error.Code != api.ErrCatalogSetupTimeout || env.Error.Details["step"] != step ||
		env.Error.Details["spaceId"] != spaceId || env.Error.Details["usecaseId"] != "general-chat" {
		t.Fatalf("timeout reply: %+v", env.Error)
	}
}

// TestServer_CatalogSetupDerivedSpaceWithoutStorage pins setup on a
// derived space another device of the account created: here it is a row
// with no storage. With no node reachable the space opens from its seed
// and the setup lands.
func TestServer_CatalogSetupDerivedSpaceWithoutStorage(t *testing.T) {
	d, teardown := newTestDepsCfg(t, unreachableNetwork(t))
	defer teardown()
	e := buildEcho(d)
	setDuration(t, &catalogSetupTimeout, 30*time.Second)

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

// TestServer_CatalogSetupTimeoutOpeningSpace pins the bound on the
// setup's waits: a space this device holds only the row of, with no
// node to pull it from, answers `503 catalog.setup_timeout` at the
// `space` step instead of holding the request.
func TestServer_CatalogSetupTimeoutOpeningSpace(t *testing.T) {
	d, teardown := newTestDepsCfg(t, unreachableNetwork(t))
	defer teardown()
	e := buildEcho(d)
	setDuration(t, &catalogSetupTimeout, 2*time.Second)

	const foreign = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi.2a5s8kx60jkm"
	if err := d.sdk.Spaces().Track(context.Background(), foreign); err != nil {
		t.Fatalf("track: %v", err)
	}
	start := time.Now()
	rec := doJSON(t, e, http.MethodPost, "/v1/catalog/general-chat/setup", `{"spaceId":"`+foreign+`"}`)
	wantSetupTimeout(t, rec, foreign, setupStepSpace)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("timeout reply took %s, want about %s", took, catalogSetupTimeout)
	}
}

// TestServer_CatalogSetupTimeoutInstalling pins the bound on the
// install section: an install waiting on the account store's write
// lock answers `503 catalog.setup_timeout` at the `install` step when
// the create timeout ends, and the next call lands once the lock is
// free. The wait bound stays at its default, so only the create
// timeout can end the call.
func TestServer_CatalogSetupTimeoutInstalling(t *testing.T) {
	d, teardown := newTestDepsCfg(t, unreachableNetwork(t))
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogInstallTimeout")
	prev := bundleCreateTimeout
	setDuration(t, &bundleCreateTimeout, 2*time.Second)

	tx, err := d.sdk.Store().WriteTx(context.Background())
	if err != nil {
		t.Fatalf("hold write tx: %v", err)
	}
	// Released on its own later, so a handler that waits the lock out
	// fails the assertions below instead of hanging the test.
	release := time.AfterFunc(40*time.Second, func() { _ = tx.Rollback() })
	start := time.Now()
	rec := doJSON(t, e, http.MethodPost, "/v1/catalog/general-chat/setup", `{"spaceId":"`+sp.Id+`"}`)
	took := time.Since(start)
	if release.Stop() {
		_ = tx.Rollback()
	}
	wantSetupTimeout(t, rec, sp.Id, setupStepInstall)
	if took > 20*time.Second {
		t.Fatalf("timeout reply took %s", took)
	}

	bundleCreateTimeout = prev
	res := setupUsecase(t, e, "general-chat", sp.Id)
	if len(res.Bundles) == 0 || !res.Bundles[0].Bundle.Derived || res.Bundles[0].Bundle.RootId == "" {
		t.Fatalf("setup after the lock is free: %+v", res.Bundles)
	}
}

// TestSetupTimedOut pins which errors a setup reports as its timeout.
func TestSetupTimedOut(t *testing.T) {
	live := context.Background()
	expired, cancelExpired := context.WithDeadline(live, time.Now().Add(-time.Second))
	defer cancelExpired()
	cancelled, cancel := context.WithCancel(live)
	cancel()
	notSynced := fmt.Errorf("bundle x: %w", bundles.ErrRegistryNotSynced)

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"deadline error", live, fmt.Errorf("write: %w", context.DeadlineExceeded), true},
		{"registry wait cut by the bound", expired, notSynced, true},
		{"registry wait that ran out on its own", live, notSynced, false},
		{"registry wait under a client that went away", cancelled, notSynced, false},
		{"cancel", cancelled, context.Canceled, false},
		{"another error past the bound", expired, bundles.ErrNotInstalled, false},
	} {
		if got := setupTimedOut(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
