package api

// PushTokenSetRequest is the body of POST /v1/push/token: this
// device's mobile push transport ("ios" | "android" — the push
// server's platform enum has no desktop entry, so desktop/headless
// servers are send-only) and the opaque APNs/FCM token the shell
// obtained. Re-POST on token rotation; DELETE on logout.
type PushTokenSetRequest struct {
	Platform string `json:"platform"`
	Token    string `json:"token"`
}

// Push platform wire values (mirror space.PushPlatform).
const (
	PushPlatformIOS     = "ios"
	PushPlatformAndroid = "android"
)

// PushTokenStatus is the body of GET /v1/push/token — the LOCAL
// registration state (does a persisted token exist on this device,
// and for which platform). No push-node round trip.
type PushTokenStatus struct {
	Registered bool   `json:"registered"`
	Platform   string `json:"platform,omitempty"`
}

// PushSubscription is one row of GET /v1/push/subscriptions: the
// base58-encoded space push PUBLIC key (the push server's space
// identifier — not a spaceId) and the subscribed topic string.
// Signatures are never returned by the server.
type PushSubscription struct {
	SpaceKey string `json:"spaceKey"`
	Topic    string `json:"topic"`
}

// PushSubscriptionsResponse is the body of GET /v1/push/subscriptions
// — the account's topic set as the push server holds it.
type PushSubscriptionsResponse struct {
	Subscriptions []PushSubscription `json:"subscriptions"`
}
