package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/editor"
)

// Block endpoints — one record per block on the per-object
// editor_blocks dataset.
//
//	GET    /v1/spaces/:spaceId/objects/:objectId/editor/blocks
//	POST   /v1/spaces/:spaceId/objects/:objectId/editor/blocks
//	PATCH  /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId
//	DELETE /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId
//
// Liveness reuses the generic subscribe primitive:
//
//	GET /v1/spaces/:spaceId/objects/:objectId/subscribe?dataset=editor_blocks

// blocksList handles GET .../blocks. Flat list in depth-first
// document order — top-level blocks first (sorted by nav.pos), each
// block followed inline by its children. Empty `records` array when
// the object has no body blocks yet (the dataset is empty until the
// first create).
func (d *deps) blocksList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	list, err := editor.List(c.Request().Context(), sp, objectId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}

	records := make([]api.Block, len(list))
	for i, b := range list {
		records[i] = blockToAPI(b)
	}
	return c.JSON(http.StatusOK, api.BlockListResponse{Records: records})
}

// blocksCreate handles POST .../blocks. Body shape:
//
//	{
//	  "type":  "paragraph",
//	  "style": { "level": 2 },
//	  "text":  "**hello**",
//	  "nav":   { "parentId": "<blockId>", "pos": "<lexid>" }
//	}
//
// `type` is required. `nav.parentId` defaults to "" (top-level).
// `nav.pos` defaults to the next lexid past the parent's current max
// — queried server-side at create time.
func (d *deps) blocksCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	var req api.BlockCreateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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

	b, err := editor.Create(c.Request().Context(), sp, objectId, in)
	if err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, blockToAPI(b))
}

// blocksPatch handles PATCH .../blocks/:blockId. Body shape:
//
//	{ "set":   { "text": "...", "style.level": 2 },
//	  "unset": ["style.checked"] }
//
// Both `set` and `unset` are optional; an empty patch is a no-op
// that returns the current record's _ver.id. Each path in `set` is
// one $set op against that path; each path in `unset` is one $unset.
// All ops land in a single any-sync change (one VersionId).
func (d *deps) blocksPatch(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	blockId := c.Param("blockId")
	if objectId == "" || blockId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and blockId required", nil)
	}

	var req api.BlockPatchRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}

	res, err := editor.Patch(c.Request().Context(), sp, objectId, blockId, editor.PatchInput{
		Set:   req.Set,
		Unset: req.Unset,
	})
	if err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	// Empty patch (no-op) returns the current state — re-read the
	// block to surface its existing _ver.id.
	if res.VersionId == "" {
		current, getErr := editor.Get(c.Request().Context(), sp, objectId, blockId)
		if getErr != nil {
			return blockOpError(c, getErr, sp.Id(), objectId)
		}
		var verId string
		if v, ok := current.Ver["id"].(string); ok {
			verId = v
		}
		return c.JSON(http.StatusOK, api.BlockPatchResponse{VersionId: verId})
	}
	return c.JSON(http.StatusOK, api.BlockPatchResponse{VersionId: string(res.VersionId)})
}

// blocksDelete handles DELETE .../blocks/:blockId. Tombstones the
// record — sticky, so reusing the same id later won't recreate it.
// Children of the deleted block are NOT cascaded; clients clean up
// descendants themselves (or use the markdown PUT path, which
// rewrites the whole body at once).
func (d *deps) blocksDelete(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	blockId := c.Param("blockId")
	if objectId == "" || blockId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and blockId required", nil)
	}
	if err := editor.Delete(c.Request().Context(), sp, objectId, blockId); err != nil {
		return blockOpError(c, err, sp.Id(), objectId)
	}
	return c.NoContent(http.StatusNoContent)
}

// blockToAPI projects a editor.Block onto the wire-shape api.Block.
// The two structs are field-aligned — the conversion is a copy.
func blockToAPI(b editor.Block) api.Block {
	return api.Block{
		Id:    b.Id,
		Ver:   b.Ver,
		Type:  b.Type,
		Style: b.Style,
		Text:  b.Text,
		Nav:   api.BlockNav{ParentId: b.Nav.ParentId, Pos: b.Nav.Pos},
	}
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
