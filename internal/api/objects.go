package api

import "encoding/json"

// ObjectGetResponse is the body of GET /v1/spaces/:spaceId/objects/:objectId:
// the object's row from the space's objects collection (any.type,
// any.collections and property values, meta included), rendered as
// JSON.
type ObjectGetResponse struct {
	ObjectId string          `json:"objectId"`
	Record   json.RawMessage `json:"record"`
}

// ObjectsCreateResponse is the body returned by POST /v1/spaces/:spaceId/objects.
// The SDK currently surfaces only the new object id; a richer info object
// will follow when the SDK exposes one.
type ObjectsCreateResponse struct {
	ObjectId string `json:"objectId"`
}

// ErrObjectAmbiguousId is the 409 a short object id answers when it
// matches several objects of the space. Details carry `shortId` and
// `candidates`, a list of ObjectCandidate.
const ErrObjectAmbiguousId = "object.ambiguous_id"

// ObjectCandidate is one object an ambiguous short id matches.
type ObjectCandidate struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}
