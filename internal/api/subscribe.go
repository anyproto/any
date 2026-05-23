package api

import "encoding/json"

// SubscribeEvent is the data payload of an SSE `event: changes` frame.
// Mirrors space.Event 1:1.
//
//   - VersionId is the per-change DAG order. Clients running the
//     subscribe-then-query-then-apply pattern dedup per op path:
//     compare this against `_ver.<op.path>` on the queried record
//     (walking the `_ver` tree segment by segment, falling back to
//     the closest `*` default key) to decide whether the snapshot
//     already covers each op. `_ver.id` is the creation marker —
//     set once and only lowered on delete — so it cannot be used as
//     a record-level high-water mark.
//
//   - Records carries the post-apply effect of the change projected
//     to a flat list of $set / $unset ops per record. The SDK has
//     already merged with full CRDT semantics; what ships is the
//     resulting field-level patch, so a thin client without a CRDT
//     engine can apply Records directly to a JSON-shaped local copy.
//     A record with Deleted=true means "drop this id from your local
//     state"; Ops is empty in that case.
type SubscribeEvent struct {
	SpaceId   string                 `json:"spaceId"`
	ObjectId  string                 `json:"objectId"`
	Dataset   string                 `json:"dataset"`
	VersionId string                 `json:"versionId"`
	Records   []SubscribeEventRecord `json:"records,omitempty"`
}

// SubscribeEventRecord is one record's worth of projected change inside
// a SubscribeEvent. Id is the record id within Dataset (for shared
// per-space datasets like "objects" this equals SubscribeEvent.ObjectId).
// Variant is empty for the canonical record; non-empty for sibling
// variants (account / device property records).
//
// Created=true means this change first materialised the record — Ops
// carries its full initial field set. Deleted=true means the change
// tombstoned the record — drop it locally; Ops is empty. The two are
// mutually exclusive; neither set means a plain field update.
//
// The record's `_ver` map is never on the wire — a consumer derives it
// from SubscribeEvent.VersionId: a created record is all-at versionId,
// and each applied op stamps `_ver.<op.path> = versionId` locally.
type SubscribeEventRecord struct {
	Id      string             `json:"id"`
	Variant string             `json:"variant,omitempty"`
	Created bool               `json:"created,omitempty"`
	Deleted bool               `json:"deleted,omitempty"`
	Ops     []SubscribeEventOp `json:"ops,omitempty"`
}

// SubscribeEventOp is one $set or $unset op inside an EventRecord. The
// SDK only ever emits $set / $unset to subscribers — $inc / $addToSet
// / $pull / $incGated are projected to the post-apply value before
// delivery, so a thin client can apply Ops naively.
//
// Path is the dotted-segment field path, always a JSON array on the
// wire (never `null` — an empty array `[]` means the record root). On
// $set, an empty Path activates the multi-field form: Payload is an
// object whose keys are dot-separated paths, each value is what to
// assign there. Payload is the JSON-shaped post-apply value for $set,
// omitted for $unset.
type SubscribeEventOp struct {
	Type    string          `json:"type"`
	Path    []string        `json:"path"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SubscribeReady is the data payload of the initial `event: ready`
// frame, sent once the SDK Subscribe call has succeeded and the
// stream is open. Empty in v1; reserved so the wire shape doesn't
// change when we add fields (e.g. server-issued cursor).
type SubscribeReady struct{}

// SubscribeClosed is the data payload of the terminal `event: closed`
// frame. Reason is one of the SubscribeClosed* constants.
type SubscribeClosed struct {
	Reason string `json:"reason"`
}

// SubscribeLagged is the data payload of an `event: lagged` frame,
// emitted when the SDK has dropped events for this subscriber since
// the last `lagged` (typically because the consumer fell behind the
// per-subscriber mailbox capacity). Total is the cumulative drop
// count reported by Subscription.Dropped at the moment the frame is
// emitted; clients should treat any `lagged` as "re-Query for current
// state, the in-stream events are no longer a full picture".
type SubscribeLagged struct {
	Total uint64 `json:"total"`
}

// Reason values for SubscribeClosed. Stable strings; clients should
// switch on these rather than message text.
const (
	// SubscribeClosedServerShutdown — the server is exiting (signal or
	// POST /v1/shutdown). Reconnect when the server is back up.
	SubscribeClosedServerShutdown = "server_shutdown"

	// SubscribeClosedSDKClosed — the SDK released the underlying
	// subscription channel (typically because the space or SDK closed).
	// Reconnect after re-resolving the space.
	SubscribeClosedSDKClosed = "sdk_closed"
)
