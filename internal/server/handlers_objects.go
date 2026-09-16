package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/anyproto/any-sync/commonspace/settings"

	"github.com/anyproto/any/internal/api"
)

// objectCreateFields is the closed create vocabulary, derived from the
// api request struct — the same source the swagger spec is generated
// from, so spec and enforcement cannot drift.
var objectCreateFields = jsonFieldNames(reflect.TypeFor[api.ObjectCreateRequest]())

// objectCreateFieldsHint rides every unknown-field rejection on object
// create, naming the right home for the most commonly misplaced keys
// (top-level name / description / a bare type-key group like "any").
const objectCreateFieldsHint = `object properties (name, description, custom fields) go under initialProperties keyed by type, e.g. {"initialProperties": {"any": {"name": "Dune"}}}`

// objectCreate handles POST /v1/spaces/:spaceId/objects.
//
//	@Summary	Create an object
//	@Tags		objects
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		body	body		api.ObjectCreateRequest	true	"Object params"
//	@Success	201		{object}	api.ObjectsCreateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects [post]
func (d *deps) objectCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	body, err := readBody(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "read body: "+err.Error(), nil)
	}

	parser := getFastjsonParser()
	defer putFastjsonParser(parser)

	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
		}
	}

	if errResp, done := checkUnknownFields(c, root, objectCreateFieldsHint, objectCreateFields...); done {
		return errResp
	}

	opts := space.CreateObjectOpts{}
	if root != nil {
		// The accepted fields are shape-checked before positive
		// extraction: a `types` string or an `initialProperties` array
		// would otherwise be skipped unread — the same silent-drop trap
		// checkUnknownFields closes for misspelled keys.
		if v := root.Get("type"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeString {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"type must be a type id", nil)
		}
		if v := root.Get("collections"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeArray {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"collections must be an array of collection ids", nil)
		}
		if v := root.Get("initialProperties"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeObject {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"initialProperties must be an object keyed by type or collection id, e.g. {\"any\": {\"name\": \"…\"}}", nil)
		}
		opts.Type = string(root.GetStringBytes("type"))
		if colls := root.GetArray("collections"); len(colls) > 0 {
			opts.Collections = make([]string, 0, len(colls))
			for _, v := range colls {
				opts.Collections = append(opts.Collections, string(v.GetStringBytes()))
			}
		}
		if ip := root.GetObject("initialProperties"); ip != nil {
			opts.InitialProperties = map[string]map[string]any{}
			var badGroup string
			ip.Visit(func(typeKey []byte, props *fastjson.Value) {
				obj, err := props.Object()
				if err != nil {
					badGroup = string(typeKey)
					return
				}
				kv := map[string]any{}
				obj.Visit(func(propKey []byte, val *fastjson.Value) {
					kv[string(propKey)] = val
				})
				opts.InitialProperties[string(typeKey)] = kv
			})
			if badGroup != "" {
				return writeError(c, http.StatusBadRequest, "request.schema",
					fmt.Sprintf("initialProperties.%s must be an object of {propertyId: value}", badGroup),
					map[string]any{"typeKey": badGroup})
			}
		}
	}

	// Membership is pre-flighted the way the …/type and …/collections
	// routes do it — a wrong slot or an unknown id answers a typed 4xx
	// instead of a refused bootstrap on a minted tree. Every object has
	// a type; a type declaring a reserved module is carried only by its
	// own root.
	if opts.Type == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"type required: every object has exactly one type (page for a plain document)", nil)
	}
	{
		if _, err := sp.Types().Get(c.Request().Context(), opts.Type); err != nil {
			if resp, done := typeLookupError(c, err, sp.Id(), opts.Type); done {
				return resp
			}
			return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": opts.Type})
		}
		if reservedCarrierType(c.Request().Context(), sp, opts.Type) {
			return reservedCarrierError(c, sp.Id(), opts.Type)
		}
	}
	for _, id := range opts.Collections {
		if _, err := sp.Collections().Get(c.Request().Context(), id); err != nil {
			if resp, done := collectionLookupError(c, err, sp.Id(), id); done {
				return resp
			}
			return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "collectionId": id})
		}
	}

	// Same descriptor value gate as propertiesSet, per initial owner
	// (see descriptor.go).
	for ownerId, patch := range opts.InitialProperties {
		defs, err := ownerProperties(c.Request().Context(), sp, ownerId)
		if err != nil {
			continue // unknown owner: the SDK rejects the write itself
		}
		if v := validateDescriptorValues(defs, patch); v != nil {
			return writeError(c, http.StatusBadRequest, "property.format_violation",
				"initial property value does not fit the property's descriptor",
				v.details())
		}
	}

	objectId, err := sp.Objects().Create(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.ObjectsCreateResponse{ObjectId: objectId})
}

// objectDelete handles DELETE /v1/spaces/:spaceId/objects/:objectId.
//
//	@Summary	Delete an object
//	@Tags		objects
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Object ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId} [delete]
func (d *deps) objectDelete(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	if err := sp.Objects().Delete(c.Request().Context(), objectId); err != nil {
		details := map[string]any{"spaceId": sp.Id(), "objectId": objectId}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		if errors.Is(err, settings.ErrCantDeleteDerivedObject) {
			return writeError(c, http.StatusConflict, "object.derived_undeletable",
				"derived objects are permanent and cannot be deleted", details)
		}
		if errors.Is(err, space.ErrReadOnlySpace) {
			return writeError(c, http.StatusForbidden, "space.read_only",
				"space is read-only for this account (guest access or reader role)", details)
		}
		if resp, done := unsupportedError(c, err, details); done {
			return resp
		}
		if errors.Is(err, space.ErrCRDTVersionNewer) {
			return writeError(c, http.StatusConflict, "sdk.crdt_version_newer",
				"the account's data was written by a newer version — this server is read-only until it is upgraded",
				crdtVersionDetails(err, details))
		}
		// Last, not through sdkOpError: the ordinary misses here are
		// any-sync's ErrAlreadyDeleted and a head-storage lookup miss,
		// neither of which carries an SDK sentinel — sdkOpError would
		// answer 500 for the most common caller mistake.
		return writeError(c, http.StatusNotFound, "sdk.not_found",
			"object not found or already deleted", details)
	}
	return c.NoContent(http.StatusNoContent)
}

// objectGet handles GET /v1/spaces/:spaceId/objects/:objectId.
//
//	@Summary	Get an object's row
//	@Description	The object's row from the space's objects collection: any.type, any.collections and property values. 404 object.not_found for an unknown id, 410 object.deleted for a deleted object.
//	@Tags		objects
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Success	200			{object}	api.ObjectGetResponse
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	410			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId} [get]
func (d *deps) objectGet(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	rec, err := sp.Objects().Get(c.Request().Context(), objectId)
	if err != nil {
		details := map[string]any{"spaceId": sp.Id(), "objectId": objectId}
		if errors.Is(err, space.ErrNotFound) {
			return writeError(c, http.StatusNotFound, "object.not_found", "object not found in this space", details)
		}
		return sdkOpError(c, err, details)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	return c.JSON(http.StatusOK, api.ObjectGetResponse{
		ObjectId: objectId,
		Record:   json.RawMessage(rec.FastJson(fa).MarshalTo(nil)),
	})
}
