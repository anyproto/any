package api

import "encoding/json"

// The usecase catalog — the server's embedded set of well-known
// bundles (docs/28-well-known-bundles.md). A usecase is the unit a
// client sets up: a set of bundles installed together, each a created
// root under a permanent `system:` id, plus the usecases it requires.
// The catalog is read-only over HTTP; its content ships with the
// binary and is validated at build time.

// CatalogUsecase is one catalog entry as the catalog declares it.
type CatalogUsecase struct {
	// Id is the usecase slug — the `:usecaseId` path segment. Never
	// enters a space.
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Requires lists the usecases set up before this one, transitively.
	Requires []string `json:"requires,omitempty"`
	// Bundles are the usecase's own installs, in setup order.
	Bundles []CatalogBundle `json:"bundles"`
}

// CatalogBundle is one bundle of a usecase: one created root under a
// `system:<name>/v<n>` id. What the root IS follows from what it
// declares — a type objects carry (`type`), the object a client opens
// (`miniapp`), records on the root (`parts`) — in any combination.
type CatalogBundle struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Derived installs the bundle on the root derived from its id —
	// never forks, never deletable. The general chat only.
	Derived bool `json:"derived,omitempty"`
	// Hidden keeps the root's type out of pickers and out of the
	// primary-type choice; needs `type` or `parts`, and excludes a
	// `weight`.
	Hidden bool `json:"hidden,omitempty"`
	// SelfTyped makes the root also carry the type it declares — a
	// root that hosts its own bundle's records (contacts layouts). A
	// type-only bundle (wiki, person) leaves it off: the definition
	// does not match a query for its type and takes none of its parts.
	// Implied for a reserved-module part (the general chat); needs
	// `type` or `parts`.
	SelfTyped bool `json:"selfTyped,omitempty"`
	// Type declares the type the root defines.
	Type *CatalogType `json:"type,omitempty"`
	// Miniapp is a value map on the built-in `miniapp` type the root
	// carries: `bundle` is this bundle's id (filled when omitted), any
	// other key must be a property the built-in declares.
	Miniapp map[string]any `json:"miniapp,omitempty"`
	// Parts declare records datasets on the root (the
	// POST …/types/:typeId/parts draft shape).
	Parts []PartDraftRequest `json:"parts,omitempty"`
}

// CatalogType is the type a catalog bundle declares on its root.
type CatalogType struct {
	// XKey is the type's handle — what clients resolve it by and what
	// relation.targetTypes name. Unique across the catalog.
	XKey string `json:"xKey"`
	// Weight orders the type against others an object carries (the
	// highest listed one is the primary type); meaningless on a hidden
	// type.
	Weight int `json:"weight,omitempty"`
	// Layout is the rendering slug, `{type, config?}`.
	Layout json.RawMessage `json:"layout,omitempty"`
	// Properties are the columns (the POST …/types/:typeId/properties
	// draft shape); each carries an xKey, the property id derives from
	// it.
	Properties []AddPropertyRequest `json:"properties,omitempty"`
}

// CatalogListResponse is the body of GET /v1/catalog.
type CatalogListResponse struct {
	Usecases []CatalogUsecase `json:"usecases"`
}

// CatalogSetupRequest is the body of POST /v1/catalog/:usecaseId/setup.
type CatalogSetupRequest struct {
	SpaceId string `json:"spaceId"`
}

// CatalogSetupResponse is the reply to a setup: every bundle the call
// touched, dependencies first, the requested usecase's bundles last.
type CatalogSetupResponse struct {
	// Usecase is the id that was asked for.
	Usecase string `json:"usecase"`
	// Bundles are the installs in setup order.
	Bundles []CatalogSetupBundle `json:"bundles"`
}

// CatalogSetupBundle is one install a setup ensured.
type CatalogSetupBundle struct {
	// Usecase is the entry the bundle belongs to (the requested one or
	// a dependency).
	Usecase string `json:"usecase"`
	// Id is the bundle id.
	Id string `json:"id"`
	// Bundle is the converged registry row.
	Bundle Bundle `json:"bundle"`
	// Installed reports whether THIS call registered the root.
	Installed bool `json:"installed"`
	// TypeId is the root's id when the bundle declares a type — the
	// namespace of its property values (`<typeId>.<propId>`).
	TypeId string `json:"typeId,omitempty"`
	// Properties maps each declared property's xKey to its id.
	Properties map[string]string `json:"properties,omitempty"`
	// Miniapp is the value map the catalog declares on the built-in
	// `miniapp` type, `bundle` included — what an install writes and an
	// adopt by a writer fills in where absent. Read the root for what it
	// carries.
	Miniapp map[string]any `json:"miniapp,omitempty"`
}

// Catalog error codes.
const (
	// ErrCatalogNotFound — no usecase with that id in the catalog.
	ErrCatalogNotFound = "catalog.not_found"
)
