package server

import (
	"errors"
	"net/http"
	"strings"

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
	g.GET("/account/limits", d.accountLimits)
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

// accountLimits handles GET /v1/account/limits.
//
// An unreachable network is not an error: each part is the network's
// last answer, and is omitted when there never was one.
//
//	@Summary		Network limits of the account
//	@Description	The file-storage pool with its usage and the shared-spaces limit. Each part is the network's last answer, kept on the device (see fetchedAt), and omitted when the network never answered. A request waits for the network at most five seconds; an unreachable network is a 200.
//	@Tags			account
//	@Produce		json
//	@Success		200	{object}	api.AccountLimitsResponse
//	@Failure		500	{object}	api.ErrorEnvelope
//	@Router			/account/limits [get]
func (d *deps) accountLimits(c echo.Context) error {
	limits, err := d.sdk.Account().Limits(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	var resp api.AccountLimitsResponse
	if s := limits.Storage; s != nil {
		resp.Storage = &api.AccountStorageLimits{LimitBytes: s.LimitBytes, UsageBytes: s.UsageBytes, FetchedAt: s.FetchedAt}
	}
	if s := limits.SharedSpaces; s != nil {
		resp.SharedSpaces = &api.AccountSharedSpacesLimits{Limit: s.Limit, FetchedAt: s.FetchedAt}
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
	for _, f := range [][2]string{{"name", req.Name}, {"description", req.Description}} {
		if strings.IndexByte(f[1], 0) >= 0 {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				f[0]+" must not contain NUL", map[string]any{"field": f[0]})
		}
	}
	err := d.sdk.Account().UpdateMetadata(c.Request().Context(), space.AccountMetadata{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		if errors.Is(err, space.ErrProfileFieldInvalid) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"name and description must not contain NUL", nil)
		}
		return sdkOpError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}
