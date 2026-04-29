package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// propertiesGet handles GET /v1/spaces/:spaceId/properties/:objectId.
// Query params: includeVariants, includeMeta (truthy = "1" / "true").
//
// The SDK returns *anyenc.Value; we render it through FastJson(arena)
// → MarshalTo, then wrap as json.RawMessage so json.Marshal of the
// outer envelope does not double-encode.
func (d *deps) propertiesGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	opts := space.PropertyReadOpts{
		IncludeVariants: queryBool(c, "includeVariants"),
		IncludeMeta:     queryBool(c, "includeMeta"),
	}

	rec, err := sp.Properties().Get(c.Request().Context(), objectId, opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}

	var raw json.RawMessage
	if rec == nil {
		raw = json.RawMessage("null")
	} else {
		fa := getFastjsonArena()
		defer putFastjsonArena(fa)
		raw = json.RawMessage(rec.FastJson(fa).MarshalTo(nil))
	}
	return c.JSON(http.StatusOK, api.PropertiesGetResponse{Record: raw})
}

// propertiesSetBase handles POST /v1/spaces/:spaceId/properties/:objectId/base/:typeId.
// Body shape:
//
//	{ "patch": { "<propId>": <value>, ... } }
//
// Patch values are passed through to PropertiesAPI.SetBase as
// *fastjson.Value so the SDK arenas the conversion in one walk.
func (d *deps) propertiesSetBase(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	typeId := c.Param("typeId")
	if objectId == "" || typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and typeId required", nil)
	}

	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil)
	}

	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
	}

	patchObj := root.GetObject("patch")
	if patchObj == nil || patchObj.Len() == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "patch required and non-empty", nil)
	}
	patch := make(map[string]any, patchObj.Len())
	patchObj.Visit(func(propId []byte, v *fastjson.Value) {
		patch[string(propId)] = v
	})

	res, err := sp.Properties().SetBase(c.Request().Context(), objectId, typeId, patch)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":  sp.Id(),
			"objectId": objectId,
			"typeId":   typeId,
		})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// queryBool returns true if the named query param is "1", "true", or
// "yes" (case-insensitive). Empty / absent / anything else → false.
func queryBool(c echo.Context, name string) bool {
	v := c.QueryParam(name)
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true
	default:
		return false
	}
}
