package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/markdown"
)

// Markdown read/write endpoints — a lossless import/export layer
// over the editor_blocks dataset (internal/editor).
//
//	GET  /v1/spaces/:spaceId/objects/:objectId/editor/markdown
//	PUT  /v1/spaces/:spaceId/objects/:objectId/editor/markdown
//	POST /v1/spaces/:spaceId/objects/:objectId/editor/markdown/append
//
// These are convenience routes — each one bundles several SDK calls
// (Query, Modify, Delete) under a single HTTP request — and explicitly
// step outside the "endpoints map 1:1 onto SDK methods" rule from
// CLAUDE.md. They survive alongside the atomic /blocks endpoints
// because LLM tools, "Export as .md" / "Import .md" UI flows, and
// programmatic API users that want whole-document round-trips depend
// on the markdown wire shape. Internally, GET reads top-level blocks
// and renders each to its canonical markdown bytes; PUT parses the
// supplied content, diffs against the stored blocks, and emits the
// same per-record create / update / delete ops the /blocks endpoints
// would — so the same `editor_blocks` SSE events fire regardless of
// which path produced the change. POST .../append is the append-only
// fast path: it skips the read+diff entirely and only creates blocks
// past the current tail, so its cost is O(appended content) rather
// than O(document) — see markdown.Append.

// markdownGet returns the joined markdown content for an object.
//
//	@Summary	Get markdown content
//	@Tags		editor
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Success	200			{object}	api.MarkdownContent
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/markdown [get]
func (d *deps) markdownGet(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	content, err := markdown.Get(c.Request().Context(), sp, objectId)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusOK, api.MarkdownContent{Content: content})
}

// markdownSet replaces the markdown content of an object.
//
//	@Summary	Set markdown content (diff-based)
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		body		body		api.MarkdownContent		true	"Markdown content"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/markdown [put]
func (d *deps) markdownSet(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	req, ok := bindBody[struct {
		Content string `json:"content"`
	}](c)
	if !ok {
		return nil
	}
	res, err := markdown.Set(c.Request().Context(), sp, objectId, req.Content)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	// Always emit non-nil arrays so the wire shape stays stable —
	// clients can iterate without nil checks.
	inserted := res.Inserted
	if inserted == nil {
		inserted = []string{}
	}
	updated := res.Updated
	if updated == nil {
		updated = []string{}
	}
	deleted := res.Deleted
	if deleted == nil {
		deleted = []string{}
	}
	return c.JSON(http.StatusOK, api.MarkdownSetResponse{
		Inserted:  inserted,
		Updated:   updated,
		Deleted:   deleted,
		Unchanged: res.Unchanged,
	})
}

// markdownAppend appends markdown content to the tail of an object
// without reading or diffing the existing document.
//
//	@Summary	Append markdown content (append-only fast path)
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		body		body		api.MarkdownContent		true	"Markdown content to append"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/markdown/append [post]
func (d *deps) markdownAppend(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	req, ok := bindBody[struct {
		Content string `json:"content"`
	}](c)
	if !ok {
		return nil
	}
	res, err := markdown.Append(c.Request().Context(), sp, objectId, req.Content)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	// Append only ever inserts; emit the same wire shape as Set with
	// non-nil arrays so clients can iterate without nil checks.
	inserted := res.Inserted
	if inserted == nil {
		inserted = []string{}
	}
	return c.JSON(http.StatusOK, api.MarkdownSetResponse{
		Inserted:  inserted,
		Updated:   []string{},
		Deleted:   []string{},
		Unchanged: res.Unchanged,
	})
}
