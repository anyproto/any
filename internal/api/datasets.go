package api

import "encoding/json"

// DatasetSchema is the wire shape for space.DatasetSchema — one
// dataset's field declaration rendered as a standard JSON Schema
// document. Schema is carried as RawMessage so the JSON Schema (which
// the SDK already marshals) isn't double-encoded into a string.
//
// The JSON Schema object looks like:
//
//	{"type":"object",
//	 "properties":{"<field>":{"type":"string","title":"…","x-scope":"synced"}},
//	 "additionalProperties":<dynamic>}
//
// The `x-scope` extension keyword on each property carries the field's
// class: "synced" (user/DAG-written, synced across devices), "derived"
// (handler-computed, read-only to writers), or "local" (device-local,
// never synced). `additionalProperties:true` marks a dynamic dataset
// whose undeclared keys are permitted and treated as synced.
//
// Datasets with behavioral schema declarations (runtime-defined ones,
// and built-ins that declare them) carry further extension keywords:
// per-field `x-mutable-by` ("author"/"any"; absent = write-once) and
// `x-stamp` ("creator"/"createTime"/"modifyTime" — derived at apply,
// client writes rejected); doc-level standard `required`, `x-delete-by`
// ("author"; absent = anyone), `x-id` ("user" with `x-id-pattern` /
// `x-id-max-length`; absent = auto-derived ids), and `x-search`
// ({title, text} — the record fields feeding the search index).
type DatasetSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	// TypeId is the owning type for ext-type and runtime-defined
	// datasets ("" for space-level built-ins). Records exist only on
	// objects carrying this type; consumers gate indexing/eviction on it.
	TypeId string `json:"typeId,omitempty"`
}

// DatasetsResponse is the body of GET /v1/spaces/:spaceId/datasets and
// GET /v1/datasets — the JSON-Schema description of every dataset the
// space (or the account's tech-space system objects) hosts, for
// consumer discovery.
type DatasetsResponse struct {
	Datasets []DatasetSchema `json:"datasets"`
}
