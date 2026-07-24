package api

import "time"

type HealthResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	Account   string    `json:"account"`
	// Warming is true while a deferred-warmup boot (config
	// deferWarmup, forced on for embedded/mobile) is still running its
	// background sync warmup — per-space headsync catch-up, profile
	// republish. Local reads and writes work throughout; clients
	// wanting a sync-progress affordance poll this or subscribe to
	// GET /v1/sync-status/subscribe for per-space convergence. Always
	// false on synchronous boots and while unauthorized.
	Warming bool `json:"warming"`
}
