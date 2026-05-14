package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/markdown"
)

// Markdown read/write endpoints — a lossless import/export layer
// over the editor_blocks dataset (internal/editor).
//
//	GET  /v1/spaces/:spaceId/objects/:objectId/markdown
//	PUT  /v1/spaces/:spaceId/objects/:objectId/markdown
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
// which path produced the change.

// markdownGet returns the joined markdown content for an object.
//
//	GET /v1/spaces/:spaceId/objects/:objectId/markdown
//	→ 200 { "content": "..." }
func (d *deps) markdownGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	content, err := markdown.Get(c.Request().Context(), sp, objectId)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusOK, map[string]any{"content": content})
}

// markdownSet replaces the markdown content of an object, computing
// the per-block diff server-side and emitting one upsert batch + one
// delete batch.
//
//	PUT /v1/spaces/:spaceId/objects/:objectId/markdown
//	body: { "content": "..." }
//	→ 200 { "inserted": [...], "updated": [...], "deleted": [...], "unchanged": N }
//
// "inserted"/"updated"/"deleted" are the lexids touched by this call;
// "unchanged" counts blocks left in place. Useful for clients that
// want to surface diff stats without rerunning the comparison.
func (d *deps) markdownSet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	res, err := markdown.Set(c.Request().Context(), sp, objectId, req.Content)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(),
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
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
	return c.JSON(http.StatusOK, map[string]any{
		"inserted":  inserted,
		"updated":   updated,
		"deleted":   deleted,
		"unchanged": res.Unchanged,
	})
}
