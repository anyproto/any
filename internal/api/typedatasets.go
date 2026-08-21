package api

import "encoding/json"

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

// DatasetDraftRequest is the body of POST /v1/spaces/:spaceId/types/:typeId/datasets.
// Mirrors space.DatasetDraft. The behavioral parts (name, idRule/
// idPattern/idMaxLen, deleteBy, skipHistory, field kinds/flags) are
// pinned for the definition's life — remove and re-add to change them;
// display parts (displayName, description, search leaves) patch via
// PATCH …/datasets/:defId.
type DatasetDraftRequest struct {
	// Name is the dataset's collection name — pinned. No "_" prefix,
	// dots, slashes or colons; built-in names are reserved.
	Name        string `json:"name"`
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
	// Fields are the initial field definitions. Declare required
	// fields here — fields added later cannot be required.
	Fields []DatasetFieldDraft `json:"fields,omitempty"`
}

// DatasetSearchFields mirrors space.SearchFields — the x-search
// mapping. Title/text may be empty (either alone suffices). Scope is
// the index scope slug the dataset's entries land under (index.
// ValidScope); empty = the indexer's default scope ("basic").
type DatasetSearchFields struct {
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	Scope string `json:"scope,omitempty"`
}

// DatasetFieldDraft is one field declaration — input to AddDataset and
// POST …/datasets/:defId/fields. Mirrors space.DatasetFieldDraft.
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
// one runtime dataset definition. Field read-back drops description and
// shape (leaf kind only) — the SDK's compiled view doesn't carry them.
type DatasetDefResponse struct {
	Id          string               `json:"id"`
	Name        string               `json:"name"`
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
	// validation (invalidReason says why). Invalid definitions never
	// register or accept data but stay listed so they can be repaired
	// (add the missing field) or removed.
	Invalid       bool   `json:"invalid,omitempty"`
	InvalidReason string `json:"invalidReason,omitempty"`
}

// DatasetFieldDef mirrors space.DatasetFieldDef.
type DatasetFieldDef struct {
	// Id is the field definition record's id — the identity
	// DELETE …/fields/:fieldId targets.
	Id        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Required  bool   `json:"required,omitempty"`
	MutableBy string `json:"mutableBy"`
	Stamp     string `json:"stamp,omitempty"`
}

// TypeDatasetsListResponse is the body of GET /v1/spaces/:spaceId/types/:typeId/datasets.
type TypeDatasetsListResponse struct {
	Datasets []DatasetDefResponse `json:"datasets"`
}

// AddDatasetResponse is the body returned by POST …/types/:typeId/datasets.
type AddDatasetResponse struct {
	DatasetDefId string `json:"datasetDefId"`
}

// AddDatasetFieldResponse is the body returned by POST …/datasets/:defId/fields.
type AddDatasetFieldResponse struct {
	FieldDefId string `json:"fieldDefId"`
}

// DatasetPatchRequest is the body of PATCH
// /v1/spaces/:spaceId/types/:typeId/datasets/:defId — a per-path patch
// over a dataset definition's mutable leaves (space.DatasetDefPatch).
//
// Mutable paths: description, displayName, search.title, search.text,
// search.scope
// (string leaves; a whole `search` replace is pinned). Everything else
// — the collection name, id rule, delete gate, field kinds/flags — is
// pinned and rejected with 400 dataset.immutable. At least one entry
// across Set/Unset required.
type DatasetPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}
