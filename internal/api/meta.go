package api

import "time"

type HealthResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	Account   string    `json:"account"`
	// Bootstrapping is true while the booted SDK's background boot pass
	// (eager space loading + offline catch-up) is still running. The
	// server serves throughout; per-space convergence is /sync-status.
	// False when unauthorized and after the pass completes.
	Bootstrapping bool `json:"bootstrapping"`
}
