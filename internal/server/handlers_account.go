package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func registerAccountRoutes(g *echo.Group, d *deps) {
	g.GET("/account", d.accountGet)
	g.PUT("/account/metadata", d.accountUpdateMetadata)
}

// accountGet handles GET /v1/account. Returns the account id plus the
// locally-stored profile — read from the tech-space, so deterministic
// across reboots and not subject to identityRepo connectivity. Empty
// when no profile has ever been written on this device.
func (d *deps) accountGet(c echo.Context) error {
	resp := api.AccountResponse{Id: d.sdk.Account().Id()}
	meta, present, err := d.sdk.Account().Metadata(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	if present && (meta.Name != "" || meta.Description != "" || meta.IconCID != "") {
		resp.Metadata = &api.AccountMetadata{
			Name:        meta.Name,
			Description: meta.Description,
			IconCID:     meta.IconCID,
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// accountUpdateMetadata handles PUT /v1/account/metadata. Body is the
// new AccountMetadata; the SDK persists it to the tech-space and pushes
// to identityRepo so the profile becomes visible in every space the
// caller is a member of (Members.Me() / Members.List()).
//
// At least one of name / description / iconCid must be set — the SDK
// rejects fully-empty metadata, and we surface that as a 400 rather
// than a 500.
func (d *deps) accountUpdateMetadata(c echo.Context) error {
	var req api.AccountMetadata
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Name == "" && req.Description == "" && req.IconCID == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description, iconCid required", nil)
	}
	err := d.sdk.Account().UpdateMetadata(c.Request().Context(), space.AccountMetadata{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
	})
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}
