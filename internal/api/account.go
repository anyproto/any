package api

// AccountResponse is the body of GET /v1/account. Metadata is the
// locally-stored profile (read from tech-space); omitted when no
// profile has ever been written on this device. Symmetric with the
// PUT /v1/account/metadata write path — readback is deterministic,
// no coordinator round-trip, no 60-second watcher tick.
type AccountResponse struct {
	Id       string           `json:"id"`
	Metadata *AccountMetadata `json:"metadata,omitempty"`
	// TechSpaceId is the account's tech space — the :spaceId for
	// account-level bundles (see docs/03-api.md § Bundles).
	TechSpaceId string `json:"techSpaceId"`
}

// AccountMetadata mirrors space.AccountMetadata.
type AccountMetadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
}

// AccessCodeRequest is the body of POST /v1/account/access-code.
type AccessCodeRequest struct {
	// Code is the invite code as typed; compared upper-cased with
	// whitespace removed.
	Code string `json:"code"`
}

// AccessCodeResponse relays the invite service's answer.
type AccessCodeResponse struct {
	// Status is "accepted" (limits are being granted) or
	// "already_redeemed" (this account redeemed a code before).
	Status       string `json:"status" enums:"accepted,already_redeemed"`
	RedemptionId string `json:"redemptionId,omitempty"`
}
