package api

import "encoding/json"

// Event is one envelope on the account-wide ephemeral event bus
// (POST /v1/events → GET /v1/events/subscribe). At-most-once: nothing
// is stored, there is no replay, a subscriber only sees events
// published after it connects. See docs/21-events.md.
//
// Type is an open dotted slug set (e.g. "process.progress",
// "ui.open_space") — new kinds need no server change. Scope routes
// delivery: EventScopeDevice fans out in-process only; account and
// space ride the SDK's pub/sub to the account's devices / the space's
// members. SpaceId is required iff Scope == EventScopeSpace. Target
// optionally narrows the subject (objectId, runId, processId, …) and
// is filterable on subscribe. Data is the free-form payload.
//
// Sender is server-stamped, never client-supplied: Identity is the
// publishing account (signature-verified for network scopes), Self is
// true when the event came from this account (any of its devices).
//
// A sessionId field is reserved for a future per-connection identity;
// not implemented.
type Event struct {
	Type    string          `json:"type"`
	Scope   string          `json:"scope"`
	SpaceId string          `json:"spaceId,omitempty"`
	Target  string          `json:"target,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Sender  *EventSender    `json:"sender,omitempty"`
}

// EventSender identifies the publisher of an Event. Stamped by the
// server; a sender field inside a publish body is rejected.
type EventSender struct {
	Identity string `json:"identity"`
	Self     bool   `json:"self"`
}

// Event Scope values — a closed set.
const (
	// EventScopeDevice — this process only: delivered to local
	// subscribers, never leaves the machine.
	EventScopeDevice = "device"
	// EventScopeAccount — every device of this account.
	EventScopeAccount = "account"
	// EventScopeSpace — every member of the space named by SpaceId.
	EventScopeSpace = "space"
)

// UI event types — the device-scope vocabulary UI windows subscribe to
// for navigation. Data carries {spaceId, objectId?, source?}:
// spaceId is the *target* space to open (independent of the envelope's
// scope routing), objectId is required for EventUIOpenObject, source is
// a free-form publisher hint (UI display only). An open set — new UI
// operations add a new type with no server change; unknown types are
// ignored client-side.
const (
	EventUIOpenSpace  = "ui.open_space"
	EventUIOpenObject = "ui.open_object"
)

// EventPublishRequest is the body of POST /v1/events — Event minus the
// server-stamped sender. Strict-bound: unknown top-level keys
// (including "sender") answer 400 request.unknown_field.
type EventPublishRequest struct {
	Type    string          `json:"type"`
	Scope   string          `json:"scope"`
	SpaceId string          `json:"spaceId,omitempty"`
	Target  string          `json:"target,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// EventPublishResponse is the body of POST /v1/events. Subscribers is
// how many local subscribers matched the event (0 = nobody listening).
// A valid publish always succeeds — the bus is fire-and-forget.
type EventPublishResponse struct {
	Subscribers int `json:"subscribers"`
}
