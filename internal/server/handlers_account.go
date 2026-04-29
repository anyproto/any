package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

func registerAccountRoutes(g *echo.Group, d *deps) {
	g.GET("/account", d.accountGet)
	g.PUT("/account/metadata", notImplemented("account.UpdateMetadata"))
}

func (d *deps) accountGet(c echo.Context) error {
	return c.JSON(http.StatusOK, api.AccountResponse{
		Id: d.sdk.Account().Id(),
	})
}
