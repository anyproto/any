package agentlog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/ensure"

	"github.com/anyproto/any/internal/api"
)

// ErrSeqDeleted is returned when a client-provided seq points at a
// tombstoned record. Record deletion is sticky (CRDT delete-wins) —
// the id can never be reused; the SDK signals the attempt as a
// whole-record rejection with ReasonErr space.ErrRecordDeleted.
var ErrSeqDeleted = errors.New("agentlog: seq points at a deleted record")

// maxSeqAllocAttempts bounds the server-assigned-seq retry loop. A
// collision only happens under concurrent writers to the same chat
// (rare — one agent writer per chat in practice); the loop re-probes
// and retries. Client-provided seq never retries (a duplicate is a
// real error to surface).
const maxSeqAllocAttempts = 8

// nextSeq returns max(seq)+1 for a dataset, or 0 when empty. The probe
// MUST include tombstones: an upsert onto a deleted id is absorbed by
// CRDT delete-wins with no rejection, so deriving from live records
// after a history wipe would restart at 0 and every append would
// vanish into the wiped range forever. Tombstones lose content fields
// (seq included) but keep their id, and ids are the zero-padded seq
// (lexical order == numeric order) — so the max is read off the id.
// Not atomic vs concurrent writers — the append-only rejection on
// create is the CAS, and appendSeqAssigned re-probes on a rejection.
func nextSeq(ctx context.Context, sp space.Space, objectId, dataset string) (int, error) {
	doc, err := sp.Query(objectId, dataset).
		Projection(space.ProjectionOpts{IncludeDeleted: true}).
		Sort("-id").Limit(1).One(ctx)
	if errors.Is(err, space.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	id := string(doc.GetStringBytes("id"))
	seq, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("max record id %q is not a seq", id)
	}
	return seq + 1, nil
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
		res, rej, err := tryCreate(ctx, sp, objectId, dataset, RecordId(seq), buildPayload(seq), opName)
		if err != nil {
			return space.ModifyResult{}, err
		}
		if rej == nil {
			return res, nil
		}
		if !isSeqCollision(rej) {
			// validation rejection — surface immediately, don't retry.
			return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected: %s", opName, rej.Reason)
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
		setIfInt(payload, FieldUnitsCovered, req.UnitsCovered)
		return payload
	}
	return appendSeqAssigned(ctx, sp, objectId, DatasetChunks, req.Seq, build, "create chunk")
}

// tryCreate is the shared single-record write: one multi-field $set
// with an explicit id, upsert so first-write creates (a second write
// with the same id becomes a MODIFY and is rejected by the append-only
// handler — the collision signal, reason "append_only:"; a write onto
// a TOMBSTONED id is rejected by the SDK with ReasonErr
// space.ErrRecordDeleted). Returns the rejection separately (nil =
// success) so the caller can tell an id-taken collision (retry /
// conflict) from a validation rejection of the payload (surface).
func tryCreate(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, *space.OpRejection, error) {
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
		return space.ModifyResult{}, nil, fmt.Errorf("agentlog: %s: modify: %w", opName, err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, &res.Rejections[0], nil
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, nil, fmt.Errorf("agentlog: %s: empty RecordIds", opName)
	}
	return res, nil, nil
}

// isSeqCollision reports whether a rejection means "this id is taken"
// — the append-only gate firing on a live record, or the SDK's
// tombstone rejection (space.ErrRecordDeleted; deleted ids are sticky
// and never reused) — vs a validation rejection of the payload (which
// must surface, not retry). Both collision kinds are retryable for
// the server-assigned path: the re-probe includes tombstones, so it
// lands past whatever consumed the id.
func isSeqCollision(rej *space.OpRejection) bool {
	return errors.Is(rej.ReasonErr, space.ErrRecordDeleted) ||
		strings.Contains(rej.Reason, "append_only")
}

// create is tryCreate for the client-provided-seq path: any rejection
// surfaces (a duplicate seq is a real error; a validation reject too).
// The SDK's tombstone rejection maps to ErrSeqDeleted so the handler
// can answer a typed 409 instead of an opaque internal error.
func create(ctx context.Context, sp space.Space, objectId, dataset, recordId string, payload map[string]any, opName string) (space.ModifyResult, error) {
	res, rej, err := tryCreate(ctx, sp, objectId, dataset, recordId, payload, opName)
	if err != nil {
		return space.ModifyResult{}, err
	}
	if rej != nil {
		if errors.Is(rej.ReasonErr, space.ErrRecordDeleted) {
			return space.ModifyResult{}, fmt.Errorf("agentlog: %s: record %s: %w", opName, recordId, ErrSeqDeleted)
		}
		return space.ModifyResult{}, fmt.Errorf("agentlog: %s: rejected: %s", opName, rej.Reason)
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
