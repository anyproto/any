package agentlog

import (
	"errors"
	"fmt"
	"strings"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// Indexes for agent_turns: `seq` is the primary order + cursor field
// (the boot window sorts -seq, chunk drill-down range-filters on it);
// `createdAt` backs period-range queries. Both are stamped on every
// record, so the indexes are dense.
func (turnsHandler) Indexes() []anystore.IndexInfo {
	return []anystore.IndexInfo{
		{Name: "idx_seq", Fields: []string{FieldSeq}},
		{Name: "idx_created", Fields: []string{FieldCreatedAt}},
	}
}

// Indexes for agent_chunks: the boot window loads the last M chunks
// PER LEVEL (hierarchical compression, ADR-006 §2), so the compound
// [level, seq] index backs "newest chunks at level N"; `periodEnd`
// backs "chunks covering period X" range queries.
func (chunksHandler) Indexes() []anystore.IndexInfo {
	return []anystore.IndexInfo{
		{Name: "idx_level_seq", Fields: []string{FieldLevel, FieldSeq}},
		{Name: "idx_period_end", Fields: []string{FieldPeriodEnd}},
	}
}

// BeforeCreate (turns) validates the creation payload and stamps
// creator/createdAt. Same contract as chat: exactly one multi-field
// $set op with an object payload; anything else rejects the record.
func (turnsHandler) BeforeCreate(ctx *handler.ChangeCtx, rec *handler.RecordChange, sink *handler.Sink) error {
	payload, err := createPayload(rec, "turn")
	if err != nil {
		return err
	}
	if err := validateTurnPayload(payload); err != nil {
		return err
	}
	stampCreate(ctx, sink)
	return nil
}

// BeforeModify (turns): agent_turns is append-only — every edit op is
// rejected. The raw layer is immutable history; corrections happen at
// higher layers (a new turn, a new chunk), never by rewriting.
func (turnsHandler) BeforeModify(_ *handler.ChangeCtx, _ *handler.RecordChange, op *handler.Op, _ *handler.Sink) error {
	return rejectOp("turn", "append_only: "+pathString(op.Path))
}

// BeforeDelete (turns): denied in v1 — turns are kept forever so chunk
// seq-range pointers stay dense and resolvable.
func (turnsHandler) BeforeDelete(_ *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	return rejectRecord("turn", "append_only")
}

// BeforeCreate (chunks) validates the creation payload and stamps
// creator/createdAt.
func (chunksHandler) BeforeCreate(ctx *handler.ChangeCtx, rec *handler.RecordChange, sink *handler.Sink) error {
	payload, err := createPayload(rec, "chunk")
	if err != nil {
		return err
	}
	if err := validateChunkPayload(payload); err != nil {
		return err
	}
	stampCreate(ctx, sink)
	return nil
}

// BeforeModify (chunks): immutable — a summary is a snapshot of a raw
// range; re-summarizing means writing a new chunk.
func (chunksHandler) BeforeModify(_ *handler.ChangeCtx, _ *handler.RecordChange, op *handler.Op, _ *handler.Sink) error {
	return rejectOp("chunk", "append_only: "+pathString(op.Path))
}

// BeforeDelete (chunks): denied in v1, same stance as turns.
func (chunksHandler) BeforeDelete(_ *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	return rejectRecord("chunk", "append_only")
}

// --- create validation ------------------------------------------------------

// createPayload enforces the shared creation shape — exactly one
// multi-field $set op (empty path, object payload) — and returns the
// payload for per-dataset key validation.
func createPayload(rec *handler.RecordChange, kind string) (*anyenc.Value, error) {
	if len(rec.Ops) != 1 {
		return nil, rejectCreate(kind, "expected exactly one multi-field $set op")
	}
	op := &rec.Ops[0]
	if op.Type != handler.OpSet || len(op.Path) != 0 {
		return nil, rejectCreate(kind, "expected multi-field $set with empty path")
	}
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeObject {
		return nil, rejectCreate(kind, "payload must be a JSON object")
	}
	return op.Payload, nil
}

func validateTurnPayload(payload *anyenc.Value) error {
	obj, err := payload.Object()
	if err != nil {
		return rejectCreate("turn", "payload must be a JSON object")
	}
	var (
		visitErr error
		hasSeq   bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldSeq:
			hasSeq = true
			visitErr = checkSeq("turn", key, v)
		case FieldFromAgent:
			visitErr = checkString("turn", key, v, MaxFromAgentBytes, false)
		case FieldUserName:
			visitErr = checkString("turn", key, v, MaxUserNameBytes, false)
		case FieldUserText:
			visitErr = checkString("turn", key, v, MaxUserTextBytes, true)
		case FieldThink:
			visitErr = checkString("turn", key, v, MaxThinkBytes, true)
		case FieldTraceRef:
			visitErr = checkString("turn", key, v, MaxIdBytes, false)
		case FieldReplies:
			visitErr = checkStringArray("turn", key, v, MaxReplies, MaxReplyBytes)
		case FieldEffects:
			visitErr = checkStringArray("turn", key, v, MaxEffects, MaxEffectBytes)
		case FieldMessageIds:
			visitErr = checkStringArray("turn", key, v, MaxMessageIds, MaxIdBytes)
		case FieldInterrupted:
			visitErr = checkBool("turn", key, v)
		case FieldLLM:
			visitErr = validateLLM(v)
		default:
			// Covers server-stamped fields (creator, createdAt) and
			// anything unknown — defends against authorship spoofing.
			visitErr = rejectCreate("turn", "field_not_allowed: "+key)
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasSeq {
		return rejectCreate("turn", "seq required")
	}
	return nil
}

// validateLLM gates the optional llm scalar bundle: string fields
// (stopReason, model) plus non-negative token counters. Unknown
// sub-keys reject — bump the data version when extending.
func validateLLM(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeObject {
		return rejectCreate("turn", "llm must be an object")
	}
	obj, err := v.Object()
	if err != nil {
		return rejectCreate("turn", "llm must be an object")
	}
	var visitErr error
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldLLMStopReason:
			if err := checkString("turn", "llm."+key, val, MaxStopReasonBytes, false); err != nil {
				visitErr = err
			} else if s, _ := val.StringBytes(); !StopReasons[string(s)] {
				visitErr = rejectCreate("turn", "llm.stopReason not in the closed set "+
					"(done|wrapup|break_soft|break_hard|length|error): "+string(s))
			}
		case FieldLLMModel:
			visitErr = checkString("turn", "llm."+key, val, MaxModelBytes, false)
		case FieldLLMInTokens, FieldLLMOutTokens, FieldLLMCacheRead,
			FieldLLMCacheWrite, FieldLLMFuelUsed, FieldLLMCells:
			visitErr = checkNonNegInt("turn", "llm."+key, val)
		case FieldLLMCostUsd:
			visitErr = checkNonNegNumber("turn", "llm."+key, val)
		default:
			visitErr = rejectCreate("turn", "llm: unknown field "+key)
		}
	})
	return visitErr
}

