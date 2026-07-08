// Package agenttrigger registers the built-in `agent_trigger` type — the
// harness trigger subsystem's storage (anybao docs/adr 006 §4). A trigger
// is a synced record (UI/agent-editable, survives restarts) carrying its
// definition (kind, spec, program, args, owner, enabled, limits) AND its
// observability rollup (lastRunAt/lastStatus/runCount/…), so a monitoring
// `list` query returns the rollup without opening run records. Run records
// live in a sibling keep-last-N dataset.
//
// Mirrors internal/agentdebug/miniapp/program: one built-in type, raw
// DefaultHandler datasets, whole-value $set writes via POST
// /v1/spaces/:id/modify and reads via POST /v1/spaces/:id/query with the
// matching dataset. DefaultHandler stores raw values untransformed, so the
// nested spec/args/limits objects round-trip as-is. No append-only or
// field validation in v2.0 — invariants are harness-enforced (single-owner,
// arming, circuit breaker); a validating handler is a later item if the
// honor system proves insufficient.
package agenttrigger

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "agent_trigger"
	Name        = "Agent Trigger"
	Description = "Harness trigger definitions + run records (cron/event triggers that run programs)"

	// DatasetTriggers holds one record per trigger (definition + rollup).
	DatasetTriggers = "agent_triggers"
	// DatasetRuns holds run records (keep-last-N), keyed by trigger+ts.
	DatasetRuns = "agent_trigger_runs"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). Two DefaultHandler datasets; no declared
// properties — the record values carry everything.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: DatasetTriggers, DataVersion: "1", Handler: handler.DefaultHandler{}},
			{Name: DatasetRuns, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}
