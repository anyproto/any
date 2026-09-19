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
	// InviteToken is the active request-to-join link, recoverable by any
	// member after its key syncs. Omitted for legacy or unavailable keys.
	// Approval remains owner/admin-only; revoked keys are never returned.
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

// ACLOwnershipRequest is the body of POST /v1/spaces/:spaceId/acl/ownership.
type ACLOwnershipRequest struct {
	NewOwner     string `json:"newOwner"`
	OldOwnerPerm string `json:"oldOwnerPerm"`
}
