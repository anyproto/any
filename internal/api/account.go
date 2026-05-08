package api

// AccountResponse is the body of GET /v1/account. Metadata is the
// locally-stored profile (read from tech-space); omitted when no
// profile has ever been written on this device. Symmetric with the
// PUT /v1/account/metadata write path — readback is deterministic,
// no coordinator round-trip, no 60-second watcher tick.
type AccountResponse struct {
	Id       string           `json:"id"`
	Metadata *AccountMetadata `json:"metadata,omitempty"`
}

// AccountMetadata mirrors space.AccountMetadata.
type AccountMetadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
}
