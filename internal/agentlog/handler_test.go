package agentlog

import (
	"errors"
	"strings"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

const alice = "alice"

func makeChange(creator string, ts int64) *handler.Change {
	return &handler.Change{
		SpaceId:   "space",
		ObjectId:  "obj",
		Dataset:   DatasetTurns,
		ChangeId:  "ch1",
		VersionId: "v1",
		Creator:   creator,
		Timestamp: ts,
	}
}

// createRec wraps a payload object into the canonical creation shape:
// one multi-field $set with empty path.
func createRec(payload *anyenc.Value) *handler.RecordChange {
	return &handler.RecordChange{
		Id:     "00000000",
		Upsert: true,
		Ops: []handler.Op{{
			Type:    handler.OpSet,
			Path:    nil,
			Payload: payload,
		}},
	}
}

func ctxAndSink() (*handler.ChangeCtx, *handler.Sink) {
	return &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}, &handler.Sink{}
}

func newStringArray(arena *anyenc.Arena, items ...string) *anyenc.Value {
	arr := arena.NewArray()
	for i, s := range items {
		arr.SetArrayItem(i, arena.NewString(s))
	}
	return arr
}

// --- turns: BeforeCreate -----------------------------------------------------

func minimalTurn(arena *anyenc.Arena) *anyenc.Value {
	payload := arena.NewObject()
	payload.Set(FieldSeq, arena.NewNumberInt(0))
	return payload
}

func TestTurnCreate_Minimal(t *testing.T) {
	arena := &anyenc.Arena{}
	ctx, sink := ctxAndSink()
	if err := (turnsHandler{}).BeforeCreate(ctx, createRec(minimalTurn(arena)), sink); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestTurnCreate_FullPayload(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	payload.Set(FieldFromAgent, arena.NewString("bobrik"))
	payload.Set(FieldUserName, arena.NewString("alice"))
	payload.Set(FieldUserText, arena.NewString("what's the status?"))
	payload.Set(FieldThink, arena.NewString("user asks about status"))
	payload.Set(FieldReplies, newStringArray(arena, "All good.", "Anything else?"))
	payload.Set(FieldEffects, newStringArray(arena, "created Book [Dune](any://s/o)"))
	payload.Set(FieldMessageIds, newStringArray(arena, "msg1", "msg2"))
	payload.Set(FieldTraceRef, arena.NewString("traceObj1"))
	payload.Set(FieldInterrupted, arena.NewTrue())
	llm := arena.NewObject()
	llm.Set(FieldLLMStopReason, arena.NewString("done"))
	llm.Set(FieldLLMInTokens, arena.NewNumberInt(1200))
	llm.Set(FieldLLMOutTokens, arena.NewNumberInt(300))
	llm.Set(FieldLLMCacheRead, arena.NewNumberInt(8000))
	llm.Set(FieldLLMCacheWrite, arena.NewNumberInt(0))
	llm.Set(FieldLLMModel, arena.NewString("claude-sonnet-4-6"))
	llm.Set(FieldLLMCostUsd, arena.NewNumberFloat64(0.0123))
	llm.Set(FieldLLMFuelUsed, arena.NewNumberInt(184223))
	llm.Set(FieldLLMCells, arena.NewNumberInt(3))
	payload.Set(FieldLLM, llm)

	ctx, sink := ctxAndSink()
	if err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestTurnCreate_StopReasonEnumEnforced(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	llm := arena.NewObject()
	llm.Set(FieldLLMStopReason, arena.NewString("end_turn")) // v1 raw string — now rejected
	payload.Set(FieldLLM, llm)
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "stopReason not in the closed set")
}

func TestTurnCreate_InterruptedMustBeBool(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	payload.Set(FieldInterrupted, arena.NewString("yes"))
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "interrupted must be a boolean")
}

func TestTurnCreate_MissingSeqRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldUserText, arena.NewString("hello"))
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "seq required")
}

func TestTurnCreate_NegativeSeqRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldSeq, arena.NewNumberInt(-1))
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "seq must be ≥ 0")
}

func TestTurnCreate_StampedFieldsRejected(t *testing.T) {
	for _, field := range []string{FieldCreator, FieldCreatedAt} {
		arena := &anyenc.Arena{}
		payload := minimalTurn(arena)
		payload.Set(field, arena.NewString("spoof"))
		ctx, sink := ctxAndSink()
		err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
		requireValidationErr(t, err, "field_not_allowed: "+field)
	}
}

func TestTurnCreate_UnknownFieldRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	payload.Set("cells", arena.NewString("nope")) // cells belong to the debug log
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "field_not_allowed: cells")
}

