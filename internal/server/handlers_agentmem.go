package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/agentmem"
	"github.com/anyproto/any/internal/api"
)

// agentBrainGet handles GET /v1/spaces/:spaceId/agent/brain.
//
//	@Summary	Resolve the per-space agent brain object id
//	@Tags		agent
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.AgentBrainResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/agent/brain [get]
func (d *deps) agentBrainGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId, err := agentmem.DeriveBrainObjectId(c.Request().Context(), sp)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusOK, api.AgentBrainResponse{ObjectId: objectId})
}

// agentMemoryCreate handles POST /v1/spaces/:spaceId/agent/memory.
// The server resolves the brain object itself — callers never pass an
// objectId; reads use GET /agent/brain to learn it for /query.
//
//	@Summary	Create a memory item on the space brain object
//	@Tags		agent
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string						true	"Space ID"
//	@Param		body	body		api.AgentMemoryCreateRequest	true	"Memory item"
//	@Success	201		{object}	api.ModifyResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/agent/memory [post]
func (d *deps) agentMemoryCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	req, ok := bindBody[api.AgentMemoryCreateRequest](c)
	if !ok {
		return nil
	}
	if req.Category == "" {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "category required", nil)
	}
	if len(req.Category) > agentmem.MaxCategoryBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "category too long",
			map[string]any{"max_bytes": agentmem.MaxCategoryBytes})
	}
	if req.Context == "" {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "context required", nil)
	}
	if len(req.Context) > agentmem.MaxContextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "context too long",
			map[string]any{"max_bytes": agentmem.MaxContextBytes, "got_bytes": len(req.Context)})
	}
	if len(req.Body) > agentmem.MaxBodyBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "body too long",
			map[string]any{"max_bytes": agentmem.MaxBodyBytes, "got_bytes": len(req.Body)})
	}
	if len(req.Edges) > agentmem.MaxEdges {
		return writeError(c, http.StatusBadRequest, api.ErrAgentMemoryInvalid, "too many edges",
			map[string]any{"max": agentmem.MaxEdges, "got": len(req.Edges)})
	}

	brainId, err := agentmem.DeriveBrainObjectId(c.Request().Context(), sp)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	res, err := agentmem.CreateItem(c.Request().Context(), sp, brainId, *req)
	if err != nil {
		return agentMemOpError(c, err, sp.Id(), brainId)
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}

// agentMemoryEvolve handles PATCH /v1/spaces/:spaceId/agent/memory/:itemId.
//
//	@Summary	Evolve a memory item's mutable fields (author only)
//	@Tags		agent
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string						true	"Space ID"
//	@Param		itemId	path		string						true	"Memory item ID"
//	@Param		body	body		api.AgentMemoryEvolveRequest	true	"Fields to set"
//	@Success	200		{object}	api.ModifyResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	403		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/agent/memory/{itemId} [patch]
func (d *deps) agentMemoryEvolve(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	itemId := c.Param("itemId")
	if itemId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "itemId required", nil)
	}
	req, ok := bindBody[api.AgentMemoryEvolveRequest](c)
	if !ok {
		return nil
	}

	brainId, err := agentmem.DeriveBrainObjectId(c.Request().Context(), sp)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	res, err := agentmem.Evolve(c.Request().Context(), sp, brainId, itemId, d.account, *req)
	if err != nil {
		return agentMemOpError(c, err, sp.Id(), brainId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// agentMemoryDelete handles DELETE /v1/spaces/:spaceId/agent/memory/:itemId.
//
//	@Summary	Delete a memory item (author only)
//	@Tags		agent
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		itemId	path		string	true	"Memory item ID"
//	@Success	200		{object}	api.ModifyResult
//	@Failure	403		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/agent/memory/{itemId} [delete]
func (d *deps) agentMemoryDelete(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	itemId := c.Param("itemId")
	if itemId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "itemId required", nil)
	}

	brainId, err := agentmem.DeriveBrainObjectId(c.Request().Context(), sp)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	res, err := agentmem.Delete(c.Request().Context(), sp, brainId, itemId, d.account)
	if err != nil {
		return agentMemOpError(c, err, sp.Id(), brainId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// agentMemOpError maps agentmem-package errors to the canonical
// envelope; unknown errors fall through to the shared sdkOpError.
func agentMemOpError(c echo.Context, err error, spaceId, brainId string) error {
	details := map[string]any{"spaceId": spaceId, "objectId": brainId}
	switch {
	case errors.Is(err, agentmem.ErrNotFound):
		return writeError(c, http.StatusNotFound, api.ErrAgentMemoryNotFound,
			"memory item not found", details)
	case errors.Is(err, agentmem.ErrNotAuthor):
		return writeError(c, http.StatusForbidden, api.ErrAgentNotAuthor,
			"only the item author can perform this action", details)
	case errors.Is(err, agentmem.ErrNoFields):
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one mutable field required (salience, accessCount, confidence, importance, context, body, tags, edges)", nil)
	}
	return sdkOpError(c, err, details)
}
