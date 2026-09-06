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
// Kind is the wire string from PropertyKind* below and is required —
// nothing is defaulted from the descriptor. Items / Properties /
// Required from space.PropertyDraft are not exposed in v1.
type AddPropertyRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// XKey is the property's handle — an alias, not a storage key
	// (values live under the content-addressed propId). Unique within
	// the type (409 property.xkey_conflict); mutable via PATCH.
	XKey string `json:"xKey,omitempty"`
	Kind string `json:"kind"`
	// Meta holds the consumer flags the server interprets — today only
	// meta["index"] = "<scope>" | "none" for the search indexer
	// (docs/13-index.md). Any other key is rejected; descriptive
	// metadata lives under xFormat.
	Meta map[string]string `json:"meta,omitempty"`
	// XFormat is the property's descriptor: everything descriptive
	// beyond the kind — the semantic slug, icon, ordering key, option
	// set, relation targets, per-format config (docs/27-descriptors.md).
	// An object; the server validates the keys it interprets (type,
	// icon, pos, options, relation, config) against the vocabulary and
	// the slug against kind, stores vendor-namespaced keys verbatim,
	// and reserves validate / compute. Every path under it is mutable
	// via PATCH.
	XFormat json.RawMessage `json:"xFormat,omitempty"`
	// Scope is the property's write/sync class: "synced" (default,
	// everyone in the space), "account" (this account's devices only),
	// or "local" (this device only, never synced). "derived" is
	// reserved for built-ins and rejected. Pinned by the first write,
	// like kind — changing a property's scope means defining a new
	// property. Property VALUE writes auto-route by the declared scope
	// (POST /v1/spaces/:spaceId/properties/:objectId/set/:typeId).
	Scope string `json:"scope,omitempty"`
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
	// PropertyKindDatetime is an instant: `{"$date": "<RFC 3339>"}` on
	// the wire in both directions (writes also accept
	// `{"$date": <unix millis>}`). The kind the `date` / `datetime`
	// formats imply — see docs/03-api.md § Types.
	PropertyKindDatetime = "datetime"
)

// TypeInfo mirrors space.TypeInfo on the wire.
type TypeInfo struct {
	Id          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
	// XKey is the stable, caller-side programmatic key. For builtin/registered
	// types it equals Id (a clean literal like "chat"); for user types it's
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
	Kind        string `json:"kind"`
	// Meta is the consumer flag map (meta["index"] for the search
	// indexer).
	Meta map[string]string `json:"meta,omitempty"`
	// Scope is the property's write/sync class (synced / derived /
	// account / local). Definitions written before scopes existed
	// read back as "synced".
	Scope      string        `json:"scope,omitempty"`
	Items      *PropertyDef  `json:"items,omitempty"`
	Properties []PropertyDef `json:"properties,omitempty"`
	Required   []string      `json:"required,omitempty"`
	// XFormat is the descriptor as stored — absent for a property that
	// never declared one, which renders structurally from Kind.
	XFormat json.RawMessage `json:"xFormat,omitempty"`
}

// PropertiesListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/properties.
type PropertiesListResponse struct {
	Properties []PropertyDef `json:"properties"`
}

// PropertyPatchRequest is the body of PATCH
// /v1/spaces/:spaceId/types/:typeId/properties/:propId — a generic
// per-path patch to a property definition (space.PropertyPatch). Set
// assigns values at dotted field paths; Unset removes them (a whole
// option subtree, e.g. "xFormat.options.high", is unset by naming it).
//
// Mutable paths: name, description, xKey, meta.index, and every path
// under xFormat. A set targets a leaf — never an object, so a
// container (options, options.<key>, relation, config, the whole bag)
// can only be unset, not replaced. Pinned paths (kind, scope, items,
// properties) are rejected with 400 property.immutable. At least one
// entry across Set/Unset required.
type PropertyPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}
