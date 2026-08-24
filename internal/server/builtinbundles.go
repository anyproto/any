package server

import (
	"context"
	"time"

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

// builtinBundleId reports whether id is a server-owned bundle id —
// reserved against client ensure on the tech space.
func builtinBundleId(id string) bool {
	for _, inst := range builtinBundles() {
		if inst.Id == id {
			return true
		}
	}
	return false
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
	sp, err := d.sdk.Spaces().Get(ctx, d.sdk.TechSpaceId())
	if err != nil {
		builtinLog.Warn("builtin bundles: tech space: " + err.Error())
		return
	}
	for _, inst := range builtinBundles() {
		createCtx, cancel := context.WithTimeout(ctx, builtinBundleTimeout)
		_, installed, err := d.bundleResolver().Ensure(ctx, createCtx, sp, inst)
		cancel()
		switch {
		case err != nil:
			builtinLog.Warn("builtin bundle " + inst.Id + ": " + err.Error())
		case installed:
			builtinLog.Info("builtin bundle installed: " + inst.Id)
		}
	}
}
