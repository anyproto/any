package server

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

var openapiJSON []byte

func init() {
	var spec any
	if err := yaml.Unmarshal(openapiYAML, &spec); err != nil {
		panic("openapi: parse yaml: " + err.Error())
	}
	var err error
	openapiJSON, err = json.Marshal(spec)
	if err != nil {
		panic("openapi: marshal json: " + err.Error())
	}
}

func serveOpenAPI(c echo.Context) error {
	return c.JSONBlob(http.StatusOK, openapiJSON)
}
