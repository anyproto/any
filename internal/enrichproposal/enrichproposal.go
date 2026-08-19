// Package enrichproposal registers the built-in `enrich_proposal` type — an
// ephemeral, reviewable meeting-enrichment plan. One object per enrichment run;
// its `enrich_proposal_items` dataset holds one record per proposed item
// (text + source + target). The object is created with this type attached
// (createObject), so item writes/edits/deletes flow through the generic
// POST /v1/spaces/:id/modify and reads through POST /v1/spaces/:id/query with
// dataset=enrich_proposal_items — no bespoke endpoint.
//
// Lifecycle: an agent drafts raw items, consolidates by editing/deleting
// records; the human reviews; the deterministic apply endpoint
// (POST /v1/spaces/:id/enrich/apply) reads the items, applies them, and DELETES
// the whole proposal object. Proposals are scaffolding, never retained — so the
// item shape is intentionally loose (DefaultHandler, no validation/stamping)
// and the type is excluded from the search index (internal/server/sdk.go).
//
// Item record shape (all optional, written by JS):
//
//	{ "id": "<derived>",
//	  "text":           "<enrichment chunk / human summary>",
//	  "source":         "any://o/<space>/<transcript>/editor_blocks/<blockId>,…",
//	  "outcome":        "enrich" | "new",
//	  "targetObjectId": "<existing obj, or '' for new>",
//	  "targetKind":     "collection" | "property",
//	  "targetProperty": "<typeXKey>.<propXKey>",   // when targetKind=property
//	  "value":          "<typed value to set>",     // when targetKind=property
//	  "newType":        "<xKey>",                    // when outcome=new
//	  "newName":        "<title>" }                  // when outcome=new
package enrichproposal

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// TypeId is reserved — content-addressable user type ids never produce it.
	TypeId      = "enrich_proposal"
	Name        = "Enrich Proposal"
	Description = "Ephemeral, reviewable meeting-enrichment plan: one record per proposed enrichment item (text + source + target). Drafted and edited by the agent, deleted after apply."

	// Dataset holds the per-item records.
	Dataset = "enrich_proposal_items"

	FieldText           = "text"
	FieldSource         = "source"
	FieldOutcome        = "outcome"        // "enrich" | "new"
	FieldTargetObjectId = "targetObjectId"
	FieldTargetKind     = "targetKind"     // "collection" | "property"
	FieldTargetProperty = "targetProperty" // "<typeXKey>.<propXKey>"
	FieldValue          = "value"
	FieldNewType        = "newType"
	FieldNewName        = "newName"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). A raw DefaultHandler dataset (items are agent
// scaffolding — no validation/stamping), but with a declared Schema so clients
// can introspect the item shape via GET /v1/spaces/:id/datasets.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: Dataset, DataVersion: "1", Handler: handler.DefaultHandler{}, Schema: datasetSchema()},
		},
	}
}

// datasetSchema declares the enrich_proposal_items field shape. All fields are
// agent-authored (ScopeSynced); Dynamic keeps it forward-compatible.
func datasetSchema() handler.Schema {
	str := func(id, name string) handler.Field {
		return handler.Field{Id: id, Name: name, Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced}
	}
	return handler.Schema{
		Dynamic: true,
		Fields: []handler.Field{
			str(FieldText, "Text"),
			str(FieldSource, "Source"),
			str(FieldOutcome, "Outcome"),
			str(FieldTargetObjectId, "Target Object ID"),
			str(FieldTargetKind, "Target Kind"),
			str(FieldTargetProperty, "Target Property"),
			str(FieldValue, "Value"),
			str(FieldNewType, "New Type"),
			str(FieldNewName, "New Name"),
		},
	}
}
