package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
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
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
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
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
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

	// Every value is checked against its property's current descriptor
	// slug here — the SDK stores descriptors opaquely and enforces only
	// kind; this server is the semantics boundary (see descriptor.go).
	defs, err := sp.Types().Properties(c.Request().Context(), typeId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	if v := validateDescriptorValues(defs, patch); v != nil {
		return writeError(c, http.StatusBadRequest, "property.format_violation",
			"value does not fit the property's descriptor", v.details())
	}

	res, err := sp.Properties().Set(c.Request().Context(), objectId, typeId, patch)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":  sp.Id(),
			"objectId": objectId,
			"typeId":   typeId,
		})
	}
	// chat.notifyMode rides this generic surface — kick the push
	// reconcile so a local mute/unmute converges immediately instead of
	// on the 5-minute tick. Kick is hash-gated and nearly free, so we
	// don't bother inspecting the patch keys; remote-origin writes
	// still ride the tick (docs/20-push.md § Settings).
	if d.push != nil && typeId == chat.TypeId {
		d.push.Kick()
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// propertiesAttachType handles
// POST /v1/spaces/:spaceId/properties/:objectId/attach/:typeId — binds a
// type to an existing object's `any.types`, admitting writes to the
// type's membership-gated datasets. Idempotent ($addToSet at the SDK
// layer). The object must already exist — an unknown id is
// `404 object.not_found`, not a silent create.
//
//	@Summary	Attach a type to an object
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		typeId		path		string	true	"Type ID"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/attach/{typeId} [post]
func (d *deps) propertiesAttachType(c echo.Context) error {
	return d.propertiesTypeBinding(c, true)
}

// propertiesDetachType handles
// POST /v1/spaces/:spaceId/properties/:objectId/detach/:typeId — removes
// a type from `any.types`. Idempotent ($pull). Values in that
// namespace and records in the type's datasets stay as orphan data,
// read-tolerant by design; detaching is not a delete.
//
//	@Summary	Detach a type from an object
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		typeId		path		string	true	"Type ID"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/detach/{typeId} [post]
func (d *deps) propertiesDetachType(c echo.Context) error {
	return d.propertiesTypeBinding(c, false)
}

// propertiesTypeBinding is the shared attach/detach body — the two
// differ only in which SDK call they make.
func (d *deps) propertiesTypeBinding(c echo.Context, attach bool) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	if isSerializedNil(typeId) {
		return serializedNilIdError(c, "typeId", typeId)
	}

	ctx := c.Request().Context()

	// Attach pre-flights the type the way objectCreate and the bundles
	// root check do: `any.types` is a synced DAG write with no
	// validation behind it, so a typo would otherwise be permanent.
	// Detach deliberately does NOT pre-flight — it is the repair path
	// for a bogus id that is already attached.
	if attach {
		if _, err := sp.Types().Get(ctx, typeId); err != nil {
			return writeError(c, http.StatusNotFound, "type.not_found",
				"this space has no such type",
				map[string]any{"typeId": typeId, "spaceId": sp.Id()})
		}
	}

	bind := sp.Properties().DetachType
	if attach {
		bind = sp.Properties().AttachType
	}
	res, err := bind(ctx, objectId, typeId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":  sp.Id(),
			"objectId": objectId,
			"typeId":   typeId,
		})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}
