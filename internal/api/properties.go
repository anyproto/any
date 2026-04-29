package api

import "encoding/json"

// PropertiesGetResponse is the body of GET /v1/spaces/:spaceId/properties/:objectId.
// Record is the property record rendered as JSON — *anyenc.Value
// converted via FastJson(arena).MarshalTo. Carried as RawMessage so it
// is not double-encoded.
type PropertiesGetResponse struct {
	Record json.RawMessage `json:"record"`
}
