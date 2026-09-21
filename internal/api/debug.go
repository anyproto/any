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
// account-wide snapshot of both direct layers: the local network (the
// top-level fields) and the internet-wide one (global). Diagnostic;
// the per-space p2p state lives in SpaceSyncStatusResponse (p2p /
// localPeers).
//
// Possibility is one of: unknown, possible, nointerfaces, restricted,
// disabled.
// State is one of: unknown, notpossible, notconnected, connected,
// restricted; it is connected when ANY direct peer is, LAN or global.
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
	// LocalDiscovery is the mDNS switch (PUT /v1/local-discovery): false
	// means announce and browse are off and Possibility reads `disabled`.
	// Distinct from Enabled, the p2p.enabled config opt-out fixed at
	// boot, which also takes the QUIC listener down.
	LocalDiscovery bool `json:"localDiscovery"`
	// Global is the internet-wide layer (iroh over the relays).
	Global GlobalP2PStatus `json:"global"`
}

// LocalDiscoveryRequest is the body of PUT /v1/local-discovery: whether
// this device announces and browses on the local network (mDNS). The
// QUIC listener and the global p2p layer are unaffected.
type LocalDiscoveryRequest struct {
	// Enabled false stops announce and browse within seconds and keeps
	// them stopped; true starts them at once. Required: an absent value
	// is 400 request.missing_field rather than a silent off. Held for
	// the process lifetime, across logout and account switches; a host
	// restates it after a restart, and may state it before the first
	// POST /v1/auth.
	Enabled *bool `json:"enabled"`
}

// LocalDiscoveryResponse is the switch state: what the live engine
// reports, or, with no engine up, what the next boot will start with.
type LocalDiscoveryResponse struct {
	Enabled bool `json:"enabled"`
}

// P2PPeerStatus is one discovered peer. SpaceIds is the union over
// every source that knows the peer: for a LAN-only peer, the spaces it
// PROVED it shares in the space exchange (the handshake reveals only
// the intersection of the two space sets, not everything the peer
// holds); for a peer also known through records, the spaces those
// records name as well. Read Sources before treating the set as the
// LAN handshake's proof.
type P2PPeerStatus struct {
	PeerId    string   `json:"peerId"`
	SpaceIds  []string `json:"spaceIds"`
	Connected bool     `json:"connected"`
	// Sources that know the peer: "lan", "global" (a space's records),
	// "account" (this account's own device record), in any
	// combination. A peer in the top-level list always carries at
	// least "lan"; one in global.peers never does.
	Sources []string `json:"sources,omitempty"`
	// LastSeen is the newest liveness evidence — a record heartbeat or
	// a local connection. Zero for LAN-only peers.
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	// Tier derived from LastSeen: active, stale, dormant, disabled. It
	// sets how often the peer is dialed. Empty for LAN-only peers.
	Tier string `json:"tier,omitempty"`
	// Failures counts consecutive failed global dials.
	Failures int `json:"failures,omitempty"`
}

// GlobalP2PStatus is the internet-wide layer: this device's iroh
// endpoint, its relay session, and every peer known through records.
type GlobalP2PStatus struct {
	Enabled bool `json:"enabled"`
	// EndpointId is this device's iroh endpoint id (its device key).
	EndpointId string `json:"endpointId"`
	// Ticket is what this device publishes for others to dial; empty
	// until the relay session is up. Relay-only by construction — it
	// never carries this device's IP addresses.
	Ticket string `json:"ticket,omitempty"`
	// HomeRelay is the relay URL inside Ticket.
	HomeRelay string `json:"homeRelay,omitempty"`
	// RelayConnected — the session to the home relay is up. Until it
	// is, this device can dial out but cannot be reached.
	RelayConnected bool `json:"relayConnected"`
	// Peers are all peers known through records, connected or not.
	Peers []P2PPeerStatus `json:"peers"`
	// Account is the account-level discovery record (pkarr).
	Account AccountDiscoveryStatus `json:"account"`
}

// AccountDiscoveryStatus is the pkarr record through which this
// account's own devices find each other — including a device that
// holds nothing but the mnemonic. Disabled when no pkarr relay is
// configured; devices then know each other only through the records of
// the spaces they share.
type AccountDiscoveryStatus struct {
	Enabled bool     `json:"enabled"`
	Relays  []string `json:"relays"`
	// Devices is how many sibling devices the record names.
	Devices int `json:"devices"`
	// OwnEntry — the record names this device with its current relay.
	OwnEntry      bool       `json:"ownEntry"`
	LastResolved  *time.Time `json:"lastResolved,omitempty"`
	LastPublished *time.Time `json:"lastPublished,omitempty"`
	// LastError is the last failed cycle; empty after a good one.
	LastError string `json:"lastError,omitempty"`
	// ClockAheadMs is how far the relays' record was dated past this
	// device's clock — a sibling whose clock runs ahead. Zero when it
	// was not.
	ClockAheadMs int64 `json:"clockAheadMs,omitempty"`
}
