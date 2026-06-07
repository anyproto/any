// Package agentlog defines the built-in `agent_log` type — the agent's
// raw conversational layer, stored as two per-object datasets attached
// to the SAME object that carries chat_messages (the type is attached
// on first agent write, making the chat object multitype: chat +
// agent_log). Scoping is structural: one chat object = one turn log.
//
// Layering model (docs/11-agent-memory.md): the agent's live context
// is a bounded window over recent records, but ALL raw turns are kept
// append-only forever and every summarization layer carries explicit
// pointers to the raw range it covers, so a reader can always drill
// down (chunk → raw turns → chat messages / debug log) via indexed
// range queries — never a bulk load.
//
// Dataset `agent_turns` — one record per agent invocation (human
// message → end_turn), immutable post-create:
//
//	{
//	  "id":        "<zero-padded seq>",   // lexical order == insertion order
//	  "_ver":      { ... },               // SDK-managed
//	  "seq":       <int>,                 // per-chat monotonic, caller-assigned
//	  "creator":   "<accountId>",         // server-stamped
//	  "createdAt": <unix-seconds>,        // server-stamped
//	  "fromAgent": "<opaque>",            // optional, like chat's fromAgent
//	  "userName":  "<sender display>",    // optional
//	  "userText":  "<triggering msg>",    // optional
//	  "think":     "<model narration>",   // optional
//	  "replies":   ["<bubble>", ...],     // optional
//	  "effects":   ["<one-liner>", ...],  // optional
//	  "messageIds":["<chat msg id>",...], // optional, same-object chat_messages refs
//	  "debugRef":  "<objectId>",          // optional, agent_debug_log page
//	  "llm": { "stopReason", "inTokens", "outTokens",
//	           "cacheRead", "cacheWrite", "model" }   // optional scalars
//	}
//
// The heavy per-LLM-API-turn detail (tool cells, raw responses) is
// deliberately NOT here — it lives once, in agent_debug_log, reachable
// via debugRef. The turn record is the conversation-replay unit the
// agent's boot window reads; it must stay lean.
//
// Dataset `agent_chunks` — one record per compression event,
// immutable post-create:
//
//	{
//	  "id":          "<zero-padded seq>",
//	  "seq":         <int>,               // chunk counter, caller-assigned
//	  "creator":     "<accountId>",       // server-stamped
//	  "createdAt":   <unix-seconds>,      // server-stamped
//	  "fromAgent":   "<opaque>",          // optional
//	  "summary":     "<dense paragraph>",
//	  "periodStart": <unix-seconds>,      // indexable range fields
//	  "periodEnd":   <unix-seconds>,
//	  "fromSeq":     <int>,               // inclusive pointers into agent_turns
//	  "toSeq":       <int>,
//	  "turnsCovered":<int>                // optional count
//	}
//
// fromSeq/toSeq are the layering contract: a chunk always knows the
// exact raw range it summarizes, so `{seq:{$gte:fromSeq,$lte:toSeq}}`
// against agent_turns on the same object reconstructs full fidelity.
// Compression never mutates or deletes turns — the pointers ARE the
// "compacted" marker.
//
// Both datasets are append-only in v1: BeforeModify and BeforeDelete
// reject everything. GC/retention is a deliberate non-feature for now;
// if it lands, chunk pointers may need id-lists instead of seq ranges.
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
	Description = "Agent raw conversation layer: append-only turn records plus compressed chunks with explicit raw-range pointers"
)

// Dataset names. Both attach to the chat object.
const (
	DatasetTurns  = "agent_turns"
	DatasetChunks = "agent_chunks"
)

// Field keys on a turn record. Literal strings, matching chat/editor.
const (
	FieldSeq        = "seq"
	FieldCreator    = "creator"
	FieldCreatedAt  = "createdAt"
	FieldFromAgent  = "fromAgent"
	FieldUserName   = "userName"
	FieldUserText   = "userText"
	FieldThink      = "think"
	FieldReplies    = "replies"
	FieldEffects    = "effects"
	FieldMessageIds = "messageIds"
	FieldDebugRef   = "debugRef"
	FieldLLM        = "llm"
)

// Sub-keys of the llm scalar bundle.
const (
	FieldLLMStopReason = "stopReason"
	FieldLLMInTokens   = "inTokens"
	FieldLLMOutTokens  = "outTokens"
	FieldLLMCacheRead  = "cacheRead"
	FieldLLMCacheWrite = "cacheWrite"
	FieldLLMModel      = "model"
)

// Field keys on a chunk record (seq/creator/createdAt/fromAgent shared
// with turns above).
const (
	FieldSummary      = "summary"
	FieldPeriodStart  = "periodStart"
	FieldPeriodEnd    = "periodEnd"
	FieldFromSeq      = "fromSeq"
	FieldToSeq        = "toSeq"
	FieldTurnsCovered = "turnsCovered"
)

// Data versions pinned to writes. Bump only when validation must
// reject older writers.
const (
	turnsDataVersion  = "agent_turns-v1"
	chunksDataVersion = "agent_chunks-v1"
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
	MaxIdBytes         = 256 // messageIds entries, debugRef
	MaxFromAgentBytes  = 256 // mirrors chat
	MaxUserNameBytes   = 256
	MaxStopReasonBytes = 64
	MaxModelBytes      = 128

	MaxSummaryBytes = 32 * 1024
)

// SeqPadWidth is the zero-pad width for record ids derived from seq —
// wide enough that lexical id order matches numeric insertion order
// for any realistic log length.
const SeqPadWidth = 8

// NewType returns the handler.Type to add to config.Config.Types.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: DatasetTurns, DataVersion: turnsDataVersion, Handler: turnsHandler{}},
			{Name: DatasetChunks, DataVersion: chunksDataVersion, Handler: chunksHandler{}},
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