func validateChunkPayload(payload *anyenc.Value) error {
	obj, err := payload.Object()
	if err != nil {
		return rejectCreate("chunk", "payload must be a JSON object")
	}
	var (
		visitErr               error
		hasSeq, hasSummary     bool
		hasLevel               bool
		hasFromSeq, hasToSeq   bool
		hasPerStart, hasPerEnd bool
		level                  int
		fromSeq, toSeq         int
		periodStart, periodEnd float64
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldSeq:
			hasSeq = true
			visitErr = checkSeq("chunk", key, v)
		case FieldLevel:
			hasLevel = true
			if visitErr = checkSeq("chunk", key, v); visitErr == nil {
				if level, _ = v.Int(); level < 1 {
					visitErr = rejectCreate("chunk", "level must be ≥ 1")
				}
			}
		case FieldFromAgent:
			visitErr = checkString("chunk", key, v, MaxFromAgentBytes, false)
		case FieldSummary:
			hasSummary = true
			visitErr = checkString("chunk", key, v, MaxSummaryBytes, false)
		case FieldPeriodStart:
			hasPerStart = true
			if visitErr = checkNonNegNumber("chunk", key, v); visitErr == nil {
				periodStart, _ = v.Float64()
			}
		case FieldPeriodEnd:
			hasPerEnd = true
			if visitErr = checkNonNegNumber("chunk", key, v); visitErr == nil {
				periodEnd, _ = v.Float64()
			}
		case FieldFromSeq:
			hasFromSeq = true
			if visitErr = checkSeq("chunk", key, v); visitErr == nil {
				fromSeq, _ = v.Int()
			}
		case FieldToSeq:
			hasToSeq = true
			if visitErr = checkSeq("chunk", key, v); visitErr == nil {
				toSeq, _ = v.Int()
			}
		case FieldTurnsCovered:
			visitErr = checkNonNegInt("chunk", key, v)
		default:
			visitErr = rejectCreate("chunk", "field_not_allowed: "+key)
		}
	})
	if visitErr != nil {
		return visitErr
	}
	switch {
	case !hasSeq:
		return rejectCreate("chunk", "seq required")
	case !hasLevel:
		return rejectCreate("chunk", "level required (≥1)")
	case !hasSummary:
		return rejectCreate("chunk", "summary required")
	case !hasFromSeq || !hasToSeq:
		return rejectCreate("chunk", "fromSeq and toSeq required")
	case !hasPerStart || !hasPerEnd:
		return rejectCreate("chunk", "periodStart and periodEnd required")
	case fromSeq > toSeq:
		return rejectCreate("chunk", fmt.Sprintf("fromSeq > toSeq (%d > %d)", fromSeq, toSeq))
	case periodStart > periodEnd:
		return rejectCreate("chunk", "periodStart > periodEnd")
	}
	return nil
}

