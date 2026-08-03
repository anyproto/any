//go:build !mobile

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/swaggo/swag/v2"

	_ "github.com/anyproto/any/internal/server/docs"
)

// strictBodySchemas lists the request schemas whose endpoints enforce
// the closed body vocabulary (checkUnknownFields / bindBodyStrict —
// unknown top-level keys answer 400 request.unknown_field). The served
// spec stamps additionalProperties: false on them so a spec-reading
// client learns the strictness at discovery time instead of via a 400.
// swaggo cannot emit the flag itself, hence the serve-time stamp; a
// name listed here but absent from the generated spec fails loudly
// (and TestServeOpenAPI pins the stamped output).
var strictBodySchemas = []string{
	"api.ObjectCreateRequest",
	"api.SpaceQueryObjectsRequest",
	"api.SpaceQueryRequest",
	"api.SpaceListQueryRequest",
	"api.TypesCreateRequest",
}

// openAPIDoc caches the stamped spec — the generated document is
// static per binary, so the unmarshal/stamp/marshal pass runs once.
var openAPIDoc struct {
	once sync.Once
	blob []byte
	err  error
}

func serveOpenAPI(c echo.Context) error {
	openAPIDoc.once.Do(func() {
		doc, err := swag.ReadDoc("swagger")
		if err != nil {
			openAPIDoc.err = err
			return
		}
		openAPIDoc.blob, openAPIDoc.err = stampStrictSchemas([]byte(doc))
	})
	if openAPIDoc.err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": openAPIDoc.err.Error()})
	}
	return c.JSONBlob(http.StatusOK, openAPIDoc.blob)
}

// stampStrictSchemas sets additionalProperties: false on every schema
// named in strictBodySchemas, under components.schemas (OpenAPI 3.x).
func stampStrictSchemas(doc []byte) ([]byte, error) {
	var spec map[string]any
	if err := json.Unmarshal(doc, &spec); err != nil {
		return nil, fmt.Errorf("parse generated spec: %w", err)
	}
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for _, name := range strictBodySchemas {
		schema, ok := schemas[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("strict schema %s missing from generated spec", name)
		}
		schema["additionalProperties"] = false
	}
	return json.Marshal(spec)
}
