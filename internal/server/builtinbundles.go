package server

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/favorites"
)

var builtinLog = logger.NewNamed("builtin-bundles")

// builtinBundles are the server-owned account-level bundles ensured on
// the tech space at engine boot. Declaration-only: the schema lives
// with each bundle's package; records ride the generic surface.
func builtinBundles() []bundles.Install {
	return []bundles.Install{{
		Id:       favorites.BundleId,
		Name:     favorites.DisplayName,
		Derived:  true,
		Datasets: favorites.Datasets(),
	}}
}

// builtinBundleIdSet is the reserved-id set, package-level so the
// request-path guards never rebuild the install payloads. Keep in step
// with builtinBundles().
var builtinBundleIdSet = map[string]struct{}{
	favorites.BundleId: {},
}

// builtinBundleId reports whether id is a server-owned bundle id —
// reserved against client ensure on the tech space.
func builtinBundleId(id string) bool {
	_, ok := builtinBundleIdSet[id]
	return ok
}

// refuseBuiltinBundleRoot refuses dataset CRUD on a server-owned
// bundle root. The declaration is first-write and its tombstones are
// sticky (a root that ever carried a declaration is never re-declared
// by the boot ensure), so one client PATCH/DELETE would diverge or
// destroy the built-in schema for the whole account, permanently. Root
// ids are pure derivations, so the check costs no registry read.
func (d *deps) refuseBuiltinBundleRoot(c echo.Context, sp space.Space, typeId string) (error, bool) {
	if !d.isTechSpace(sp.Id()) {
		return nil, false
	}
	for id := range builtinBundleIdSet {
		rootId, err := sp.Bundles().DerivedRootId(c.Request().Context(), id)
		if err == nil && rootId == typeId {
			return writeError(c, http.StatusConflict, "bundle.reserved",
				"the dataset declaration on a built-in bundle root is server-owned",
				map[string]any{"bundleId": id}), true
		}
	}
	return nil, false
}

// builtinBundleTimeout bounds one boot-time Ensure. Derived installs
// run the convergence wait (30s connected / 3s with no peer) before
// installing, so the bound sits above that, not at it.
const builtinBundleTimeout = 2 * time.Minute

// ensureBuiltinBundles installs the built-in bundles on the tech space
// once the SDK's bootstrap pass completes. Idempotent and offline-
// capable — the roots are derived, so every device computes the same
// ids and a re-ensure adopts. Best-effort: a failure is logged and
// retried next boot, never fatal, and nothing here blocks serving —
// same contract as registerDevice.
func (d *deps) ensureBuiltinBundles(ctx context.Context) {
	select {
	case <-d.sdk.BootstrapDone():
	case <-ctx.Done():
		return
	}
	// Retry with backoff: clients cannot self-heal a missed install
	// (ensure of a built-in id is 409), so one transient failure —
	// e.g. the first tech-space materialization racing the boot pass,
	// the same race spawnWorker retries — must not cost the process
	// lifetime. 2s doubling to a 1m cap, until done or cancelled.
	backoff := 2 * time.Second
	pending := builtinBundles()
	for len(pending) > 0 {
		pending = d.ensureBundlesOnce(ctx, pending)
		if len(pending) == 0 {
			return
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff *= 2; backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

// ensureBundlesOnce runs one ensure pass and returns the installs
// that still need a retry.
func (d *deps) ensureBundlesOnce(ctx context.Context, insts []bundles.Install) (failed []bundles.Install) {
	sp, err := d.sdk.Spaces().Get(ctx, d.sdk.TechSpaceId())
	if err != nil {
		builtinLog.Warn("builtin bundles: tech space: " + err.Error())
		return insts
	}
	for _, inst := range insts {
		createCtx, cancel := context.WithTimeout(ctx, builtinBundleTimeout)
		_, installed, err := d.bundleResolver().Ensure(ctx, createCtx, sp, inst)
		cancel()
		switch {
		case err != nil:
			builtinLog.Warn("builtin bundle " + inst.Id + ": " + err.Error())
			failed = append(failed, inst)
		case installed:
			builtinLog.Info("builtin bundle installed: " + inst.Id)
		}
	}
	return failed
}
