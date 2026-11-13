package api

import "time"

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

// AccountMetadata mirrors space.AccountMetadata. IconCID is the
// avatar's content address, set by PUT /v1/account/icon and read-only
// on PUT /v1/account/metadata.
type AccountMetadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
}

// AccountLimitsResponse is the body of GET /v1/account/limits: what the
// network grants the account. A part is omitted while the network has
// never answered for it on this device; storage also on a network that
// has no file nodes or keeps no account pool.
type AccountLimitsResponse struct {
	Storage      *AccountStorageLimits      `json:"storage,omitempty"`
	SharedSpaces *AccountSharedSpacesLimits `json:"sharedSpaces,omitempty"`
}

// AccountStorageLimits is the account's file-storage pool: every file
// is charged to its uploader, across all spaces.
type AccountStorageLimits struct {
	// LimitBytes is the pool's cap; 0 when the network grants none.
	LimitBytes uint64 `json:"limitBytes"`
	// UsageBytes is the bytes of durable files the account uploaded,
	// across its spaces.
	UsageBytes uint64 `json:"usageBytes"`
	// FetchedAt is when the network gave this answer.
	FetchedAt time.Time `json:"fetchedAt"`
}

// AccountSharedSpacesLimits bounds the shared spaces the account owns.
type AccountSharedSpacesLimits struct {
	// Limit is how many shared spaces the account may own.
	Limit uint32 `json:"limit"`
	// FetchedAt is when the network gave this answer.
	FetchedAt time.Time `json:"fetchedAt"`
}

// Error code namespace for the account avatar.
const (
	// ErrAccountIconNotFound — GET /v1/account/icon with no avatar set. 404.
	ErrAccountIconNotFound = "account.icon_not_found"
	// ErrAccountIconTooLarge — the upload exceeds the avatar size cap. 413.
	ErrAccountIconTooLarge = "account.icon_too_large"
	// ErrAccountIconNotImage — the upload's bytes do not carry the
	// signature of an accepted raster image format (SVG is refused). 415.
	ErrAccountIconNotImage = "account.icon_not_image"
	// ErrIdentityIconNotFound — GET /v1/identities/:identity/icon for an
	// identity not in the directory, whose profile names no avatar, or
	// whose avatar is over the size cap and refused unread. 404.
	ErrIdentityIconNotFound = "identity.icon_not_found"
)

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