// --- field checks ------------------------------------------------------------

// checkString validates an optional string field: non-empty when
// present (unless allowEmpty), bounded.
func checkString(kind, key string, v *anyenc.Value, maxBytes int, allowEmpty bool) error {
	if v.Type() != anyenc.TypeString {
		return rejectCreate(kind, key+" must be a string")
	}
	s := v.GetStringBytes()
	if len(s) == 0 && !allowEmpty {
		return rejectCreate(kind, key+" must be non-empty when present")
	}
	if len(s) > maxBytes {
		return rejectCreate(kind, fmt.Sprintf("%s too long (%d > %d bytes)", key, len(s), maxBytes))
	}
	return nil
}

func checkStringArray(kind, key string, v *anyenc.Value, maxItems, maxItemBytes int) error {
	if v.Type() != anyenc.TypeArray {
		return rejectCreate(kind, key+" must be an array of strings")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate(kind, key+" must be an array of strings")
	}
	if len(items) > maxItems {
		return rejectCreate(kind, fmt.Sprintf("%s: too many entries (%d > %d)", key, len(items), maxItems))
	}
	for i, item := range items {
		if item.Type() != anyenc.TypeString {
			return rejectCreate(kind, fmt.Sprintf("%s[%d] must be a string", key, i))
		}
		if len(item.GetStringBytes()) > maxItemBytes {
			return rejectCreate(kind, fmt.Sprintf("%s[%d] too long (> %d bytes)", key, i, maxItemBytes))
		}
	}
	return nil
}

// checkSeq validates a non-negative integer sequence field.
func checkSeq(kind, key string, v *anyenc.Value) error {
	if v.Type() != anyenc.TypeNumber {
		return rejectCreate(kind, key+" must be a number")
	}
	n, err := v.Int()
	if err != nil {
		return rejectCreate(kind, key+" must be an integer")
	}
	if n < 0 {
		return rejectCreate(kind, key+" must be ≥ 0")
	}
	return nil
}

func checkNonNegInt(kind, key string, v *anyenc.Value) error {
	return checkSeq(kind, key, v)
}

func checkNonNegNumber(kind, key string, v *anyenc.Value) error {
	if v.Type() != anyenc.TypeNumber {
		return rejectCreate(kind, key+" must be a number")
	}
	f, err := v.Float64()
	if err != nil || f < 0 {
		return rejectCreate(kind, key+" must be ≥ 0")
	}
	return nil
}

func checkBool(kind, key string, v *anyenc.Value) error {
	if v.Type() != anyenc.TypeTrue && v.Type() != anyenc.TypeFalse {
		return rejectCreate(kind, key+" must be a boolean")
	}
	return nil
}

// --- stamping & rejection ----------------------------------------------------

// stampCreate queues sink.Derive ops for creator/createdAt — same
// pattern as chat.stampCreate. No modifiedAt: records are immutable.
func stampCreate(ctx *handler.ChangeCtx, sink *handler.Sink) {
	if ctx == nil || ctx.Change == nil || sink == nil {
		return
	}
	a := &anyenc.Arena{}
	if c := ctx.Change.Creator; c != "" {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreator},
			Payload: a.NewString(c),
		})
	}
	if ts := ctx.Change.Timestamp; ts > 0 {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreatedAt},
			Payload: a.NewNumberInt(int(ts)),
		})
	}
}

func pathString(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	return strings.Join(path, ".")
}

func rejectCreate(kind, msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_log.%s.create: %s", kind, msg))
}

func rejectRecord(kind, msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_log.%s: %s", kind, msg))
}

func rejectOp(kind, msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_log.%s: %s", kind, msg))
}
