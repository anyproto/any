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
// real content — so it runs through Resolver, which enforces a
// quiescence delay, a per-install Merge (salvage) and Keep (refuse)
// hook, and remembers its verdicts for the process's lifetime.
package bundles

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"
)

var log = logger.NewNamed("bundles")

// ErrNotInstalled reports that the space carries no usable install:
// nobody has registered a root yet, or the registered root's tree has
// not reached this device. Callers surface it as "not yet", never as a
// failure — the answer changes as the space syncs.
var ErrNotInstalled = errors.New("bundle not installed")

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
	// SoleInstaller reports whether this device may create the root.
	// Nil = any member installs. Every other member only adopts what
	// the sole installer registered, which is what keeps two members
	// from installing content-carrying roots the app cannot merge back
	// together. The rule must be decidable from local state and give
	// the same answer on every member, so exactly one of them says yes.
	SoleInstaller func(info space.SpaceInfo, accountId string) bool
	// Merge lifts whatever content matters out of a losing root (and
	// its derived children) into the winner's, before the loser is
	// deleted. Nil when the install has nothing worth salvaging. Runs
	// on every resolution attempt, so it must be idempotent.
	Merge func(ctx context.Context, sp space.Space, winnerRootId, loserRootId string) error
	// Keep reports that a loser must NOT be deleted — the escape hatch
	// for content Merge cannot carry over. Nil = always deletable. A
	// true verdict is remembered for the process's lifetime, so the
	// probe stops running on every pass.
	Keep func(ctx context.Context, sp space.Space, loserRootId string) (bool, error)
}

// Resolver installs bundles and resolves their losing roots, carrying
// the decisions that must not be re-made from scratch on every call.
//
// A losing root is deleted only once it has stopped moving: its tree
// arrives change by change, so "no content here" read too early is a
// statement about what has synced, not about what exists. Resolver
// therefore waits out Grace from its own first sight of a loser and
// skips anything the SDK still reports as syncing.
type Resolver struct {
	// Grace is how long a losing root must have been observed before
	// it may be deleted. Set at construction; tests shorten it.
	Grace time.Duration
	// Quiescent reports whether a losing root has stopped receiving
	// changes, so what is projected locally is the whole of it. Set at
	// construction to the SDK's per-object sync state; tests
	// substitute it, since an offline device never reaches a settled
	// state.
	Quiescent func(sp space.Space, objectId string) bool

	mu sync.Mutex
	// firstSeen dates each loser's first observation, keyed
	// spaceId/rootId. Process-local: a restart restarts the clock,
	// which only ever delays a deletion.
	firstSeen map[string]time.Time
	// kept memoizes Keep's true verdicts so a loser the app refuses to
	// delete stops costing a probe on every pass.
	kept map[string]struct{}
}

const (
	// DefaultGrace is the quiescence delay before a losing root may be
	// deleted.
	DefaultGrace = 5 * time.Minute

	// Retry schedule for the async path: a loser installed on another
	// device cannot be deleted until its tree has synced here, which
	// can take a while and can never happen offline.
	retryDelay    = 30 * time.Second
	retryMaxDelay = 10 * time.Minute
	retryAttempts = 6
)

// NewResolver builds a Resolver with the given quiescence delay.
func NewResolver(grace time.Duration) *Resolver {
	return &Resolver{
		Grace:     grace,
		Quiescent: syncQuiescent,
		firstSeen: map[string]time.Time{},
		kept:      map[string]struct{}{},
	}
}

// Ensure returns the space's converged registry row for the install,
// creating the root when this device is the sole installer and nobody
// has registered one yet. Fully local: an adopted install is a read
// and writes nothing.
//
// ErrNotInstalled means "not yet" — no row, or a winner whose tree has
// not reached this device (naming it would hand callers an id that
// rejects writes). The winner is provisional until the space syncs.
func (r *Resolver) Ensure(ctx context.Context, sp space.Space, info space.SpaceInfo, inst Install, accountId string) (space.Bundle, error) {
	b, err := sp.Bundles().Get(ctx, inst.Id)
	switch {
	case err == nil:
	case !errors.Is(err, space.ErrBundleUnknown):
		return space.Bundle{}, fmt.Errorf("bundle %s: get: %w", inst.Id, err)
	case inst.SoleInstaller != nil && !inst.SoleInstaller(info, accountId):
		return space.Bundle{}, ErrNotInstalled
	default:
		b, err = sp.Bundles().Ensure(ctx, space.EnsureBundleRequest{
			Id:   inst.Id,
			Name: inst.Name,
			NewRoot: func(ctx context.Context) (string, error) {
				return sp.Objects().Create(ctx, space.CreateObjectOpts{Types: inst.RootTypes})
			},
		})
		if err != nil {
			return space.Bundle{}, fmt.Errorf("bundle %s: ensure: %w", inst.Id, err)
		}
	}
	if b.RootId == "" {
		return space.Bundle{}, ErrNotInstalled
	}
	local, err := rootLocal(ctx, sp, b.RootId)
	if err != nil {
		return space.Bundle{}, fmt.Errorf("bundle %s: root probe: %w", inst.Id, err)
	}
	if !local {
		return space.Bundle{}, ErrNotInstalled
	}
	return b, nil
}

