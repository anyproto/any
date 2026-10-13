package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

func registerAccountRoutes(g *echo.Group, d *deps) {
	g.GET("/account", d.accountGet)
	g.PUT("/account/metadata", d.accountUpdateMetadata)
	g.PUT("/account/icon", d.accountIconPut)
	g.GET("/account/icon", d.accountIconGet)
	g.DELETE("/account/icon", d.accountIconDelete)
	g.POST("/account/access-code", d.accountRedeemAccessCode)
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
		m := accountMetadataToAPI(meta)
		resp.Metadata = &m
	}
	return c.JSON(http.StatusOK, resp)
}

// accountUpdateMetadata handles PUT /v1/account/metadata.
//
// Name and description only: the avatar is a file, written by
// PUT /v1/account/icon, and the stored one is kept across this write.
//
//	@Summary	Update account metadata (name, description)
//	@Tags		account
//	@Accept		json
//	@Param		body	body	api.AccountMetadata	true	"At least one of name, description; iconCid is refused"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/metadata [put]
func (d *deps) accountUpdateMetadata(c echo.Context) error {
	req, ok := bindBodyStrict[api.AccountMetadata](c, "")
	if !ok {
		return nil
	}
	if req.IconCID != "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"iconCid is read-only here: upload the avatar with PUT /v1/account/icon", map[string]any{"field": "iconCid"})
	}
	if req.Name == "" && req.Description == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description required", nil)
	}
	err := d.sdk.Account().UpdateMetadata(c.Request().Context(), space.AccountMetadata{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		if errors.Is(err, space.ErrProfileFieldInvalid) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"name and description must not contain NUL", map[string]any{"field": "name|description"})
		}
		return sdkOpError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}
