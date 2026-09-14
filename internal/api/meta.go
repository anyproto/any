package api

import "time"

type HealthResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	// NetworkId is the any-sync network this server joins: the networkId
	// of the nodeconf it started with. Set whether or not an account is
	// authorized. The server names no networks; clients map well-known
	// ids (production, staging) themselves.
	NetworkId string `json:"networkId"`
	Account   string `json:"account"`
	// Bootstrapping is true while the booted SDK's background boot pass
	// (eager space loading + offline catch-up) is still running. The
	// server serves throughout; per-space convergence is /sync-status.
	// False when unauthorized and after the pass completes.
	Bootstrapping bool `json:"bootstrapping"`
	// CRDTVersion is the account's CRDT data-model version state
	// (absent when unauthorized): the version this server's SDK
	// supports, the one recorded on the account's tech space, and
	// `newer` — true when the recorded one is above the supported one,
	// which makes the account read-only until the server is upgraded
	// (every synced write answers 409 sdk.crdt_version_newer).
	CRDTVersion *CRDTVersionState `json:"crdtVersion,omitempty"`
}

// CRDTVersionState mirrors the SDK's account CRDT-version state.
type CRDTVersionState struct {
	Supported int  `json:"supported"`
	Stored    int  `json:"stored"`
	Newer     bool `json:"newer"`
}
