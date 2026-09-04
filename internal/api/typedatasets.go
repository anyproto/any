package api

import (
	"encoding/json"
	"reflect"
)

// Wire labels for the dataset-schema behavioral vocabulary. Mirror the
// SDK enums 1:1 (space.Mutability / Stamp / IdRule / DeletePolicy).
const (
	MutableNever    = "never"
	MutableByAuthor = "author"
	MutableByAnyone = "any"

	StampCreator    = "creator"
	StampCreateTime = "createTime"
	StampModifyTime = "modifyTime"

	IdRuleAuto = "auto"
	IdRuleUser = "user"

	DeleteByAnyone = "anyone"
	DeleteByAuthor = "author"
)

// Module slugs a dataset declaration may name. `records` is the SDK's
// built-in generic module (a schema-enforced dataset the declaration
// fully describes); `editor` and `chat` are this server's compiled-in
// modules — the module owns their schema, so a declaration carries no
// fields.
const (
	ModuleRecords = "records"
	ModuleEditor  = "editor"
	ModuleChat    = "chat"
)

// Canonical collections of the compiled-in modules — what a shared
// dataset of the module is, and the default the editor CLI writes.
const (
	CollectionEditorBlocks = "editor_blocks"
	CollectionChatMessages = "chat_messages"
)

// PartDraftRequest is the body of POST /v1/spaces/:spaceId/types/:typeId/parts
// and one element of a bundle ensure's `parts`. Mirrors space.PartDraft:
// a display unit of the type owning one or more datasets. The key is
// pinned; the display slice patches via PATCH …/parts/:partId; datasets
// evolve via POST …/parts/:partId/datasets.
type PartDraftRequest struct {
	// Key is the part's slug ([a-z][a-z0-9_]*, ≤ 64), unique within the
	// type — pinned.
	Key string `json:"key"`
	// Name / Icon / Pos are the display slice; clients sort parts by
	// pos. Hidden parts are not shown by default but stay revealable.
	Name   string `json:"name,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Pos    string `json:"pos,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	// UI is the widget descriptor — {type, config} in the xFormat shape
	// (v1 slugs: document, chat, table, list, board, gallery, chart,
	// properties; open set, an unknown slug renders the module default).
	// Written whole.
	UI json.RawMessage `json:"ui,omitempty"`
	// Uses names other datasets OF THIS TYPE the part renders without
	// owning them (dataset keys).
	Uses []string `json:"uses,omitempty"`
	// Datasets are the part's initial dataset declarations.
	Datasets []DatasetDraftRequest `json:"datasets,omitempty"`
}

// PartDefResponse mirrors space.PartDef — the compiled view of one part.
type PartDefResponse struct {
	Id       string               `json:"id"`
	Key      string               `json:"key"`
	Name     string               `json:"name,omitempty"`
	Icon     string               `json:"icon,omitempty"`
	Pos      string               `json:"pos,omitempty"`
	Hidden   bool                 `json:"hidden,omitempty"`
	UI       json.RawMessage      `json:"ui,omitempty"`
	Uses     []string             `json:"uses,omitempty"`
	Datasets []DatasetDefResponse `json:"datasets"`
}

// TypePartsListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/parts.
type TypePartsListResponse struct {
	Parts []PartDefResponse `json:"parts"`
}

// AddPartResponse is the body returned by POST …/types/:typeId/parts.
type AddPartResponse struct {
	PartId string `json:"partId"`
}

// PartPatchRequest is the body of PATCH
// /v1/spaces/:spaceId/types/:typeId/parts/:partId — a per-path patch over
// the part's mutable leaves: name, icon, pos (strings), hidden
// (boolean), ui (an object, replaced whole), uses (an array of dataset
// keys). The key is pinned → 400 dataset.immutable. At least one entry
// across Set/Unset required.
type PartPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}

