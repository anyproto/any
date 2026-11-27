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
	// Deletion is the account's deletion state on the network, as the
	// network last gave it; omitted while it has never answered on
	// this device.
	Deletion *AccountDeletion `json:"deletion,omitempty"`
}

// AccountDeletion is the account's deletion state on the network: the
// body of POST and DELETE /v1/account/deletion, a part of GET
// /v1/account and of a waiting login, and the data of the
// account.deletion event.
type AccountDeletion struct {
	// State: active — no deletion is requested; pending — requested
	// and revertible until deleteAt; deleted — the network has deleted
	// the account's data.
	State string `json:"state" enums:"active,pending,deleted"`
	// DeleteAt is when the network deletes the account's data. Set
	// while state is pending.
	DeleteAt *time.Time `json:"deleteAt,omitempty"`
	// FetchedAt is when the network gave this answer.
	FetchedAt time.Time `json:"fetchedAt"`
}

// EventAccountDeletion is the device-scope event published when the
// account's deletion state changes: data is AccountDeletion.
const EventAccountDeletion = "account.deletion"

// Error code namespace for the account's deletion from the network.
const (
	// ErrAccountDeletionPending — a deletion request while one is
	// already pending. 409.
	ErrAccountDeletionPending = "account.deletion_pending"
	// ErrAccountDeletionNotPending — a revert while no deletion is
	// pending. 409.
	ErrAccountDeletionNotPending = "account.deletion_not_pending"
	// ErrAccountDeleted — a request or a revert once the network has
	// deleted the account's data. 409.
	ErrAccountDeleted = "account.deleted"
	// ErrAccountNotOnNetwork — a request or a revert for an account
	// the network has never registered. 409.
	ErrAccountNotOnNetwork = "account.not_on_network"
	// ErrAccountNetworkUnavailable — the network did not complete a
	// request or a revert. 503.
	ErrAccountNetworkUnavailable = "account.network_unavailable"
)

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