func TestTurnCreate_UnknownLLMFieldRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	llm := arena.NewObject()
	llm.Set("temperature", arena.NewNumberFloat64(0.7))
	payload.Set(FieldLLM, llm)
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "llm: unknown field temperature")
}

func TestTurnCreate_RepliesMustBeStrings(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalTurn(arena)
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewNumberInt(42))
	payload.Set(FieldReplies, arr)
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "replies[0] must be a string")
}

// --- turns: append-only ------------------------------------------------------

func TestTurnModify_Rejected(t *testing.T) {
	ctx, sink := ctxAndSink()
	op := &handler.Op{Type: handler.OpSet, Path: []string{FieldUserText}}
	err := (turnsHandler{}).BeforeModify(ctx, nil, op, sink)
	requireValidationErr(t, err, "append_only")
}

func TestTurnDelete_Rejected(t *testing.T) {
	ctx, sink := ctxAndSink()
	err := (turnsHandler{}).BeforeDelete(ctx, nil, sink)
	requireValidationErr(t, err, "append_only")
}

// --- chunks: BeforeCreate ----------------------------------------------------

func minimalChunk(arena *anyenc.Arena) *anyenc.Value {
	payload := arena.NewObject()
	payload.Set(FieldSeq, arena.NewNumberInt(0))
	payload.Set(FieldLevel, arena.NewNumberInt(1))
	payload.Set(FieldSummary, arena.NewString("alice asked about X; agent created Y"))
	payload.Set(FieldPeriodStart, arena.NewNumberInt(1700000000))
	payload.Set(FieldPeriodEnd, arena.NewNumberInt(1700003600))
	payload.Set(FieldFromSeq, arena.NewNumberInt(0))
	payload.Set(FieldToSeq, arena.NewNumberInt(9))
	return payload
}

func TestChunkCreate_MissingLevelRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Del(FieldLevel)
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "level required")
}

func TestChunkCreate_ZeroLevelRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Set(FieldLevel, arena.NewNumberInt(0))
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "level must be ≥ 1")
}

func TestChunkCreate_Level2OK(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Set(FieldLevel, arena.NewNumberInt(2)) // over level-1 chunks
	ctx, sink := ctxAndSink()
	if err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink); err != nil {
		t.Fatalf("level-2 chunk should be valid: %v", err)
	}
}

func TestChunkCreate_Minimal(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Set(FieldTurnsCovered, arena.NewNumberInt(10))
	ctx, sink := ctxAndSink()
	if err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestChunkCreate_MissingPointersRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldSeq, arena.NewNumberInt(0))
	payload.Set(FieldLevel, arena.NewNumberInt(1))
	payload.Set(FieldSummary, arena.NewString("s"))
	payload.Set(FieldPeriodStart, arena.NewNumberInt(1700000000))
	payload.Set(FieldPeriodEnd, arena.NewNumberInt(1700003600))
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "fromSeq and toSeq required")
}

func TestChunkCreate_InvertedRangeRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Set(FieldFromSeq, arena.NewNumberInt(9))
	payload.Set(FieldToSeq, arena.NewNumberInt(3))
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "fromSeq > toSeq")
}

func TestChunkCreate_InvertedPeriodRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalChunk(arena)
	payload.Set(FieldPeriodStart, arena.NewNumberInt(1700009999))
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeCreate(ctx, createRec(payload), sink)
	requireValidationErr(t, err, "periodStart > periodEnd")
}

func TestChunkModify_Rejected(t *testing.T) {
	ctx, sink := ctxAndSink()
	op := &handler.Op{Type: handler.OpSet, Path: []string{FieldSummary}}
	err := (chunksHandler{}).BeforeModify(ctx, nil, op, sink)
	requireValidationErr(t, err, "append_only")
}

func TestChunkDelete_Rejected(t *testing.T) {
	ctx, sink := ctxAndSink()
	err := (chunksHandler{}).BeforeDelete(ctx, nil, sink)
	requireValidationErr(t, err, "append_only")
}

// --- shared ------------------------------------------------------------------

func TestRecordId_LexicalOrder(t *testing.T) {
	if RecordId(2) >= RecordId(10) {
		t.Fatalf("lexical order broken: %q >= %q", RecordId(2), RecordId(10))
	}
	if got := RecordId(7); got != "00000007" {
		t.Fatalf("RecordId(7) = %q", got)
	}
}

func requireValidationErr(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", contains)
	}
	if !errors.Is(err, handler.ErrValidation) {
		t.Fatalf("expected handler.ErrValidation, got: %v", err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}
