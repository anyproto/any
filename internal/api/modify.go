package api

// ModifyResult is the response shape for Space.Modify, Space.Delete,
// and PropertiesAPI.SetBase. Always includes versionId/changeId/recordIds
// — clients use RecordIds[0] to read auto-derived ids when they
// submitted a record with empty Id.
//
// Rejections is the partial-success list: ops the handler refused at
// apply time (kind mismatch, unknown property, immutable field…). The
// change still committed with this versionId/changeId, but those ops
// did not land. Empty / omitted means everything took.
type ModifyResult struct {
	VersionId  string        `json:"versionId"`
	ChangeId   string        `json:"changeId"`
	RecordIds  []string      `json:"recordIds"`
	Rejections []OpRejection `json:"rejections,omitempty"`
}

// OpRejection mirrors space.OpRejection 1:1 on the wire. RecordIndex
// and OpIndex are positional; RecordId resolves the auto-derived id
// for empty-id records; Reason is human-readable text from the
// handler. OpIndex == -1 means the whole record was rejected
// (BeforeCreate / BeforeDelete).
type OpRejection struct {
	RecordIndex int    `json:"recordIndex"`
	RecordId    string `json:"recordId,omitempty"`
	OpIndex     int    `json:"opIndex"`
	Reason      string `json:"reason"`
}
