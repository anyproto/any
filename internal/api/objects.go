package api

// ObjectsCreateResponse is the body returned by POST /v1/spaces/:spaceId/objects.
// The SDK currently surfaces only the new object id; a richer info object
// will follow when the SDK exposes one.
type ObjectsCreateResponse struct {
	ObjectId string `json:"objectId"`
}
