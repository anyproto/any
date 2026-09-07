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
	"strings"
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
	// XKey is the root type's handle (`type.xkey`) — what a client
	// resolves the type by and what other declarations' relation
	// targets name. An XKey alone declares a marker type (no columns,
	// no parts). Written on install; adopt never patches it.
	XKey string
	// Parts are declared on the root at install (derived or created);
	// the root then defines a type (typeId = rootId) whose carriers
	// take the datasets.
	Parts []space.PartDraft
	// Properties are declared on the root at install with ids derived
	// from (root, xKey), so concurrent installs mint one column per
	// handle. Every draft carries an XKey.
	Properties []space.PropertyDraft
	// Layout, Weight and Hidden seed the root type's metadata on
	// install; adopt never patches them. Hidden is explicit. They need
	// Parts or Properties — the SDK refuses them alone.
	Layout map[string]any
	Weight int
	Hidden bool
	// SelfTyped makes the root also CARRY the type it declares
	// (any.types gains the root's own id): the root is then an
	// instance of itself and hosts the type's datasets and values —
	// a records host (favourites entries, an app's layouts). Off, the
	// root is the definition only: a type OTHER objects carry (a wiki,
	// a person) never matches a query for itself and takes none of its
	// own parts. The SDK implies it for a part declaring a reserved
	// module (the root is the type's sole carrier — the general chat)
	// and on the tech space; needs a type declaration.
	SelfTyped bool
	// SystemInstall marks the server's own catalog install: it lifts the
	// reserved-module refusal (the SDK's SystemInstall ensure option).
	// Never set from client input.
	SystemInstall bool
}

// DeclaresType reports whether the install makes the root a type
// definition — Parts, Properties or an XKey (the SDK's
// EnsureBundleRequest.DeclaresType rule).
func (i Install) DeclaresType() bool {
	return len(i.Parts) > 0 || len(i.Properties) > 0 || i.XKey != ""
}

// ReservedIdPrefix marks the bundle ids the server's embedded catalog
// owns. A client install under it is refused; the prefix is the rule,
// the catalog its only writer.
const ReservedIdPrefix = "system:"

// ReservedId reports whether a bundle id is under the server's prefix.
func ReservedId(id string) bool { return strings.HasPrefix(id, ReservedIdPrefix) }

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
	// OfflineIndexWait replaces IndexWait when no peer is connected.
	// The wait buys information only from peers we can reach; with
	// none, thirty seconds of retrying learns exactly what the first
	// second did. Set at construction; tests shorten it.
	OfflineIndexWait time.Duration
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
	// DefaultOfflineIndexWait is the same wait with nobody to hear
	// from: long enough for a peer that is mid-dial to land, short
	// enough that an offline device is not stalled for nothing.
	DefaultOfflineIndexWait = 3 * time.Second

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
		Grace:            grace,
		RetryDelay:       retryDelay,
		IndexWait:        DefaultIndexWait,
		OfflineIndexWait: DefaultOfflineIndexWait,
		Quiescent:        syncQuiescent,
		firstSeen:        map[string]time.Time{},
		retrying:         map[string]struct{}{},
	}
}

// Reset forgets every loser observation and in-flight retry claim: the
// account behind the spaces is going away, so nothing recorded here
// applies to what boots next. Callers join the retry goroutines
// first; a straggler's release only deletes an absent key.
func (r *Resolver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.firstSeen = map[string]time.Time{}
	r.retrying = map[string]struct{}{}
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
	return r.ensure(ctx, createCtx, sp, inst, r.oneWait(ctx, sp), nil)
}

// SetupResult is one install a Setup call ensured, in order.
type SetupResult struct {
	Install   Install
	Bundle    space.Bundle
	Installed bool
}

// SetupError names the install a Setup call failed on. The results
// before it stand — every step is idempotent, so the caller re-runs
// the whole setup and resumes.
type SetupError struct {
	Install Install
	Err     error
}

func (e *SetupError) Error() string { return "bundle " + e.Install.Id + ": " + e.Err.Error() }
func (e *SetupError) Unwrap() error { return e.Err }

