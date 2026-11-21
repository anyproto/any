package api

// AccountMetadata is shared with account.go — used here for join-time
// metadata (Service.Join) and ACL.AddAccounts entries.

// Member status string values. Mirror space.MemberStatus 1:1.
const (
	MemberStatusUnknown  = "unknown"
	MemberStatusJoining  = "joining"
	MemberStatusActive   = "active"
	MemberStatusRemoved  = "removed"
	MemberStatusDeclined = "declined"
	MemberStatusRemoving = "removing"
	MemberStatusCanceled = "canceled"
)

// Member is the wire shape of space.Member. Permission and Status are
// rendered as strings; identityRepo-decoded profile fields surface
// when populated.
type Member struct {
	Identity        string `json:"identity"`
	Permission      string `json:"permission"`
	Status          string `json:"status"`
	Name            string `json:"name,omitempty"`
	Description     string `json:"description,omitempty"`
	IconCID         string `json:"iconCid,omitempty"`
	RequestRecordId string `json:"requestRecordId,omitempty"`
}

// MembersListResponse is the body of GET /v1/spaces/:spaceId/members.
type MembersListResponse struct {
	Members []Member `json:"members"`
	// RotationPending reports that members were removed from the space
	// without a read key change and still hold the current key: they
	// read what is written until it changes. An admin or the owner of
	// the parent removes members of a child space this way. A device
	// of an admin or the owner of this space that holds the space
	// changes the key by itself, at start and while it runs; the field
	// reads false once that record is applied here.
	RotationPending bool `json:"rotationPending"`
}

// JoinRequest mirrors space.JoinRequestInfo.
type JoinRequest struct {
	RecordId    string `json:"recordId"`
	Identity    string `json:"identity"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	IconCID     string `json:"iconCid,omitempty"`
}

// JoinRequestsResponse is the body of GET /v1/spaces/:spaceId/members/requests.
type JoinRequestsResponse struct {
	Requests []JoinRequest `json:"requests"`
}

// Invite endpoints --------------------------------------------------

// InviteCreateResponse is the body returned by POST /v1/spaces/:spaceId/invites.
// `inviteToken` is the share-friendly base58 token (space.EncodeInvite) —
// the only piece a joiner needs to call POST /v1/spaces/join.
type InviteCreateResponse struct {
	SpaceId     string `json:"spaceId"`
	InviteToken string `json:"inviteToken"`
}

// InviteInfo mirrors space.InviteInfo. RecordId is what the
// DELETE /v1/spaces/:spaceId/invites/:recordId path expects.
type InviteInfo struct {
	RecordId string `json:"recordId"`
	// Permission is "none" for the request-to-join invites v1 mints —
	// the role is chosen at accept time, not carried by the invite.
	Permission string `json:"permission"`
	// InviteToken is the same share token POST /invites returned at
	// mint time, recovered from the minting account's synced custody.
	// Present only on that account's devices — other members (any
	// role) never held the private key and get no token. Also omitted
	// for invites minted before custody shipped (regenerate once to
	// make the token durable) and for custody gone stale (invite
	// replaced or revoked on another device).
	InviteToken string `json:"inviteToken,omitempty"`
}

// InvitesListResponse is the body of GET /v1/spaces/:spaceId/invites.
type InvitesListResponse struct {
	Invites []InviteInfo `json:"invites"`
}

// SpaceJoinRequest is the body of POST /v1/spaces/join. Mirrors
// space.JoinRequest.
type SpaceJoinRequest struct {
	InviteToken string          `json:"inviteToken"`
	Metadata    AccountMetadata `json:"metadata,omitempty"`
}

// ChildMember is an account of a child space as its ACL holds it, read
// from the parent side. An ACL names accounts by identity: without the
// child's keys there is no name, description or icon.
type ChildMember struct {
	Identity   string `json:"identity"`
	Permission string `json:"permission" enums:"owner,admin,writer,reader,guest,none"`
	Status     string `json:"status" enums:"unknown,joining,active,removed,declined,removing,canceled"`
}

// ChildMembersResponse is the body of GET /v1/children/:childId/members.
type ChildMembersResponse struct {
	Members []ChildMember `json:"members"`
	// RotationPending reports that members removed from the child
	// still hold its current read key: true from a removal made from
	// the parent side until an admin or the owner of the child has
	// changed the key. A child that stays pending has no device of an
	// admin or the owner left to change it.
	RotationPending bool `json:"rotationPending"`
}

// Member event kinds — wire values for MemberEvent.Kind.
const (
	MemberEventKindAdded   = "added"
	MemberEventKindChanged = "changed"
	MemberEventKindRemoved = "removed"
)

// MemberEventPayload is the data payload of an `event: member` frame
// on the members subscribe SSE stream. Kind discriminates the change;
// Member carries the post-event state; Previous carries pre-event
// state (nil for Added).
type MemberEventPayload struct {
	Kind     string  `json:"kind"`
	Member   Member  `json:"member"`
	Previous *Member `json:"previous"`
}

// ACL endpoints -----------------------------------------------------

// ACLAcceptRequest is the body of POST /v1/spaces/:spaceId/acl/accept.
// `permission` is granted on accept; valid values are the
// SpacePermission* constants minus SpacePermissionOwner.
type ACLAcceptRequest struct {
	RequestRecordId string `json:"requestRecordId"`
	Permission      string `json:"permission"`
}

// ACLDeclineRequest is the body of POST /v1/spaces/:spaceId/acl/decline.
type ACLDeclineRequest struct {
	Identity string `json:"identity"`
}

// ACLPermissionChange is one entry in a ChangePermissions batch.
type ACLPermissionChange struct {
	Identity   string `json:"identity"`
	Permission string `json:"permission"`
}

// ACLChangePermissionsRequest is the body of POST /v1/spaces/:spaceId/acl/permissions.
type ACLChangePermissionsRequest struct {
	Changes []ACLPermissionChange `json:"changes"`
}

// ACLRemoveRequest is the body of POST /v1/spaces/:spaceId/acl/remove.
type ACLRemoveRequest struct {
	Identities []string `json:"identities"`
}

// ACLAddAccount is one entry in an AddAccounts batch.
type ACLAddAccount struct {
	Identity   string          `json:"identity"`
	Permission string          `json:"permission"`
	Metadata   AccountMetadata `json:"metadata,omitempty"`
}

// ACLAddRequest is the body of POST /v1/spaces/:spaceId/acl/add.
type ACLAddRequest struct {
	Accounts []ACLAddAccount `json:"accounts"`
}

// ACLOwnershipRequest is the body of POST /v1/spaces/:spaceId/acl/ownership
// and of POST /v1/children/:childId/acl/ownership.
type ACLOwnershipRequest struct {
	NewOwner string `json:"newOwner"`
	// OldOwnerPerm is the role the previous owner keeps. Required.
	OldOwnerPerm string `json:"oldOwnerPerm" enums:"reader,writer,admin"`
}
