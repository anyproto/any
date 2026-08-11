package server

import (
	"errors"
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
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/agent/turns [post]
func (d *deps) agentTurnAppend(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	req, ok := bindBodyStrict[api.AgentTurnAppendRequest](c, "")
	if !ok {
		return nil
	}
	if req.Seq != nil && *req.Seq < 0 {
		return writeError(c, http.StatusBadRequest, api.ErrAgentSeqRequired,
			"seq must be non-negative (omit to let the server assign it)", nil)
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
	if req.LLM != nil && req.LLM.StopReason != "" && !agentlog.StopReasons[req.LLM.StopReason] {
		return writeError(c, http.StatusBadRequest, api.ErrAgentTurnInvalid,
			"llm.stopReason not in the closed set "+
				"(done|wrapup|break_soft|break_hard|length|error)",
			map[string]any{"got": req.LLM.StopReason})
	}

	res, err := agentlog.AppendTurn(c.Request().Context(), sp, objectId, *req)
	if err != nil {
		return agentAppendError(c, err, sp.Id(), objectId)
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
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/agent/chunks [post]
func (d *deps) agentChunkCreate(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	req, ok := bindBodyStrict[api.AgentChunkCreateRequest](c, "")
	if !ok {
		return nil
	}
	if req.Seq != nil && *req.Seq < 0 {
		return writeError(c, http.StatusBadRequest, api.ErrAgentSeqRequired,
			"seq must be non-negative (omit to let the server assign it)", nil)
	}
	if req.Level != nil && *req.Level < 1 {
		return writeError(c, http.StatusBadRequest, api.ErrAgentChunkInvalid,
			"level must be ≥ 1 (omit for the level-1 default)", nil)
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

	res, err := agentlog.CreateChunk(c.Request().Context(), sp, objectId, *req)
	if err != nil {
		return agentAppendError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}

// agentAppendError maps turn/chunk append failures. A client-provided
// seq landing on a tombstoned record is a caller-visible conflict —
// the id was consumed by a wiped range and can never be reused.
func agentAppendError(c echo.Context, err error, spaceId, objectId string) error {
	details := map[string]any{"spaceId": spaceId, "objectId": objectId}
	if errors.Is(err, agentlog.ErrSeqDeleted) {
		return writeError(c, http.StatusConflict, api.ErrAgentSeqDeleted,
			"seq points at a deleted record (history was wiped); omit seq to let the server assign the next one", details)
	}
	return sdkOpError(c, err, details)
}
