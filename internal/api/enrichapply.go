package api

// EnrichApplyRequest is the body for POST /v1/spaces/:spaceId/enrich/apply —
// deterministically apply a reviewed enrich_proposal and delete it.
type EnrichApplyRequest struct {
	ProposalId string `json:"proposalId"`
}

// EnrichApplyResponse reports what the apply did. Failures is per-item; a
// non-empty list still means the rest applied.
type EnrichApplyResponse struct {
	Created             int      `json:"created"`
	PropertiesSet       int      `json:"propertiesSet"`
	EnrichedDataWritten int      `json:"enrichedDataWritten"`
	ProposalDeleted     bool     `json:"proposalDeleted"`
	Failures            []string `json:"failures"`
}
