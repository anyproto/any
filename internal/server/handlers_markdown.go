package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/markdown"
)

// Markdown read/write endpoints — a lossless import/export layer
// over the editor_blocks dataset (internal/editor).
//
//	GET   /v1/spaces/:spaceId/objects/:objectId/editor/markdown
//	PUT   /v1/spaces/:spaceId/objects/:objectId/editor/markdown
//	PATCH /v1/spaces/:spaceId/objects/:objectId/editor/markdown
//	POST  /v1/spaces/:spaceId/objects/:objectId/editor/markdown/append
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
// which path produced the change. GET also returns the document's
// version, and a PUT carrying it as ifVersion writes only while the
// document is still at it (409 markdown.conflict otherwise), so a stale
// save cannot revert a newer body. PATCH is the surgical variant:
// oldText → newText replacements resolved against the CURRENT
// rendering, then fed through PUT's diff — minimal block ops, and a
// stale quote fails loudly instead of clobbering concurrent edits.
// POST .../append is the append-only
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
//	@Success	200			{object}	api.MarkdownDocument
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/{collection}/markdown [get]
func (d *deps) markdownGet(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	collection, errResp, done := d.editorCollection(c, sp)
	if done {
		return errResp
	}
	doc, err := markdown.Get(c.Request().Context(), sp, objectId, collection)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusOK, api.MarkdownDocument{Content: doc.Content, Version: doc.Version})
}

// markdownSet replaces the markdown content of an object, optionally
// only while the document is still at ifVersion.
//
//	@Summary	Set markdown content (diff-based)
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		collection	path		string					true	"Editor collection (editor_blocks or <typeId>_<key>)"
//	@Param		body		body		api.MarkdownSetRequest	true	"Markdown content and the version it was edited from"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	409			{object}	api.ErrorEnvelope	"markdown.conflict — details carry the current content and version"
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/{collection}/markdown [put]
func (d *deps) markdownSet(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	collection, errResp, done := d.editorCollection(c, sp)
	if done {
		return errResp
	}
	req, ok := bindBodyStrict[api.MarkdownSetRequest](c, "")
	if !ok {
		return nil
	}
	res, err := markdown.Set(c.Request().Context(), sp, objectId, collection, req.Content, req.IfVersion)
	if err != nil {
		return markdownWriteError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, markdownSetResponseToAPI(res))
}

// markdownSetResponseToAPI converts a markdown.SetResult to the wire
// shape, always emitting non-nil arrays so clients can iterate
// without nil checks.
func markdownSetResponseToAPI(res markdown.SetResult) api.MarkdownSetResponse {
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
	return api.MarkdownSetResponse{
		Inserted:  inserted,
		Updated:   updated,
		Deleted:   deleted,
		Unchanged: res.Unchanged,
		Version:   res.Version,
	}
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
//	@Param		collection	path		string					true	"Editor collection (editor_blocks or <typeId>_<key>)"
//	@Param		body		body		api.MarkdownContent		true	"Markdown content to append"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/{collection}/markdown/append [post]
func (d *deps) markdownAppend(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	collection, errResp, done := d.editorCollection(c, sp)
	if done {
		return errResp
	}
	req, ok := bindBodyStrict[api.MarkdownContent](c, "")
	if !ok {
		return nil
	}
	res, err := markdown.Append(c.Request().Context(), sp, objectId, collection, req.Content)
	if err != nil {
		return markdownWriteError(c, err, sp.Id(), objectId)
	}
	// Append only ever inserts; updated/deleted are always empty.
	return c.JSON(http.StatusOK, markdownSetResponseToAPI(res))
}

// markdownEdit applies targeted oldText → newText replacements
// against the rendered markdown — the surgical alternative to PUT for
// callers that know the text they want changed but not the block ids.
//
//	@Summary	Edit markdown content (targeted match/replace)
//	@Tags		editor
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		collection	path		string					true	"Editor collection (editor_blocks or <typeId>_<key>)"
//	@Param		body		body		api.MarkdownEditRequest	true	"Targeted replacements"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/{collection}/markdown [patch]
func (d *deps) markdownEdit(c echo.Context) error {
	// Body validation runs before resolveSpace so 400s don't pay for
	// a space lookup.
	req, ok := bindBodyStrict[api.MarkdownEditRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Edits) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "edits required", nil)
	}
	edits := make([]markdown.Edit, len(req.Edits))
	for i, e := range req.Edits {
		if e.OldText == "" {
			return writeError(c, http.StatusBadRequest, "request.missing_field",
				fmt.Sprintf("edits[%d].oldText required", i), nil)
		}
		edits[i] = markdown.Edit{OldText: e.OldText, NewText: e.NewText, ReplaceAll: e.ReplaceAll}
	}

	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	collection, errResp, done := d.editorCollection(c, sp)
	if done {
		return errResp
	}
	res, err := markdown.EditContent(c.Request().Context(), sp, objectId, collection, edits)
	if err != nil {
		return markdownWriteError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, markdownSetResponseToAPI(res))
}

// markdownWriteError maps the markdown writes' typed errors to the
// canonical envelope; messages carry the recovery step so an agent
// caller can fix its request without extra discovery.
func markdownWriteError(c echo.Context, err error, spaceId, objectId string) error {
	var (
		conflict  markdown.ConflictError
		tooLarge  markdown.BlockTooLargeError
		noMatch   markdown.NoMatchError
		ambiguous markdown.AmbiguousMatchError
		overlap   markdown.OverlapError
	)
	switch {
	case errors.As(err, &conflict):
		return writeError(c, http.StatusConflict, api.ErrMarkdownConflict,
			"the body changed — merge details.content and retry with details.version",
			map[string]any{"content": conflict.Current.Content, "version": conflict.Current.Version})
	case errors.As(err, &tooLarge):
		return writeError(c, http.StatusBadRequest, api.ErrMarkdownBlockTooLarge,
			fmt.Sprintf("block %d is %d bytes, over the %d-byte cap per block — split it", tooLarge.Index, tooLarge.Bytes, editor.MaxTextBytes),
			map[string]any{"blockIndex": tooLarge.Index, "gotBytes": tooLarge.Bytes, "maxBytes": editor.MaxTextBytes})
	case errors.As(err, &noMatch):
		return writeError(c, http.StatusBadRequest, api.ErrMarkdownNoMatch,
			fmt.Sprintf("edits[%d].oldText not found in the current document — GET .../editor/markdown and quote the exact text", noMatch.Index),
			map[string]any{"editIndex": noMatch.Index})
	case errors.As(err, &ambiguous):
		return writeError(c, http.StatusBadRequest, api.ErrMarkdownAmbiguous,
			fmt.Sprintf("edits[%d].oldText occurs %d times — include more surrounding context to make it unique, or set replaceAll", ambiguous.Index, ambiguous.Occurrences),
			map[string]any{"editIndex": ambiguous.Index, "occurrences": ambiguous.Occurrences})
	case errors.As(err, &overlap):
		return writeError(c, http.StatusBadRequest, api.ErrMarkdownOverlap,
			fmt.Sprintf("edits[%d] and edits[%d] match overlapping text — merge them into one edit", overlap.IndexA, overlap.IndexB),
			map[string]any{"editIndices": []int{overlap.IndexA, overlap.IndexB}})
	}
	// Everything else (cancellation, read-only, dead/unknown object →
	// 404 object.not_found) goes through the shared SDK-op mapping,
	// same as the other markdown handlers.
	return sdkOpError(c, err, map[string]any{"spaceId": spaceId, "objectId": objectId})
}
