package server

import (
	"errors"
	"net/http"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// A collection is a type without parts or layout: a column group
// objects are filed under (`any.collections`) next to their one type
// (`any.type`). The property-definition routes below are the type ones
// behind a shared owner parameter — the SDK's definition surface is
// one for both.

// collectionList handles GET /v1/spaces/:spaceId/collections.
//
//	@Summary	List collections
//	@Tags		collections
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		includeHidden	query		bool	false	"Include hidden collections"
//	@Success	200				{object}	api.CollectionsListResponse
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections [get]
func (d *deps) collectionList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	infos, err := sp.Collections().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	// Hidden collections (the built-in miniapp / bin, a bundle
	// install's `hidden`) stay out of the default listing and come back
	// with includeHidden=true; GET …/collections/:id resolves them
	// always.
	includeHidden := c.QueryParam("includeHidden") == "true"
	out := make([]api.CollectionInfo, 0, len(infos))
	for _, col := range infos {
		if col.Hidden && !includeHidden {
			continue
		}
		out = append(out, collectionInfoToAPI(col))
	}
	return c.JSON(http.StatusOK, api.CollectionsListResponse{Collections: out})
}

// collectionGet handles GET /v1/spaces/:spaceId/collections/:collectionId.
//
//	@Summary	Get a collection
//	@Tags		collections
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		collectionId	path		string	true	"Collection ID"
//	@Success	200				{object}	api.CollectionInfo
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	404				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId} [get]
func (d *deps) collectionGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	id := c.Param("collectionId")
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "collectionId required", nil)
	}
	info, err := sp.Collections().Get(c.Request().Context(), id)
	if err != nil {
		if resp, done := collectionLookupError(c, err, sp.Id(), id); done {
			return resp
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "collectionId": id})
	}
	return c.JSON(http.StatusOK, collectionInfoToAPI(info))
}

// collectionLookupError maps the two typed misses of a collection read:
// 404 for an unknown id, 400 collection.not_a_collection when the id
// names a type.
func collectionLookupError(c echo.Context, err error, spaceId, id string) (error, bool) {
	details := map[string]any{"spaceId": spaceId, "collectionId": id}
	switch {
	case errors.Is(err, space.ErrNotACollection):
		return writeError(c, http.StatusBadRequest, "collection.not_a_collection",
			"the id names a type — use the …/types routes", details), true
	case errors.Is(err, space.ErrNotFound):
		return writeError(c, http.StatusNotFound, "collection.not_found", "collection not found", details), true
	}
	return nil, false
}

// collectionCreate handles POST /v1/spaces/:spaceId/collections.
//
//	@Summary	Create a collection
//	@Tags		collections
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string							true	"Space ID"
//	@Param		body	body		api.CollectionsCreateRequest	true	"Collection params"
//	@Success	201		{object}	api.CollectionsCreateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections [post]
func (d *deps) collectionCreate(c echo.Context) error {
	req, ok := bindBodyStrict[api.CollectionsCreateRequest](c,
		"inline property definitions are not part of collection create — create the collection, "+
			"then add each property via POST /v1/spaces/{spaceId}/collections/{collectionId}/properties")
	if !ok {
		return nil
	}
	// The handle rules are the type's: required, unique across both
	// surfaces (a collection and a type never share a handle).
	if req.XKey == "" {
		return writeError(c, http.StatusBadRequest, "type.xkey_required",
			"xKey is required: a collection needs a stable programmatic handle (derive a slug from the name)", nil)
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if errResp, done := requireHandleFree(c, sp, req.XKey); done {
		return errResp
	}
	for k, v := range req.Meta {
		if code, reason := checkTypeMetaEntry(k, v, false); code != "" {
			return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": "meta." + k})
		}
	}
	id, err := sp.Collections().Create(c.Request().Context(), space.CollectionCreateParams{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		XKey:        req.XKey,
		Hidden:      req.Hidden,
		Meta:        req.Meta,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.CollectionsCreateResponse{CollectionId: id})
}

// collectionPatch handles PATCH /v1/spaces/:spaceId/collections/:collectionId
// — the display and listing metadata of a user collection. Absent
// fields keep their value; an empty string clears a text field.
//
//	@Summary	Patch a collection's display metadata
//	@Tags		collections
//	@Accept		json
//	@Param		spaceId			path	string						true	"Space ID"
//	@Param		collectionId	path	string						true	"Collection ID"
//	@Param		body			body	api.CollectionPatchRequest	true	"Fields to change"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId} [patch]
func (d *deps) collectionPatch(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	id := c.Param("collectionId")
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "collectionId required", nil)
	}
	req, ok := bindBodyStrict[api.CollectionPatchRequest](c, "")
	if !ok {
		return nil
	}
	patch := space.CollectionPatch{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		Hidden:      req.Hidden,
	}
	if len(req.Meta) > 0 {
		patch.Meta = make(map[string]any, len(req.Meta))
		for k, v := range req.Meta {
			if code, reason := checkTypeMetaEntry(k, v, true); code != "" {
				return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": "meta." + k})
			}
			if v == nil {
				// A clear keeps the key: catalog setup seeds only the keys
				// a collection lacks, and a cleared one must stay cleared.
				v = ""
			}
			patch.Meta[k] = v
		}
	}
	if patch.Name == nil && patch.Description == nil && patch.IconCID == nil && patch.Hidden == nil && len(patch.Meta) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description, iconCid, hidden, meta is required", nil)
	}
	if errResp, done := requireCollection(c, sp, id); done {
		return errResp
	}
	if err := sp.Collections().Patch(c.Request().Context(), id, patch); err != nil {
		if resp, done := collectionLookupError(c, err, sp.Id(), id); done {
			return resp
		}
		if errors.Is(err, space.ErrTypeRegistered) {
			return writeError(c, http.StatusBadRequest, "collection.registered",
				"collection is a registered built-in; its metadata is statically declared", map[string]any{"collectionId": id})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "collectionId": id})
	}
	return c.NoContent(http.StatusNoContent)
}

