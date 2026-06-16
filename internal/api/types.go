package api

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
// unknown values with 400 invalid_request. Items / Properties /
// Required from space.PropertyDraft are not exposed in v1.
type AddPropertyRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	XKey        string `json:"xKey,omitempty"`
	Kind        string `json:"kind"`
	// Meta is an opaque consumer flag map, stored verbatim on the
	// property definition. meta["index"] = "<scope>" marks the property
	// for the search indexer (docs/13-index.md).
	Meta map[string]string `json:"meta,omitempty"`
}

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
	Meta       map[string]string `json:"meta,omitempty"`
	Items      *PropertyDef      `json:"items,omitempty"`
	Properties []PropertyDef     `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
}

// PropertiesListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/properties.
type PropertiesListResponse struct {
	Properties []PropertyDef `json:"properties"`
}
