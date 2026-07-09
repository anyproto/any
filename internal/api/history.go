package api

import "encoding/json"

// Version-history wire shapes (SDK Space.History(); SDK
// docs/version-history-proposal.md). A version is a ChangeId — the
// content-hash CID of a DAG change, stable across peers and restarts.
// "State at version X" is the projection of exactly X's causal past.

// HistoryListResponse is one page of GET
// /spaces/{spaceId}/objects/{objectId}/history.
type HistoryListResponse struct {
	Changes []HistoryChange `json:"changes"`
	// Cursor resumes the next page; empty = history exhausted.
	Cursor string `json:"cursor,omitempty"`
}

// HistoryChange is one listed change, or one coalesced group of
// consecutive same-author changes (groupSize > 1) whose handle is the
// group's newest ChangeId.
type HistoryChange struct {
	Version   string                 `json:"version"`
	Author    string                 `json:"author"`
	Timestamp int64                  `json:"timestamp"` // author clock, Unix seconds — display-only
	Dataset   string                 `json:"dataset"`
	TraceIds  []string               `json:"traceIds,omitempty"`
	Touched   []HistoryTouchedRecord `json:"touched,omitempty"`
	// Truncated is RESERVED (always false today): the SDK keeps full
	// history locally. It becomes meaningful with the future
	// snapshot-horizon contract.
	Truncated bool `json:"truncated,omitempty"`
	GroupSize int  `json:"groupSize"`
}

// HistoryTouchedRecord names one record a change touched with its op
// kinds (e.g. "$set", "delete").
type HistoryTouchedRecord struct {
	Dataset  string   `json:"dataset"`
	RecordId string   `json:"recordId"`
	Ops      []string `json:"ops,omitempty"`
}

// HistoryViewResponse is GET
// /spaces/{spaceId}/objects/{objectId}/history/{version}: the object's
// live records as of that version, grouped by dataset. Synced scope
// only — local/account values have no history and are excluded.
type HistoryViewResponse struct {
	Version  string               `json:"version"`
	Datasets []HistoryViewDataset `json:"datasets"`
}

// HistoryViewDataset is one dataset's records at a version. Records
// are raw dataset rows (same shape as /query results).
type HistoryViewDataset struct {
	Dataset string            `json:"dataset"`
	Records []json.RawMessage `json:"records"`
}

// HistoryRecordResponse is GET .../history/{version}/datasets/
// {dataset}/records/{recordId}: one record at a version. Exists=false
// when the record was not present at that cut; Deleted=true when it
// was tombstoned (Record then carries the tombstone row).
type HistoryRecordResponse struct {
	Version  string          `json:"version"`
	Dataset  string          `json:"dataset"`
	RecordId string          `json:"recordId"`
	Exists   bool            `json:"exists"`
	Deleted  bool            `json:"deleted,omitempty"`
	Record   json.RawMessage `json:"record,omitempty"`
}

// HistoryDiffResponse is GET .../history/diff. Base is empty for a
// per-change effect diff (version diffed against its DAG parents).
type HistoryDiffResponse struct {
	Base     string               `json:"base,omitempty"`
	Version  string               `json:"version"`
	Datasets []HistoryDatasetDiff `json:"datasets"`
}

// HistoryDatasetDiff groups record diffs of one dataset.
type HistoryDatasetDiff struct {
	Dataset string              `json:"dataset"`
	Records []HistoryRecordDiff `json:"records"`
}

// HistoryRecordDiff is one record's difference. Kind is one of
// "added", "removed", "changed", "deleted".
type HistoryRecordDiff struct {
	Id     string             `json:"id"`
	Kind   string             `json:"kind"`
	Fields []HistoryFieldDiff `json:"fields,omitempty"`
}

// HistoryFieldDiff is one leaf-level field difference; absent side is
// omitted. Peer-local bookkeeping (_ver etc.) never appears.
type HistoryFieldDiff struct {
	Path   []string        `json:"path"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}
