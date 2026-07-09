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
// SpaceIndexObjectId is the deterministic id of the in-space
// `spaceIndex` derived object — stable across peers, useful for
// clients that want to attach a subscribe stream for live metadata
// updates. Omitted when the server can't resolve a Space handle for
// this row (e.g. tombstoned entries in `GET /v1/spaces`).
//
// GeneralChatObjectId is the deterministic id of the space's single
// "general" chat object (see chat.GeneralChatSeed). Populated —
// materializing the object on first sight — on single-space responses
// (create / get / one-to-one / join); omitted on the `GET /v1/spaces`
// list rows, which stay a cheap read that never materializes chats.
// Clients should write to and read this chat rather than creating
// their own, so a space keeps exactly one chat.
//
// SpaceType is the app-level classification tag (read from the in-space
// spaceIndex), distinct from the on-wire header Type: 1-1 spaces carry
// "anytype.onetoone", regular spaces "anytype.space". Use it to filter
// direct chats vs regular spaces client-side. Author is the space owner's
// account identity, resolved best-effort from the ACL (empty when the ACL
// isn't loadable).
type SpaceInfo struct {
	Id                  string    `json:"id"`
	Type                string    `json:"type,omitempty"`
	SpaceType           string    `json:"spaceType,omitempty"`
	Author              string    `json:"author,omitempty"`
	Name                string    `json:"name,omitempty"`
	Description         string    `json:"description,omitempty"`
	IconCID             string    `json:"iconCid,omitempty"`
	Status              string    `json:"status"`
	OwnRole             string    `json:"ownRole"`
	CreatedAt           time.Time `json:"createdAt"`
	SpaceIndexObjectId  string    `json:"spaceIndexObjectId,omitempty"`
	GeneralChatObjectId string    `json:"generalChatObjectId,omitempty"`
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

// SpaceListResponse is the body of GET /v1/spaces.
type SpaceListResponse struct {
	Spaces []SpaceInfo `json:"spaces"`
}

// GeneralChatResponse is the body of GET /v1/spaces/:spaceId/chat — the
// deterministic per-space general chat object id (see
// chat.GeneralChatSeed). Same derive-on-first-use semantics as
// AgentBrainResponse: the GET materializes the object if it doesn't
// exist yet, so a client can call it once and then write/read the
// chat_messages dataset on the returned id.
type GeneralChatResponse struct {
	ObjectId string `json:"objectId"`
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
