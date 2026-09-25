package api

// DeviceInfo is the wire shape of one row in the account's tech-space
// `devices` dataset: one row per device (peer), row id = peerId. The
// registry is member-replicated across the account's devices; each
// device upserts its own row (name/os/version stamped server-side on
// boot and via PUT /v1/devices/me).
//
// Apps is an open slug set (same convention as index scopes and UI
// command actions): presence of a slug means "installed on this
// device"; the per-slug object carries free-form app metadata
// (conventionally a `version` string). Nothing app-specific is baked
// into the server.
//
// ActiveClaims carries the per-app claim this device made — for
// itself, or for the device its Target names. Winners are resolved
// reader-side — see DevicesListResponse.Active and docs/23-devices.md
// § Election.
type DeviceInfo struct {
	PeerId  string `json:"peerId"`
	Name    string `json:"name,omitempty"`
	OS      string `json:"os,omitempty"`
	Version string `json:"version,omitempty"`

	Apps         map[string]map[string]any    `json:"apps,omitempty"`
	ActiveClaims map[string]DeviceActiveClaim `json:"activeClaims,omitempty"`
}

// DeviceActiveClaim is one device's claim that a device — itself, or
// the one Target names — is the active instance of one app slug. Seq
// is a writer-supplied monotonic counter (claim = max of all visible
// seqs + 1); At is the claim's unix-seconds timestamp. Deliberately
// NOT a CRDT version id: versionIds are peer-locally allocated, so
// they cannot arbitrate across devices — the claim data itself is what
// every reader resolves on.
type DeviceActiveClaim struct {
	Seq    int64  `json:"seq"`
	At     int64  `json:"at"`
	Target string `json:"target,omitempty"`
}

// DevicesListResponse is the body of GET /v1/devices.
//
// Active maps app slug → the peerId of that app's active device,
// resolved server-side by the canonical election rule (claims rank by
// highest Seq, then highest At, then lexicographically-largest claimer
// peerId; the winner is the target of the best claim whose target's
// row still carries the app slug). Both the UI and agent runtimes MUST
// consume this field rather than reimplementing the rule — one
// implementation, no divergent winners. A slug is absent when no claim
// names a device that has the app.
//
// Self is THIS server's own peerId — how a consumer (UI, agent
// runtime) tells whether it is the active device without a separate
// identity call.
type DevicesListResponse struct {
	Devices []DeviceInfo      `json:"devices"`
	Active  map[string]string `json:"active,omitempty"`
	Self    string            `json:"self"`
}

// DeviceUpdateRequest is the body of PUT /v1/devices/me — the
// restricted self-row upsert. peerId / os / version are stamped
// server-side and cannot be supplied; the body carries only the
// caller-owned fields, and only present fields are written (empty
// name = leave as-is). Apps entries merge per slug; an explicit null
// value uninstalls that slug (`{"apps": {"bao": null}}`).
type DeviceUpdateRequest struct {
	Name string                    `json:"name,omitempty"`
	Apps map[string]map[string]any `json:"apps,omitempty"`
}

// DeviceActivateRequest is the body of POST /v1/devices/activate —
// claim the active role for one app slug for the device PeerId names,
// or for THIS device when PeerId is absent or null. An empty PeerId is
// refused rather than read as a self claim.
type DeviceActivateRequest struct {
	App    string  `json:"app"`
	PeerId *string `json:"peerId,omitempty"`
}
