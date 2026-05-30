package api

import "encoding/json"

// SubscribeEventOp is one $set or $unset op the SDK emits to
// subscribers (windowed Query.Subscribe or — historically — the raw
// apply stream). $inc / $addToSet / $pull / $incGated are projected
// to the post-apply value as $set / $unset before delivery, so a thin
// client without a CRDT engine can apply Ops naively.
//
// Path is the dotted-segment field path, always a JSON array on the
// wire (never `null` — an empty array `[]` means the record root). On
// $set, an empty Path activates the multi-field form: Payload is an
// object whose top-level keys are themselves dot-separated paths to
// assign at. Payload is the JSON-shaped post-apply value for $set,
// omitted for $unset.
type SubscribeEventOp struct {
	Type    string          `json:"type"`
	Path    []string        `json:"path"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SubscribeReady is the data payload of the initial `event: ready`
// SSE frame on every subscribe-shaped endpoint (query/subscribe,
// members/subscribe, sync-status/subscribe). Empty in v1; reserved so
// the wire shape doesn't change when we add fields.
type SubscribeReady struct{}

// SubscribeClosed is the data payload of the terminal `event: closed`
// frame. Reason is one of the SubscribeClosed* constants.
type SubscribeClosed struct {
	Reason string `json:"reason"`
}

// Reason values for SubscribeClosed. Stable strings; clients should
// switch on these rather than message text. Shared across every SSE
// family — query/subscribe, sync-status/subscribe, members/subscribe.
const (
	// SubscribeClosedServerShutdown — the server is exiting (signal or
	// POST /v1/shutdown). Reconnect when the server is back up.
	SubscribeClosedServerShutdown = "server_shutdown"

	// SubscribeClosedSDKClosed — the SDK released the underlying
	// subscription channel (typically because the space or SDK closed).
	// Reconnect after re-resolving the space.
	SubscribeClosedSDKClosed = "sdk_closed"

	// SubscribeClosedOverflow — only on query/subscribe streams. The
	// per-sub mailbox filled before the consumer could drain it; the
	// SDK closes the subscription rather than dropping events. Recovery
	// is resubscribe (which re-Snapshots).
	SubscribeClosedOverflow = "overflow"

	// SubscribeClosedDrifted — only on query/subscribe streams. More
	// than DriftBudgetPercent of the held window left without
	// replacements; the SDK closes the subscription rather than
	// re-Query the database on the hot path. Recovery is resubscribe.
	SubscribeClosedDrifted = "drifted"
)

// QuerySubscribeSnapshot is the data payload of the `event: snapshot`
// frame on a query/subscribe stream. It mirrors QueryResponse — the
// same point-in-time materialised window the bare Snapshot endpoint
// returns. Carried as its own type so future extensions (e.g. cursor)
// can land here without entangling Snapshot's HTTP shape.
type QuerySubscribeSnapshot struct {
	Records []json.RawMessage `json:"records"`
	Total   *int              `json:"total,omitempty"`
}

// QuerySubscribeEvent is one batch of windowed transitions delivered
// in an `event: changes` SSE frame. It groups every record-level
// change observed during one CRDT apply:
//
//   - Added — records that entered the visible window.
//   - Updated — records already in the window whose state changed.
//   - Removed — records that left the visible window. The wire does
//     NOT distinguish between deleted / filter-rejected / displaced
//     (pushed past Limit); from the consumer's view, drop the id from
//     local state regardless of cause.
//
// VersionId is the per-change DAG order of the underlying CRDT apply.
// Useful for fence-and-replay semantics ("I've processed up to X —
// discard ≤ X"). VersionIds are locally-scoped (each peer assigns its
// own); don't compare across peers.
//
// Total is intentionally absent — the windowed engine does not
// maintain a live counter. Callers who need a refreshed count call
// Snapshot again.
type QuerySubscribeEvent struct {
	VersionId string                  `json:"versionId"`
	Added     []QuerySubscribeRecord  `json:"added,omitempty"`
	Updated   []QuerySubscribeRecord  `json:"updated,omitempty"`
	Removed   []string                `json:"removed,omitempty"`
}

// QuerySubscribeRecord is one record's worth of state inside a
// QuerySubscribeEvent's Added / Updated slices. Doc is the full
// post-apply JSON value (safe to retain past the event). Ops carries
// the per-field $set / $unset ops from the triggering change — same
// shape as SubscribeEventOp on the raw stream, so callers can apply
// atomic updates against a local mirror without re-materialising the
// whole record.
type QuerySubscribeRecord struct {
	Id  string             `json:"id"`
	Doc json.RawMessage    `json:"doc"`
	Ops []SubscribeEventOp `json:"ops,omitempty"`
}
