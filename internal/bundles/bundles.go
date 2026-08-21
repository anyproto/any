// Package bundles wires the HTTP bundles surface onto the SDK's
// per-space bundles registry (any-sync-sdk docs/bundles.md).
//
// A bundle is one root object registered under a stable id, with every
// setup object hanging off it, so the converged root id transitively
// names the whole install and children are re-derivable on any device.
//
// The root comes in two shapes. A CREATED root gets a fresh id, so two
// devices installing while apart mint two: the registry converges on
// one winner, keeps the others in Bundle.Losers, and children bind by
// ParentId so cleaning up a loser is one cascade delete. A DERIVED
// root (Install.Derived) is computed from the bundle id, so every
// device lands on the same one and no fork is possible — at the price
// of permanence, a derived tree being undeletable.
//
// The server registers nothing of its own: clients declare what they
// install, so this package is a generic engine, not a catalog.
// Deleting a loser is the CLIENT's decision — only it knows whether
// the loser's content was worth merging — so the engine only enforces
// what is decidable without knowing the content: a created install
// waits for the registry to converge before minting a root, and a
// loser is deletable only once it has demonstrably stopped arriving.
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
	// ErrRegistryNotSynced reports that a member could not converge the
	// space's registry before installing, so the install would be made
	// blind. Retryable.
	ErrRegistryNotSynced = errors.New("bundle registry not synced")
	// ErrLoserNotReady reports that a losing root is still arriving:
	// not yet fully synced, or still inside the quiescence window.
	// Retryable.
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
	// Derived installs the bundle on the root DERIVED from its id
	// instead of a created one: the same root id on every device,
	// computed offline, so concurrent installs cannot fork and the
	// convergence gate below has nothing to protect.
	//
	// The price is permanence — a derived tree cannot be deleted, so
	// the bundle can never be uninstalled. For setups that must exist
	// on both sides of a partition (a space's chat, and above all a
	// 1-1's, where nobody is the owner) that is the point; for
	// anything a user may remove it is the wrong trade.
	Derived bool
}

// Resolver installs bundles and deletes their losing roots, carrying
// the timing decisions that must not be re-made from scratch on every
// call.
//
// A losing root is deleted only once it has stopped moving: its tree
// arrives change by change, so a client that merged "everything" from
// a half-arrived loser merged only what had landed. Resolver therefore
// requires the SDK to report the root as fully synced AND waits out
// Grace from the first time this process saw the loser listed.
type Resolver struct {
	// Grace is how long a losing root must have been observed before
	// it may be deleted. Set at construction; tests shorten it.
	Grace time.Duration
	// RetryDelay is the first backoff step of ResolveRetry; each pass
	// doubles it up to a ten-minute cap. Set at construction; tests
	// shorten it.
	RetryDelay time.Duration
	// IndexWait bounds the convergence wait an install runs before
	// minting a root — for a derived install too, where expiring it
	// is not a refusal but is not free either (see converge). Set at
	// construction; tests shorten it.
	IndexWait time.Duration
	// Quiescent reports whether a root has stopped receiving changes,
	// so what is projected locally is the whole of it. Set at
	// construction to the SDK's per-object sync state; tests
	// substitute it, since an offline device never settles.
	Quiescent func(sp space.Space, objectId string) bool

	mu sync.Mutex
	// firstSeen dates each loser's first OBSERVATION, keyed
	// spaceId/rootId — warmed wherever losers are surfaced, not only
	// where they are resolved, so the grace window runs while the
	// client is deciding rather than starting over at its first
	// resolve call. Process-local: a restart restarts the clock, which
	// only ever delays a deletion.
	firstSeen map[string]time.Time
	// retrying dedups in-flight background retries per loser, so a
	// polling client cannot stack a loop per request.
	retrying map[string]struct{}
}

