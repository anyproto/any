package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// derivedSpaceList handles GET /v1/spaces/derived — the boot-resolved
// registry (derivedspaces.go) joined against the account's space list.
// Resolving never creates anything. `created` means this device holds a
// usable row: written here, or received from another of the account's
// devices (rows sync). A tombstoned row reports created=false with
// status "deleted" so clients don't treat the id as writable.
//
//	@Summary	List well-known derived spaces
//	@Tags		spaces
//	@Produce	json
//	@Success	200	{object}	api.DerivedSpaceListResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/derived [get]
func (d *deps) derivedSpaceList(c echo.Context) error {
	rows, err := d.sdk.Spaces().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	byId := make(map[string]space.SpaceInfo, len(rows))
	for _, r := range rows {
		byId[r.Id] = r
	}
	out := api.DerivedSpaceListResponse{Spaces: make([]api.DerivedSpaceInfo, 0, len(d.derived))}
	for _, r := range d.derived {
		info := api.DerivedSpaceInfo{Name: r.Name, SpaceId: r.SpaceId}
		if row, ok := byId[r.SpaceId]; ok {
			info.Status = spaceStatusString(row.Status)
			info.Created = row.Status != space.StatusDeleted
		}
		out.Spaces = append(out.Spaces, info)
	}
	return c.JSON(http.StatusOK, out)
}

// derivedSpaceOpen handles POST /v1/spaces/derived/:name. It opens a
// derived space this device holds a row for and returns its SpaceInfo;
// it creates none. Without a row the answer is
// 404 space.derived_not_created: such an account makes a regular space
// (POST /v1/spaces with anySpaceType). Unregistered names are 404
// space.derived_unknown, so the raw derive primitive never reaches the
// wire; a tombstoned row is 409 space.deleted.
//
//	@Summary	Open a well-known derived space this device holds
//	@Tags		spaces
//	@Produce	json
//	@Param		name	path		string	true	"Registry name (e.g. bao)"
//	@Success	200		{object}	api.SpaceInfo
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/derived/{name} [post]
func (d *deps) derivedSpaceOpen(c echo.Context) error {
	ctx := c.Request().Context()
	def, ok := d.derivedSpaceByName(c.Param("name"))
	if !ok {
		return writeError(c, http.StatusNotFound, "space.derived_unknown",
			"unknown derived space name", map[string]any{"name": c.Param("name")})
	}
	rows, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	has := false
	for _, r := range rows {
		if r.Id != def.SpaceId {
			continue
		}
		if r.Status == space.StatusDeleted {
			return spaceError(c, space.ErrSpaceDeleted, def.SpaceId)
		}
		has = true
	}
	if !has {
		return writeError(c, http.StatusNotFound, "space.derived_not_created",
			"this device holds no such derived space, and none is created; create a space with anySpaceType instead",
			map[string]any{"name": def.Name, "spaceId": def.SpaceId})
	}
	// With a row, Derive opens the space and leaves its metadata alone.
	sp, err := d.sdk.Spaces().Derive(ctx, space.DeriveRequest{Seed: []byte(def.Seed)})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"name": def.Name})
	}
	return c.JSON(http.StatusOK, d.spaceToAPI(ctx, sp))
}