// DatasetDraftRequest is one dataset declaration — an element of
// PartDraftRequest.Datasets or the body of POST
// /v1/spaces/:spaceId/types/:typeId/parts/:partId/datasets. Mirrors
// space.DatasetDraft. The behavioral parts (key, module, shared,
// idRule/idPattern/idMaxLen, deleteBy, skipHistory, field kinds/flags)
// are pinned for the definition's life — remove and re-add to change
// them; display parts (displayName, description, search leaves) patch
// via PATCH …/datasets/:defId.
type DatasetDraftRequest struct {
	// Key is the dataset's slug inside its type ([a-z][a-z0-9_]*, ≤ 64)
	// — pinned. A namespaced dataset lives in the collection
	// `<typeId>_<key>`; a shared dataset's key is its module's canonical
	// collection name and may be omitted.
	Key string `json:"key,omitempty"`
	// Module is the serving module: "records" (the default), "editor"
	// or "chat".
	Module string `json:"module,omitempty"`
	// Shared makes the type participate in the module's canonical
	// collection (editor_blocks, chat_messages) instead of a namespaced
	// one, so two types sharing the editor give an object carrying both
	// a single body. Editor: either; chat: shared only; records: never.
	Shared      bool   `json:"shared,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	// Dynamic keeps a free-form keyspace next to the declared fields.
	Dynamic bool `json:"dynamic,omitempty"`
	// IdRule: "auto" (default — ids derived from the change) or "user"
	// (caller-supplied ids, constrained by idPattern/idMaxLen; the id
	// doubles as the upsert idempotency key).
	IdRule    string `json:"idRule,omitempty"`
	IdPattern string `json:"idPattern,omitempty"`
	IdMaxLen  int    `json:"idMaxLen,omitempty"`
	// DeleteBy: "anyone" (default) or "author" (requires a
	// stamp:creator field among fields).
	DeleteBy string `json:"deleteBy,omitempty"`
	// SkipHistory keeps the dataset out of the version-history index.
	SkipHistory bool `json:"skipHistory,omitempty"`
	// Search is the optional search-extraction annotation (x-search):
	// which record fields feed the search index's title/text, and
	// optionally which index scope the entries land under.
	Search *DatasetSearchFields `json:"search,omitempty"`
	// Fields are the initial field definitions (records datasets only —
	// a module owns its schema). Declare required fields here — fields
	// added later cannot be required.
	Fields []DatasetFieldDraft `json:"fields,omitempty"`
}

// DatasetSearchFields mirrors space.SearchFields — the x-search
// mapping. Title/text may be empty (either alone suffices). Scope is
// the index scope slug the dataset's entries land under (index.
// ValidScope); empty = the indexer's default scope ("basic").
type DatasetSearchFields struct {
	Title string `json:"title,omitempty"`
	// Text accepts a bare field-key string OR a non-empty array of
	// unique field keys; a single key always reads back as the bare
	// string. The generated schema can only show the array form — the
	// string form is equally valid on the wire.
	Text  SearchText `json:"text,omitempty"`
	Scope string     `json:"scope,omitempty"`
}

// SearchText is the search `text` mapping's wire form: a bare field
// key or a non-empty array of field keys (the indexer joins the mapped
// values into one body). A single key marshals as the bare
// string, so single-field declarations and discovery output keep the
// canonical scalar shape. An empty string unmarshals to nil ("no text
// mapping"); an empty array stays a non-nil empty slice so declaration
// validation can reject it explicitly.
type SearchText []string

func (t SearchText) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}
	return json.Marshal([]string(t))
}

func (t *SearchText) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*t = nil
		} else {
			*t = SearchText{s}
		}
		return nil
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		// A typed error so bind failures name the field
		// (bindErrorMessage renders UnmarshalTypeError with its Field)
		// instead of degrading to the generic body-shape message.
		return &json.UnmarshalTypeError{
			Value: "neither a string nor an array of strings",
			Type:  reflect.TypeFor[SearchText](),
			Field: "search.text",
		}
	}
	*t = SearchText(keys)
	return nil
}

// DatasetFieldDraft is one field declaration — input to a records
// dataset draft and POST …/datasets/:defId/fields. Mirrors
// space.DatasetFieldDraft.
type DatasetFieldDraft struct {
	// Key is the on-record field name — pinned.
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Kind is a PropertyKind* wire string. Required unless stamp
	// implies one (creator ⇒ string, createTime/modifyTime ⇒ datetime).
	Kind string `json:"kind,omitempty"`
	// Shape optionally refines array/object values.
	Shape *DatasetFieldShape `json:"shape,omitempty"`
	// Scope: "synced" (default) or "local"; "derived" is implied by
	// stamp and rejected otherwise.
	Scope string `json:"scope,omitempty"`
	// Required: field must be present on create. Incompatible with stamp.
	Required bool `json:"required,omitempty"`
	// MutableBy: "never" (default — write-once), "author", "any".
	// "author" requires a stamp:creator field in the dataset.
	MutableBy string `json:"mutableBy,omitempty"`
	// Stamp: "creator" | "createTime" | "modifyTime" — value derived at
	// apply time, client writes rejected.
	Stamp string `json:"stamp,omitempty"`
	// XFormat is the field's descriptor — the same object a property
	// definition carries (AddPropertyRequest.XFormat), validated the
	// same way against the field's kind. Mutable via
	// PATCH …/fields/:fieldId.
	XFormat json.RawMessage `json:"xFormat,omitempty"`
}

// DatasetFieldShape is a recursive JSON-Schema-subset value shape
// (handler.FieldShape on the wire): a kind plus optional items /
// properties refinement.
type DatasetFieldShape struct {
	Kind       string                        `json:"kind"`
	Items      *DatasetFieldShape            `json:"items,omitempty"`
	Properties map[string]*DatasetFieldShape `json:"properties,omitempty"`
}

// DatasetDefResponse mirrors space.DatasetDef — the compiled view of
// one dataset definition.
type DatasetDefResponse struct {
	Id string `json:"id"`
	// Key is the slug inside the type; collection is the name reads and
	// writes address (`dataset` on /query, /modify, /upsert …) — the
	// module's canonical collection when shared, `<typeId>_<key>`
	// otherwise. Server-computed, never client-set.
	Key        string `json:"key"`
	Collection string `json:"collection"`
	Module     string `json:"module"`
	Shared     bool   `json:"shared,omitempty"`
	// PartId is the owning part's id.
	PartId      string               `json:"partId"`
	DisplayName string               `json:"displayName,omitempty"`
	Description string               `json:"description,omitempty"`
	Dynamic     bool                 `json:"dynamic,omitempty"`
	IdRule      string               `json:"idRule"`
	IdPattern   string               `json:"idPattern,omitempty"`
	IdMaxLen    int                  `json:"idMaxLen,omitempty"`
	DeleteBy    string               `json:"deleteBy"`
	SkipHistory bool                 `json:"skipHistory,omitempty"`
	Search      *DatasetSearchFields `json:"search,omitempty"`
	Fields      []DatasetFieldDef    `json:"fields"`
	// Invalid marks a definition whose folded declaration fails
	// validation (invalidReason says why) — a records fold missing a
	// creator stamp behind an author rule, an unknown module, a shared
	// rule violation. Invalid definitions never register or accept data
	// but stay listed so they can be repaired or removed.
	Invalid       bool   `json:"invalid,omitempty"`
	InvalidReason string `json:"invalidReason,omitempty"`
}

// DatasetFieldDef mirrors space.DatasetFieldDef.
type DatasetFieldDef struct {
	// Id is the field definition record's id — the identity
	// PATCH / DELETE …/fields/:fieldId target.
	Id          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	// Shape is the full declared value shape (kind plus items /
	// properties) when one was declared beyond the bare kind.
	Shape     *DatasetFieldShape `json:"shape,omitempty"`
	Scope     string             `json:"scope"`
	Required  bool               `json:"required,omitempty"`
	MutableBy string             `json:"mutableBy"`
	Stamp     string             `json:"stamp,omitempty"`
	// XFormat is the field's descriptor as stored; absent when none was
	// declared.
	XFormat json.RawMessage `json:"xFormat,omitempty"`
}

// TypeDatasetsListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/datasets
// — every dataset of every part, flat.
type TypeDatasetsListResponse struct {
	Datasets []DatasetDefResponse `json:"datasets"`
}

// AddDatasetResponse is the body returned by POST …/parts/:partId/datasets.
type AddDatasetResponse struct {
	DatasetDefId string `json:"datasetDefId"`
	// Collection is the computed collection name the new dataset's
	// records live in.
	Collection string `json:"collection"`
}

// AddDatasetFieldResponse is the body returned by POST …/datasets/:defId/fields.
type AddDatasetFieldResponse struct {
	FieldDefId string `json:"fieldDefId"`
}

// DatasetFieldPatchRequest is the body of PATCH
// /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId —
// a per-path patch over one field definition's mutable leaves
// (space.TypesAPI.PatchDatasetField): name, description (strings) and
// every path under xFormat (the property PATCH rules — a set targets a
// leaf, a container can only be unset). The behavioral declaration
// (key, kind, shape, scope, required, mutableBy, stamp) is pinned and
// rejected with 400 dataset.immutable. At least one entry across
// Set/Unset required.
type DatasetFieldPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}

// DatasetPatchRequest is the body of PATCH
// /v1/spaces/:spaceId/types/:typeId/datasets/:defId — a per-path patch
// over a dataset definition's mutable leaves (space.DatasetDefPatch).
//
// Mutable paths: description, displayName, search.title, search.text,
// search.scope
// (string leaves; a whole `search` replace is pinned). Everything else
// — the key, module, shared flag, id rule, delete gate, field
// kinds/flags — is pinned and rejected with 400 dataset.immutable. At
// least one entry across Set/Unset required.
type DatasetPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}
