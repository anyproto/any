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
type SpaceInfo struct {
	Id          string    `json:"id"`
	Type        string    `json:"type,omitempty"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	IconCID     string    `json:"iconCid,omitempty"`
	Status      string    `json:"status"`
	OwnRole     string    `json:"ownRole"`
	CreatedAt   time.Time `json:"createdAt"`
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
