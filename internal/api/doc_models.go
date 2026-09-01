package api

// Request/response structs for handlers that parse with fastjson or
// use anonymous inline structs. They are never decoded into at
// runtime, but they are NOT doc-only: swaggo generates the OpenAPI
// schemas from them AND the server derives its strict unknown-field
// vocabularies from their json tags (jsonFieldNames in
// internal/server), so each struct is the single source of truth for
// its endpoint's accepted body fields. Field comments become spec
// descriptions — write them for the spec-reading client (agents
// included), and keep every struct in lockstep with what its handler
// actually reads.

// ObjectCreateRequest documents the body of POST /v1/spaces/:spaceId/objects.
// AUTHORITATIVE like QueryBodyParams: the server derives its strict
// unknown-field rejection from these json tags — this is the whole
// create vocabulary.
type ObjectCreateRequest struct {
	// Types lists the type ids attached at create; `nav` is appended
	// server-side when absent.
	Types []string `json:"types,omitempty"`
	// InitialProperties carries the object's starting property values,
	// keyed by type id then property id — the ONLY home for them:
	// {"initialProperties": {"any": {"name": "Dune"}}}. A top-level
	// name/description/type-group key is rejected.
	InitialProperties map[string]map[string]any `json:"initialProperties,omitempty"`
	// Nav overrides the auto-stamped tree placement.
	Nav *ObjectCreateNav `json:"nav,omitempty"`
}

// ObjectCreateNav documents the optional nav override in ObjectCreateRequest.
type ObjectCreateNav struct {
	Type     int    `json:"type,omitempty" enums:"1,2"`
	ParentId string `json:"parentId,omitempty"`
	Pos      string `json:"pos,omitempty"`
}

// QueryBodyParams is the shared windowed query/subscribe vocabulary —
// the exact top-level field set every query surface accepts beyond its
// own addressing extras (objectId / dataset). It is AUTHORITATIVE, not
// doc-only: the server derives its strict unknown-field rejection from
// these json tags (jsonFieldNames in internal/server), so a field
// absent here is a field the server 400s. Keep it in lockstep with
// applyQueryParams.
type QueryBodyParams struct {
	// Filter is a mongo-style condition over record fields; omitted or
	// empty matches every record. Operator grammar: docs/09-query.md
	// (a bad operator answers 400 filter.unknown_operator listing the
	// full set).
	Filter map[string]any `json:"filter,omitempty"`
	// Sort lists field paths, "-" prefix for descending (e.g.
	// "-createdAt"). Required when limit > 0 on subscribe, so the
	// window is well-defined.
	Sort []string `json:"sort,omitempty"`
	// Limit bounds the window; 0 or absent = unbounded.
	Limit int `json:"limit,omitempty"`
	// Offset skips past the first N matches of the sorted result.
	Offset int `json:"offset,omitempty"`
	// IncludeTotal populates `total` + `hasNext` in the snapshot reply.
	// Page-bounded in the current SDK — see docs/09-query.md caveat.
	IncludeTotal bool `json:"includeTotal,omitempty"`
	// MailboxCapacity (subscribe only) sizes the event mailbox before
	// the stream closes with reason "overflow". Default 256, min 16.
	MailboxCapacity int `json:"mailboxCapacity,omitempty"`
	// DriftBudgetPercent (subscribe only) bounds window drift before
	// the stream closes with reason "drifted". Default 30.
	DriftBudgetPercent int `json:"driftBudgetPercent,omitempty"`
	// Projection shapes the records that come back, mongo-style: a flat
	// object of dotted field paths to 1 (include) or -1 (exclude).
	// `{"any":1,"nav":1}` is include mode — nothing but those subtrees;
	// `{"_ver":-1}` is exclude mode — every user field but that one.
	// Omitted, records ship their full form. Three rules worth knowing:
	// `id` always rides along and cannot be excluded, `_ver` is narrowed
	// to the projection automatically (never name a `_ver` path), and
	// `_addSeq`/`_applySeq` drop unless named. Full grammar and the
	// divergences from mongo: docs/09-query.md § Projection.
	Projection map[string]int `json:"projection,omitempty"`
}

// SpaceQueryObjectsRequest documents the body of
// POST /v1/spaces/:spaceId/objects/query[/subscribe] and the per-object
// files query — surfaces addressed entirely by the path.
type SpaceQueryObjectsRequest struct {
	QueryBodyParams
}