// Setup ensures an ordered list of installs — a usecase and its
// dependencies — against ONE registry-convergence wait. Ensure waits
// before every install it cannot adopt, so a non-owner with an
// unconverged registry would otherwise pay the full wait per bundle
// before its refusal; here the first install that needs the verdict
// pays it, the rest reuse it.
//
// beforeInstall runs before a root is minted for an entry (never on
// adopt, never on a declaration heal): the caller's own pre-install
// rules, such as a handle-conflict check. A non-nil error stops the
// walk at that entry.
func (r *Resolver) Setup(ctx, createCtx context.Context, sp space.Space, installs []Install,
	beforeInstall func(ctx context.Context, sp space.Space, inst Install) error) ([]SetupResult, error) {
	wait := r.oneWait(ctx, sp)
	out := make([]SetupResult, 0, len(installs))
	for _, inst := range installs {
		b, installed, err := r.ensure(ctx, createCtx, sp, inst, wait, beforeInstall)
		if err != nil {
			return out, &SetupError{Install: inst, Err: err}
		}
		out = append(out, SetupResult{Install: inst, Bundle: b, Installed: installed})
	}
	return out, nil
}

// oneWait memoizes the registry-convergence wait for one call: the
// first caller pays it, later ones read the verdict.
func (r *Resolver) oneWait(ctx context.Context, sp space.Space) func() error {
	var (
		once sync.Once
		err  error
	)
	return func() error {
		once.Do(func() {
			waitCtx, cancel := context.WithTimeout(ctx, r.waitFor(sp))
			err = sp.WaitIndexSynced(waitCtx)
			cancel()
		})
		return err
	}
}

