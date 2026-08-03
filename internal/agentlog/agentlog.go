// Package agentlog defines the built-in `agent_log` type — the agent's
// raw conversational layer, stored as two per-object datasets attached
// to the SAME object that carries chat_messages (the type is attached
// on first agent write, making the chat object multitype: chat +
// agent_log). Scoping is structural: one chat object = one turn log.
//
// Layering model (docs/11-agent-memory.md): the agent's live context
// is a bounded window over recent records, all raw turns are kept
// (never edited) and every summarization layer carries explicit
// pointers to the raw range it covers, so a reader can always drill
// down (chunk → raw turns → chat messages / run trace) via indexed
// range queries — never a bulk load.
//
// Dataset `agent_turns` — one record per agent invocation (human
// message → end_turn), immutable post-create:
//
//	{
//	  "id":        "<zero-padded seq>",   // lexical order == insertion order
//	  "_ver":      { ... },               // SDK-managed
//	  "seq":       <int>,                 // per-chat monotonic, server-assigned
//	  "creator":   "<accountId>",         // server-stamped
//	  "createdAt": <unix-seconds>,        // server-stamped
//	  "fromAgent": "<opaque>",            // optional, like chat's fromAgent
//	  "userName":  "<sender display>",    // optional
//	  "userText":  "<triggering msg>",    // optional
//	  "think":     "<model narration>",   // optional
//	  "replies":   ["<bubble>", ...],     // optional
//	  "effects":   ["<one-liner>", ...],  // optional
//	  "messageIds":["<chat msg id>",...], // optional, same-object chat_messages refs
//	  "traceRef":  "<objectId>",          // optional, the run's trace object
//	  "interrupted": <bool>,              // optional, invocation was broken/cut
//	  "llm": { "stopReason", "inTokens", "outTokens", "cacheRead",
//	           "cacheWrite", "model", "costUsd", "fuelUsed", "cells" }
//	}
//
// The heavy per-LLM-API-turn detail (tool cells, raw responses) is
// deliberately NOT here — it lives once in the run's trace object,
// reachable via traceRef. The turn record is the conversation-replay
// unit the agent's boot window reads; it must stay lean. `stopReason`
// is a neutral outcome (StopReasons), not a raw provider string.
//
// Dataset `agent_chunks` — one record per compression event,
// immutable post-create:
//
//	{
//	  "id":          "<zero-padded seq>",
//	  "seq":         <int>,               // chunk counter, server-assigned
//	  "level":       <int>,               // 1 = over turns, 2+ = over chunks
//	  "creator":     "<accountId>",       // server-stamped
//	  "createdAt":   <unix-seconds>,      // server-stamped
//	  "fromAgent":   "<opaque>",          // optional
//	  "summary":     "<dense paragraph>",
//	  "periodStart": <unix-seconds>,      // indexable range fields
//	  "periodEnd":   <unix-seconds>,
//	  "fromSeq":     <int>,               // inclusive child pointers (§2)
//	  "toSeq":       <int>,
//	  "unitsCovered":<int>                // optional child-unit count
//	}
//
// Hierarchical compression (ADR-006 §2): a level-1 chunk summarizes a
// contiguous range of agent_turns; a level-N chunk summarizes a
// contiguous range of level-(N-1) CHUNKS. fromSeq/toSeq point at the
// child seqs at the level below, so drill-down is recursive
// (chunk → child chunks → … → raw turns) and never a bulk load.
// Compression never mutates or deletes what it covers — the pointers
// ARE the "compacted" marker.
//
// Both datasets are write-once: every modify is rejected (the
// "append_only" reason doubles as the duplicate-seq collision signal,
// api.go); deletes are author-only. A wiped range leaves chunk seq
// pointers dangling — readers tolerate sparse ranges.
package agentlog

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// TypeId is reserved — content-addressable user type ids never
// produce this string.
const TypeId = "agent_log"

const (
	Name        = "Agent Log"
	Description = "Agent raw conversation layer: write-once turn records plus compressed chunks with explicit raw-range pointers"
)

// Dataset names. Both attach to the chat object.
const (
	DatasetTurns  = "agent_turns"
	DatasetChunks = "agent_chunks"
)

