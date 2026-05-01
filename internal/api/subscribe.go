package api

// SubscribeEvent is the data payload of an SSE `event: change` frame.
// Mirrors space.Event 1:1 — routing tuple plus AddSeq, no per-record
// delta. Consumers re-Query for current state when they need a richer
// view.
type SubscribeEvent struct {
	SpaceId  string `json:"spaceId"`
	ObjectId string `json:"objectId"`
	Dataset  string `json:"dataset"`
	AddSeq   uint64 `json:"addSeq"`
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