func (r *Resolver) ensure(ctx, createCtx context.Context, sp space.Space, inst Install, wait func() error,
	beforeInstall func(ctx context.Context, sp space.Space, inst Install) error) (space.Bundle, bool, error) {
	// A type-declaring request reaches the SDK's Ensure only when the
	// adopted root does not carry the declaration yet (the SDK heals
	// on its own adopt path: parts when none exist, properties per
	// handle). Once the declaration exists it is first-write-pinned, so
	// adoption stays the pure read the contract promises — a
	// reader/guest re-running the documented idempotent ensure must
	// not land in Ensure's write gate.
	settled := func(b space.Bundle) bool {
		if !inst.DeclaresType() {
			return true
		}
		missing := false
		if len(inst.Parts) > 0 {
			defs, err := sp.Types().Parts(ctx, b.RootId)
			if err != nil {
				// A transient read error must not push the caller into
				// the SDK Ensure's write gate — adopt; the declaration
				// heals on a later ensure.
				return true
			}
			missing = len(defs) == 0
		}
		if !missing && inst.XKey != "" {
			// A handle the root lacks (an install that predates it) is
			// filled by the SDK's adopt path, like an absent property.
			info, err := sp.Types().Get(ctx, b.RootId)
			if err != nil {
				return true
			}
			missing = info.XKey == ""
		}
		if !missing && len(inst.Properties) > 0 {
			props, err := sp.Types().Properties(ctx, b.RootId)
			if err != nil {
				return true
			}
			have := make(map[string]struct{}, len(props))
			for _, p := range props {
				have[p.XKey] = struct{}{}
			}
			for _, p := range inst.Properties {
				if _, ok := have[p.XKey]; !ok {
					// Absent by handle — the SDK's own rule (a definition
					// is present when its id exists, live or tombstoned,
					// or a live one carries the handle). A handle whose
					// definition was removed on purpose falls through
					// too: the SDK sees the tombstone and writes nothing,
					// so the cost is one no-op Ensure per call.
					missing = true
					break
				}
			}
		}
		if !missing {
			return true
		}
		switch sp.Info().OwnRole {
		case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
			return false // fall through so the SDK heals the declaration
		default:
			// A reader/guest cannot heal and must never hit the write
			// gate re-running the documented idempotent ensure.
			return true
		}
	}
	existing, adopted, err := r.tryAdopt(ctx, sp, inst)
	if err != nil || (adopted && settled(existing)) {
		return existing, false, err
	}
	if existing.RootId == "" {
		if err := r.converge(ctx, sp, inst, wait); err != nil {
			return space.Bundle{}, false, err
		}
		// The converged registry may name a winner the pre-read could
		// not see.
		if existing, adopted, err = r.tryAdopt(ctx, sp, inst); err != nil || (adopted && settled(existing)) {
			return existing, false, err
		}
		// A genuine install (no winner anywhere): the caller's own
		// pre-install rules run now, after the wait and before the
		// root is minted.
		if existing.RootId == "" && beforeInstall != nil {
			if err := beforeInstall(ctx, sp, inst); err != nil {
				return space.Bundle{}, false, err
			}
		}
	}

	req := space.EnsureBundleRequest{
		Id: inst.Id, Name: inst.Name, XKey: inst.XKey,
		Parts: inst.Parts, Properties: inst.Properties,
		Layout: inst.Layout, Weight: inst.Weight, Hidden: inst.Hidden,
		SelfTyped: inst.SelfTyped,
	}
	var opts []space.EnsureOption
	if inst.SystemInstall {
		opts = append(opts, space.SystemInstall())
	}
	var created string
	if inst.Derived {
		req.DerivedRoot = true
		req.RootTypes = inst.RootTypes
		req.RootProperties = inst.RootProperties
	} else if req.DeclaresType() {
		// SDK-minted created root: Ensure creates the object, stamps
		// it as its own type with the root types and seeded values in
		// one change, and declares — the only create the tech space
		// allows, and the same shape everywhere.
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
	b, registered, err := sp.Bundles().Ensure(createCtx, req, opts...)
	if err != nil {
		return space.Bundle{}, false, fmt.Errorf("bundle %s: ensure: %w", inst.Id, err)
	}
	r.observe(sp, b)

	// Installed means THIS call minted the returned winner. For a
	// created root the SDK's registered bool alone is weaker — it
	// reports that this call wrote a registering change, but an
	// inbound install landing between its read and apply can leave our
	// fresh root a loser while the returned RootId is someone else's
	// winner. A derived install has no such contest, so registered is
	// the exact answer: both sides of a partition can report true for
	// the one root they share, and materializing a root someone else
	// registered reports false.
	installed := registered && created != "" && b.RootId == created
	if inst.Derived || req.DeclaresType() {
		// Derived: registered is exact. SDK-minted created root: the
		// minted id is not observable here, so registered is the
		// answer, with the same narrow inbound-race weakness the
		// created-root comment above describes.
		installed = registered
	}
	if err := rootLocal(ctx, sp, b.RootId); err != nil {
		return b, false, err
	}
	return b, installed, nil
}

// converge runs the pre-install convergence wait (memoized by the
// caller — one wait per Ensure or Setup) and decides what an expired
// one means for this caller.
func (r *Resolver) converge(ctx context.Context, sp space.Space, inst Install, wait func() error) error {
	err := wait()
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

// waitFor is how long to hold the convergence wait open: the full
// bound while a peer is connected, the offline bound while none is.
// A head-sync round against nobody answers the same way every time,
// so the long wait would spend an offline device's whole deadline
// learning what its first attempt already told it — and the install
// it gates either proceeds regardless (derived, owner) or is refused
// either way (any other member).
func (r *Resolver) waitFor(sp space.Space) time.Duration {
	st := sp.SyncStatus().Space()
	if st.NetworkPeers == 0 && st.LocalPeers == 0 {
		return r.OfflineIndexWait
	}
	return r.IndexWait
}

// WaitConverged runs the registry-convergence wait the install gate
// uses, bounded by the same knobs (offline fast expiry), and reports
// whether the registry converged. The read-side lock for the bundles
// surface: a read that answers after true reports definitive absence;
// after false the caller labels the read provisional.
func (r *Resolver) WaitConverged(ctx context.Context, sp space.Space) bool {
	waitCtx, cancel := context.WithTimeout(ctx, r.waitFor(sp))
	defer cancel()
	return sp.WaitIndexSynced(waitCtx) == nil
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
// so a local root always has an objects row; a root registered by
// another device has none until its tree arrives. One primary-key
// read, available on the tech handle too.
func rootLocal(ctx context.Context, sp space.Space, rootId string) error {
	if _, err := sp.Objects().Get(ctx, rootId); err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return ErrRootNotLocal
		}
		return fmt.Errorf("bundle root probe: %w", err)
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
