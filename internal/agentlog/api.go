package agentlog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/ensure"

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
	var lastSeq int
	for range maxSeqAllocAttempts {
		seq, err := nextSeq(ctx, sp, objectId, dataset)
		if err != nil {
			return space.ModifyResult{}, fmt.Errorf("agentlog: %s: alloc seq: %w", opName, err)
		}
		res, reason, err := tryCreate(ctx, sp, objectId, dataset, RecordId(seq), buildPayload(seq), opName)
		if err != nil {
			return space.ModifyResult{}, err
		}
		if reason == "" {
			return res, nil
		}
		if !isSeqCollision(reason) {
			// validation rejection — surface immediately, don't retry.
			return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected: %s", opName, reason)
		}
		lastSeq = seq // collision — re-probe and retry
	}
	return space.ModifyResult{}, fmt.Errorf(
		"agentlog: %s: seq allocation exhausted after %d attempts (last seq %d collided)",
		opName, maxSeqAllocAttempts, lastSeq)
}

// ensureType attaches the agent_log type to the object's any.types
// (making the chat object multitype: chat + agent_log).
func ensureType(ctx context.Context, sp space.Space, objectId string) error {
	return ensure.TypeAttached(ctx, sp, objectId, TypeId)
}

// RecordId renders the canonical record id for a seq — zero-padded so
// lexical id order matches numeric insertion order.
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
// with the same id becomes a MODIFY and is rejected by the append-only
// handler — the collision signal, reason "append_only:"). Returns the
// rejection REASON separately (empty = success) so the caller can tell
// a seq collision (retry) from a validation rejection (surface): only
// an "append_only" reason means the id is taken.
func tryCreate(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, string, error) {
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
		return space.ModifyResult{}, "", fmt.Errorf("agentlog: %s: modify: %w", opName, err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, res.Rejections[0].Reason, nil
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, "", fmt.Errorf("agentlog: %s: empty RecordIds", opName)
	}
	return res, "", nil
}

// isSeqCollision reports whether a rejection reason is the append-only
// gate firing on an existing id (a seq collision) vs a validation
// rejection of the payload (which must surface, not retry).
func isSeqCollision(reason string) bool {
	return strings.Contains(reason, "append_only")
}

// create is tryCreate for the client-provided-seq path: any rejection
// surfaces (a duplicate seq is a real error; a validation reject too).
func create(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, error) {
	res, reason, err := tryCreate(ctx, sp, objectId, dataset, recordId, payload, opName)
	if err != nil {
		return space.ModifyResult{}, err
	}
	if reason != "" {
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected: %s", opName, reason)
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
