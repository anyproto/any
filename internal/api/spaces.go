package api

import "time"

// SpaceCreateRequest is the body of POST /v1/spaces. Mirrors
// space.CreateRequest.
type SpaceCreateRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
	SpaceType   string `json:"spaceType,omitempty"`
}

// SpaceOneToOneRequest is the body of POST /v1/spaces/one-to-one. The
// single field mirrors the arg of Service.OneToOne — the other party's
// account identity (the `id` from their GET /v1/account), exchanged
// out-of-band. Derivation is symmetric: both peers calling with each
// other's identity land on the same spaceId.
type SpaceOneToOneRequest struct {
	OtherIdentity string `json:"otherIdentity"`
}

// SpaceRegisterIncomingRequest is the body of
// POST /v1/spaces/one-to-one/register-incoming. Mirrors
// Service.RegisterIncoming: drop an out-of-band incoming 1-1 request
// (the app learned of the peer's intent through its own channel) into a
// device-local `one_to_one_pending` row for the user to approve, without
// materializing storage. DisplayHint is an optional name/icon snapshot
// for the UI. No-op if a row for the derived space already exists.
type SpaceRegisterIncomingRequest struct {
	PeerIdentity string          `json:"peerIdentity"`
	DisplayHint  AccountMetadata `json:"displayHint,omitempty"`
}

// SpaceInfo is the wire shape for space.SpaceInfo. Status and OwnRole
// are rendered as strings rather than the SDK's uint8 enums; see
// SpaceStatus / SpacePermission for the mapping.
//
// OwnRole is the caller's own role in the space, mirrored from ACL
// state by the SDK (one pass at space load + one per applied ACL
// record, so permission changes land as row updates). Present on list
// rows and single-space responses alike. "none" doubles as "not
// mirrored yet" — treat it as unknown/no-access, with
// GET …/members/me as the authoritative per-space read. On a 1-1
// space participants report "writer", never "owner" (the ACL owner is
// a synthetic shared key).
//
// SpaceIndexObjectId is the deterministic id of the in-space
// `spaceIndex` derived object — stable across peers, useful for
// clients that want to attach a subscribe stream for live metadata
// updates. Omitted when the server can't resolve a Space handle for
// this row (e.g. tombstoned entries in `GET /v1/spaces`).
//
// SpaceType is the app-level classification tag (read from the in-space
// spaceIndex), distinct from the on-wire header Type: 1-1 spaces carry
// "any.onetoone", created spaces "any.space". Use it to filter direct chats vs regular
// spaces client-side. Author is the space owner's
// account identity, resolved best-effort from the ACL (empty when the ACL
// isn't loadable).
// Settings is the account-private, client-owned per-space settings
// object (free-form keys, scalar values — numbers surface as JSON
// numbers/float64). Written per key via
// PATCH /v1/spaces/:spaceId/settings; synced across the account's own
// devices through the tech space, never visible to other members.
// Omitted when never written.
//
// Push is the space's push-notification key material (see
// docs/20-push.md § Receiver-side keys), mirrored from ACL state so a
// mobile client can cache it and decrypt push payloads while `any` is
// not running. Populated on list rows AND single-space responses (it's
// a plain row field — no space load needed); omitted until the SDK's
// per-space mirror has run, e.g. on a joiner whose access is still
// pending. Rotation (encKey/encKeyId change) is observed live on the
// `POST /v1/spaces/query/subscribe` stream.
type SpaceInfo struct {
	Id                 string         `json:"id"`
	Type               string         `json:"type,omitempty"`
	SpaceType          string         `json:"spaceType,omitempty"`
	Author             string         `json:"author,omitempty"`
	Name               string         `json:"name,omitempty"`
	Description        string         `json:"description,omitempty"`
	IconCID            string         `json:"iconCid,omitempty"`
	Status             string         `json:"status"`
	OwnRole            string         `json:"ownRole" enums:"owner,admin,writer,reader,guest,none"`
	CreatedAt          time.Time      `json:"createdAt"`
	Settings           map[string]any `json:"settings,omitempty"`
	SpaceIndexObjectId string         `json:"spaceIndexObjectId,omitempty"`
	Push               *SpacePushKeys `json:"push,omitempty"`
	// Derived marks a space created by the account's own derivation
	// (POST /v1/spaces/derived/:name or another consumer of the SDK's
	// Derive). Derived spaces are permanent — DELETE refuses them with
	// 409 space.derived_undeletable. Absent on created / joined / 1-1
	// spaces.
	Derived bool `json:"derived,omitempty"`
}

// DerivedSpaceInfo is one row of GET /v1/spaces/derived — a registry
// entry resolved against the account: the deterministic spaceId the
// entry derives to, and whether a usable space row exists (materialized
// here or on any of the account's devices). Resolving ids never
// creates anything; materialize with POST /v1/spaces/derived/:name.
// Status is the raw row status when a row exists (omitted otherwise);
// a "deleted" row — wedged before the permanence guard existed —
// reports created=false and refuses materialization.
type DerivedSpaceInfo struct {
	Name    string `json:"name"`
	SpaceId string `json:"spaceId"`
	Created bool   `json:"created"`
	Status  string `json:"status,omitempty"`
}