// collectionProperties handles GET /v1/spaces/:spaceId/collections/:collectionId/properties.
//
//	@Summary	List properties of a collection
//	@Tags		collections
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		collectionId	path		string	true	"Collection ID"
//	@Success	200				{object}	api.PropertiesListResponse
//	@Failure	404				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId}/properties [get]
func (d *deps) collectionProperties(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	id := c.Param("collectionId")
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "collectionId required", nil)
	}
	// Existence first: the SDK's Properties answers an empty slice for
	// an unknown id, indistinguishable from "no properties yet".
	if _, err := sp.Collections().Get(c.Request().Context(), id); err != nil {
		if resp, done := collectionLookupError(c, err, sp.Id(), id); done {
			return resp
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "collectionId": id})
	}
	return d.propertiesOf(c, sp, id)
}

// collectionAddProperty handles POST /v1/spaces/:spaceId/collections/:collectionId/properties.
//
//	@Summary	Add a property to a collection
//	@Tags		collections
//	@Accept		json
//	@Produce	json
//	@Param		spaceId			path		string					true	"Space ID"
//	@Param		collectionId	path		string					true	"Collection ID"
//	@Param		body			body		api.AddPropertyRequest	true	"Property draft"
//	@Success	201				{object}	api.AddPropertyResponse
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	404				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId}/properties [post]
func (d *deps) collectionAddProperty(c echo.Context) error {
	return d.typeAddProperty(withOwnerParam(c))
}

// collectionRemoveProperty handles DELETE /v1/spaces/:spaceId/collections/:collectionId/properties/:propId.
//
//	@Summary	Remove a property from a collection
//	@Tags		collections
//	@Param		spaceId			path	string	true	"Space ID"
//	@Param		collectionId	path	string	true	"Collection ID"
//	@Param		propId			path	string	true	"Property ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId}/properties/{propId} [delete]
func (d *deps) collectionRemoveProperty(c echo.Context) error {
	return d.typeRemoveProperty(withOwnerParam(c))
}

// collectionPatchProperty handles PATCH /v1/spaces/:spaceId/collections/:collectionId/properties/:propId.
//
//	@Summary	Patch a property of a collection
//	@Tags		collections
//	@Accept		json
//	@Param		spaceId			path	string						true	"Space ID"
//	@Param		collectionId	path	string						true	"Collection ID"
//	@Param		propId			path	string						true	"Property ID"
//	@Param		body			body	api.PropertyPatchRequest	true	"Patch"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/collections/{collectionId}/properties/{propId} [patch]
func (d *deps) collectionPatchProperty(c echo.Context) error {
	return d.typePatchProperty(withOwnerParam(c))
}

// withOwnerParam exposes the `collectionId` path parameter as `typeId`
// so the type property handlers — owner-agnostic in the SDK — serve
// the collection routes unchanged.
func withOwnerParam(c echo.Context) echo.Context {
	c.Set(ownerSurface, "collection")
	names := c.ParamNames()
	values := c.ParamValues()
	outNames := make([]string, len(names))
	outValues := make([]string, len(names))
	for i, n := range names {
		if n == "collectionId" {
			n = "typeId"
		}
		outNames[i] = n
		outValues[i] = values[i]
	}
	c.SetParamNames(outNames...)
	c.SetParamValues(outValues...)
	return c
}

func collectionInfoToAPI(col space.CollectionInfo) api.CollectionInfo {
	xkey := col.XKey
	if xkey == "" && col.BuiltIn {
		xkey = col.Id
	}
	return api.CollectionInfo{
		Id:          col.Id,
		Name:        col.Name,
		Description: col.Description,
		IconCID:     col.IconCID,
		XKey:        xkey,
		BuiltIn:     col.BuiltIn,
		Hidden:      col.Hidden,
		Meta:        col.Meta,
	}
}
