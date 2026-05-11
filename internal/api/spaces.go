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

// SpaceInfo is the wire shape for space.SpaceInfo. Status and OwnRole
// are rendered as strings rather than the SDK's uint8 enums; see
// SpaceStatus / SpacePermission for the mapping.
//
// SpaceIndexObjectId is the deterministic id of the in-space
// `spaceIndex` derived object — stable across peers, useful for
// clients that want to attach a subscribe stream for live metadata
// updates. Omitted when the server can't resolve a Space handle for
// this row (e.g. tombstoned entries in `GET /v1/spaces`).
type SpaceInfo struct {
	Id                 string    `json:"id"`
	Type               string    `json:"type,omitempty"`
	Name               string    `json:"name,omitempty"`
	Description        string    `json:"description,omitempty"`
	IconCID            string    `json:"iconCid,omitempty"`
	Status             string    `json:"status"`
	OwnRole            string    `json:"ownRole"`
	CreatedAt          time.Time `json:"createdAt"`
	SpaceIndexObjectId string    `json:"spaceIndexObjectId,omitempty"`
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

// Space status string values exposed on the wire. Mirror the
// space.Status enum 1:1.
const (
	SpaceStatusUnknown    = "unknown"
	SpaceStatusActive     = "active"
	SpaceStatusJoining    = "joining"
	SpaceStatusLeaving    = "leaving"
	SpaceStatusDeleted    = "deleted"
	SpaceStatusRemoteDead = "remote_dead"
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
