package api

// Backlink is one reverse reference: the object ObjectId links to the
// requested object through property PropId, declared on type TypeId.
// Only live references count — the value's array contains the target's
// "any://<objectId>" URI and the declaring type is currently attached
// to the referencing object.
type Backlink struct {
	ObjectId string `json:"objectId"`
	TypeId   string `json:"typeId"`
	PropId   string `json:"propId"`
}

// BacklinksResponse is the body of
// GET /v1/spaces/:spaceId/objects/:objectId/backlinks. Backlinks is
// never null — an object nobody references gets [].
type BacklinksResponse struct {
	Backlinks []Backlink `json:"backlinks"`
}
