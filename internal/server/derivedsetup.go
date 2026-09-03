package server

import (
	"context"
	"time"

	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/bundles"
)

// Restore pass for the well-known derived spaces (derivedspaces.go)
// and the bundles they carry — any-sync-sdk docs/bundles.md.
//
// Creating a bundle is a client call (POST /v1/spaces/:id/bundles) and
// needs no gate: the space id is derived from the account keys, so the
// install can happen locally and sync afterwards. Restoring a device
// that already had the space is the case that needs care — a client
// that ensures against a registry it has not synced yet mints a
// competing root. This pass front-runs that: it converges the space
// list, projects the space index, and reads the registry, so by the
// time a client asks, the answer is the account's converged one.
//
// It installs nothing and deletes nothing. Only a client knows what a
// bundle is for and whether a losing root's content was worth merging,
// so both decisions stay explicit calls.

// Bounds on the restore gates. Both waits retry until their context
// expires, so an offline boot must not sit on them forever.
// WaitListSynced falls through to the local space list; an expired
// index wait skips the entry until the next boot, since a registry
// read before convergence is the blind read this pass exists to
// prevent. A space that was never set up is not a stall — it converges
// to an empty registry.
const (
	derivedListSyncWait  = 90 * time.Second
	derivedIndexSyncWait = 90 * time.Second
)

// backgroundCtx is the process-lifetime context for work that must
// outlive the request that started it — bounded by shutdown, never by
// a client disconnect.
func (d *deps) backgroundCtx() context.Context {
	if d.shutdownCtx != nil {
		return d.shutdownCtx
	}
	return context.Background()
}

// bundleResolver returns the process's bundles engine, building it on
// first use.
func (d *deps) bundleResolver() *bundles.Resolver {
	d.installsOnce.Do(func() {
		d.installs = bundles.NewResolver(bundles.DefaultGrace)
	})
	return d.installs
}

// bootstrapDerivedSetups warms the registry of every well-known
// derived space this account already has. It never materializes a
// space: creating one is an explicit act
// (POST /v1/spaces/derived/:name), and a boot pass that created them
// would hand every account a space it never asked for.
//
// Runs as an engine goroutine off bootAccount — bounded by the
// engine's ctx and reading the engine it was started for (never the
// live deps fields, which a switch replaces underneath it). Failures
// are logged, never fatal, and nothing here blocks serving.
func (d *deps) bootstrapDerivedSetups(ctx context.Context, eng *engine) {
	select {
	case <-eng.sdk.BootstrapDone():
	case <-ctx.Done():
		return
	}
	for _, def := range eng.derived {
		if ctx.Err() != nil {
			return
		}
		d.adoptDerivedSetup(ctx, eng.sdk, def)
	}
}

// adoptDerivedSetup runs the restore gates for one entry and surfaces
// what the converged registry carries.
func (d *deps) adoptDerivedSetup(ctx context.Context, sdk *anysyncsdk.SDK, def resolvedDerivedSpace) {
	// Local state first — offline-first: a space this device already
	// carries answers the "does it exist" question with no network at
	// all, and its registry is readable immediately.
	if !derivedSpaceListed(ctx, sdk, def.SpaceId) {
		// Unknown locally: the account may still have it on another
		// device, so converge the space list before concluding
		// anything.
		waitCtx, cancel := context.WithTimeout(ctx, derivedListSyncWait)
		err := sdk.Spaces().WaitListSynced(waitCtx)
		cancel()
		if err != nil {
			engineLog.Warn("derived space list wait",
				zap.String("name", def.Name), zap.Error(err))
			return
		}
		if !derivedSpaceListed(ctx, sdk, def.SpaceId) {
			return
		}
	}

	sp, err := sdk.Spaces().Get(ctx, def.SpaceId)
	if err != nil {
		engineLog.Warn("derived space open",
			zap.String("name", def.Name), zap.Error(err))
		return
	}
	// The registry lives on the spaceIndex object: without its state
	// projected locally it reads empty, and a client ensuring against
	// an empty registry installs a second root. Returns immediately
	// when the index is already local.
	waitCtx, cancel := context.WithTimeout(ctx, derivedIndexSyncWait)
	err = sp.WaitIndexSynced(waitCtx)
	cancel()
	if err != nil {
		engineLog.Warn("derived space index wait",
			zap.String("name", def.Name), zap.Error(err))
		return
	}

	rows, err := d.bundleResolver().List(ctx, sp)
	if err != nil {
		engineLog.Warn("derived space bundles",
			zap.String("name", def.Name), zap.Error(err))
		return
	}
	for _, b := range rows {
		if len(b.Losers) > 0 {
			// Surfaced, not resolved: the losing roots may hold
			// content only the client that installed them can judge.
			// It reads them from GET /bundles and calls /resolve.
			engineLog.Warn("bundle has unresolved losing roots",
				zap.String("spaceId", sp.Id()), zap.String("bundle", b.Id),
				zap.Strings("losers", b.Losers))
		}
	}
}

// derivedSpaceListed reports whether the account's space list carries a
// usable row for the id (a tombstoned row is not one).
func derivedSpaceListed(ctx context.Context, sdk *anysyncsdk.SDK, spaceId string) bool {
	rows, err := sdk.Spaces().List(ctx)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.Id == spaceId {
			return r.Status != space.StatusDeleted
		}
	}
	return false
}
