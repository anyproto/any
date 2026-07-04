package api

import "encoding/json"

// TypesCreateRequest is the body of POST /v1/spaces/:spaceId/types.
// Mirrors space.TypeCreateParams.
type TypesCreateRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
	// XKey is the stable, caller-side programmatic key for the type
	// (e.g. "agent_memory"). REQUIRED on create and unique per space: the
	// server rejects an empty xKey (type.xkey_required) and one that
	// collides with an existing type's xKey or id (type.xkey_conflict).
	// Clients derive it as a slug of Name. It's the only human handle a
	// type resolves by — the display Name is not a resolution key.
	XKey string `json:"xKey,omitempty"`
}

// TypesCreateResponse is the body returned by POST /v1/spaces/:spaceId/types.
type TypesCreateResponse struct {
	TypeId string `json:"typeId"`
}

// AddPropertyRequest is the body of POST /v1/spaces/:spaceId/types/:typeId/properties.
// Kind is the wire string from PropertyKind* below; the server rejects
// unknown values with 400 invalid_request. Kind may be omitted when
// Format is set — it then defaults from the format type (links ⇒ array,
// date/datetime ⇒ string). Items / Properties / Required from
// space.PropertyDraft are not exposed in v1.
type AddPropertyRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	XKey        string `json:"xKey,omitempty"`
	Kind        string `json:"kind,omitempty"`
	// Meta is an opaque consumer flag map, stored verbatim on the
	// property definition. meta["index"] = "<scope>" marks the property
	// for the search indexer (docs/13-index.md).
	Meta map[string]string `json:"meta,omitempty"`
	// Format declares the property's value convention. format.type is
	// pinned for the property's life; ui/filter stay mutable.
	Format *PropertyFormat `json:"format,omitempty"`
	// Scope is the property's write/sync class: "synced" (default,
	// everyone in the space), "account" (this account's devices only),
	// or "local" (this device only, never synced). "derived" is
	// reserved for built-ins and rejected. Pinned by the first write,
	// like kind — changing a property's scope means defining a new
	// property. Property VALUE writes auto-route by the declared scope
	// (POST /v1/spaces/:spaceId/properties/:objectId/set/:typeId).
	Scope string `json:"scope,omitempty"`
}

// PropertyFormat is the wire shape of a property's format annotation.
// The server validates the semantics on its write path (type/ui
// vocabulary, ui/type compatibility, filter parses as a query
// condition) and validates property VALUES against the format on the
// property-write endpoints (a datetime parses, links are well-formed
// any:// URIs). No object-existence or object-type checks.
type PropertyFormat struct {
	// Type is one of the FormatType* wire strings.
	Type string `json:"type"`
	// UI is one of the FormatUI* wire strings; optional. links accepts
	// any UI; date/datetime accept none.
	UI string `json:"ui,omitempty"`
	// Filter is a mongo-style condition object over candidate objects
	// (e.g. {"type": {"$in": ["page"]}}); optional, links only.
	Filter json.RawMessage `json:"filter,omitempty"`
}

// FormatType* are the wire strings of space.FormatType. "tags" is
// reserved until the space-level tag table lands — the SDK rejects it.
const (
	FormatTypeLinks    = "links"
	FormatTypeDate     = "date"
	FormatTypeDatetime = "datetime"
	FormatTypeTags     = "tags"
)

// FormatUI* are the accepted presentation hints for format-bearing
// properties. Opaque to the SDK; vocabulary enforced by this server.
const (
	FormatUISelect      = "select"
	FormatUIMultiselect = "multiselect"
	FormatUILink        = "link"
	FormatUILinks       = "links"
)

// AddPropertyResponse is the body returned by AddProperty.
type AddPropertyResponse struct {
	PropId string `json:"propId"`
}

// PropertyKind* are the wire-string forms of space.PropertyKind. Mirror
// the SDK enum 1:1; expand when the SDK adds new kinds.
const (
	PropertyKindString  = "string"
	PropertyKindNumber  = "number"
	PropertyKindBoolean = "boolean"
	PropertyKindNull    = "null"
	PropertyKindArray   = "array"
	PropertyKindObject  = "object"
)

// TypeInfo mirrors space.TypeInfo on the wire.
type TypeInfo struct {
	Id          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
	// XKey is the stable, caller-side programmatic key. For builtin/registered
	// types it equals Id (a clean literal like "program"); for user types it's
	// the value set at create (or derived from Name by the client). Clients use
	// it as the stable type handle in dotted property paths.
	XKey    string `json:"xKey,omitempty"`
	BuiltIn bool   `json:"builtIn,omitempty"`
}

// TypesListResponse is the body of GET /v1/spaces/:spaceId/types.
type TypesListResponse struct {
	Types []TypeInfo `json:"types"`
}

// PropertyDef mirrors space.PropertyDef on the wire. Recursive Items /
// Properties fields and Required are emitted only when populated.
type PropertyDef struct {
	Id          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	XKey        string `json:"xKey,omitempty"`
	XKind       string `json:"xKind,omitempty"`
	Kind        string `json:"kind"`
	// Meta is the opaque consumer flag map set at AddProperty time
	// (e.g. meta["index"] = "<scope>" for the search indexer).
	Meta map[string]string `json:"meta,omitempty"`
	// Scope is the property's write/sync class (synced / derived /
	// account / local). Definitions written before scopes existed
	// read back as "synced".
	Scope      string        `json:"scope,omitempty"`
	Items      *PropertyDef  `json:"items,omitempty"`
	Properties []PropertyDef `json:"properties,omitempty"`
	Required   []string      `json:"required,omitempty"`
	// Format is the property's value-format annotation; absent for
	// properties that never declared one.
	Format *PropertyFormat `json:"format,omitempty"`
}

// PropertiesListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/properties.
type PropertiesListResponse struct {
	Properties []PropertyDef `json:"properties"`
}
