package api

import "encoding/json"

// AggregateResponse is the body of POST /v1/spaces/:spaceId/aggregate
// and POST /v1/spaces/:spaceId/objects/aggregate — both use Agg.All
// (or Agg.Explain with explain=true) under the hood.
//
// Records are pipeline RESULT documents, not dataset rows: a $group
// doc carries the group key as `id` (never `_id`), a $count doc is
// `{<name>: N}` with no id at all. Rendered the same way as
// QueryResponse records (anyenc → FastJson → RawMessage).
//
// Plan replaces Records when the request set explain=true. It is the
// pushed prefix's access plan plus the in-pipeline stage list —
// diagnostic only, NOT a stable format; don't parse it.
// Records uses omitzero, not omitempty: the result path always sends a
// non-nil slice (so an empty result is `"records": []`, matching
// QueryResponse), while the explain path leaves it nil and the field
// disappears entirely.
type AggregateResponse struct {
	Records []json.RawMessage `json:"records,omitzero"`
	Plan    *string           `json:"plan,omitempty"`
}
