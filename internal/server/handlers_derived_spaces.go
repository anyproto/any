package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// derivedSpaceList handles GET /v1/spaces/derived — the embedded
// registry (derivedspaces.go) resolved against the account. Ids come
// from Service.DeriveId (pure computation — nothing is created or
// loaded); `created` reports whether a tech-space row exists for the
// id, i.e. the space was materialized here or on any of the account's
// devices.
//
//	@Summary	List well-known derived spaces
//	@Tags		spaces
//	@Produce	json
//	@Success	200	{object}	api.DerivedSpaceListResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/derived [get]
func (d *deps) derivedSpaceList(c echo.Context) error {
	ctx := c.Request().Context()
	rows, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		known[r.Id] = struct{}{}
	}
	out := api.DerivedSpaceListResponse{Spaces: make([]api.DerivedSpaceInfo, 0, len(derivedSpaceDefs))}
	for _, def := range derivedSpaceDefs {
		id, err := d.sdk.Spaces().DeriveId(ctx, space.DeriveRequest{Seed: []byte(def.Seed)})
		if err != nil {
			return sdkOpError(c, err, map[string]any{"name": def.Name})
		}
		_, created := known[id]
		out.Spaces = append(out.Spaces, api.DerivedSpaceInfo{
			Name:    def.Name,
			SpaceId: id,
			Created: created,
		})
	}
	return c.JSON(http.StatusOK, out)
}

// derivedSpaceCreate handles POST /v1/spaces/derived/:name —
// Service.Derive for a registry entry. Materializes the space (lazily:
// nothing exists until the first call) and returns its SpaceInfo.
// Idempotent — repeat calls land on the same space; unregistered names
// are 404 so the raw derive primitive never reaches the wire.
//
//	@Summary	Materialize a well-known derived space
//	@Tags		spaces
//	@Produce	json
//	@Param		name	path		string	true	"Registry name (e.g. bao)"
//	@Success	201		{object}	api.SpaceInfo
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/derived/{name} [post]
func (d *deps) derivedSpaceCreate(c echo.Context) error {
	name := c.Param("name")
	def, ok := derivedSpaceByName(name)
	if !ok {
		return writeError(c, http.StatusNotFound, "space.derived_unknown",
			"unknown derived space name", map[string]any{"name": name})
	}
	sp, err := d.sdk.Spaces().Derive(c.Request().Context(), space.DeriveRequest{Seed: []byte(def.Seed)})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"name": def.Name})
	}
	return c.JSON(http.StatusCreated, spaceToAPI(c.Request().Context(), sp))
}

// derivedSpaceId reports whether spaceId belongs to the embedded
// registry — the DELETE pre-check for derived spaces that were never
// materialized (no tech-space row yet, so the SDK's own flag-based
// refusal can't see them; deleting would write a sticky tombstone that
// wedges the well-known id). DeriveId is pure computation, so the scan
// is cheap. Best-effort: a DeriveId error skips the entry (the SDK
// guard still covers every materialized derived space).
func (d *deps) derivedSpaceId(c echo.Context, spaceId string) bool {
	for _, def := range derivedSpaceDefs {
		id, err := d.sdk.Spaces().DeriveId(c.Request().Context(), space.DeriveRequest{Seed: []byte(def.Seed)})
		if err == nil && id == spaceId {
			return true
		}
	}
	return false
}
