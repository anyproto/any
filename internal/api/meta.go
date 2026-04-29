package api

import "time"

type HealthResponse struct {
	Status    string    `json:"status"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	Account   string    `json:"account"`
}
