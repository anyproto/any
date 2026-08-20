package api

// Bundles registry — the per-space record of what has been installed
// into a space (a marketplace bundle, a chat, an app's setup). Clients
// register their own; the server keeps no catalog of its own.
//
// Wire note: bundle ids carry a version suffix and therefore a slash
// ("general-chat/v1"). In a path segment they must be percent-encoded
// (`general-chat%2Fv1`); request bodies take them verbatim.

// Bundle is one row of a space's bundles registry.
type Bundle struct {
	// Id is the stable bundle identifier — the record id. Permanent:
	// a successor install takes a new id (record deletes are refused,
	// so a reused id could never be reclaimed).
	Id string `json:"id"`
	// Name is the display name, stamped as `any.name` on the root by
	// whichever device installed it.
	Name string `json:"name,omitempty"`
	// RootId is the winning root object id. Setup objects are derived
	// from it, so this one id names the whole install. Provisional
	// until the space syncs.
	RootId string `json:"rootId"`
	// Roots is every root ever claimed for this bundle — the add-only
	// audit trail. A resolved loser stays listed; its death is
	// recorded by the deletion of its tree.
	Roots []string `json:"roots,omitempty"`
	// Losers is the live conflict set: claimed roots that are neither
	// the winner nor already deleted. Non-empty means two devices
	// installed concurrently — merge what matters out of each, then
	// resolve it.
	Losers []string `json:"losers,omitempty"`
	// Derived reports that the winner is the root derived from the
	// bundle id: the same id on every device, so this install cannot
	// fork — and cannot be uninstalled, a derived object being
	// undeletable. Absent means an ordinary created root.
	Derived bool `json:"derived,omitempty"`
}

// BundleEnsureRequest is the body of POST /v1/spaces/:spaceId/bundles.
type BundleEnsureRequest struct {
	// Id is the bundle identifier. Required.
	Id string `json:"id"`
	// Name is the display name, written on install.
	Name string `json:"name,omitempty"`
	// RootTypes are attached to the root object at birth, so the
	// install's datasets are writable on it with no extra call.
	RootTypes []string `json:"rootTypes,omitempty"`
	// RootProperties seeds the root's property values, keyed
	// typeId → propId → value (same shape as POST /objects).
	RootProperties map[string]map[string]any `json:"rootProperties,omitempty"`
	// Derived installs the bundle on the root derived from its id
	// rather than a created one. Every device computes that id
	// offline, so the install never forks and never waits for the
	// registry to converge — which is the only way both sides of a
	// 1-1 (where nobody is the owner) can install while apart.
	//
	// Permanent in both directions: a derived root cannot be deleted,
	// so the bundle can never be uninstalled, and an existing install
	// on a created root is adopted rather than migrated. Ask for it
	// for a space's chat; not for anything a user may remove.
	Derived bool `json:"derived,omitempty"`
}

// BundleEnsureResponse is the reply to an Ensure call.
type BundleEnsureResponse struct {
	// Bundle is the converged registry row.
	Bundle Bundle `json:"bundle"`
	// Installed reports whether THIS call created the root. False
	// means an existing install was adopted and nothing was written.
	//
	// For a derived install it reports what THIS DEVICE did: both
	// sides of a partition can report true for the one root they
	// share. False still means an existing install was adopted.
	Installed bool `json:"installed"`
}

// BundleListResponse is the reply to GET /v1/spaces/:spaceId/bundles.
type BundleListResponse struct {
	Bundles []Bundle `json:"bundles"`
}

// BundleResolveRequest is the body of
// POST /v1/spaces/:spaceId/bundles/:bundleId/resolve.
type BundleResolveRequest struct {
	// LoserRootId is the losing root to delete, cascading to its
	// derived children. Call it only once whatever mattered has been
	// merged out — the server never merges for you.
	LoserRootId string `json:"loserRootId"`
}

// BundleChildRequest is the body of
// POST /v1/spaces/:spaceId/bundles/:bundleId/children.
type BundleChildRequest struct {
	// Seed derives the child deterministically under the bundle's
	// current winner. Permanent — a successor object takes a new seed.
	Seed string `json:"seed"`
	// Types are attached on first materialization.
	Types []string `json:"types,omitempty"`
}

// BundleChildResponse carries the derived child's object id.
type BundleChildResponse struct {
	ObjectId string `json:"objectId"`
}

// Bundles-registry error codes.
const (
	// ErrBundleNotFound — no live record for the bundle id.
	ErrBundleNotFound = "bundle.not_found"
	// ErrBundleNotReady — the winning root's tree has not reached this
	// device yet, so its id is not writable. Retryable.
	ErrBundleNotReady = "bundle.not_ready"
	// ErrBundleNotLoser — the resolve target is the current winner, or
	// was never claimed for this bundle.
	ErrBundleNotLoser = "bundle.not_loser"
	// ErrBundleLoserNotReady — the losing root is still syncing or
	// still inside the quiescence window, so what is stored locally is
	// not yet the whole of it. Retryable.
	ErrBundleLoserNotReady = "bundle.loser_not_ready"
)
