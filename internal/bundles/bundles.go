// Package bundles wires the HTTP bundles surface onto the SDK's
// per-space bundles registry (any-sync-sdk docs/bundles.md).
//
// A bundle is one non-derived root object registered under a stable
// id; every setup object hangs off it as a derived child
// (DeriveObjectOpts.ParentId), so the converged root id transitively
// names the whole install, children are re-derivable on any device,
// and cleaning up a losing concurrent install is one cascade delete.
//
// The server registers nothing of its own: clients declare what they
// install, so this package is a generic engine, not a catalog. Two
// devices installing while apart each mint a root; the registry
// converges on one winner and keeps the others in Bundle.Losers.
// Deleting a loser is the CLIENT's decision — only it knows whether
// the loser's content was worth merging — so the engine only enforces
// what is decidable without knowing the content: a loser is deletable
// once it has stopped arriving.
package bundles

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"
)

var log = logger.NewNamed("bundles")

var (
	// ErrNotInstalled reports that the space carries no live record for
	// the bundle id.
	ErrNotInstalled = errors.New("bundle not installed")
	// ErrRootNotLocal reports that the winning root's tree has not
	// reached this device, so its id is not writable yet. Retryable —
	// the answer changes as the space syncs.
	ErrRootNotLocal = errors.New("bundle root not local")
	// ErrLoserNotReady reports that a losing root is still arriving:
	// still syncing, or still inside the quiescence window. Retryable.
	ErrLoserNotReady = errors.New("bundle loser not ready")
)

// Install is one bundle to register, as the caller declared it.
type Install struct {
	// Id is the registry record id — permanent and versioned.
	Id string
	// Name is the display name. The SDK stamps it as `any.name` on a
	// freshly created root, which is also what puts the root's tree in
	// the head-sync diff.
	Name string
	// RootTypes are attached to the root object at birth.
	RootTypes []string
	// RootProperties seeds the root's property values, keyed
	// typeId → propId → value.
	RootProperties map[string]map[string]any
}

// Resolver installs bundles and deletes their losing roots, carrying
// the timing decisions that must not be re-made from scratch on every
// call.
//
// A losing root is deleted only once it has stopped moving: its tree
// arrives change by change, so a client that merged "everything" from
// a half-arrived loser merged only what had landed. Resolver therefore
// waits out Grace from its own first sight of the loser and refuses
// anything the SDK still reports as syncing.
type Resolver struct {
	// Grace is how long a losing root must have been observed before
	// it may be deleted. Set at construction; tests shorten it.
	Grace time.Duration
	// RetryDelay is the first backoff step of ResolveRetry; each pass
	// doubles it up to a ten-minute cap. Set at construction; tests
	// shorten it.
	RetryDelay time.Duration
	// Quiescent reports whether a root has stopped receiving changes,
	// so what is projected locally is the whole of it. Set at
	// construction to the SDK's per-object sync state; tests
	// substitute it, since an offline device never settles.
	Quiescent func(sp space.Space, objectId string) bool

	mu sync.Mutex
	// firstSeen dates each loser's first observation, keyed
	// spaceId/rootId. Process-local: a restart restarts the clock,
	// which only ever delays a deletion.
	firstSeen map[string]time.Time
}

const (
	// DefaultGrace is the quiescence delay before a losing root may be
	// deleted.
	DefaultGrace = 5 * time.Minute

	// Retry schedule for the background path: a loser installed on
	// another device cannot be deleted until its tree has synced here,
	// which can take a while and can never happen offline.
	retryDelay    = 30 * time.Second
	retryMaxDelay = 10 * time.Minute
	retryAttempts = 6
)

// NewResolver builds a Resolver with the given quiescence delay.
func NewResolver(grace time.Duration) *Resolver {
	return &Resolver{
		Grace:      grace,
		RetryDelay: retryDelay,
		Quiescent:  syncQuiescent,
		firstSeen:  map[string]time.Time{},
	}
}

// Ensure adopts the space's existing install or creates one, reporting
// which happened. Fully local and offline-capable: an adopted install
// is a read that writes nothing, a fresh one creates the root and
// registers it in a single change.
//
// The winner is provisional until the space syncs — a concurrent
// install elsewhere can win, surfacing this root in Losers.
// ErrRootNotLocal means a winner exists but its tree has not arrived,
// so there is no id worth handing back yet.
func (r *Resolver) Ensure(ctx context.Context, sp space.Space, inst Install) (space.Bundle, bool, error) {
	before, err := sp.Bundles().Get(ctx, inst.Id)
	switch {
	case err == nil:
	case !errors.Is(err, space.ErrBundleUnknown):
		return space.Bundle{}, false, fmt.Errorf("bundle %s: get: %w", inst.Id, err)
	}
	installed := before.RootId == ""

	b, err := sp.Bundles().Ensure(ctx, space.EnsureBundleRequest{
		Id:   inst.Id,
		Name: inst.Name,
		NewRoot: func(ctx context.Context) (string, error) {
			return sp.Objects().Create(ctx, space.CreateObjectOpts{
				Types:             inst.RootTypes,
				InitialProperties: inst.RootProperties,
			})
		},
	})
	if err != nil {
		return space.Bundle{}, false, fmt.Errorf("bundle %s: ensure: %w", inst.Id, err)
	}
	if err := rootLocal(ctx, sp, b.RootId); err != nil {
		return b, installed, err
	}
	return b, installed, nil
}

