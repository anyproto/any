package api

import "encoding/json"

// QueryResponse is the body of POST /v1/spaces/:spaceId/query.
// Each record is a *anyenc.Value rendered as JSON via FastJson(arena).MarshalTo.
// Carried as RawMessage so the outer envelope is not double-encoded.
type QueryResponse struct {
	Records []json.RawMessage `json:"records"`
}
