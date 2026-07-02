// Package enricheddata registers the built-in `enriched_data` type — a
// durable, sourced enrichment collection attached to a target object. The type
// multitype-attaches to any object on first write (an existing note/task/page
// gains `enriched_data` alongside its own types, the same pattern chat+agent_log
// use). One record per enrichment fact:
//
//	{ "id":        "<derived>",
//	  "text":      "<the enrichment chunk (collection) / provenance note (property)>",
//	  "source":    "any://<space>/<transcript>#<blockId>,<blockId>",  // durable provenance
//	  "target":    "",                       // collection item
//	             // | "<typeXKey>.<propXKey>" // property item: which field this enriched
//	  "value":     "<the value that was set>",// property items only (UI shows sourced value)
//	  "createdBy": "<accountId>",             // server-stamped
//	  "createdAt": <unix-seconds> }           // server-stamped
//
// For a COLLECTION enrichment the fact lives only here (text + source). For a
// PROPERTY enrichment the apply step also sets the real property on the object
// and records {target, value, source} here so the UI can show that the
// property's value came from a source. `source` points at the transcript, not
// the proposal — provenance survives the proposal's deletion.
//
// Writes go through the bespoke POST /v1/spaces/:s/objects/:o/enriched-data
// endpoint (it attaches the type to the pre-existing target first); reads go
// through POST /v1/spaces/:id/query with dataset=enriched_data.
package enricheddata

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// TypeId is reserved — content-addressable user type ids never produce it.
	TypeId      = "enriched_data"
	Name        = "Enriched Data"
	Description = "Sourced enrichment collection on an object: one record per fact (text + source link); property enrichments also carry the set value and its target property path."

	// Dataset is the per-object collection of enrichment records.
	Dataset = "enriched_data"

	FieldText      = "text"
	FieldSource    = "source"
	FieldTarget    = "target"
	FieldValue     = "value"
	FieldCreatedBy = "createdBy"
	FieldCreatedAt = "createdAt"
)

// Validation bounds. Generous — enrichment chunks can be paragraphs.
const (
	MaxTextBytes   = 64 * 1024
	MaxSourceBytes = 8 * 1024
	MaxTargetBytes = 1024
	MaxValueBytes  = 64 * 1024
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go).
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: Dataset, DataVersion: "enriched_data-v1", Handler: dataHandler{}, Schema: datasetSchema()},
		},
	}
}

// datasetSchema declares the enriched_data field schema so clients can
// introspect the shape via GET /v1/spaces/:id/datasets. createdBy/createdAt are
// server-stamped (ScopeDerived, rejected from client ops); the rest are
// user/DAG-written (ScopeSynced). Dynamic keeps it forward-compatible.
func datasetSchema() handler.Schema {
	return handler.Schema{
		Dynamic: true,
		Fields: []handler.Field{
			{Id: FieldText, Name: "Text", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldSource, Name: "Source", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldTarget, Name: "Target", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldValue, Name: "Value", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldCreatedBy, Name: "Created By", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeDerived},
			{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeDerived},
		},
	}
}

// dataHandler validates create payloads and stamps createdBy/createdAt.
// Implementation in handler.go.
type dataHandler struct {
	handler.DefaultHandler
}
