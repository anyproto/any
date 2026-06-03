package api

import "encoding/json"

// QueryResponse is the body of POST /v1/spaces/:spaceId/query and
// POST /v1/spaces/:spaceId/objects/query — both use Query.Snapshot
// under the hood. Each record is a *anyenc.Value rendered as JSON via
// FastJson(arena).MarshalTo. Carried as RawMessage so the outer
// envelope is not double-encoded.
//
// Total is the unbounded filter-matching count (independent of
// limit/offset), populated only when the request body sets
// includeTotal=true. Pointer so the field is omitted when the caller
// didn't ask (`null` would be misleading; absent is unambiguous), and
// an explicit zero still survives the round trip.
//
// HasNext reports whether more filter-matching records exist past this
// page (offset+len(records) < total). The SDK derives it from the same
// count, so it is meaningful only when includeTotal=true — gated on the
// same flag and omitted otherwise (when unknown the SDK forces it
// false, which would falsely read as "no more pages").
type QueryResponse struct {
	Records []json.RawMessage `json:"records"`
	Total   *int              `json:"total,omitempty"`
	HasNext *bool             `json:"hasNext,omitempty"`
}
