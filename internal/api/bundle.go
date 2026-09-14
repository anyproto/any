package api

import "encoding/json"

// Bundles registry — the per-space record of what has been installed
// into a space (a marketplace bundle, a chat, an app's setup). Clients
// register their own; the server keeps no catalog of its own.
//
// Wire note: bundle ids carry a version suffix and therefore a slash
// ("favorites/v1"). In a path segment they must be percent-encoded
// (`favorites%2Fv1`); request bodies take them verbatim. Ids under
// the `system:` prefix are the server's (its embedded catalog installs
// them) — a client install under it is refused.

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
	// install's datasets are writable on it with no extra call. On a
	// root that declares a type they ride the root's first change next
	// to its own type — one object that is both a type and a carrier
	// of another (the wiki: the type its pages carry and a `miniapp`).
	RootTypes []string `json:"rootTypes,omitempty"`
	// RootProperties seeds the root's property values, keyed
	// typeId → propId → value (same shape as POST /objects), written
	// with RootTypes.
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
	// Parts declares the root's parts with their datasets (same shape
	// as POST …/types/:typeId/parts); the root becomes a type
	// implementing itself, typeId = rootId, and the records are written
	// through POST …/upsert / …/modify on the root (dataset = the
	// computed collection, `<rootId>_<key>` for a namespaced one).
	// Declared once on install; later evolution goes through the
	// …/types/:rootId/parts routes. Parts or properties are required
	// on the tech space.
	Parts []PartDraftRequest `json:"parts,omitempty"`
	// Properties declares property definitions on the root (same shape
	// as POST …/types/:typeId/properties, xKey REQUIRED and unique):
	// the root becomes a type objects carry, and each property's id is
	// derived from (rootId, xKey) so two devices installing while apart
	// mint one column per handle. Resolve xKey → propId through
	// GET …/types/:rootId/properties. Declared once on install (an
	// adopt fills in only definitions the root lacks); later evolution
	// goes through the …/types/:rootId/properties routes.
	Properties []AddPropertyRequest `json:"properties,omitempty"`
	// XKey is the root type's handle (same meaning as on POST …/types):
	// what a client resolves the type by, and what relation.targetTypes
	// in other declarations name. Unique within the space among listed
	// types (409 type.xkey_conflict). An xKey alone declares a MARKER
	// type — no properties, no parts, just a flag objects carry.
	// Written on install. A writer's adopt fills in a handle the root
	// lacks (an install that predates it); an existing handle is never
	// changed.
	XKey string `json:"xKey,omitempty"`
	// Layout and Weight seed the root type's rendering slice (same
	// shape as POST …/types); Hidden keeps it out of GET …/types. All
	// three are written on install only — an adopt never patches them.
	// Hidden is explicit: a root that only hosts its bundle's records
	// should ask for it (a listed type is one a client may attach
	// elsewhere, granting that object the bundle's collections); a root
	// that is a type objects carry stays listed.
	Layout json.RawMessage `json:"layout,omitempty"`
	Weight int             `json:"weight,omitempty"`
	Hidden bool            `json:"hidden,omitempty"`
	// SelfTyped makes the root CARRY the type it declares, so it holds
	// that type's property values and its datasets — what a root that
	// keeps its own bundle's records needs (an app's layouts). Off, the
	// root is the type definition and nothing else: it matches no query
	// for the type and takes none of its collections, which is what a
	// type OTHER objects carry wants (a wiki, a person). Needs a type
	// declaration; implied for a part declaring a reserved module and
	// on the tech space.
	SelfTyped bool `json:"selfTyped,omitempty"`
}

// BundleEnsureResponse is the reply to an Ensure call.
type BundleEnsureResponse struct {
	// Bundle is the converged registry row.
	Bundle Bundle `json:"bundle"`
	// Installed reports whether THIS call registered the install.
	// False means an existing one was adopted — which for a derived
	// bundle may still materialize the root's tree on this device,
	// since that id is one every device can mint.
	//
	// For a derived install it reports what THIS DEVICE did: both
	// sides of a partition can report true for the one root they
	// share.
	Installed bool `json:"installed"`
}

// BundleListResponse is the reply to GET /v1/spaces/:spaceId/bundles.
type BundleListResponse struct {
	Bundles []Bundle `json:"bundles"`
	// Synced reports whether the registry converged before this read
	// (the read-side lock): true means an absent bundle is definitively
	// not installed; false (the wait expired — cold offline device)
	// means absence is provisional.
	Synced bool `json:"synced"`
}

// BundleGetResponse is the reply to GET /v1/spaces/:spaceId/bundles/:bundleId.
type BundleGetResponse struct {
	Bundle Bundle `json:"bundle"`
	// Synced: see BundleListResponse.Synced.
	Synced bool `json:"synced"`
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
	// ErrBundleReserved — the id is under the server's `system:` prefix;
	// only the server's own catalog installs there.
	ErrBundleReserved = "bundle.reserved"
	// ErrDatasetModuleReserved — a part or dataset draft names a module
	// reserved to the server's own installs.
	ErrDatasetModuleReserved = "dataset.module_reserved"
)
