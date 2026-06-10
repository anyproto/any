package agentlog

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// ensureType attaches the agent_log type to the object's any.types if
// not already present (making the chat object multitype: chat +
// agent_log). Idempotent and cheap — same pattern as chat.ensureType.
func ensureType(ctx context.Context, sp space.Space, objectId string) error {
	if rec, err := sp.Properties().Get(ctx, objectId, space.PropertyReadOpts{}); err == nil && rec != nil {
		for _, v := range rec.GetArray("any", "types") {
			if string(v.GetStringBytes()) == TypeId {
				return nil
			}
		}
	}
	_, err := sp.Properties().AttachType(ctx, objectId, TypeId)
	return err
}

// RecordId renders the canonical record id for a seq — zero-padded so
// lexical id order matches numeric insertion order (the agentdebug
// trick).
func RecordId(seq int) string {
	return fmt.Sprintf("%0*d", SeqPadWidth, seq)
}

// AppendTurn writes one immutable turn record to the chat object's
// agent_turns dataset. The record id is derived from seq; appending
// the same seq twice surfaces as a handler rejection (the would-be
// modify hits the append-only gate), so callers can treat "rejected"
// as a seq collision and retry with a fresh probe.
func AppendTurn(ctx context.Context, sp space.Space, objectId string, req api.AgentTurnAppendRequest) (space.ModifyResult, error) {
	if err := ensureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: append turn: ensure type: %w", err)
	}
	if req.Seq == nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: append turn: seq required")
	}
	payload := map[string]any{
		FieldSeq: *req.Seq,
	}
	setIfString(payload, FieldFromAgent, req.FromAgent)
	setIfString(payload, FieldUserName, req.UserName)
	setIfString(payload, FieldUserText, req.UserText)
	setIfString(payload, FieldThink, req.Think)
	setIfString(payload, FieldDebugRef, req.DebugRef)
	setIfStrings(payload, FieldReplies, req.Replies)
	setIfStrings(payload, FieldEffects, req.Effects)
	setIfStrings(payload, FieldMessageIds, req.MessageIds)
	if req.LLM != nil {
		llm := map[string]any{}
		setIfString(llm, FieldLLMStopReason, req.LLM.StopReason)
		setIfString(llm, FieldLLMModel, req.LLM.Model)
		setIfInt(llm, FieldLLMInTokens, req.LLM.InTokens)
		setIfInt(llm, FieldLLMOutTokens, req.LLM.OutTokens)
		setIfInt(llm, FieldLLMCacheRead, req.LLM.CacheRead)
		setIfInt(llm, FieldLLMCacheWrite, req.LLM.CacheWrite)
		if len(llm) > 0 {
			payload[FieldLLM] = llm
		}
	}
	return create(ctx, sp, objectId, DatasetTurns, RecordId(*req.Seq), payload, "append turn")
}

// CreateChunk writes one immutable chunk record to the chat object's
// agent_chunks dataset.
func CreateChunk(ctx context.Context, sp space.Space, objectId string, req api.AgentChunkCreateRequest) (space.ModifyResult, error) {
	if err := ensureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: create chunk: ensure type: %w", err)
	}
	if req.Seq == nil || req.FromSeq == nil || req.ToSeq == nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: create chunk: seq, fromSeq, toSeq required")
	}
	payload := map[string]any{
		FieldSeq:         *req.Seq,
		FieldSummary:     req.Summary,
		FieldPeriodStart: req.PeriodStart,
		FieldPeriodEnd:   req.PeriodEnd,
		FieldFromSeq:     *req.FromSeq,
		FieldToSeq:       *req.ToSeq,
	}
	setIfString(payload, FieldFromAgent, req.FromAgent)
	setIfInt(payload, FieldTurnsCovered, req.TurnsCovered)
	return create(ctx, sp, objectId, DatasetChunks, RecordId(*req.Seq), payload, "create chunk")
}

// create is the shared single-record write: one multi-field $set with
// an explicit id, upsert so first-write creates (a second write with
// the same id becomes a modify and is rejected by the append-only
// handler — which is exactly the collision semantics we want).
func create(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, error) {
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  dataset,
		Records: []space.RecordModify{{
			Id:     recordId,
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  "",
				Value: payload,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: modify: %w", opName, err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected: %s", opName, res.Rejections[0].Reason)
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: empty RecordIds", opName)
	}
	return res, nil
}

func setIfString(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func setIfStrings(m map[string]any, key string, vals []string) {
	if len(vals) > 0 {
		m[key] = vals
	}
}

func setIfInt(m map[string]any, key string, val int) {
	if val != 0 {
		m[key] = val
	}
}
