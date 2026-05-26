package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/swaggo/swag"

	_ "github.com/anyproto/any/internal/server/docs"
)

func serveOpenAPI(c echo.Context) error {
	doc, err := swag.ReadDoc("swagger")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSONBlob(http.StatusOK, []byte(doc))
}
