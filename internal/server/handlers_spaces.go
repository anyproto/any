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
	g.DELETE("/spaces/:spaceId", d.spaceDelete)

	// Space lifecycle the SDK exposes but doesn't implement yet.
	g.POST("/spaces/join", notImplemented("Spaces.Join"))
	g.POST("/spaces/derive", notImplemented("Spaces.Derive"))
	g.POST("/spaces/one-to-one", notImplemented("Spaces.OneToOne"))

	// Object lifecycle + data plane.
	g.POST("/spaces/:spaceId/objects", d.objectCreate)
	g.POST("/spaces/:spaceId/objects/derive", d.objectDerive)
	g.POST("/spaces/:spaceId/objects/query", d.spaceQueryObjects)
	g.DELETE("/spaces/:spaceId/objects/:objectId", d.objectDelete)
	g.GET("/spaces/:spaceId/objects/:objectId/markdown", d.markdownGet)
	g.PUT("/spaces/:spaceId/objects/:objectId/markdown", d.markdownSet)
	g.GET("/spaces/:spaceId/objects/:objectId/subscribe", d.subscribeObject)
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

	// Members + ACL — Space.Members()/ACL() return nil today.
	g.GET("/spaces/:spaceId/members", notImplemented("Members.List"))
	g.GET("/spaces/:spaceId/members/:identity", notImplemented("Members.Get"))
	g.POST("/spaces/:spaceId/acl/invite", notImplemented("ACL.Invite"))
	g.POST("/spaces/:spaceId/acl/accept", notImplemented("ACL.Accept"))
	g.POST("/spaces/:spaceId/acl/decline", notImplemented("ACL.Decline"))
	g.POST("/spaces/:spaceId/acl/remove", notImplemented("ACL.Remove"))
	g.POST("/spaces/:spaceId/acl/permissions", notImplemented("ACL.Permissions"))
	g.POST("/spaces/:spaceId/acl/ownership", notImplemented("ACL.Ownership"))
	g.POST("/spaces/:spaceId/acl/self-remove", notImplemented("ACL.SelfRemove"))

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
	return c.JSON(http.StatusCreated, spaceInfoToAPI(sp.Info()))
}

func (d *deps) spaceList(c echo.Context) error {
	infos, err := d.sdk.Spaces().List(c.Request().Context())
	if err != nil {
		return spaceError(c, err, "")
	}
	out := make([]api.SpaceInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, spaceInfoToAPI(info))
	}
	return c.JSON(http.StatusOK, api.SpaceListResponse{Spaces: out})
}

func (d *deps) spaceGet(c echo.Context) error {
	id := c.Param("spaceId")
	sp, err := d.sdk.Spaces().Get(c.Request().Context(), id)
	if err != nil {
		return spaceError(c, err, id)
	}
	return c.JSON(http.StatusOK, spaceInfoToAPI(sp.Info()))
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
