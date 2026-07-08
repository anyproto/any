package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func registerSpaceRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces", d.spaceCreate)
	g.GET("/spaces", d.spaceList)
	// Generic windowed query/subscribe over the account's space list,
	// via Service.Query on the tech-space `spaces` dataset. Registered
	// before the :spaceId routes so the static `query` segment isn't
	// swallowed by the param matcher.
	g.POST("/spaces/query", d.spaceListQuery)
	g.POST("/spaces/query/subscribe", d.spaceListQuerySubscribe)
	g.GET("/spaces/:spaceId", d.spaceGet)
	g.PATCH("/spaces/:spaceId", d.spaceUpdate)
	g.DELETE("/spaces/:spaceId", d.spaceDelete)
	g.POST("/spaces/:spaceId/sync", d.spaceSync)
	g.POST("/spaces/:spaceId/search", d.search)

	g.POST("/spaces/join", d.spaceJoin)
	// Space lifecycle the SDK exposes but doesn't implement yet.
	g.POST("/spaces/derive", notImplemented("Spaces.Derive"))

	// One-to-one (direct) spaces — derived 1-1 shared by two identities.
	// Static `/spaces/one-to-one[...]` segments are registered before the
	// `:spaceId` matcher so they aren't swallowed (and there is no bare
	// POST /spaces/:spaceId to collide with). See docs/03-api.md § Spaces
	// and the SDK's docs/13-one-to-one-spaces.md. Discovery of incoming
	// (pending) requests is via the space list filtered on
	// status=one_to_one_pending — no bespoke endpoint.
	g.POST("/spaces/one-to-one", d.spaceOneToOne)
	g.POST("/spaces/one-to-one/register-incoming", d.spaceOneToOneRegisterIncoming)
	g.POST("/spaces/:spaceId/one-to-one/accept", d.spaceOneToOneAccept)
	g.POST("/spaces/:spaceId/one-to-one/decline", d.spaceOneToOneDecline)

	// Object lifecycle + data plane.
	g.POST("/spaces/:spaceId/objects", d.objectCreate)
	g.POST("/spaces/:spaceId/objects/query", d.spaceQueryObjects)
	g.POST("/spaces/:spaceId/objects/query/subscribe", d.spaceQueryObjectsSubscribe)
	g.POST("/spaces/:spaceId/objects/aggregate", d.spaceAggregateObjects)
	g.DELETE("/spaces/:spaceId/objects/:objectId", d.objectDelete)
	// Reverse reference lookup over links-format property values —
	// consumer-side read, no SDK method behind it (handlers_backlinks.go).
	g.GET("/spaces/:spaceId/objects/:objectId/backlinks", d.objectBacklinks)

	// Editor (built-in type — see internal/editor). Atomic blocks +
	// markdown bridge, both backed by the per-object editor_blocks
	// dataset. Liveness reuses the per-object query/subscribe endpoint
	// with dataset=editor_blocks.
	g.GET("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownGet)
	g.PUT("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownSet)
	g.POST("/spaces/:spaceId/objects/:objectId/editor/markdown/append", d.markdownAppend)
	g.POST("/spaces/:spaceId/objects/:objectId/editor/blocks", d.blocksCreate)
	g.PATCH("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksPatch)
	g.DELETE("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksDelete)

	// Chat (built-in type — see internal/chat). Writes only here;
	// reads + liveness go through /query and /query/subscribe with
	// dataset=chat_messages.
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages", d.chatSend)
	g.PATCH("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatEdit)
	g.DELETE("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatDelete)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji", d.chatReact)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/read-all", d.chatReadAll)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read", d.chatRead)

	// Agent data layer (built-in types — see internal/agentlog,
	// internal/agentmem and docs/11-agent-memory.md). Writes only here;
	// reads + liveness go through /query and /query/subscribe with
	// dataset ∈ {agent_turns, agent_chunks, agent_memory_items}.
	// Turns/chunks attach to the chat object (multitype chat +
	// agent_log); memory items live on the per-space brain object,
	// which the server resolves itself (GET /agent/brain exposes the
	// deterministic id for reads).
	g.POST("/spaces/:spaceId/objects/:objectId/agent/turns", d.agentTurnAppend)
	g.POST("/spaces/:spaceId/objects/:objectId/agent/chunks", d.agentChunkCreate)
	g.GET("/spaces/:spaceId/agent/brain", d.agentBrainGet)
	g.POST("/spaces/:spaceId/agent/memory", d.agentMemoryCreate)
	g.PATCH("/spaces/:spaceId/agent/memory/:itemId", d.agentMemoryEvolve)
	g.DELETE("/spaces/:spaceId/agent/memory/:itemId", d.agentMemoryDelete)
	g.POST("/spaces/:spaceId/query", d.spaceQuery)
	g.POST("/spaces/:spaceId/query/subscribe", d.spaceQuerySubscribe)
	g.POST("/spaces/:spaceId/aggregate", d.spaceAggregate)
	g.POST("/spaces/:spaceId/modify", d.spaceModify)
	g.POST("/spaces/:spaceId/delete-records", d.spaceDeleteRecords)

	// Dataset schema discovery — JSON Schema (with per-field x-scope) for
	// every dataset the space hosts. See Space.Datasets().
	g.GET("/spaces/:spaceId/datasets", d.spaceDatasets)

	// Types.
	g.GET("/spaces/:spaceId/types", d.typeList)
	g.POST("/spaces/:spaceId/types", d.typeCreate)
	g.GET("/spaces/:spaceId/types/:typeId", d.typeGet)
	g.DELETE("/spaces/:spaceId/types/:typeId", notImplemented("Types.Delete"))
	g.GET("/spaces/:spaceId/types/:typeId/properties", d.typeProperties)
	g.POST("/spaces/:spaceId/types/:typeId/properties", d.typeAddProperty)
	g.DELETE("/spaces/:spaceId/types/:typeId/properties/:propId", notImplemented("Types.RemoveProperty"))
	g.PATCH("/spaces/:spaceId/types/:typeId/properties/:propId", notImplemented("Types.UpdatePropertyMeta"))

	// Properties. Scoped properties (v0.0.11) unified the former
	// base/account/device set endpoints into one scope-aware Set — the
	// patch's propIds determine the scope (all must share one).
	g.GET("/spaces/:spaceId/properties/:objectId", d.propertiesGet)
	g.POST("/spaces/:spaceId/properties/:objectId/set/:typeId", d.propertiesSet)
	g.POST("/spaces/:spaceId/properties/:objectId/attach/:typeId", notImplemented("Properties.AttachType"))
	g.POST("/spaces/:spaceId/properties/:objectId/detach/:typeId", notImplemented("Properties.DetachType"))

	// Members. Static segments before the :identity wildcard so /me,
	// /requests, and /subscribe don't get swallowed by the param matcher.
	g.GET("/spaces/:spaceId/members", d.memberList)
	g.GET("/spaces/:spaceId/members/me", d.memberMe)
	g.GET("/spaces/:spaceId/members/requests", d.memberJoinRequests)
	g.GET("/spaces/:spaceId/members/subscribe", d.subscribeMembers)
	g.GET("/spaces/:spaceId/members/:identity", d.memberGet)

	// Invites — owner/admin side mints + revokes; joiners use the
	// /v1/spaces/join endpoint with the share-friendly token.
	g.POST("/spaces/:spaceId/invites", d.inviteCreate)
	g.GET("/spaces/:spaceId/invites", d.inviteList)
	g.GET("/spaces/:spaceId/invites/:recordId", d.inviteGet)
	g.DELETE("/spaces/:spaceId/invites", d.inviteRevokeAll)
	g.DELETE("/spaces/:spaceId/invites/:recordId", d.inviteRevoke)

	// ACL — owner/admin operations on the membership state.
	g.POST("/spaces/:spaceId/acl/accept", d.aclAccept)
	g.POST("/spaces/:spaceId/acl/decline", d.aclDecline)
	g.POST("/spaces/:spaceId/acl/permissions", d.aclChangePermissions)
	g.POST("/spaces/:spaceId/acl/remove", d.aclRemove)
	g.POST("/spaces/:spaceId/acl/add", d.aclAdd)
	g.POST("/spaces/:spaceId/acl/ownership", d.aclOwnership)
	g.POST("/spaces/:spaceId/acl/self-remove", d.aclSelfRemove)
	g.POST("/spaces/:spaceId/acl/cancel-join", d.aclCancelJoin)
	g.POST("/spaces/:spaceId/acl/stop-sharing", d.aclStopSharing)

	// Files (files v2 — see internal/server/handlers_files.go and
	// docs/17-files.md). Attach streams the raw request body (exempt
	// from the global BodyLimit — see routes.go); /content serves raw
	// bytes with Range support. Static segments (stats, subscribe)
	// before the :fileId matcher so they aren't swallowed. Payload-row
	// reads go through the per-object files/query[/subscribe] bridge —
	// the payloads dataset lives on a derived child object the generic
	// /query can't reach.
	g.POST("/spaces/:spaceId/objects/:objectId/files", d.fileAttach)
	g.POST("/spaces/:spaceId/objects/:objectId/files/query", d.filesQuery)
	g.POST("/spaces/:spaceId/objects/:objectId/files/query/subscribe", d.filesQuerySubscribe)
	g.GET("/spaces/:spaceId/files", d.fileList)
	g.GET("/spaces/:spaceId/files/stats", d.fileStats)
	g.GET("/spaces/:spaceId/files/subscribe", d.fileSubscribe)
	g.GET("/spaces/:spaceId/files/:fileId", d.fileGet)
	g.GET("/spaces/:spaceId/files/:fileId/content", d.fileContent)
	g.GET("/spaces/:spaceId/files/:fileId/status", d.fileStatusGet)
	g.POST("/spaces/:spaceId/files/:fileId/pin", d.filePin)
	g.POST("/spaces/:spaceId/files/:fileId/retry", d.fileRetry)
	g.POST("/spaces/:spaceId/files/:fileId/offload", d.fileOffload)

	// Sync status — per-space rollup + per-object state. The peers
	// row stays 501 until the SDK exposes a stable per-space peer
	// list on SyncStatusAPI (DebugAPI.Space surfaces the diagnostic
	// equivalent today, see /debug below).
	g.GET("/spaces/:spaceId/sync-status", d.syncStatusSpaceGet)
	g.GET("/spaces/:spaceId/sync-status/objects/:objectId", d.syncStatusObjectGet)
	g.GET("/spaces/:spaceId/sync-status/objects/:objectId/subscribe", d.syncStatusObjectSubscribe)
	g.GET("/spaces/:spaceId/sync-status/peers", notImplemented("SyncStatus.Peers"))

	// Debug — diagnostic surface; not a stable interface. Production UI
	// should use /sync-status (above) once the SDK lands the
	// production-grade methods. See internal/api/debug.go and
	// any-sync-sdk/space/debug.go for the field-level docs.
	g.GET("/spaces/:spaceId/debug", d.debugSpace)
	g.GET("/spaces/:spaceId/debug/objects/:objectId", d.debugObject)
}

// @Summary	Create a space
// @Tags		spaces
// @Accept		json
// @Produce	json
// @Param		body	body		api.SpaceCreateRequest	true	"Space params"
// @Success	201		{object}	api.SpaceInfo
// @Failure	400		{object}	api.ErrorEnvelope
// @Failure	500		{object}	api.ErrorEnvelope
// @Router		/spaces [post]
func (d *deps) spaceCreate(c echo.Context) error {
	var req api.SpaceCreateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	sp, err := d.sdk.Spaces().Create(c.Request().Context(), space.CreateRequest{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		SpaceType:   req.SpaceType,
	})
	if err != nil {
		return spaceError(c, err, "")
	}
	return c.JSON(http.StatusCreated, spaceToAPI(sp))
}

// @Summary	List spaces
// @Tags		spaces
// @Produce	json
// @Param		status	query		string	false	"Filter by status; defaults to active-only. Pass 'all' to include deleted/dead rows."
// @Success	200	{object}	api.SpaceListResponse
// @Failure	500	{object}	api.ErrorEnvelope
// @Router		/spaces [get]
func (d *deps) spaceList(c echo.Context) error {
	ctx := c.Request().Context()
	infos, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return spaceError(c, err, "")
	}
	// TEMPORARY WORKAROUND: default the list to active spaces only.
	// Soft-deleted spaces are never offloaded yet (no proper space
	// deletion / offloading — see docs/07-roadmap.md), so the raw list
	// accumulates dozens of dead rows that swamp the real ones. Until
	// that lands, hide non-active rows by default; `?status=all` (or an
	// explicit status string) opts back into the full list. Remove this
	// filter once deletion actually reclaims the rows.
	statusFilter := c.QueryParam("status")
	if statusFilter == "" {
		statusFilter = api.SpaceStatusActive
	}
	out := make([]api.SpaceInfo, 0, len(infos))
	for _, info := range infos {
		if statusFilter != "all" && spaceStatusString(info.Status) != statusFilter {
			continue
		}
		row := spaceInfoToAPI(info)
		// Eagerly-resident spaces (post-boot) give us the
		// deterministic spaceIndex object id without touching disk.
		// On a row the SDK can't resolve to a handle (rare —
		// e.g. tombstoned), skip the lookup and emit the row
		// without the field.
		if sp, err := d.sdk.Spaces().Get(ctx, info.Id); err == nil {
			row.SpaceIndexObjectId = sp.SpaceIndexObjectId()
		}
		out = append(out, row)
	}
	return c.JSON(http.StatusOK, api.SpaceListResponse{Spaces: out})
}

// @Summary	Get a space
// @Tags		spaces
// @Produce	json
// @Param		spaceId	path		string	true	"Space ID"
// @Success	200		{object}	api.SpaceInfo
// @Failure	500		{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId} [get]
func (d *deps) spaceGet(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().Get(c.Request().Context(), id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceToAPI(sp))
}

// spaceUpdate handles PATCH /v1/spaces/:spaceId.
//
//	@Summary	Update space metadata
//	@Tags		spaces
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		body	body	api.SpaceUpdateRequest	true	"At least one field required"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId} [patch]
func (d *deps) spaceUpdate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	var req api.SpaceUpdateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Name == nil && req.Description == nil && req.IconCID == nil {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description, iconCid is required", nil)
	}
	if err := sp.SetMetadata(c.Request().Context(), space.SetMetadataRequest{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
	}); err != nil {
		return spaceError(c, err, sp.Id())
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceSync forces an immediate head-sync (diff) round against the
// space's responsible nodes instead of waiting for the periodic
// headsync timer. Blocks until the round completes. Normal operation
// never needs this — periodic + reactive sync keep the space current —
// it exists for on-demand convergence ("sync now", tests).
//
//	@Summary	Force an immediate head-sync round (sync now)
//	@Tags		spaces
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/sync [post]
func (d *deps) spaceSync(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.SyncHeads(c.Request().Context()); err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary	Delete a space
// @Tags		spaces
// @Param		spaceId	path	string	true	"Space ID"
// @Success	204
// @Failure	500	{object}	api.ErrorEnvelope
// @Router		/spaces/{spaceId} [delete]
func (d *deps) spaceDelete(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().Delete(c.Request().Context(), id); err != nil {
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceOneToOne handles POST /v1/spaces/one-to-one — Service.OneToOne.
// Derives the 1-1 (direct) space shared with the given account identity
// and activates it locally (implicit self-approval → status active).
// Same id regardless of key order; idempotent; overrides a prior local
// decline (un-decline).
//
//	@Summary	Open a 1-1 (direct) space
//	@Tags		spaces
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.SpaceOneToOneRequest	true	"Peer account identity"
//	@Success	201		{object}	api.SpaceInfo
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/one-to-one [post]
func (d *deps) spaceOneToOne(c echo.Context) error {
	var req api.SpaceOneToOneRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.OtherIdentity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "otherIdentity required", nil)
	}
	sp, err := d.sdk.Spaces().OneToOne(c.Request().Context(), req.OtherIdentity)
	if err != nil {
		return oneToOneError(c, err, "otherIdentity")
	}
	return c.JSON(http.StatusCreated, spaceToAPI(sp))
}

// spaceOneToOneAccept handles POST /v1/spaces/:spaceId/one-to-one/accept
// — Service.AcceptOneToOne. Approves an incoming pending 1-1 by space id
// (the peer identity is read off the row), materializing and activating
// it. Equivalent to re-running OneToOne(peer); idempotent.
//
//	@Summary	Accept an incoming 1-1 (direct) space
//	@Tags		spaces
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID (from a one_to_one_pending row)"
//	@Success	200		{object}	api.SpaceInfo
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/one-to-one/accept [post]
func (d *deps) spaceOneToOneAccept(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().AcceptOneToOne(c.Request().Context(), id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceToAPI(sp))
}

// spaceOneToOneDecline handles POST /v1/spaces/:spaceId/one-to-one/decline
// — Service.DeclineOneToOne. Rejects an incoming pending 1-1, writing a
// synced sticky marker so it is suppressed on every device and never
// auto-resurfaces. A later explicit POST /v1/spaces/one-to-one overrides
// it.
//
//	@Summary	Decline an incoming 1-1 (direct) space
//	@Tags		spaces
//	@Param		spaceId	path	string	true	"Space ID (from a one_to_one_pending row)"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/one-to-one/decline [post]
func (d *deps) spaceOneToOneDecline(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().DeclineOneToOne(c.Request().Context(), id); err != nil {
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceOneToOneRegisterIncoming handles
// POST /v1/spaces/one-to-one/register-incoming — Service.RegisterIncoming.
// The out-of-band discovery path: record an incoming 1-1 request learned
// through the app's own channel (QR, link, member list) as a device-local
// one_to_one_pending row for the user to approve, without materializing
// storage. No-op if a row for the derived space already exists.
//
//	@Summary	Register an out-of-band incoming 1-1 request
//	@Tags		spaces
//	@Accept		json
//	@Param		body	body	api.SpaceRegisterIncomingRequest	true	"Peer identity + optional display hint"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/one-to-one/register-incoming [post]
func (d *deps) spaceOneToOneRegisterIncoming(c echo.Context) error {
	var req api.SpaceRegisterIncomingRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.PeerIdentity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "peerIdentity required", nil)
	}
	err := d.sdk.Spaces().RegisterIncoming(c.Request().Context(), req.PeerIdentity, space.AccountMetadata{
		Name:        req.DisplayHint.Name,
		Description: req.DisplayHint.Description,
		IconCID:     req.DisplayHint.IconCID,
	})
	if err != nil {
		return oneToOneError(c, err, "peerIdentity")
	}
	return c.NoContent(http.StatusNoContent)
}

// oneToOneError maps the OneToOne / RegisterIncoming family of SDK errors
// to the canonical envelope. The SDK rejects self-pairing and undecodable
// identities but doesn't yet export errors.Is-able sentinels for them, so
// we string-match at the boundary (same pragmatic pattern spaceJoin uses
// for "join pending"). Grow this into an errors.Is map once the SDK
// exports the sentinels. `field` is the request field the identity came
// from (otherIdentity / peerIdentity), echoed in details.
func oneToOneError(c echo.Context, err error, field string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "cannot pair with self"):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"cannot open a 1-1 space with your own identity",
			map[string]any{"field": field, "reason": "self"})
	case strings.Contains(msg, "decode identity"):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid account identity",
			map[string]any{"field": field})
	default:
		return spaceError(c, err, "")
	}
}