// EnsureResolved is Ensure plus one inline resolution pass over the
// bundle's losing roots. Resolution failures are logged, not returned:
// they are always retryable, and the install itself is usable.
func (r *Resolver) EnsureResolved(ctx context.Context, sp space.Space, info space.SpaceInfo, inst Install, accountId string) (space.Bundle, error) {
	b, err := r.Ensure(ctx, sp, info, inst, accountId)
	if err != nil {
		return space.Bundle{}, err
	}
	if len(b.Losers) > 0 {
		if _, _, err := r.Resolve(ctx, sp, inst, b); err != nil {
			log.Warn("loser resolution deferred",
				zap.String("bundle", inst.Id), zap.String("spaceId", sp.Id()), zap.Error(err))
		}
	}
	return b, nil
}

// Resolve merges then deletes the bundle's losing roots, returning how
// many it deleted and how many are still pending — unresolved and not
// refused by Keep. A pending loser is normal: it may still be syncing,
// or be waiting out the quiescence delay.
//
// Errors are expected and retryable: the SDK can only delete a loser
// whose tree has synced to this device, so a loser installed on
// another device stays unresolvable until it arrives. Every step is
// idempotent.
func (r *Resolver) Resolve(ctx context.Context, sp space.Space, inst Install, b space.Bundle) (resolved, pending int, err error) {
	var errs []error
	for _, loser := range b.Losers {
		key := sp.Id() + "/" + loser
		if r.isKept(key) {
			continue
		}
		// Quiescence: a tree that is still arriving has not shown its
		// content yet, and neither Keep nor Merge can judge what is
		// not here.
		if !r.ripe(key) || !r.Quiescent(sp, loser) {
			pending++
			continue
		}
		if inst.Keep != nil {
			keep, kerr := inst.Keep(ctx, sp, loser)
			if kerr != nil {
				pending++
				errs = append(errs, fmt.Errorf("bundle %s: keep check %s: %w", inst.Id, loser, kerr))
				continue
			}
			if keep {
				r.markKept(key)
				continue
			}
		}
		if inst.Merge != nil {
			if merr := inst.Merge(ctx, sp, b.RootId, loser); merr != nil {
				pending++
				errs = append(errs, fmt.Errorf("bundle %s: merge %s: %w", inst.Id, loser, merr))
				continue
			}
		}
		if rerr := sp.Bundles().ResolveLoser(ctx, inst.Id, loser); rerr != nil {
			pending++
			errs = append(errs, fmt.Errorf("bundle %s: resolve %s: %w", inst.Id, loser, rerr))
			continue
		}
		resolved++
	}
	return resolved, pending, errors.Join(errs...)
}

// ResolveRetry runs Resolve until nothing is pending, backing off
// between passes and re-reading the registry so each pass merges into
// the current winner. Blocking — callers own the goroutine, and must
// pass a context that outlives the request that triggered it.
func (r *Resolver) ResolveRetry(ctx context.Context, sp space.Space, inst Install, b space.Bundle) {
	delay := retryDelay
	for range retryAttempts {
		_, pending, err := r.Resolve(ctx, sp, inst, b)
		if err != nil {
			log.Warn("loser resolution deferred",
				zap.String("bundle", inst.Id), zap.String("spaceId", sp.Id()), zap.Error(err))
		}
		if pending == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMaxDelay)
		cur, gerr := sp.Bundles().Get(ctx, inst.Id)
		if gerr != nil {
			log.Warn("registry re-read failed",
				zap.String("bundle", inst.Id), zap.String("spaceId", sp.Id()), zap.Error(gerr))
			continue
		}
		b = cur
	}
}

// syncQuiescent is the default Quiescent probe: a tree the SDK still
// reports as syncing is mid-arrival, and neither Keep nor Merge can
// judge content that is not here yet.
func syncQuiescent(sp space.Space, objectId string) bool {
	return sp.SyncStatus().Object(objectId).State != space.SyncStateSyncing
}

// ripe records the loser's first observation and reports whether the
// quiescence delay has elapsed since.
func (r *Resolver) ripe(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	first, ok := r.firstSeen[key]
	if !ok {
		first = time.Now()
		r.firstSeen[key] = first
	}
	return time.Since(first) >= r.Grace
}

func (r *Resolver) isKept(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.kept[key]
	return ok
}

func (r *Resolver) markKept(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kept[key] = struct{}{}
}

// rootLocal reports whether the root object's tree has been projected
// on this device. The SDK stamps `any.name` on every registered root,
// so a local root always has a property record; a root registered by
// another device has none until its tree arrives. One primary-key read.
func rootLocal(ctx context.Context, sp space.Space, rootId string) (bool, error) {
	rec, err := sp.Properties().Get(ctx, rootId)
	if err != nil {
		return false, err
	}
	return rec != nil, nil
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
