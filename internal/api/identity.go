package api

// IdentityInfo is the wire shape of space.IdentityInfo — one row in the
// account-global identities directory: every account identity this
// account has encountered (across spaces, 1-1s, inbox invites). It is a
// device-local, persistent cache.
//
// Name / Description / IconCID are the last identityRepo profile resolved
// for this identity and are OMITTED until resolved: profiles are
// encrypted, so a known identity surfaces id-only until its decryption
// key arrives (via a shared space's ACL or a 1-1 invite) and the
// background fetch completes. Clients must tolerate an empty name.
//
// SpaceIds is the set of spaces where this identity is currently seen
// (pruned when we leave/offload a space). The directory carries NO
// per-space rights — roles (owner/admin/writer/reader) live on the
// members list (GET /v1/spaces/:id/members), which is the authoritative
// per-space roster.
type IdentityInfo struct {
	Identity    string   `json:"identity"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	IconCID     string   `json:"iconCid,omitempty"`
	SpaceIds    []string `json:"spaceIds"`
}

// IdentitiesListResponse is the body of GET /v1/identities.
type IdentitiesListResponse struct {
	Identities []IdentityInfo `json:"identities"`
}

// IdentityEventPayload is the data payload of an `event: identities`
// frame on the identities subscribe SSE stream — one frame per directory
// change batch, mirroring space.IdentityListEvent. Added/Updated carry
// the post-event row; Removed carries the identity strings that left the
// directory.
type IdentityEventPayload struct {
	Added   []IdentityInfo `json:"added,omitempty"`
	Updated []IdentityInfo `json:"updated,omitempty"`
	Removed []string       `json:"removed,omitempty"`
}
