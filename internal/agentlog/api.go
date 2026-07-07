package agentlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// maxSeqAllocAttempts bounds the server-assigned-seq retry loop. A
// collision only happens under concurrent writers to the same chat
// (rare — one agent writer per chat in practice); the loop re-probes
// and retries. Client-provided seq never retries (a duplicate is a
// real error to surface).
const maxSeqAllocAttempts = 8

// nextSeq returns max(seq)+1 for a dataset, or 0 when empty. Not atomic
// vs concurrent writers — the Upsert-collision on create is the CAS,
// and appendServerSeq re-probes on a rejection.
func nextSeq(ctx context.Context, sp space.Space, objectId, dataset string) (int, error) {
	doc, err := sp.Query(objectId, dataset).Sort("-" + FieldSeq).Limit(1).One(ctx)
	if errors.Is(err, space.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return doc.GetInt(FieldSeq) + 1, nil
}

// appendSeqAssigned writes a seq-keyed record. If reqSeq is non-nil the
// caller controls the seq (a collision surfaces as an error). If nil,
// the server allocates max+1 and retries on collision (ADR-006 §1 —
// deletes the client probe/retry dance).
func appendSeqAssigned(
	ctx context.Context, sp space.Space, objectId, dataset string,
	reqSeq *int, buildPayload func(seq int) map[string]any, opName string,
) (space.ModifyResult, error) {
	if reqSeq != nil {
		return create(ctx, sp, objectId, dataset, RecordId(*reqSeq), buildPayload(*reqSeq), opName)
	}
	var lastReject string
	for range maxSeqAllocAttempts {
		seq, err := nextSeq(ctx, sp, objectId, dataset)
		if err != nil {
			return space.ModifyResult{}, fmt.Errorf("agentlog: %s: alloc seq: %w", opName, err)
		}
		res, rejected, err := tryCreate(ctx, sp, objectId, dataset, RecordId(seq), buildPayload(seq), opName)
		if err != nil {
			return space.ModifyResult{}, err
		}
		if !rejected {
			return res, nil
		}
		lastReject = fmt.Sprintf("seq %d collided", seq)
	}
	return space.ModifyResult{}, fmt.Errorf(
		"agentlog: %s: seq allocation exhausted after %d attempts (%s)",
		opName, maxSeqAllocAttempts, lastReject)
}

// ensureType attaches the agent_log type to the object's any.types if
// not already present (making the chat object multitype: chat +
// agent_log). Idempotent and cheap — same pattern as chat.ensureType.
func ensureType(ctx context.Context, sp space.Space, objectId string) error {
	if rec, err := sp.Properties().Get(ctx, objectId); err == nil && rec != nil {
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
	build := func(seq int) map[string]any {
		payload := map[string]any{FieldSeq: seq}
		setIfString(payload, FieldFromAgent, req.FromAgent)
		setIfString(payload, FieldUserName, req.UserName)
		setIfString(payload, FieldUserText, req.UserText)
		setIfString(payload, FieldThink, req.Think)
		setIfString(payload, FieldTraceRef, req.TraceRef)
		setIfStrings(payload, FieldReplies, req.Replies)
		setIfStrings(payload, FieldEffects, req.Effects)
		setIfStrings(payload, FieldMessageIds, req.MessageIds)
		if req.Interrupted {
			payload[FieldInterrupted] = true
		}
		if req.LLM != nil {
			llm := map[string]any{}
			setIfString(llm, FieldLLMStopReason, req.LLM.StopReason)
			setIfString(llm, FieldLLMModel, req.LLM.Model)
			setIfInt(llm, FieldLLMInTokens, req.LLM.InTokens)
			setIfInt(llm, FieldLLMOutTokens, req.LLM.OutTokens)
			setIfInt(llm, FieldLLMCacheRead, req.LLM.CacheRead)
			setIfInt(llm, FieldLLMCacheWrite, req.LLM.CacheWrite)
			setIfInt(llm, FieldLLMFuelUsed, req.LLM.FuelUsed)
			setIfInt(llm, FieldLLMCells, req.LLM.Cells)
			if req.LLM.CostUsd > 0 {
				llm[FieldLLMCostUsd] = req.LLM.CostUsd
			}
			if len(llm) > 0 {
				payload[FieldLLM] = llm
			}
		}
		return payload
	}
	return appendSeqAssigned(ctx, sp, objectId, DatasetTurns, req.Seq, build, "append turn")
}

// CreateChunk writes one immutable chunk record to the chat object's
// agent_chunks dataset.
func CreateChunk(ctx context.Context, sp space.Space, objectId string, req api.AgentChunkCreateRequest) (space.ModifyResult, error) {
	if err := ensureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: create chunk: ensure type: %w", err)
	}
	if req.FromSeq == nil || req.ToSeq == nil {
		return space.ModifyResult{}, fmt.Errorf("agentlog: create chunk: fromSeq, toSeq required")
	}
	level := 1 // ADR-006 §2: default level is 1 (over turns)
	if req.Level != nil {
		level = *req.Level
	}
	build := func(seq int) map[string]any {
		payload := map[string]any{
			FieldSeq:         seq,
			FieldLevel:       level,
			FieldSummary:     req.Summary,
			FieldPeriodStart: req.PeriodStart,
			FieldPeriodEnd:   req.PeriodEnd,
			FieldFromSeq:     *req.FromSeq,
			FieldToSeq:       *req.ToSeq,
		}
		setIfString(payload, FieldFromAgent, req.FromAgent)
		setIfInt(payload, FieldTurnsCovered, req.TurnsCovered)
		return payload
	}
	return appendSeqAssigned(ctx, sp, objectId, DatasetChunks, req.Seq, build, "create chunk")
}

// tryCreate is the shared single-record write: one multi-field $set
// with an explicit id, upsert so first-write creates (a second write
// with the same id becomes a modify and is rejected by the append-only
// handler — the collision signal). Returns rejected=true separately so
// the seq-allocation loop can retry; a transport error is still an
// error.
func tryCreate(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, bool, error) {
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
		return space.ModifyResult{}, false, fmt.Errorf("agentlog: %s: modify: %w", opName, err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, true, nil
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, false, fmt.Errorf("agentlog: %s: empty RecordIds", opName)
	}
	return res, false, nil
}

// create is tryCreate for the client-provided-seq path: a rejection is
// a real collision to surface (not retried).
func create(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, error) {
	res, rejected, err := tryCreate(ctx, sp, objectId, dataset, recordId, payload, opName)
	if err != nil {
		return space.ModifyResult{}, err
	}
	if rejected {
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected (seq collision)", opName)
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
