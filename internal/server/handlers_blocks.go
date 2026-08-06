package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/editor"
)

// Block endpoints — writes only. Reads go through the per-object
// query primitive with dataset=editor_blocks.
//
//	POST   /v1/spaces/:spaceId/objects/:objectId/editor/blocks
//	PATCH  /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId
//	DELETE /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId
//
// Snapshot:
//
//	POST /v1/spaces/:spaceId/query
//	{ "objectId": "<oid>", "dataset": "editor_blocks",
//	  "sort": ["nav.pos"] }
//
// Liveness:
//
//	POST /v1/spaces/:spaceId/query/subscribe
//	{ "objectId": "<oid>", "dataset": "editor_blocks",
//	  "sort": ["nav.pos"] }

// blocksCreate handles POST .../editor/blocks.
//
//	@Summary	Create a block
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		body		body		api.BlockCreateRequest	true	"Block params (type required)"
//	@Success	201			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/blocks [post]
func (d *deps) blocksCreate(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	req, ok := bindBodyStrict[api.BlockCreateRequest](c, "")
	if !ok {
		return nil
	}
	if req.Type == "" {
		return writeError(c, http.StatusBadRequest, api.ErrBlockTypeMissing, "type required", nil)
	}

	in := editor.CreateInput{
		Type:  req.Type,
		Style: req.Style,
		Text:  req.Text,
	}
	if req.Nav != nil {
		in.ParentId = req.Nav.ParentId
		in.Pos = req.Nav.Pos
	}

	res, err := editor.Create(c.Request().Context(), sp, objectId, in)
	if err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}

// blocksPatch handles PATCH .../editor/blocks/:blockId.
//
//	@Summary	Patch a block (atomic set/unset)
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		blockId		path		string					true	"Block ID"
//	@Param		body		body		api.BlockPatchRequest	true	"Set/unset ops"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/blocks/{blockId} [patch]
func (d *deps) blocksPatch(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	blockId := c.Param("blockId")
	if blockId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "blockId required", nil)
	}

	req, ok := bindBodyStrict[api.BlockPatchRequest](c, "")
	if !ok {
		return nil
	}

	res, err := editor.Patch(c.Request().Context(), sp, objectId, blockId, editor.PatchInput{
		Set:   req.Set,
		Unset: req.Unset,
	})
	if err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// blocksDelete handles DELETE .../editor/blocks/:blockId.
//
//	@Summary	Delete a block
//	@Tags		editor
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Object ID"
//	@Param		blockId		path	string	true	"Block ID"
//	@Success	200	{object}	api.ModifyResult
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/blocks/{blockId} [delete]
func (d *deps) blocksDelete(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	blockId := c.Param("blockId")
	if blockId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "blockId required", nil)
	}
	res, err := editor.Delete(c.Request().Context(), sp, objectId, blockId)
	if err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// blockOpError maps blocks-package errors to the canonical envelope.
// Conservative: known sentinels get specific 4xx codes; everything
// else falls through to the shared sdkOpError (5xx internal).
func blockOpError(c echo.Context, err error, spaceId, objectId string) error {
	switch {
	case errors.Is(err, editor.ErrNotFound):
		return writeError(c, http.StatusNotFound, api.ErrBlockNotFound,
			"block not found",
			map[string]any{"spaceId": spaceId, "objectId": objectId})
	}
	return sdkOpError(c, err, map[string]any{"spaceId": spaceId, "objectId": objectId})
}