// SpaceQueryRequest documents the body of
// POST /v1/spaces/:spaceId/query[/subscribe] — one object's dataset,
// addressed in the body.
type SpaceQueryRequest struct {
	// ObjectId names the object whose dataset is queried. Required.
	ObjectId string `json:"objectId"`
	// Dataset names the per-object dataset (chat_messages,
	// editor_blocks, …). Required.
	Dataset string `json:"dataset"`
	// IncludeDeleted (snapshot only) returns the dataset's record-level
	// tombstones next to the live rows: `{id, _deletedAt, _ver, …}`
	// with the content wiped. Lets a writer of an `id: user` dataset
	// find the highest id ever used — a deleted id is burned, so the
	// live maximum is not the next free one. Refused on `/subscribe`
	// (400 request.invalid_field); the other query surfaces reject it
	// as an unknown field (a deleted OBJECT is purged, not tombstoned).
	IncludeDeleted bool `json:"includeDeleted,omitempty"`
	QueryBodyParams
}

// SpaceListQueryRequest documents the body of
// POST /v1/spaces/query[/subscribe] — the account's tech-space rows.
// Unlike SpaceQueryRequest there is NO objectId: the target object is
// fixed server-side to the tech-space index.
type SpaceListQueryRequest struct {
	// Dataset defaults to "spaces"; "profile" is the only other
	// reachable value (closed allowlist — identities is deliberately
	// excluded, read it via GET /v1/identities).
	Dataset string `json:"dataset,omitempty" enums:"spaces,profile"`
	QueryBodyParams
}

// SpaceAggregateObjectsRequest documents the body of
// POST /v1/spaces/:spaceId/objects/aggregate.
type SpaceAggregateObjectsRequest struct {
	Pipeline         []map[string]any `json:"pipeline"`
	GroupLimit       *int             `json:"groupLimit,omitempty"`
	AccumArrayLimit  *int             `json:"accumArrayLimit,omitempty"`
	MemoryLimitBytes *int             `json:"memoryLimitBytes,omitempty"`
	Explain          bool             `json:"explain,omitempty"`
}

// SpaceAggregateRequest documents the body of
// POST /v1/spaces/:spaceId/aggregate.
type SpaceAggregateRequest struct {
	ObjectId         string           `json:"objectId"`
	Dataset          string           `json:"dataset"`
	Pipeline         []map[string]any `json:"pipeline"`
	GroupLimit       *int             `json:"groupLimit,omitempty"`
	AccumArrayLimit  *int             `json:"accumArrayLimit,omitempty"`
	MemoryLimitBytes *int             `json:"memoryLimitBytes,omitempty"`
	Explain          bool             `json:"explain,omitempty"`
}

// SpaceModifyRequest documents the body of POST /v1/spaces/:spaceId/modify.
type SpaceModifyRequest struct {
	ObjectId string         `json:"objectId"`
	Dataset  string         `json:"dataset"`
	Records  []RecordModify `json:"records"`
	TraceIds []string       `json:"traceIds,omitempty"`
	// Scope selects the write route: "synced" (default — the object's
	// own DAG change) or "local" (device-only materialization for
	// fields the dataset schema declares local-scope; explicit record
	// ids, no upsert, no traceIds). See docs/03-api.md § Modify records.
	Scope string `json:"scope,omitempty" enums:"synced,local"`
}

// RecordModify documents one record in a modify batch.
type RecordModify struct {
	Id     string `json:"id,omitempty"`
	Upsert bool   `json:"upsert,omitempty"`
	Ops    []Op   `json:"ops"`
}

// Op documents one operation in a modify batch.
type Op struct {
	Type  string `json:"type" enums:"$set,$unset,$inc,$addToSet,$pull"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// DeleteRecordsRequest documents the body of POST /v1/spaces/:spaceId/delete-records.
type DeleteRecordsRequest struct {
	ObjectId  string   `json:"objectId"`
	Dataset   string   `json:"dataset,omitempty"`
	RecordIds []string `json:"recordIds"`
	TraceIds  []string `json:"traceIds,omitempty"`
}

// PropertiesSetRequest documents the body of POST /v1/spaces/:spaceId/properties/:objectId/set/:typeId.
type PropertiesSetRequest struct {
	Patch map[string]any `json:"patch"`
}

// MarkdownContent documents the body of GET/PUT .../editor/markdown.
type MarkdownContent struct {
	Content string `json:"content"`
}

// MarkdownSetResponse documents the response of PUT .../editor/markdown.
type MarkdownSetResponse struct {
	Inserted  []string `json:"inserted"`
	Updated   []string `json:"updated"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
}
