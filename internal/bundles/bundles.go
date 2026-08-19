// Package bundles wires this app's installs onto the SDK's per-space
// bundles registry (any-sync-sdk docs/bundles.md).
//
// An install is one non-derived root object registered under a stable
// bundle id; every setup object hangs off it as a derived child
// (DeriveObjectOpts.ParentId), so the converged root id transitively
// names the whole install, children are re-derivable on any device,
// and cleaning up a losing concurrent install is one cascade delete.
//
// Two devices installing while offline each mint a root; the registry
// converges on one winner and keeps the others in Bundle.Losers.
// Resolving a loser is always an app decision — the loser can hold
// real content — so it runs through ResolveLosers with a per-install
// Merge (salvage) and Keep (refuse) hook.
package bundles

import (
	"context"
	"errors"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"
)

// Install describes one bundle this app installs into a space.
type Install struct {
	// Id is the registry record id — permanent and versioned
	// ("bao/v1"). Record deletes are rejected by the SDK handler, so a
	// successor install takes a new id rather than reusing this one.
	Id string
	// Name is the display name. The SDK stamps it as `any.name` on a
	// freshly created root.
	Name string
	// RootTypes are attached to the root object at birth, so the
	// install's datasets are writable on it without a separate
	// type-attach step.
	RootTypes []string
	// Merge lifts whatever content matters out of a losing root (and
	// its derived children) into the winner's, before the loser is
	// deleted. Nil when the install has nothing worth salvaging. Runs
	// on every resolution attempt, so it must be idempotent.
	Merge func(ctx context.Context, sp space.Space, winnerRootId, loserRootId string) error
	// Keep reports that a loser must NOT be deleted yet — the escape
	// hatch for content Merge cannot carry over. Nil = always
	// deletable. Re-evaluated on every attempt: a loser that reads
	// empty only because its tree has not synced yet is re-checked
	// before the next deletion attempt.
	Keep func(ctx context.Context, sp space.Space, loserRootId string) (bool, error)
}

// Ensure installs the bundle or adopts the existing install and
// returns the converged registry row. Fully local: with a winner
// already registered no root is created and nothing is written; a
// fresh install creates the root and registers it in one change. The
// winner is provisional until the space syncs — re-read after.
func Ensure(ctx context.Context, sp space.Space, inst Install) (space.Bundle, error) {
	b, err := sp.Bundles().Ensure(ctx, space.EnsureBundleRequest{
		Id:   inst.Id,
		Name: inst.Name,
		NewRoot: func(ctx context.Context) (string, error) {
			return sp.Objects().Create(ctx, space.CreateObjectOpts{Types: inst.RootTypes})
		},
	})
	if err != nil {
		return space.Bundle{}, fmt.Errorf("bundle %s: ensure: %w", inst.Id, err)
	}
	return b, nil
}

// ResolveLosers merges then deletes every losing root of a converged
// bundle, returning how many were resolved. Losers held back by Keep
// are skipped silently — they are content the app refuses to drop, not
// a failure.
//
// Errors are expected and retryable: the SDK can only delete a loser
// whose tree has synced to this device, so a loser installed on
// another device stays unresolvable until it arrives. Callers retry
// after sync; every step is idempotent.
func ResolveLosers(ctx context.Context, sp space.Space, inst Install, b space.Bundle) (int, error) {
	var (
		resolved int
		errs     []error
	)
	for _, loser := range b.Losers {
		if inst.Keep != nil {
			keep, err := inst.Keep(ctx, sp, loser)
			if err != nil {
				errs = append(errs, fmt.Errorf("bundle %s: keep check %s: %w", inst.Id, loser, err))
				continue
			}
			if keep {
				continue
			}
		}
		if inst.Merge != nil {
			if err := inst.Merge(ctx, sp, b.RootId, loser); err != nil {
				errs = append(errs, fmt.Errorf("bundle %s: merge %s: %w", inst.Id, loser, err))
				continue
			}
		}
		if err := sp.Bundles().ResolveLoser(ctx, inst.Id, loser); err != nil {
			errs = append(errs, fmt.Errorf("bundle %s: resolve %s: %w", inst.Id, loser, err))
			continue
		}
		resolved++
	}
	return resolved, errors.Join(errs...)
}

// Child derives one setup object under the bundle root. Deterministic
// per (space, root, seed): every device derives the same id from the
// winner, opens it immediately and lets the content sync in. Deleting
// the root cascade-deletes it.
//
// Seeds are permanent — bump the version suffix for a successor object
// rather than reusing one.
func Child(ctx context.Context, sp space.Space, rootId, seed string, types ...string) (string, error) {
	if rootId == "" {
		return "", fmt.Errorf("bundles: child %q: empty root id", seed)
	}
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:     []byte(seed),
		ParentId: rootId,
		Types:    types,
	})
	if err != nil {
		return "", fmt.Errorf("bundles: derive child %q: %w", seed, err)
	}
	return objectId, nil
}