// Field keys on a turn record. Literal strings, matching chat/editor.
const (
	FieldSeq         = "seq"
	FieldCreator     = "creator"
	FieldCreatedAt   = "createdAt"
	FieldFromAgent   = "fromAgent"
	FieldUserName    = "userName"
	FieldUserText    = "userText"
	FieldThink       = "think"
	FieldReplies     = "replies"
	FieldEffects     = "effects"
	FieldMessageIds  = "messageIds"
	FieldTraceRef    = "traceRef"
	FieldInterrupted = "interrupted"
	FieldLLM         = "llm"
)

// Sub-keys of the llm scalar bundle.
const (
	FieldLLMStopReason = "stopReason"
	FieldLLMInTokens   = "inTokens"
	FieldLLMOutTokens  = "outTokens"
	FieldLLMCacheRead  = "cacheRead"
	FieldLLMCacheWrite = "cacheWrite"
	FieldLLMModel      = "model"
	FieldLLMCostUsd    = "costUsd"
	FieldLLMFuelUsed   = "fuelUsed"
	FieldLLMCells      = "cells"
)

// StopReasons is the closed set of neutral invocation outcomes
// (ADR-005 Outcome + ADR-006 §1) — replaces the v1 raw-provider
// strings ("end_turn"). Every invocation ends in exactly one.
var StopReasons = map[string]bool{
	"done":       true,
	"wrapup":     true,
	"break_soft": true,
	"break_hard": true,
	"length":     true,
	"error":      true,
}

// Field keys on a chunk record (seq/creator/createdAt/fromAgent shared
// with turns above).
const (
	FieldLevel        = "level"
	FieldSummary      = "summary"
	FieldPeriodStart  = "periodStart"
	FieldPeriodEnd    = "periodEnd"
	FieldFromSeq      = "fromSeq"
	FieldToSeq        = "toSeq"
	FieldUnitsCovered = "unitsCovered"
	// FieldTurnsCovered is the LEGACY name of unitsCovered. The
	// validator still accepts it so records stored under the old key
	// stay applyable on replay; nothing writes it anymore and it is
	// undocumented on the wire.
	FieldTurnsCovered = "turnsCovered"
)

// Data versions pinned to writes. Bump only when validation must
// reject older writers.
const (
	turnsDataVersion  = "agent_turns-v2"
	chunksDataVersion = "agent_chunks-v2"
)

// Validation limits. Conservative; revisit if real usage hits them.
const (
	MaxUserTextBytes   = 32 * 1024 // mirrors chat.MaxTextBytes
	MaxThinkBytes      = 64 * 1024
	MaxReplyBytes      = 32 * 1024
	MaxReplies         = 64
	MaxEffectBytes     = 2 * 1024
	MaxEffects         = 256
	MaxMessageIds      = 64
	MaxIdBytes         = 256 // messageIds entries, traceRef
	MaxFromAgentBytes  = 256 // mirrors chat
	MaxUserNameBytes   = 256
	MaxStopReasonBytes = 64
	MaxModelBytes      = 128

	MaxSummaryBytes = 32 * 1024
)

// SeqPadWidth is the zero-pad width for record ids derived from seq —
// wide enough that lexical id order matches numeric insertion order
// for any realistic log length. The pad is load-bearing for the seq
// allocator: nextSeq reads the max seq off the lexically-greatest
// record id, so ids longer than the pad would break the ordering.
// MaxSeq caps seq accordingly (enforced at create).
const SeqPadWidth = 8

// MaxSeq is the largest storable seq — the widest value the id pad
// keeps lexically ordered.
const MaxSeq = 1e8 - 1

// NewType returns the handler.Type to add to config.Config.Types.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			// SkipHistory: turns and chunks are write-once, so every
			// record has exactly one live version — a history index
			// would be pure dead weight.
			{Name: DatasetTurns, DataVersion: turnsDataVersion, Handler: turnsHandler{}, Indexes: turnsHandler{}.Indexes(), SkipHistory: true},
			{Name: DatasetChunks, DataVersion: chunksDataVersion, Handler: chunksHandler{}, Indexes: chunksHandler{}.Indexes(), SkipHistory: true},
		},
	}
}

// turnsHandler owns agent_turns; chunksHandler owns agent_chunks.
// Implementations live in handler.go.
type turnsHandler struct {
	handler.DefaultHandler
}

type chunksHandler struct {
	handler.DefaultHandler
}

func (turnsHandler) Init(_ context.Context) error  { return nil }
func (chunksHandler) Init(_ context.Context) error { return nil }
