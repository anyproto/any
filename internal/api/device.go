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
// ActiveClaims carries this device's per-app claim to be the app's
// active instance. Winners are resolved reader-side — see
// DevicesListResponse.Active and docs/23-devices.md § Election.
type DeviceInfo struct {
	PeerId  string `json:"peerId"`
	Name    string `json:"name,omitempty"`
	OS      string `json:"os,omitempty"`
	Version string `json:"version,omitempty"`

	Apps         map[string]map[string]any    `json:"apps,omitempty"`
	ActiveClaims map[string]DeviceActiveClaim `json:"activeClaims,omitempty"`
}

// DeviceActiveClaim is one device's claim to be the active instance of
// one app slug. Seq is a writer-supplied monotonic counter (claim =
// max of all visible seqs + 1); At is the claim's unix-seconds
// timestamp. Deliberately NOT a CRDT version id: versionIds are
// peer-locally allocated, so they cannot arbitrate across devices —
// the claim data itself is what every reader resolves on.
type DeviceActiveClaim struct {
	Seq int64 `json:"seq"`
	At  int64 `json:"at"`
}

// DevicesListResponse is the body of GET /v1/devices.
//
// Active maps app slug → the peerId of that app's active device,
// resolved server-side by the canonical election rule (highest claim
// Seq, tiebreak highest At, final tiebreak lexicographically-largest
// peerId, candidates limited to devices whose row still carries the
// app slug). Both the UI and agent runtimes MUST consume this field
// rather than reimplementing the rule — one implementation, no
// divergent winners. A slug is absent when no installed device has
// claimed it.
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
// claim the active role for one app slug on THIS device.
type DeviceActivateRequest struct {
	App string `json:"app"`
}
