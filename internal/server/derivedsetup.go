package server

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/bundles"
)

// Setup of the well-known derived spaces (derivedspaces.go), driven by
// the SDK's bundles registry — any-sync-sdk docs/bundles.md.
//
// Creation and restore are different problems. Creating the space is a
// local act: the id is derived from the account keys, so the setup can
// install immediately and let it sync. Restoring a device that already
// had the space must NOT install a second time — it has to see the
// account's converged state first, or it mints a competing root that
// then has to be merged away. Hence two entry points:
//
//   - creation (POST /v1/spaces/derived/:name): derive the space, run
//     the setup, no waits;
//   - restore (this boot pass): wait for the space list to converge,
//     adopt only what is already there.
//
// Both end in the same idempotent Ensure, which is what makes the
// split safe rather than load-bearing: a device that installs anyway —
// offline, list wait expired — converges on the registry's winner and
// resolves its own root as a loser afterwards.

// Bounds on the restore gates. Both waits retry until their context
// expires, so an offline boot must not sit on them forever: it falls
// through to the local answer and the next boot (or an explicit
// materialize) picks the setup up.
const (
	derivedListSyncWait  = 90 * time.Second
	derivedIndexSyncWait = 90 * time.Second
)

// Loser resolution retries: a loser installed on another device cannot
// be deleted until its tree has synced here, which can take a while
// and can never happen offline. Bounded backoff, dropped at shutdown —
// the next boot retries from scratch, and every step is idempotent.
const (
	loserRetryDelay    = 30 * time.Second
	loserRetryMaxDelay = 10 * time.Minute
	loserRetryAttempts = 6
)

// bootstrapDerivedSetups adopts the setup of every registry entry that
// carries one, for spaces this account already has. It never
// materializes a space: creating one is an explicit act
// (POST /v1/spaces/derived/:name), and a boot pass that created them
// would hand every account a space it never asked for.
//
// Runs in the background off bootAccount — failures are logged, never
// fatal, and nothing here blocks serving.
func (d *deps) bootstrapDerivedSetups(ctx context.Context) {
	select {
	case <-d.sdk.BootstrapDone():
	case <-ctx.Done():
		return
	}
	for _, def := range d.derived {
		if !def.hasInstall() {
			continue
		}
		d.adoptDerivedSetup(ctx, def)
	}
}

// adoptDerivedSetup runs the restore side of the split for one entry.
func (d *deps) adoptDerivedSetup(ctx context.Context, def resolvedDerivedSpace) {
	// Local state first — offline-first: a space this device already
	// carries answers the "does it exist" question with no network at
	// all, and its setup is adopted immediately.
	if !d.derivedSpaceListed(ctx, def.SpaceId) {
		// Unknown locally: the account may still have it on another
		// device, so converge the space list before concluding
		// anything. On timeout (offline) we simply skip — installing
		// blind would be the one thing that creates a conflict.
		waitCtx, cancel := context.WithTimeout(ctx, derivedListSyncWait)
		err := d.sdk.Spaces().WaitListSynced(waitCtx)
		cancel()
		if err != nil {
			engineLog.Warn("derived space list wait",
				zap.String("name", def.Name), zap.Error(err))
			return
		}
		if !d.derivedSpaceListed(ctx, def.SpaceId) {
			return
		}
	}

	sp, err := d.sdk.Spaces().Get(ctx, def.SpaceId)
	if err != nil {
		engineLog.Warn("derived space open",
			zap.String("name", def.Name), zap.Error(err))
		return
	}
	// The registry lives on the spaceIndex object: without its state
	// projected locally, Ensure reads an empty registry and installs a
	// second root. Returns immediately when the index is already local.
	waitCtx, cancel := context.WithTimeout(ctx, derivedIndexSyncWait)
	err = sp.WaitIndexSynced(waitCtx)
	cancel()
	if err != nil {
		engineLog.Warn("derived space index wait",
			zap.String("name", def.Name), zap.Error(err))
		return
	}
	d.runDerivedSetup(ctx, sp, def)
}

// derivedSpaceListed reports whether the account's space list carries a
// usable row for the id (a tombstoned row is not one).
func (d *deps) derivedSpaceListed(ctx context.Context, spaceId string) bool {
	rows, err := d.sdk.Spaces().List(ctx)
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

// runDerivedSetup installs or adopts the entry's bundle and kicks
// conflict resolution when the registry reports losing roots.
// Idempotent — an adopted install writes nothing.
func (d *deps) runDerivedSetup(ctx context.Context, sp space.Space, def resolvedDerivedSpace) {
	b, err := bundles.Ensure(ctx, sp, def.Install)
	if err != nil {
		engineLog.Warn("derived space setup",
			zap.String("name", def.Name), zap.Error(err))
		return
	}
	if len(b.Losers) > 0 {
		go d.resolveLosers(ctx, sp, def.Install, b)
	}
}

// resolveLosers merges and deletes the bundle's losing roots, retrying
// with backoff: a loser created on another device is undeletable until
// its tree reaches this one. Re-reads the registry each attempt so the
// winner it merges into is the current one.
func (d *deps) resolveLosers(ctx context.Context, sp space.Space, inst bundles.Install, b space.Bundle) {
	delay := time.Duration(0)
	for attempt := 0; attempt < loserRetryAttempts; attempt++ {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			cur, err := sp.Bundles().Get(ctx, inst.Id)
			if err != nil {
				continue
			}
			b = cur
		}
		if len(b.Losers) == 0 {
			return
		}
		_, err := bundles.ResolveLosers(ctx, sp, inst, b)
		if err == nil {
			return
		}
		engineLog.Warn("bundle loser resolution deferred",
			zap.String("bundle", inst.Id), zap.String("spaceId", sp.Id()), zap.Error(err))
		delay = min(max(delay*2, loserRetryDelay), loserRetryMaxDelay)
	}
}
