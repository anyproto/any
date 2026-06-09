package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/agentlog"
	"github.com/anyproto/any/internal/api"
)

// agentTurnAppend handles POST /v1/spaces/:spaceId/objects/:objectId/agent/turns.
//
//	@Summary	Append one immutable agent turn record
//	@Tags		agent
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string						true	"Space ID"
//	@Param		objectId	path		string						true	"Chat object ID"
//	@Param		body		body		api.AgentTurnAppendRequest	true	"Turn record"
//	@Success	201			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/agent/turns [post]
func (d *deps) agentTurnAppend(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	var req api.AgentTurnAppendRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Seq == nil || *req.Seq < 0 {
		return writeError(c, http.StatusBadRequest, api.ErrAgentSeqRequired,
			"seq required (non-negative int)", nil)
	}
	if len(req.UserText) > agentlog.MaxUserTextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid, "userText too long",
			map[string]any{"max_bytes": agentlog.MaxUserTextBytes, "got_bytes": len(req.UserText)})
	}
	if len(req.Think) > agentlog.MaxThinkBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid, "think too long",
			map[string]any{"max_bytes": agentlog.MaxThinkBytes, "got_bytes": len(req.Think)})
	}
	if len(req.Replies) > agentlog.MaxReplies {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid, "too many replies",
			map[string]any{"max": agentlog.MaxReplies, "got": len(req.Replies)})
	}
	if len(req.Effects) > agentlog.MaxEffects {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid, "too many effects",
			map[string]any{"max": agentlog.MaxEffects, "got": len(req.Effects)})
	}
	if len(req.MessageIds) > agentlog.MaxMessageIds {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid, "too many messageIds",
			map[string]any{"max": agentlog.MaxMessageIds, "got": len(req.MessageIds)})
	}

	res, err := agentlog.AppendTurn(c.Request().Context(), sp, objectId, req)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}

// agentChunkCreate handles POST /v1/spaces/:spaceId/objects/:objectId/agent/chunks.
//
//	@Summary	Create one immutable compressed-history chunk
//	@Tags		agent
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string						true	"Space ID"
//	@Param		objectId	path		string						true	"Chat object ID"
//	@Param		body		body		api.AgentChunkCreateRequest	true	"Chunk record"
//	@Success	201			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/agent/chunks [post]
func (d *deps) agentChunkCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	var req api.AgentChunkCreateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Seq == nil || *req.Seq < 0 {
		return writeError(c, http.StatusBadRequest, api.ErrAgentSeqRequired,
			"seq required (non-negative int)", nil)
	}
	if req.Summary == "" {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid, "summary required", nil)
	}
	if len(req.Summary) > agentlog.MaxSummaryBytes {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid, "summary too long",
			map[string]any{"max_bytes": agentlog.MaxSummaryBytes, "got_bytes": len(req.Summary)})
	}
	if req.FromSeq == nil || req.ToSeq == nil {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid,
			"fromSeq and toSeq required (the raw turn range this chunk covers)", nil)
	}
	if *req.FromSeq < 0 || *req.ToSeq < *req.FromSeq {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid,
			"invalid turn range: need 0 ≤ fromSeq ≤ toSeq",
			map[string]any{"fromSeq": *req.FromSeq, "toSeq": *req.ToSeq})
	}
	if req.PeriodStart <= 0 || req.PeriodEnd < req.PeriodStart {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid,
			"invalid period: need 0 < periodStart ≤ periodEnd (unix seconds)",
			map[string]any{"periodStart": req.PeriodStart, "periodEnd": req.PeriodEnd})
	}

	res, err := agentlog.CreateChunk(c.Request().Context(), sp, objectId, req)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}
