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
// declares — a type objects have (`type`), a collection objects are
// filed under (`collection`), the object a client opens (`miniapp`),
// records on the root (`parts`) — in any combination but type with
// collection, or collection with parts.
type CatalogBundle struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Derived installs the bundle on the root derived from its id —
	// never forks, never deletable. The general chat only.
	Derived bool `json:"derived,omitempty"`
	// Hidden keeps the root's definition out of pickers; needs `type`,
	// `collection` or `parts`.
	Hidden bool `json:"hidden,omitempty"`
	// RootType is the root's type when the bundle declares nothing (a
	// bare app root): a registered type id, `page` for a plain
	// document. Required then, refused next to a declaration.
	RootType string `json:"rootType,omitempty"`
	// Type declares the type the root defines; Collection the
	// collection. Exclusive. A root hosting its own records (parts)
	// needs no flag: a definition implements itself.
	Type       *CatalogType       `json:"type,omitempty"`
	Collection *CatalogCollection `json:"collection,omitempty"`
	// Miniapp is a value map on the built-in `miniapp` collection the
	// root is filed under: `bundle` is this bundle's id (filled when
	// omitted), any other key must be a property the built-in declares.
	Miniapp map[string]any `json:"miniapp,omitempty"`
	// Parts declare records datasets on the root (the
	// POST …/types/:typeId/parts draft shape).
	Parts []PartDraftRequest `json:"parts,omitempty"`
	// Supersedes names bundles of the same usecase this one stands in
	// for. A space that has any of them installed keeps them and never
	// receives this bundle; every other space receives this bundle and
	// never the ones it names. A superseded bundle may share its xKey
	// with the bundle that supersedes it — the two never meet in a space.
	Supersedes []string `json:"supersedes,omitempty"`
}

// CatalogType is the type a catalog bundle declares on its root.
type CatalogType struct {
	// XKey is the type's handle — what clients resolve it by and what
	// relation.targetTypes name. Unique across the catalog, types and
	// collections together.
	XKey string `json:"xKey"`
	// Layout is the rendering slug, `{type, config?}`.
	Layout json.RawMessage `json:"layout,omitempty"`
	// Properties are the columns (the POST …/types/:typeId/properties
	// draft shape); each carries an xKey, the property id derives from
	// it.
	Properties []AddPropertyRequest `json:"properties,omitempty"`
}

// CatalogCollection is the collection a catalog bundle declares on its
// root: a handle and columns, no parts, no layout.
type CatalogCollection struct {
	XKey       string               `json:"xKey"`
	Properties []AddPropertyRequest `json:"properties,omitempty"`
	// Meta is the definition's open bag of consumer flags (see
	// CollectionsCreateRequest): one string, bool or number per
	// single-level key, opaque to the server. Setup writes each key the
	// installed definition lacks and never overwrites one it carries.
	Meta map[string]any `json:"meta,omitempty"`
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
	// CollectionId is the root's id when the bundle declares a
	// collection.
	CollectionId string `json:"collectionId,omitempty"`
	// Properties maps each declared property's xKey to its id.
	Properties map[string]string `json:"properties,omitempty"`
	// Miniapp is the value map the catalog declares on the built-in
	// `miniapp` collection, `bundle` included — what an install writes and an
	// adopt by a writer fills in where absent. Read the root for what it
	// carries.
	Miniapp map[string]any `json:"miniapp,omitempty"`
}

// Catalog error codes.
const (
	// ErrCatalogNotFound — no usecase with that id in the catalog.
	ErrCatalogNotFound = "catalog.not_found"
)
