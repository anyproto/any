package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any/internal/api"
)

// propertiesGet handles GET /v1/spaces/:spaceId/properties/:objectId.
//
//	@Summary	Get properties of an object
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		objectId		path		string	true	"Object ID"
//	@Success	200				{object}	api.PropertiesGetResponse
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId} [get]
func (d *deps) propertiesGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	// Get returns the full property record including meta (_ver etc.) —
	// the SDK dropped its read-opts arg, meta is always present now.
	rec, err := sp.Properties().Get(c.Request().Context(), objectId)
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

// propertiesSet handles POST /v1/spaces/:spaceId/properties/:objectId/set/:typeId.
// Wraps the scope-aware Properties().Set: every propId in the patch must
// resolve to the SAME declared scope (the SDK rejects mixed-scope or
// unknown-key patches). Renamed from the former `/base/:typeId` +
// SetBase surface when scoped properties landed (v0.0.11).
//
//	@Summary	Set properties on an object (single declared scope)
//	@Tags		properties
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string							true	"Space ID"
//	@Param		objectId	path		string							true	"Object ID"
//	@Param		typeId		path		string							true	"Type ID"
//	@Param		body		body		api.PropertiesSetRequest	true	"Patch map"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/set/{typeId} [post]
func (d *deps) propertiesSet(c echo.Context) error {
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

	// Format-bearing properties get their value shapes checked here —
	// the SDK stores formats opaquely; this server is the semantics
	// boundary (see propformat.go).
	defs, err := sp.Types().Properties(c.Request().Context(), typeId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	if v := validateFormatValues(defs, patch); v != nil {
		return writeError(c, http.StatusBadRequest, "property.format_violation",
			"value does not match the property's declared format", v.details())
	}

	res, err := sp.Properties().Set(c.Request().Context(), objectId, typeId, patch)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":  sp.Id(),
			"objectId": objectId,
			"typeId":   typeId,
		})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}
