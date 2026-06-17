package api

// UICommand is one agent→UI directive published to the account-wide
// in-memory command channel (POST /v1/ui/commands) and delivered to
// every connected subscriber as an `event: command` SSE frame
// (GET /v1/ui/commands/subscribe).
//
// Action is an open slug set; UICommandOpenSpace / UICommandOpenObject
// are the v1 vocabulary. SpaceId is the *target* space — independent of
// where the publisher runs, so one channel drives navigation anywhere.
// ObjectId is required iff Action == UICommandOpenObject. Source is a
// free-form hint (e.g. "bobrik"); UI display only. See
// docs/15-ui-commands.md.
type UICommand struct {
	Action   string `json:"action"`
	SpaceId  string `json:"spaceId"`
	ObjectId string `json:"objectId,omitempty"`
	Source   string `json:"source,omitempty"`
}

// UICommand Action values — an open slug set, not exhaustive. New UI
// operations add a new action (plus payload fields) with no server
// change; unknown actions are ignored client-side.
const (
	UICommandOpenSpace  = "open_space"
	UICommandOpenObject = "open_object"
)

// UICommandPublishResponse is the body of POST /v1/ui/commands.
// Subscribers is how many connected UI windows received the command
// (0 = nobody listening). The publish still succeeds with 0 — the
// channel is fire-and-forget, at-most-once.
type UICommandPublishResponse struct {
	Subscribers int `json:"subscribers"`
}