const (
	// DefaultGrace is the quiescence delay before a losing root may be
	// deleted.
	DefaultGrace = 5 * time.Minute
	// DefaultIndexWait bounds the registry-convergence wait on the
	// install path. Short: it rides a client request, and refusing is
	// correct — the client retries.
	DefaultIndexWait = 30 * time.Second

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
		IndexWait:  DefaultIndexWait,
		Quiescent:  syncQuiescent,
		firstSeen:  map[string]time.Time{},
		retrying:   map[string]struct{}{},
	}
}

// Ensure adopts the space's existing install or creates one, reporting
// which happened.
//
// Adoption is a pure read — no registry write, so a reader or guest
// member can resolve an install they may not create.
//
// Installing waits for the registry to converge first. It rides the
// space's index tree, and a member that ensures against state it has
// not synced yet reads "nothing installed" and mints a root competing
// with the one already out there. WaitIndexSynced is the gate rather
// than a bare head-sync round: a nil round is not proof of convergence
// (any-sync swallows per-peer failures), while the wait also demands
// the Synced rollup — and its local fast path keeps an offline owner
// of an already-seeded space instant.
//
// When the wait cannot complete, who is asking decides: the space's
// OWNER installs anyway — nobody else could have installed into a
// space only this account has, and its own devices converge through
// the registry — while any other member is refused with
// ErrRegistryNotSynced rather than left to fork. A DERIVED install is
// never refused: its root id is a pure function of the bundle id, so
// there is no competing root to mint.
//
// createCtx runs the create-and-register section and should outlive
// the caller's request: a cancellation between minting the root and
// registering it leaves an orphan object nothing references.
//
// The winner is provisional until the space syncs. ErrRootNotLocal
// means a winner exists but its tree has not arrived, so there is no
// id worth handing back yet — except for a derived winner, which this
// device mints for itself instead of refusing.
func (r *Resolver) Ensure(ctx, createCtx context.Context, sp space.Space, inst Install) (space.Bundle, bool, error) {
	existing, adopted, err := r.tryAdopt(ctx, sp, inst)
	if err != nil || adopted {
		return existing, false, err
	}
	if existing.RootId == "" {
		if err := r.converge(ctx, sp, inst); err != nil {
			return space.Bundle{}, false, err
		}
		// The converged registry may name a winner the pre-read could
		// not see.
		if existing, adopted, err = r.tryAdopt(ctx, sp, inst); err != nil || adopted {
			return existing, false, err
		}
	}

	req := space.EnsureBundleRequest{Id: inst.Id, Name: inst.Name}
	var created string
	if inst.Derived {
		req.DerivedRoot = true
		req.RootTypes = inst.RootTypes
		req.RootProperties = inst.RootProperties
	} else {
		req.NewRoot = func(ctx context.Context) (string, error) {
			rootId, err := sp.Objects().Create(ctx, space.CreateObjectOpts{
				Types:             inst.RootTypes,
				InitialProperties: inst.RootProperties,
			})
			created = rootId
			return rootId, err
		}
	}
	b, registered, err := sp.Bundles().Ensure(createCtx, req)
	if err != nil {
		return space.Bundle{}, false, fmt.Errorf("bundle %s: ensure: %w", inst.Id, err)
	}
	r.observe(sp, b)

	// Installed means THIS call's root won — a concurrent install can
	// have registered first, in which case ours is already a loser.
	// For a derived install there is no such contest, so the SDK's own
	// "did this call register it" is the exact answer: both sides of a
	// partition can report true for the one root they share, and
	// materializing a root someone else registered reports false.
	installed := created != "" && b.RootId == created
	if inst.Derived {
		installed = registered
	}
	if err := rootLocal(ctx, sp, b.RootId); err != nil {
		return b, false, err
	}
	return b, installed, nil
}

