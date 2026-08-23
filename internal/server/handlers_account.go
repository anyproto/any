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

// accountGet handles GET /v1/account.
//
//	@Summary	Get account info
//	@Tags		account
//	@Produce	json
//	@Success	200	{object}	api.AccountResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account [get]
func (d *deps) accountGet(c echo.Context) error {
	resp := api.AccountResponse{Id: d.sdk.Account().Id(), TechSpaceId: d.sdk.TechSpaceId()}
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

// accountUpdateMetadata handles PUT /v1/account/metadata.
//
//	@Summary	Update account metadata
//	@Tags		account
//	@Accept		json
//	@Param		body	body	api.AccountMetadata	true	"At least one field required"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/metadata [put]
func (d *deps) accountUpdateMetadata(c echo.Context) error {
	req, ok := bindBodyStrict[api.AccountMetadata](c, "")
	if !ok {
		return nil
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