// DerivedSpaceListResponse is the body of GET /v1/spaces/derived.
type DerivedSpaceListResponse struct {
	Spaces []DerivedSpaceInfo `json:"spaces"`
}

// SpacePushKeys is the per-space key material a push RECEIVER caches —
// the wire twin of the SDK's space.PushKeys, byte-compatible with
// anytype-heart's spacePushNotificationKey /
// spacePushNotificationEncryptionKey space-view details.
//
// SpaceKey is base64(std) of the protobuf-marshalled ed25519 private
// key identifying the space on the push server. EncKey is base64(std)
// of the raw AES payload key derived from the CURRENT ACL read key;
// EncKeyId is hex(sha256(raw EncKey bytes)) — the value an incoming
// push carries as its KeyId. Clients keep an append-only per-space
// {encKeyId → encKey} cache: EncKey rotates with the ACL read key, and
// payloads encrypted before a rotation still arrive under the old id.
// Holding EncKey decrypts push payloads only (one-way derivation from
// the read key), never space data.
type SpacePushKeys struct {
	SpaceKey string `json:"spaceKey"`
	EncKey   string `json:"encKey"`
	EncKeyId string `json:"encKeyId"`
}

// SpaceUpdateRequest is the body of PATCH /v1/spaces/:spaceId. Pointer
// semantics mirror space.SetMetadataRequest: a nil field is
// leave-unchanged; a non-nil pointer to an empty string sets the
// field to empty. spaceType is intentionally not patchable — it's
// pinned by the initial Create. At least one of Name, Description, or
// IconCID must be present; an all-empty body returns
// 400 request.missing_field.
type SpaceUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	IconCID     *string `json:"iconCid,omitempty"`
}

// SpaceSettingsPatchRequest is the body of
// PATCH /v1/spaces/:spaceId/settings — a per-key patch of the
// account-private `settings` object on the space's tech-space row
// (deliberately separate from PATCH /v1/spaces/:spaceId, which writes
// the member-replicated name/description/icon). Keys are the caller's
// vocabulary: non-empty, dot-free (single level under `settings`).
// Values are scalars — string, number, or bool. At least one set or
// unset entry is required; a key may not appear in both. Works on any
// row the account knows, deleted/tombstoned included.
type SpaceSettingsPatchRequest struct {
	Set   map[string]any `json:"set,omitempty"`
	Unset []string       `json:"unset,omitempty"`
}

// SpaceListResponse is the body of GET /v1/spaces.
type SpaceListResponse struct {
	Spaces []SpaceInfo `json:"spaces"`
}

// Space status string values exposed on the wire. Mirror the
// space.Status enum 1:1.
const (
	SpaceStatusUnknown    = "unknown"
	SpaceStatusActive     = "active"
	SpaceStatusJoining    = "joining"
	SpaceStatusLeaving    = "leaving"
	SpaceStatusDeleted    = "deleted"
	SpaceStatusRemoteDead = "remote_dead"
	// SpaceStatusOneToOnePending is an incoming 1-1 (direct) space
	// awaiting local approval — not yet materialized or synced. Discover
	// these via GET /v1/spaces?status=one_to_one_pending (hidden from the
	// active-only default list), then accept/decline. Device-local.
	SpaceStatusOneToOnePending = "one_to_one_pending"
	// SpaceStatusOneToOneDeclined is a 1-1 the user declined — a synced,
	// account-wide sticky marker. Hidden from the default list like
	// deleted; an explicit POST /v1/spaces/one-to-one overrides it.
	SpaceStatusOneToOneDeclined = "one_to_one_declined"
	// SpaceStatusInvitePending is a regular space another account added
	// this account to directly (ACL add by identity). Already a full
	// member; approval is a local gate — nothing is downloaded until
	// accepted. Synced account-wide. Discover via
	// GET /v1/spaces?status=invite_pending, then
	// POST /v1/spaces/:spaceId/invite/accept or .../invite/decline.
	SpaceStatusInvitePending = "invite_pending"
	// SpaceStatusInviteDeclined is a direct-add invite the user declined
	// — synced, sticky, non-terminal (accept overrides). Hidden from the
	// default list like deleted.
	SpaceStatusInviteDeclined = "invite_declined"
	// SpaceStatusGuestRevoked is a guest-key (public-access) space whose
	// shared guest identity was removed from the ACL — the owner revoked
	// public access. The local copy stays readable; new content no longer
	// arrives. Remove with DELETE /v1/spaces/:spaceId.
	SpaceStatusGuestRevoked = "guest_revoked"
)

// Space permission string values for SpaceInfo.OwnRole. Mirror the
// space.Permission enum 1:1. Pre-staged for the ACL/Members endpoints
// that still return 501.
const (
	SpacePermissionNone   = "none"
	SpacePermissionReader = "reader"
	SpacePermissionGuest  = "guest"
	SpacePermissionWriter = "writer"
	SpacePermissionAdmin  = "admin"
	SpacePermissionOwner  = "owner"
)
