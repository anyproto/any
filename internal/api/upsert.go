package api

// UpsertRequest is the body of POST /v1/spaces/:spaceId/upsert —
// schema-driven batch ingest into an id:user dataset (space.UpsertBatch).
// Per record: absent id ⇒ created; present ⇒ only declared-mutable
// fields are diffed, changed ones written per-path, identical records
// skipped; a differing write-once field ⇒ whole-record rejection. The
// record id is the idempotency key — re-running an identical batch is a
// no-op. Not transactional against concurrent writers: intended
// deployment is a single ingest writer per dataset.
type UpsertRequest struct {
	ObjectId string         `json:"objectId"`
	Dataset  string         `json:"dataset"`
	Records  []UpsertRecord `json:"records"`
	// PageSize caps records per emitted change. 0 = 500.
	PageSize int      `json:"pageSize,omitempty"`
	TraceIds []string `json:"traceIds,omitempty"`
}

// UpsertRecord is one row: the caller-supplied record id plus the
// desired field values (top-level field → value).
type UpsertRecord struct {
	Id     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// UpsertResult mirrors space.UpsertResult. Pages holds one ModifyResult
// per emitted change, in page order (pages with nothing to write are
// absent). Rejections is the partial-success list — the call still
// returns 200; counters cover the records that landed.
type UpsertResult struct {
	Pages      []ModifyResult    `json:"pages"`
	Created    int               `json:"created"`
	Updated    int               `json:"updated"`
	Skipped    int               `json:"skipped"`
	Rejections []UpsertRejection `json:"rejections,omitempty"`
}

// UpsertRejection reports one rejected record. Code is a stable machine
// code: upsert.immutable_field, upsert.not_author, upsert.record_deleted,
// or upsert.rejected (creation screening — missing required field, id
// pattern/length violation, undeclared field, stamped-field write —
// with the specific cause in reason).
type UpsertRejection struct {
	Index  int    `json:"index"`
	Id     string `json:"id,omitempty"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}