// converge runs the pre-install convergence wait and decides what an
// expired one means for this caller.
func (r *Resolver) converge(ctx context.Context, sp space.Space, inst Install) error {
	waitCtx, cancel := context.WithTimeout(ctx, r.IndexWait)
	err := sp.WaitIndexSynced(waitCtx)
	cancel()
	if err == nil {
		return nil
	}
	switch {
	case inst.Derived:
		// Not a gate — a derived install cannot mint a competing id,
		// and refusing would deadlock the case it exists for: in a 1-1
		// neither writer can ever take the owner escape below.
		//
		// It is not free either. If the space already carries a
		// CREATED install this device has not seen, the derived claim
		// demotes it to a loser on every replica, irreversibly (the
		// derived root cannot be deleted). The wait is what narrows
		// that window, which is why it is the full one and not a token
		// pause; proceeding past it accepts the demotion.
	case sp.Info().OwnRole == space.PermissionOwner:
		// Nobody else could have installed into a space only this
		// account has; its own devices converge through the registry.
	default:
		return fmt.Errorf("bundle %s: %w", inst.Id, ErrRegistryNotSynced)
	}
	log.Warn("installing without a converged registry",
		zap.String("bundle", inst.Id), zap.String("spaceId", sp.Id()),
		zap.Bool("derived", inst.Derived), zap.Error(err))
	return nil
}

// tryAdopt is the pure-read half of Ensure: adopted=true when the
// space's install can be handed back as it is.
//
// An empty row means nothing is installed here and minting is still on
// the table. A winner whose tree has not arrived is normally a refusal
// — its id would reject every write — EXCEPT when it is the canonical
// derived root of a derived install: that root is this device's to
// mint, so the install path re-materializes the very same id instead
// of making the client poll for a tree it could produce itself.
func (r *Resolver) tryAdopt(ctx context.Context, sp space.Space, inst Install) (space.Bundle, bool, error) {
	b, err := r.adopt(ctx, sp, inst.Id)
	switch {
	case err == nil:
		return b, true, nil
	case errors.Is(err, ErrNotInstalled):
		return space.Bundle{}, false, nil
	case inst.Derived && b.Derived && errors.Is(err, ErrRootNotLocal):
		return b, false, nil
	default:
		return space.Bundle{}, false, err
	}
}

// adopt returns a live install without writing anything. On
// ErrRootNotLocal it still returns the row — the caller may be able to
// materialize that root itself.
func (r *Resolver) adopt(ctx context.Context, sp space.Space, bundleId string) (space.Bundle, error) {
	b, err := r.Get(ctx, sp, bundleId)
	if err != nil {
		return space.Bundle{}, err
	}
	if b.RootId == "" {
		return space.Bundle{}, ErrNotInstalled
	}
	if err := rootLocal(ctx, sp, b.RootId); err != nil {
		return b, err
	}
	return b, nil
}

// Get returns the space's registry row for the bundle id.
// ErrNotInstalled when there is none locally.
func (r *Resolver) Get(ctx context.Context, sp space.Space, bundleId string) (space.Bundle, error) {
	b, err := sp.Bundles().Get(ctx, bundleId)
	if errors.Is(err, space.ErrBundleUnknown) {
		return space.Bundle{}, fmt.Errorf("bundle %s: %w", bundleId, ErrNotInstalled)
	}
	if err != nil {
		return space.Bundle{}, fmt.Errorf("bundle %s: get: %w", bundleId, err)
	}
	r.observe(sp, b)
	return b, nil
}

// List returns every live registry row in the space.
func (r *Resolver) List(ctx context.Context, sp space.Space) ([]space.Bundle, error) {
	rows, err := sp.Bundles().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("bundles: list: %w", err)
	}
	for _, b := range rows {
		r.observe(sp, b)
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
		r.forget(sp, loserRootId)
		return nil // already resolved
	}
	if !r.ready(sp, loserRootId) {
		return ErrLoserNotReady
	}
	if err := sp.Bundles().ResolveLoser(ctx, bundleId, loserRootId); err != nil {
		if errors.Is(err, space.ErrLoserNotSynced) {
			return ErrLoserNotReady
		}
		return fmt.Errorf("bundle %s: resolve %s: %w", bundleId, loserRootId, err)
	}
	r.forget(sp, loserRootId)
	return nil
}