// spaceError maps SDK-side errors to the canonical envelope. It is
// deliberately conservative: the SDK's error sentinels for "unknown
// space" aren't yet exported, so we recognize context errors and fall
// back to 500 + internal for everything else. As the SDK grows
// errors.Is-able sentinels we should grow this map (e.g. ErrSpaceNotFound
// → 404 space.not_found, ErrSpaceExists → 409 space.exists).
func spaceError(c echo.Context, err error, spaceID string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	details := map[string]any{}
	if spaceID != "" {
		details["spaceId"] = spaceID
	}
	if len(details) == 0 {
		details = nil
	}
	return writeError(c, http.StatusInternalServerError, "internal", err.Error(), details)
}

func spaceInfoToAPI(info space.SpaceInfo) api.SpaceInfo {
	return api.SpaceInfo{
		Id:          info.Id,
		Type:        info.Type,
		SpaceType:   info.SpaceType,
		Author:      info.Author,
		Name:        info.Name,
		Description: info.Description,
		IconCID:     info.IconCID,
		Status:      spaceStatusString(info.Status),
		OwnRole:     spacePermissionString(info.OwnRole),
		CreatedAt:   info.CreatedAt,
	}
}

// spaceToAPI is the Space-handle variant of spaceInfoToAPI — populates
// SpaceIndexObjectId from the resident space handle so single-space
// responses always carry it.
func spaceToAPI(sp space.Space) api.SpaceInfo {
	out := spaceInfoToAPI(sp.Info())
	out.SpaceIndexObjectId = sp.SpaceIndexObjectId()
	return out
}

func spaceStatusString(s space.Status) string {
	switch s {
	case space.StatusActive:
		return api.SpaceStatusActive
	case space.StatusJoining:
		return api.SpaceStatusJoining
	case space.StatusLeaving:
		return api.SpaceStatusLeaving
	case space.StatusDeleted:
		return api.SpaceStatusDeleted
	case space.StatusRemoteDead:
		return api.SpaceStatusRemoteDead
	case space.StatusOneToOnePending:
		return api.SpaceStatusOneToOnePending
	case space.StatusOneToOneDeclined:
		return api.SpaceStatusOneToOneDeclined
	default:
		return api.SpaceStatusUnknown
	}
}

func spacePermissionString(p space.Permission) string {
	switch p {
	case space.PermissionReader:
		return api.SpacePermissionReader
	case space.PermissionGuest:
		return api.SpacePermissionGuest
	case space.PermissionWriter:
		return api.SpacePermissionWriter
	case space.PermissionAdmin:
		return api.SpacePermissionAdmin
	case space.PermissionOwner:
		return api.SpacePermissionOwner
	default:
		return api.SpacePermissionNone
	}
}
