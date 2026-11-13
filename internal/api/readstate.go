package api

// UnreadResponse is one page of GET /spaces/{spaceId}/read-state/unread
// (SDK Space.ReadState().UnreadSince): the changes that became unread on
// this device after the cursor and still are, in that order.
type UnreadResponse struct {
	Changes []UnreadChange `json:"changes"`
	// Cursor resumes after the last returned change. Opaque.
	Cursor string `json:"cursor"`
}

// UnreadChange is one unread change: the object and records it touched
// and the read-tracking tags that say why it counts.
type UnreadChange struct {
	ObjectId  string   `json:"objectId"`
	Dataset   string   `json:"dataset"`
	RecordIds []string `json:"recordIds"`
	Tags      []string `json:"tags"`
	ChangeId  string   `json:"changeId"`
}