// ResolveRetry keeps trying one loser with backoff, for the window a
// client has already asked for: the merge decision was made, only the
// timing is missing. Blocking — callers own the goroutine, and must
// pass a context that outlives the request that triggered it. At most
// one loop per loser; dropped on restart, and the client's retry
// re-arms it.
func (r *Resolver) ResolveRetry(ctx context.Context, sp space.Space, bundleId, loserRootId string) {
	key := sp.Id() + "/" + bundleId + "/" + loserRootId
	if !r.claimRetry(key) {
		return
	}
	defer r.releaseRetry(key)

	delay := r.RetryDelay
	for range retryAttempts {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMaxDelay)

		err := r.Resolve(ctx, sp, bundleId, loserRootId)
		switch {
		case err == nil,
			// Verdicts retrying cannot change.
			errors.Is(err, space.ErrBundleNotLoser),
			errors.Is(err, ErrNotInstalled):
			return
		}
		log.Warn("loser resolution deferred",
			zap.String("bundle", bundleId), zap.String("spaceId", sp.Id()),
			zap.String("root", loserRootId), zap.Error(err))
	}
}

// observe dates every loser the row carries, so the quiescence window
// runs from when the conflict became visible rather than from the
// client's first resolve attempt.
func (r *Resolver) observe(sp space.Space, b space.Bundle) {
	if len(b.Losers) == 0 {
		return
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, loser := range b.Losers {
		key := sp.Id() + "/" + loser
		if _, ok := r.firstSeen[key]; !ok {
			r.firstSeen[key] = now
		}
	}
}

// ready reports whether a losing root has settled enough for its local
// state to be the whole of it: fully synced per the SDK, and observed
// for at least Grace.
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

// forget drops a resolved loser's observation, so the map does not
// grow with roots that no longer exist.
func (r *Resolver) forget(sp space.Space, loserRootId string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.firstSeen, sp.Id()+"/"+loserRootId)
}

func (r *Resolver) claimRetry(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, running := r.retrying[key]; running {
		return false
	}
	r.retrying[key] = struct{}{}
	return true
}

func (r *Resolver) releaseRetry(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.retrying, key)
}

// syncQuiescent is the default Quiescent probe. Only an explicit
// "synced" counts: the SDK reports unknown for a tree that has never
// produced a status hook — which is exactly the state after a restart,
// and the state of a tree that has not arrived — so treating unknown
// as settled would license deleting a loser whose content nobody here
// has ever seen.
func syncQuiescent(sp space.Space, objectId string) bool {
	return sp.SyncStatus().Object(objectId).State == space.SyncStateSynced
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
// A DERIVED root cannot be a parent — any-sync rejects a derived
// object as a ParentId — so a derived install's children hang off it
// by seed instead, with the root id folded in. They converge just as
// well (the root id is canonical), and the cascade the parent binding
// buys is moot on a root that can never be deleted.
//
// Seeds are permanent — bump the version suffix for a successor object
// rather than reusing one.
func Child(ctx context.Context, sp space.Space, b space.Bundle, seed string, types ...string) (string, error) {
	if b.RootId == "" {
		return "", fmt.Errorf("bundles: child %q: empty root id", seed)
	}
	opts := space.DeriveObjectOpts{
		Seed:     []byte(seed),
		ParentId: b.RootId,
		Types:    types,
	}
	if b.Derived {
		opts = space.DeriveObjectOpts{
			Seed:  []byte(b.RootId + "/" + seed),
			Types: types,
		}
	}
	objectId, err := sp.Objects().Derive(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("bundles: derive child %q: %w", seed, err)
	}
	return objectId, nil
}
