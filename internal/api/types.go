package api

// TypesCreateRequest is the body of POST /v1/spaces/:spaceId/types.
// Mirrors space.TypeCreateParams.
type TypesCreateRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
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
	BuiltIn     bool   `json:"builtIn,omitempty"`
}

// TypesListResponse is the body of GET /v1/spaces/:spaceId/types.
type TypesListResponse struct {
	Types []TypeInfo `json:"types"`
}

// PropertyDef mirrors space.PropertyDef on the wire. Recursive Items /
// Properties fields and Required are emitted only when populated.
type PropertyDef struct {
	Id          string        `json:"id"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	XKey        string        `json:"xKey,omitempty"`
	XKind       string        `json:"xKind,omitempty"`
	Kind        string        `json:"kind"`
	Items       *PropertyDef  `json:"items,omitempty"`
	Properties  []PropertyDef `json:"properties,omitempty"`
	Required    []string      `json:"required,omitempty"`
}

// PropertiesListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/properties.
type PropertiesListResponse struct {
	Properties []PropertyDef `json:"properties"`
}