// Get returns the space's registry row for the bundle id.
// ErrNotInstalled when there is none locally.
func (r *Resolver) Get(ctx context.Context, sp space.Space, bundleId string) (space.Bundle, error) {
	b, err := sp.Bundles().Get(ctx, bundleId)
	if errors.Is(err, space.ErrBundleUnknown) {
		return space.Bundle{}, ErrNotInstalled
	}
	if err != nil {
		return space.Bundle{}, fmt.Errorf("bundle %s: get: %w", bundleId, err)
	}
	return b, nil
}

// List returns every live registry row in the space.
func (r *Resolver) List(ctx context.Context, sp space.Space) ([]space.Bundle, error) {
	rows, err := sp.Bundles().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("bundles: list: %w", err)
	}
	return rows, nil
}

// Resolve deletes one losing root, cascading to its derived children.
// The caller has already merged whatever mattered out of it — the
// server never merges, because only the client knows what its content
// means.
//
// Verdicts that cannot change come first: a target that is the current
// winner or was never claimed is ErrBundleNotLoser, and a claimed root
// no longer listed as a loser is already gone, so the call is
// idempotent. What remains is timing — ErrLoserNotReady while the
// loser is still arriving, since a client cannot have merged content
// that has not landed.
func (r *Resolver) Resolve(ctx context.Context, sp space.Space, bundleId, loserRootId string) error {
	b, err := r.Get(ctx, sp, bundleId)
	if err != nil {
		return err
	}
	if loserRootId == b.RootId || !slices.Contains(b.Roots, loserRootId) {
		return fmt.Errorf("bundle %s: %s: %w", bundleId, loserRootId, space.ErrBundleNotLoser)
	}
	if !slices.Contains(b.Losers, loserRootId) {
		return nil // already resolved
	}
	if !r.ready(sp, loserRootId) {
		return ErrLoserNotReady
	}
	if err := sp.Bundles().ResolveLoser(ctx, bundleId, loserRootId); err != nil {
		return fmt.Errorf("bundle %s: resolve %s: %w", bundleId, loserRootId, err)
	}
	return nil
}

// ResolveRetry keeps trying one loser with backoff, for the window a
// client has already asked for: the merge decision was made, only the
// timing is missing. Blocking — callers own the goroutine, and must
// pass a context that outlives the request that triggered it. Dropped
// on restart; the client's retry re-arms it.
func (r *Resolver) ResolveRetry(ctx context.Context, sp space.Space, bundleId, loserRootId string) {
	delay := r.RetryDelay
	for range retryAttempts {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMaxDelay)

		err := r.Resolve(ctx, sp, bundleId, loserRootId)
		if err == nil || errors.Is(err, space.ErrBundleNotLoser) {
			return
		}
		log.Warn("loser resolution deferred",
			zap.String("bundle", bundleId), zap.String("spaceId", sp.Id()),
			zap.String("root", loserRootId), zap.Error(err))
	}
}

// ready reports whether a losing root has settled enough for its local
// state to be the whole of it.
func (r *Resolver) ready(sp space.Space, loserRootId string) bool {
	key := sp.Id() + "/" + loserRootId
	r.mu.Lock()
	first, ok := r.firstSeen[key]
	if !ok {
		first = time.Now()
		r.firstSeen[key] = first
	}
	r.mu.Unlock()
	return time.Since(first) >= r.Grace && r.Quiescent(sp, loserRootId)
}

// syncQuiescent is the default Quiescent probe: a tree the SDK still
// reports as syncing is mid-arrival, and what is stored locally is not
// yet the whole of it.
func syncQuiescent(sp space.Space, objectId string) bool {
	return sp.SyncStatus().Object(objectId).State != space.SyncStateSyncing
}

// rootLocal reports whether the root object's tree has been projected
// on this device. The SDK stamps `any.name` on every registered root,
// so a local root always has a property record; a root registered by
// another device has none until its tree arrives. One primary-key read.
func rootLocal(ctx context.Context, sp space.Space, rootId string) error {
	rec, err := sp.Properties().Get(ctx, rootId)
	if err != nil {
		return fmt.Errorf("bundle root probe: %w", err)
	}
	if rec == nil {
		return ErrRootNotLocal
	}
	return nil
}

// Child derives one setup object under a bundle root. Deterministic
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
