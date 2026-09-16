package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bin"
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
// Wraps the scope-aware Properties().Set: the path id is the owner —
// the object's type or one of its collections — and every propId in
// the patch must resolve to the SAME declared scope (the SDK rejects
// mixed-scope or unknown-key patches).
//
//	@Summary	Set properties on an object (single declared scope)
//	@Tags		properties
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string							true	"Space ID"
//	@Param		objectId	path		string							true	"Object ID"
//	@Param		typeId		path		string							true	"Type or collection ID (the owner)"
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
	defs, err := ownerProperties(c.Request().Context(), sp, typeId)
	if err != nil && !errors.Is(err, space.ErrNotFound) {
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
	if d.push != nil && typeId == chat.Module {
		d.push.Kick()
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// propertiesSetType handles
// POST /v1/spaces/:spaceId/properties/:objectId/type/:typeId — sets
// the object's one type (`any.type`, a $set: a previous type is
// replaced; its values and dataset records stay as orphan data,
// read-tolerant). There is no unset: every object has exactly one
// type. The object must already exist — an unknown id is
// `404 object.not_found`, not a silent create.
//
//	@Summary	Set the object's type
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		typeId		path		string	true	"Type ID"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/type/{typeId} [post]
func (d *deps) propertiesSetType(c echo.Context) error {
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
	// Pre-flighted the way objectCreate and the bundles root check do:
	// `any.type` is a synced DAG write with no validation behind it,
	// so a typo would otherwise be permanent.
	if _, err := sp.Types().Get(ctx, typeId); err != nil {
		if errors.Is(err, space.ErrNotAType) {
			return writeError(c, http.StatusBadRequest, "type.not_a_type",
				"the id names a collection — add it through …/collections/:collectionId",
				map[string]any{"collectionId": typeId, "spaceId": sp.Id()})
		}
		return writeError(c, http.StatusNotFound, "type.not_found",
			"this space has no such type",
			map[string]any{"typeId": typeId, "spaceId": sp.Id()})
	}
	if reservedCarrierType(ctx, sp, typeId) {
		return reservedCarrierError(c, sp.Id(), typeId)
	}
	res, err := sp.Properties().SetType(ctx, objectId, typeId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId, "typeId": typeId})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// propertiesAttachCollection handles
// POST /v1/spaces/:spaceId/properties/:objectId/collections/:collectionId
// — files the object under a collection (`any.collections`, $addToSet,
// idempotent), admitting writes to its columns. The object must already
// exist — an unknown id is `404 object.not_found`, not a silent
// create. `collections/bin` is move to bin: the same change stamps
// `bin.movedAt` / `bin.movedBy` (binBinding).
//
//	@Summary	Add the object to a collection
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		objectId		path		string	true	"Object ID"
//	@Param		collectionId	path		string	true	"Collection ID"
//	@Success	200				{object}	api.ModifyResult
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	404				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/collections/{collectionId} [post]
func (d *deps) propertiesAttachCollection(c echo.Context) error {
	return d.propertiesCollectionBinding(c, true)
}

// propertiesDetachCollection handles
// DELETE /v1/spaces/:spaceId/properties/:objectId/collections/:collectionId
// — removes the object from a collection ($pull, idempotent). Values
// in that namespace stay as orphan data, read-tolerant by design;
// detaching is not a delete. `collections/bin` is restore from the
// bin: the same change clears the move stamps.
//
//	@Summary	Remove the object from a collection
//	@Tags		properties
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		objectId		path		string	true	"Object ID"
//	@Param		collectionId	path		string	true	"Collection ID"
//	@Success	200				{object}	api.ModifyResult
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	404				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/properties/{objectId}/collections/{collectionId} [delete]
func (d *deps) propertiesDetachCollection(c echo.Context) error {
	return d.propertiesCollectionBinding(c, false)
}

// propertiesCollectionBinding is the shared attach/detach body — the
// two differ only in which SDK call they make.
func (d *deps) propertiesCollectionBinding(c echo.Context, attach bool) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	collectionId := c.Param("collectionId")
	if collectionId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "collectionId required", nil)
	}
	if isSerializedNil(collectionId) {
		return serializedNilIdError(c, "collectionId", collectionId)
	}

	ctx := c.Request().Context()

	// Attach pre-flights the collection the way objectCreate does:
	// `any.collections` is a synced DAG write with no validation behind
	// it, so a typo would otherwise be permanent. Detach deliberately
	// does NOT pre-flight — it is the repair path for a bogus id that
	// is already attached.
	if attach {
		if _, err := sp.Collections().Get(ctx, collectionId); err != nil {
			if errors.Is(err, space.ErrNotACollection) {
				return writeError(c, http.StatusBadRequest, "collection.not_a_collection",
					"the id names a type — set it through …/type/:typeId",
					map[string]any{"typeId": collectionId, "spaceId": sp.Id()})
			}
			return writeError(c, http.StatusNotFound, "collection.not_found",
				"this space has no such collection",
				map[string]any{"collectionId": collectionId, "spaceId": sp.Id()})
		}
	}

	bind := sp.Properties().DetachCollection
	if attach {
		bind = sp.Properties().AttachCollection
	}
	if collectionId == bin.Id {
		bind = func(ctx context.Context, objectId, _ string) (space.ModifyResult, error) {
			return d.binBinding(ctx, sp, objectId, attach)
		}
	}
	res, err := bind(ctx, objectId, collectionId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":      sp.Id(),
			"objectId":     objectId,
			"collectionId": collectionId,
		})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// binBinding is the attach/detach body for the built-in `bin`
// collection (internal/bin): move to bin stamps `bin.movedAt` /
// `bin.movedBy`, restore clears them. The stamps ride the SAME synced
// change as the membership op on the objects row — the SDK's
// write-time preflight grants a namespace the change itself attaches
// — so a bin member never lacks its stamps and a restored object never
// keeps stale ones, and one changeId names the move. movedBy is this
// account, the change's signer; movedAt the server clock, written as
// an instant. Restore unsets the whole `bin` namespace: a per-leaf
// $unset leaves an empty `bin: {}` behind, which reads as a member to
// any client testing the key.
func (d *deps) binBinding(ctx context.Context, sp space.Space, objectId string, attach bool) (space.ModifyResult, error) {
	var ops []space.Op
	if attach {
		ops = []space.Op{
			{Type: space.OpAddToSet, Path: "any.collections", Value: bin.Id},
			{Type: space.OpSet, Path: bin.Id + "." + bin.PropMovedAt, Value: time.Now().UTC()},
			{Type: space.OpSet, Path: bin.Id + "." + bin.PropMovedBy, Value: d.sdk.Account().Id()},
		}
	} else {
		ops = []space.Op{
			{Type: space.OpPull, Path: "any.collections", Value: bin.Id},
			{Type: space.OpUnset, Path: bin.Id},
		}
	}
	return sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  objectsDataset,
		Records:  []space.RecordModify{{Id: objectId, Upsert: true, Ops: ops}},
	})
}
