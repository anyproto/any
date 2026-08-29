package api

import "encoding/json"

// Local store — device-local, non-CRDT any-store collections under
// /v1/local (task-local-store.md; docs/26-local-store.md). Records are
// plain documents with a string `id`; no `_ver`, no tombstones, no
// subscribe. Filters, sorts, modifiers and pipelines are the any-store
// shapes /query and /aggregate already accept.

// LocalCollection addresses one local collection on the wire.
type LocalCollection struct {
	// Scope is "account" or "space".
	Scope string `json:"scope"`
	// SpaceId binds a space-scoped collection; required iff scope is
	// "space". Nothing drops the collection when the space goes away.
	SpaceId string `json:"spaceId,omitempty"`
	// Name: ^[a-z0-9][a-z0-9_-]{0,63}$.
	Name string `json:"name"`
}

// LocalIndex is one range index on a local collection.
type LocalIndex struct {
	// Name is derived from Fields when empty (e.g. "kind,-at").
	Name string `json:"name,omitempty"`
	// Fields are field paths, "-" prefix for descending.
	Fields []string `json:"fields"`
	Unique bool     `json:"unique,omitempty"`
	Sparse bool     `json:"sparse,omitempty"`
}

// LocalCollectionInfo describes an existing local collection.
type LocalCollectionInfo struct {
	LocalCollection
	// StorageName is the collection's any-store name (the "l_"-tagged
	// form). It is what a pipeline's $out / $merge `into` / $lookup
	// `from` must name — those stages take raw collection names.
	StorageName string       `json:"storageName"`
	Count       int          `json:"count"`
	Indexes     []LocalIndex `json:"indexes"`
}

// LocalListResponse is the body of GET /v1/local/collections.
type LocalListResponse struct {
	Collections []LocalCollectionInfo `json:"collections"`
}

// LocalEnsureRequest is the body of PUT /v1/local/collections.
type LocalEnsureRequest struct {
	LocalCollection
	// Indexes to ensure on the collection (idempotent).
	Indexes []LocalIndex `json:"indexes,omitempty"`
}

// LocalEnsureResponse is the body of PUT /v1/local/collections: 201
// when this call created the collection, 200 when it already existed.
type LocalEnsureResponse struct {
	Collection LocalCollectionInfo `json:"collection"`
	Created    bool                `json:"created"`
}

// LocalDocsRequest is the body of POST /v1/local/insert and
// POST /v1/local/upsert. At most 1000 docs per request; written in
// chunks of 256 per transaction, so a failure mid-way leaves earlier
// chunks committed.
type LocalDocsRequest struct {
	Coll LocalCollection `json:"coll"`
	// Docs are JSON objects. A missing `id` is minted server-side
	// (insert and upsert alike); it must be a string when present.
	Docs []map[string]any `json:"docs"`
}

// LocalIdsResponse is the body of insert / upsert: the ids of every
// written document, in request order.
type LocalIdsResponse struct {
	Ids []string `json:"ids"`
}

// LocalUpdateRequest is the body of POST /v1/local/update.
type LocalUpdateRequest struct {
	Coll LocalCollection `json:"coll"`
	Id   string          `json:"id"`
	// Modifier is a mongo-style modifier ($set / $unset / $inc / …).
	Modifier map[string]any `json:"modifier"`
	// Upsert creates the document from the modifier when the id is
	// absent instead of answering 404 local.doc_not_found.
	Upsert bool `json:"upsert,omitempty"`
}

// LocalUpdateResponse is the body of POST /v1/local/update.
type LocalUpdateResponse struct {
	// Modified reports whether the modifier changed the document.
	Modified bool `json:"modified"`
	// Record is the document after the update.
	Record json.RawMessage `json:"record"`
}

// LocalDeleteRequest is the body of POST /v1/local/delete: exactly one
// of Ids / Filter. Filter deletes are collected under one read and
// removed in chunks of 256 — NOT atomic per call; a concurrent writer
// can interleave.
type LocalDeleteRequest struct {
	Coll   LocalCollection `json:"coll"`
	Ids    []string        `json:"ids,omitempty"`
	Filter map[string]any  `json:"filter,omitempty"`
}

// LocalDeleteResponse is the body of POST /v1/local/delete.
type LocalDeleteResponse struct {
	// Deleted counts documents actually removed (an id that was already
	// gone is not counted, not an error).
	Deleted int `json:"deleted"`
}

// LocalGetRequest is the body of POST /v1/local/get.
type LocalGetRequest struct {
	Coll LocalCollection `json:"coll"`
	Id   string          `json:"id"`
}

// LocalRecordResponse is the body of POST /v1/local/get.
type LocalRecordResponse struct {
	Record json.RawMessage `json:"record"`
}

// LocalQueryRequest is the body of POST /v1/local/query. The reply is
// QueryResponse; `total`/`hasNext` ride includeTotal as on /query.
type LocalQueryRequest struct {
	Coll   LocalCollection `json:"coll"`
	Filter map[string]any  `json:"filter,omitempty"`
	Sort   []string        `json:"sort,omitempty"`
	// Limit defaults to 100, capped at 1000.
	Limit        int  `json:"limit,omitempty"`
	Offset       int  `json:"offset,omitempty"`
	IncludeTotal bool `json:"includeTotal,omitempty"`
}

// LocalAggregateRequest is the body of POST /v1/local/aggregate. The
// stage vocabulary is GET /v1/local/meta's `stages`. $out / $merge /
// $lookup name collections by StorageName and must name LOCAL ones
// (400 local.bad_sink_target otherwise); a sink pipeline answers
// `written` instead of `records`.
type LocalAggregateRequest struct {
	Coll             LocalCollection  `json:"coll"`
	Pipeline         []map[string]any `json:"pipeline"`
	GroupLimit       *int             `json:"groupLimit,omitempty"`
	AccumArrayLimit  *int             `json:"accumArrayLimit,omitempty"`
	MemoryLimitBytes *int             `json:"memoryLimitBytes,omitempty"`
	Explain          bool             `json:"explain,omitempty"`
}

// LocalAggregateResponse is the body of POST /v1/local/aggregate:
// exactly one of Records (result docs), Plan (explain=true) or Written
// (a $out / $merge pipeline — documents written to the target).
type LocalAggregateResponse struct {
	Records []json.RawMessage `json:"records,omitzero"`
	Plan    *string           `json:"plan,omitempty"`
	Written *int              `json:"written,omitempty"`
}

// LocalIndexesRequest is the body of POST /v1/local/indexes.
type LocalIndexesRequest struct {
	Coll   LocalCollection `json:"coll"`
	Ensure []LocalIndex    `json:"ensure,omitempty"`
	// Drop names indexes to remove (a missing name is not an error).
	Drop []string `json:"drop,omitempty"`
}

// LocalIndexesResponse is the body of POST /v1/local/indexes: the
// collection's indexes after the change.
type LocalIndexesResponse struct {
	Indexes []LocalIndex `json:"indexes"`
}

// LocalMetaResponse is the body of GET /v1/local/meta: the aggregation
// grammar as any-store advertises it.
type LocalMetaResponse struct {
	Stages       []string `json:"stages"`
	Accumulators []string `json:"accumulators"`
}
