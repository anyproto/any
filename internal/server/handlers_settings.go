package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// spaceSettingsPatch handles PATCH /v1/spaces/:spaceId/settings —
// Service.SetSettings: a per-key patch of the account-private
// `settings` object on the space's tech-space row. Deliberately
// separate from PATCH /v1/spaces/:spaceId, which writes the
// MEMBER-REPLICATED spaceIndex (name/description/icon) — mixing
// account-private and member-visible writes on one endpoint is a
// trap. Settings work with push disabled (they are a generic
// client-settings surface; push merely reads settings.notifyMode).
//
// No resolveSpace here: Spaces().Get materializes the space, but
// SetSettings works on ANY known row — deleted tombstones and pending
// invites included (mute a pending 1-1 before accepting). The SDK's
// own existence check covers the unknown-id case; body validation
// runs first so 400s never pay for a lookup.
//
// Reads are passthrough: SpaceInfo.settings on GET /v1/spaces[/:id],
// or the raw rows from POST /v1/spaces/query[/subscribe] for live
// cross-device updates.
//
//	@Summary	Patch the account-private per-space settings
//	@Tags		spaces
//	@Accept		json
//	@Param		spaceId	path	string							true	"Space ID"
//	@Param		body	body	api.SpaceSettingsPatchRequest	true	"Per-key set/unset patch"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/settings [patch]
func (d *deps) spaceSettingsPatch(c echo.Context) error {
	var req api.SpaceSettingsPatchRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if len(req.Set) == 0 && len(req.Unset) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one set or unset entry is required", nil)
	}
	// Mirror the SDK's SettingsOps rules at the boundary so violations
	// come back as clean 400s instead of stringly 500s: single-level
	// keys, scalar values, no set/unset overlap.
	for key, val := range req.Set {
		if msg := settingsKeyProblem(key); msg != "" {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				msg, map[string]any{"key": key})
		}
		switch val.(type) {
		case string, bool, float64: // the only shapes JSON decodes scalars to
		default:
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"settings values must be scalars (string, number, bool)", map[string]any{"key": key})
		}
	}
	for _, key := range req.Unset {
		if msg := settingsKeyProblem(key); msg != "" {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				msg, map[string]any{"key": key})
		}
		if _, both := req.Set[key]; both {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"key present in both set and unset", map[string]any{"key": key})
		}
	}

	id := c.Param("spaceId")
	if err := d.sdk.Spaces().SetSettings(c.Request().Context(), id, req.Set, req.Unset); err != nil {
		// spaceimpl guards the unknown-id case with a plain error (no
		// exported sentinel yet) — string-match, same pragmatic pattern
		// as inviteStateError.
		if strings.Contains(err.Error(), "unknown space") {
			return writeError(c, http.StatusNotFound, "space.not_found",
				"space not found", map[string]any{"spaceId": id})
		}
		return spaceError(c, err, id)
	}
	// Local settings writes converge immediately: kick the push
	// reconcile (hash-gated and nearly free, so no need to check which
	// keys changed). Remote-origin writes still ride the 5-minute tick
	// (docs/20-push.md § Settings).
	if d.push != nil {
		d.push.Kick()
	}
	return c.NoContent(http.StatusNoContent)
}

// settingsKeyProblem enforces the v1 key shape (non-empty, dot-free —
// a dotted key would silently become a deeper path in the CRDT).
// Returns the rejection message, or "" when the key is valid.
func settingsKeyProblem(key string) string {
	if key == "" {
		return "settings keys must be non-empty"
	}
	if strings.ContainsRune(key, '.') {
		return "settings keys must be single-level (no dots)"
	}
	return ""
}
