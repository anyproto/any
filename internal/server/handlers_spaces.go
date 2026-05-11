package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func registerSpaceRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces", d.spaceCreate)
	g.GET("/spaces", d.spaceList)
	g.GET("/spaces/:spaceId", d.spaceGet)
	g.PATCH("/spaces/:spaceId", d.spaceUpdate)
	g.DELETE("/spaces/:spaceId", d.spaceDelete)

	g.POST("/spaces/join", d.spaceJoin)
	// Space lifecycle the SDK exposes but doesn't implement yet.
	g.POST("/spaces/derive", notImplemented("Spaces.Derive"))
	g.POST("/spaces/one-to-one", notImplemented("Spaces.OneToOne"))

	// Object lifecycle + data plane.
	g.POST("/spaces/:spaceId/objects", d.objectCreate)
	g.POST("/spaces/:spaceId/objects/derive", d.objectDerive)
	g.POST("/spaces/:spaceId/objects/query", d.spaceQueryObjects)
	g.DELETE("/spaces/:spaceId/objects/:objectId", d.objectDelete)
	g.GET("/spaces/:spaceId/objects/:objectId/subscribe", d.subscribeObject)

	// Editor (built-in type — see internal/editor). Atomic blocks +
	// markdown bridge, both backed by the per-object body_blocks
	// dataset. Liveness reuses the generic /subscribe endpoint with
	// dataset=body_blocks.
	g.GET("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownGet)
	g.PUT("/spaces/:spaceId/objects/:objectId/editor/markdown", d.markdownSet)
	g.GET("/spaces/:spaceId/objects/:objectId/editor/blocks", d.blocksList)
	g.POST("/spaces/:spaceId/objects/:objectId/editor/blocks", d.blocksCreate)
	g.PATCH("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksPatch)
	g.DELETE("/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId", d.blocksDelete)

	// Chat (built-in type — see internal/chat). Liveness reuses the
	// existing /subscribe?dataset=chat_messages — no chat-specific
	// subscribe endpoint.
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages", d.chatSend)
	g.GET("/spaces/:spaceId/objects/:objectId/chat/messages", d.chatList)
	g.PATCH("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatEdit)
	g.DELETE("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId", d.chatDelete)
	g.POST("/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji", d.chatReact)
	g.POST("/spaces/:spaceId/query", d.spaceQuery)
	g.POST("/spaces/:spaceId/modify", d.spaceModify)
	g.POST("/spaces/:spaceId/delete-records", d.spaceDeleteRecords)

	// Types.
	g.GET("/spaces/:spaceId/types", d.typeList)
	g.POST("/spaces/:spaceId/types", d.typeCreate)
	g.GET("/spaces/:spaceId/types/:typeId", d.typeGet)
	g.DELETE("/spaces/:spaceId/types/:typeId", notImplemented("Types.Delete"))
	g.GET("/spaces/:spaceId/types/:typeId/properties", d.typeProperties)
	g.POST("/spaces/:spaceId/types/:typeId/properties", d.typeAddProperty)
	g.DELETE("/spaces/:spaceId/types/:typeId/properties/:propId", notImplemented("Types.RemoveProperty"))
	g.PATCH("/spaces/:spaceId/types/:typeId/properties/:propId", notImplemented("Types.UpdatePropertyMeta"))

	// Properties.
	g.GET("/spaces/:spaceId/properties/subscribe", d.subscribeProperties)
	g.GET("/spaces/:spaceId/properties/:objectId", d.propertiesGet)
	g.POST("/spaces/:spaceId/properties/:objectId/base/:typeId", d.propertiesSetBase)
	g.POST("/spaces/:spaceId/properties/:objectId/account/:typeId", notImplemented("Properties.SetAccount"))
	g.POST("/spaces/:spaceId/properties/:objectId/device/:typeId", notImplemented("Properties.SetDevice"))
	g.POST("/spaces/:spaceId/properties/:objectId/attach/:typeId", notImplemented("Properties.AttachType"))
	g.POST("/spaces/:spaceId/properties/:objectId/detach/:typeId", notImplemented("Properties.DetachType"))

	// Members. Static segments before the :identity wildcard so /me and
	// /requests don't get swallowed by the param matcher.
	g.GET("/spaces/:spaceId/members", d.memberList)
	g.GET("/spaces/:spaceId/members/me", d.memberMe)
	g.GET("/spaces/:spaceId/members/requests", d.memberJoinRequests)
	g.GET("/spaces/:spaceId/members/:identity", d.memberGet)

	// Invites — owner/admin side mints + revokes; joiners use the
	// /v1/spaces/join endpoint with the share-friendly token.
	g.POST("/spaces/:spaceId/invites", d.inviteCreate)
	g.GET("/spaces/:spaceId/invites", d.inviteList)
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

	// Sync status — Space.SyncStatus() returns nil today.
	g.GET("/spaces/:spaceId/sync-status", notImplemented("SyncStatus.Space"))
	g.GET("/spaces/:spaceId/sync-status/objects/:objectId", notImplemented("SyncStatus.Object"))
	g.GET("/spaces/:spaceId/sync-status/peers", notImplemented("SyncStatus.Peers"))
}

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

func (d *deps) spaceList(c echo.Context) error {
	ctx := c.Request().Context()
	infos, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return spaceError(c, err, "")
	}
	out := make([]api.SpaceInfo, 0, len(infos))
	for _, info := range infos {
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

func (d *deps) spaceGet(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().Get(c.Request().Context(), id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceToAPI(sp))
}

// spaceUpdate handles PATCH /v1/spaces/:spaceId. Pointer-to-string
// fields let callers patch one piece of metadata without clobbering
// the others. Returns 204 — the spaceIndex apply is local-write-now,
// mirror-into-tech-space-async, so the post-write Info() may briefly
// show stale values. Callers that want the converged state re-GET.
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

func (d *deps) spaceDelete(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().Delete(c.Request().Context(), id); err != nil {
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
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
