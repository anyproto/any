package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// derivedSpaceList handles GET /v1/spaces/derived — the boot-resolved
// registry (derivedspaces.go) joined against the account's space list.
// Resolving never creates anything. `created` means a usable row exists
// (materialized here or on any of the account's devices — rows sync);
// a tombstoned row (pre-guard wedge) reports created=false with
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

// derivedSpaceCreate handles POST /v1/spaces/derived/:name —
// Service.Derive for a registry entry. Materializes the space (lazily:
// nothing exists until the first call) and returns its SpaceInfo; the
// registry DisplayName rides DeriveRequest.Name (first
// materialization only — the SDK leaves a pre-existing row's metadata
// alone). Idempotent — repeat calls land on the same space;
// unregistered names are 404 so the raw derive primitive never
// reaches the wire. A tombstoned row (wedged pre-guard) is refused
// with 409 space.deleted rather than reported as success.
//
//	@Summary	Materialize a well-known derived space
//	@Tags		spaces
//	@Produce	json
//	@Param		name	path		string	true	"Registry name (e.g. bao)"
//	@Success	201		{object}	api.SpaceInfo
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/derived/{name} [post]
func (d *deps) derivedSpaceCreate(c echo.Context) error {
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
	for _, r := range rows {
		if r.Id == def.SpaceId && r.Status == space.StatusDeleted {
			return spaceError(c, space.ErrSpaceDeleted, def.SpaceId)
		}
	}
	sp, err := d.sdk.Spaces().Derive(ctx, space.DeriveRequest{
		Seed: []byte(def.Seed),
		Name: def.DisplayName,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"name": def.Name})
	}
	return c.JSON(http.StatusCreated, spaceToAPI(ctx, sp))
}
