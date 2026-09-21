package api

import "time"

// SpaceDebugResponse is the wire shape of Space.Debug().Space().
// In-memory only — peers reset on server restart, so Peers is empty
// until at least one diff round has completed against a responsible
// peer.
type SpaceDebugResponse struct {
	SpaceId string          `json:"spaceId"`
	Peers   []PeerSyncStats `json:"peers"`
}

// PeerSyncStats mirrors space.PeerSyncStats. New + Changed are
// post-deletionState counts handed to the SDK's TreeSyncer
// (trees we'll actually pull / push). LastErr is omitted when the
// last round succeeded.
type PeerSyncStats struct {
	PeerId     string    `json:"peerId"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	New        int       `json:"new"`
	Changed    int       `json:"changed"`
	LastErr    string    `json:"lastErr,omitempty"`
}

// ObjectDebugResponse is the wire shape of
// Space.Debug().Object(objectId). The tree-structure fields are
// gathered under the object tree mutex so they form a
// jointly-consistent snapshot; the read blocks local writes for
// the duration of the IterateRoot walk.
//
// Diagnostic-only: this surface mirrors the SDK's DebugAPI, which
// is explicitly not stable. Production UI should subscribe to the
// per-space /sync-status endpoints once the SDK lands them.
type ObjectDebugResponse struct {
	ObjectId string `json:"objectId"`

	// SyncState is one of: unknown, offline, syncing, synced, error.
	// Pending is the set of heads the per-space syncstatus tracker is
	// waiting for a responsible-node HeadsApply on; empty means
	// converged with the last sender we trust.
	SyncState  string    `json:"syncState"`
	Pending    []string  `json:"pending"`
	LastSyncAt time.Time `json:"lastSyncAt"`

	// Heads / HeadsCount: current tree heads. HeadsCount > 1 means the
	// object is diverged across concurrent writers right now.
	// BranchCount counts every DAG branch that ever existed —
	// merged-in lineages (one per extra parent on a merge) plus
	// still-open ones (HeadsCount - 1). TreeLen is total change count.
	// Snapshots is the number of changes carrying IsSnapshot; costs a
	// full IterateRoot walk.
	Heads       []string `json:"heads"`
	HeadsCount  int      `json:"headsCount"`
	BranchCount int      `json:"branchCount"`
	TreeLen     int      `json:"treeLen"`
	Snapshots   int      `json:"snapshots"`

	// LatestVersionId is the lexid-max OrderId across Heads. Empty
	// when the tree has no heads (root-only / transient cold-restore).
	// Local to this peer — VersionIds don't match across peers.
	LatestVersionId string `json:"latestVersionId"`

	// MaxAddSeq is the controller's delivery-order watermark — the
	// highest AddSeq the apply path has seen. Exposed here as a
	// sanity check that the controller is keeping up with the tree;
	// not a cross-peer primitive.
	MaxAddSeq uint64 `json:"maxAddSeq"`
}

// P2PStatusResponse is the wire shape of SDK.P2PStatus() — the
// account-wide local-network (LAN) layer snapshot. Diagnostic; the
// per-space p2p state lives in SpaceSyncStatusResponse (p2p /
// localPeers).
//
// Possibility is one of: unknown, possible, nointerfaces, restricted.
// State is one of: unknown, notpossible, notconnected, connected,
// restricted.
type P2PStatusResponse struct {
	// PeerId is THIS device's peer id. Devices of one account must
	// each have a distinct peerId — devices sharing one cannot pair.
	PeerId          string          `json:"peerId"`
	Enabled         bool            `json:"enabled"`
	ListenerStarted bool            `json:"listenerStarted"`
	Port            int             `json:"port"`
	Possibility     string          `json:"possibility"`
	State           string          `json:"state"`
	Peers           []P2PPeerStatus `json:"peers"`
	// LocalNetwork is the host's answer from PUT /v1/p2p/local-network:
	// false means this process was told not to use the local network,
	// and Possibility reads `restricted` because of it. Distinct from
	// Enabled, which is the p2p.enabled config opt-out fixed at boot.
	LocalNetwork bool `json:"localNetwork"`
}

// LocalNetworkRequest is the body of PUT /v1/p2p/local-network — the
// host telling the server whether this device may use the local
// network at all. Two causes collapse into this one flag: the OS
// refused the permission, and the user turned LAN discovery off. The
// server does not distinguish them; the client knows which it is.
type LocalNetworkRequest struct {
	// Enabled false stops mDNS announce and browse within seconds and
	// leaves them stopped; true hands the decision back to the SDK's
	// own interface check. Persisted for the process lifetime only —
	// the host re-states it after every start.
	Enabled bool `json:"enabled"`
}

// LocalNetworkResponse is the resulting state, echoed so a caller need
// not re-read GET /v1/debug/p2p to confirm.
type LocalNetworkResponse struct {
	Enabled bool `json:"enabled"`
}

// P2PPeerStatus is one discovered LAN peer: the spaces it PROVED it
// shares with this account in the space exchange (the handshake reveals
// only the intersection of the two space sets, not everything the peer
// holds) and whether a connection is live right now.
type P2PPeerStatus struct {
	PeerId    string   `json:"peerId"`
	SpaceIds  []string `json:"spaceIds"`
	Connected bool     `json:"connected"`
}
