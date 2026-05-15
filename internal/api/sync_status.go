package api

import "time"

// SpaceSyncStatusResponse is the wire shape of Space.SyncStatus().Space()
// — the rolled-up sync state for one space. Cheap to read; safe to
// poll on a render tick.
//
// State is one of: unknown, offline, syncing, synced, error.
// Synced / Total count regular objects (excluding ACL / settings /
// spaceIndex / members system trees). NetworkPeers is the count of
// responsible nodes we've heard from.
type SpaceSyncStatusResponse struct {
	SpaceId      string    `json:"spaceId"`
	State        string    `json:"state"`
	Synced       int       `json:"synced"`
	Total        int       `json:"total"`
	NetworkPeers int       `json:"networkPeers"`
	LastSyncedAt time.Time `json:"lastSyncedAt"`
}

// ObjectSyncStatusResponse is the wire shape of
// Space.SyncStatus().Object(objectId). Unknown ids return State =
// "unknown" with a zero LastSyncAt — same contract as the SDK.
type ObjectSyncStatusResponse struct {
	ObjectId   string    `json:"objectId"`
	State      string    `json:"state"`
	LastSyncAt time.Time `json:"lastSyncAt"`
}

// SyncStatusReady is the data payload of the initial `event: ready`
// frame on /sync-status subscribe streams. Empty in v1; reserved so
// future additive fields don't break the wire shape.
type SyncStatusReady struct{}

// SyncStatusLagged is the data payload of an `event: lagged` frame on
// a sync-status subscribe stream. Emitted when the server's
// per-subscriber forwarder dropped a state event because the consumer
// fell behind. State-flip events are sparse, so this should be rare;
// when it happens the client should re-GET the current status to
// resync.
type SyncStatusLagged struct {
	Total uint64 `json:"total"`
}
