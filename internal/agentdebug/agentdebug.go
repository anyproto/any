// Package agentdebug registers the built-in `agent_debug_log` type: a
// per-invocation agent trace whose boot context, system prompt, turns, and
// run summary live as structured dataset records rather than appended markdown
// editor blocks.
//
// Mirrors internal/miniapp and internal/program: one built-in type, a raw
// DefaultHandler dataset, per-path $set writes via POST /v1/spaces/:id/modify
// and reads via POST /v1/spaces/:id/query with dataset=agent_debug_log.
//
// Unlike program/mini_app (one "main" record per object), a debug log holds an
// ordered *array* of records — one per log entry. Each record carries a numeric
// `seq` (0-based, monotonic) and a `kind` discriminator; readers sort by `seq`
// to reconstruct the invocation timeline. The record id is the zero-padded seq
// plus the kind (e.g. "000002_turn") so lexical id order also matches insertion
// order. Entry kinds:
//
//	boot           — { seq, kind, ts, prompt, build, spaceType, chatId, identity, botIdentity, rawArgs }
//	system_prompt  — { seq, kind, ts, chars, text }
//	turn           — { seq, kind, ts, n, stopReason, durationMs, inTokens, outTokens, cost, cells[] }
//	                 (cells[] = {code, result, isError, executed}; the full
//	                 messages[]/response are intentionally not stored — redundant)
//	done           — { seq, kind, ts, status, model, turns, totalMs, totalIn, totalOut, totalCost, finalText }
//
// The toolcaller's debug collector (dcInit/dcLogInitialContext/dcLogTurn/
// dcFlush in cmd/bobrik-watch/programs/toolcall_core@v1.js) writes these via
// anyHelper.setRecord. DefaultHandler stores raw values with no transformation
// so nested arrays/objects (the per-turn cells[]) round-trip as-is.
package agentdebug

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "agent_debug_log"
	Name        = "Agent Debug Log"
	Description = "Per-invocation agent trace stored as structured dataset records (boot, system prompt, turns, summary)"

	// Dataset is the per-object dataset holding the ordered entry records.
	Dataset = "agent_debug_log"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). One DefaultHandler dataset; the type itself has no
// extra properties — the object's display name (any.name) carries the prompt.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			// SkipHistory: machine-written grow-only trace — a history
			// index would duplicate the log itself for history nobody
			// opens.
			{Name: Dataset, DataVersion: "1", Handler: handler.DefaultHandler{}, SkipHistory: true},
		},
	}
}
