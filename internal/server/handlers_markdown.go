package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
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
// which path produced the change. PATCH is the surgical variant: the
// caller quotes oldText → newText replacements against the rendered
// markdown, the server resolves them against the CURRENT rendering
// and reuses PUT's diff pipeline — so a targeted edit lands as the
// minimal block ops and a stale quote fails loudly instead of
// clobbering concurrent edits. POST .../append is the append-only
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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
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
//	@Param		body		body		api.MarkdownEditRequest	true	"Targeted replacements"
//	@Success	200			{object}	api.MarkdownSetResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/editor/markdown [patch]
func (d *deps) markdownEdit(c echo.Context) error {
	// Body validation runs before resolveSpace so 400s don't pay for
	// a space lookup.
	req, ok := bindBody[api.MarkdownEditRequest](c)
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
	res, err := markdown.EditContent(c.Request().Context(), sp, objectId, edits)
	if err != nil {
		return markdownEditError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, markdownSetResponseToAPI(res))
}

// markdownEditError maps EditContent's typed match errors to the
// canonical envelope; messages carry the recovery step so an agent
// caller can fix its request without extra discovery.
func markdownEditError(c echo.Context, err error, spaceId, objectId string) error {
	var (
		noMatch   markdown.NoMatchError
		ambiguous markdown.AmbiguousMatchError
		overlap   markdown.OverlapError
	)
	switch {
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
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
		map[string]any{"spaceId": spaceId, "objectId": objectId})
}
