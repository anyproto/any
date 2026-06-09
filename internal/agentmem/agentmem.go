// Package agentmem defines the built-in `agent_memory` type — typed
// agent memory items stored as records on a per-space "brain" object's
// `agent_memory_items` dataset.
//
// Memory is space-wide (a preference learned in chat A applies in chat
// B), so unlike agent_turns/agent_chunks (internal/agentlog, attached
// to each chat object) the dataset lives on ONE well-known object per
// space: the brain object, derived from a fixed seed via the SDK's
// deterministic Objects().Derive — same mechanic as the spaceIndex
// object and the UI's primary chat. Every caller computes the same id;
// no discovery query, no create race. See brain.go.
//
// Wire / storage shape of one item record:
//
//	{
//	  "id":          "<auto-derived from changeId>",
//	  "_ver":        { ... },              // SDK-managed
//	  "creator":     "<accountId>",        // server-stamped
//	  "createdAt":   <unix-seconds>,       // server-stamped
//	  "modifiedAt":  <unix-seconds>,       // bumped on evolve
//	  "fromAgent":   "<opaque>",           // optional, create-only
//	  "category":    "preference",         // required lowercase slug, OPEN set
//	  "context":     "<one-line summary>", // required
//	  "body":        "<markdown>",         // optional memory text
//	  "tags":        ["...", ...],         // real arrays — no CSV-in-text,
//	  "entities":    ["...", ...],         //   no category-in-tags[0] hacks
//	  "keywords":    ["...", ...],
//	  "confidence":  <0..10>,              // defaulted server-side when absent
//	  "importance":  <1..10>,
//	  "salience":    <0..10>,              // mutable (decay)
//	  "accessCount": <int ≥ 0>,            // mutable (recall tracking)
//	  "validFrom":   <unix-seconds>,       // defaults to createdAt
//	  "edges":       [{"to": "<itemId>", "type": "<slug>", "strength": <0..1>}, ...],
//	  "chatId":      "<chat object id>",   // optional provenance
//	}
//
// Builtin categories (documented, not enforced — agents may invent new
// ones): claim, preference, decision, lesson, episode, taskstate,
// insight. Edge types likewise open; known values: related_to,
// caused_by, leads_to, contradicts, supersedes, supported_by.
//
// There is deliberately NO vector field. Semantic indexing is an
// external service that tails /query/subscribe on this dataset, embeds
// context+body, and keys its ANN index by record id. Until it exists,
// semantic recall is non-functional (docs/07-roadmap.md); fallback
// recall is the indexed category / createdAt / validFrom queries.
// "embeddingRef" is reserved as a future external-index backref and
// rejected on writes in v1.
package agentmem

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// TypeId is reserved — content-addressable user type ids never
// produce this string. NOTE: the legacy bobrik runtime created a USER
// type whose xKey is also "agent_memory"; that one is a content-
// addressed CID id and does not collide with this literal.
const TypeId = "agent_memory"

const (
	Name        = "Agent Memory"
	Description = "Typed agent memory items on the per-space brain object (facts, preferences, lessons, …)"
)

// Dataset is the per-object dataset holding the item records.
const Dataset = "agent_memory_items"

// Field keys on an item record.
const (
	FieldCreator     = "creator"
	FieldCreatedAt   = "createdAt"
	FieldModifiedAt  = "modifiedAt"
	FieldFromAgent   = "fromAgent"
	FieldCategory    = "category"
	FieldContext     = "context"
	FieldBody        = "body"
	FieldTags        = "tags"
	FieldEntities    = "entities"
	FieldKeywords    = "keywords"
	FieldConfidence  = "confidence"
	FieldImportance  = "importance"
	FieldSalience    = "salience"
	FieldAccessCount = "accessCount"
	FieldValidFrom   = "validFrom"
	FieldEdges       = "edges"
	FieldChatId      = "chatId"
)

// Edge sub-record keys.
const (
	FieldEdgeTo       = "to"
	FieldEdgeType     = "type"
	FieldEdgeStrength = "strength"
)

// BuiltinCategories is the documented (open) category set, surfaced
// to clients/docs; the handler only enforces the slug shape.
var BuiltinCategories = []string{
	"claim", "preference", "decision", "lesson", "episode", "taskstate", "insight",
}

// Server-side defaults applied in BeforeCreate when the field is
// absent from the payload.
const (
	DefaultConfidence = 5
	DefaultImportance = 5
	DefaultSalience   = 10
)

const dataVersion = "agent_memory_items-v1"

// Validation limits.
const (
	MaxCategoryBytes  = 64
	MaxContextBytes   = 512
	MaxBodyBytes      = 64 * 1024
	MaxTags           = 64
	MaxTagBytes       = 256
	MaxEntities       = 64
	MaxEntityBytes    = 256
	MaxKeywords       = 64
	MaxKeywordBytes   = 256
	MaxEdges          = 32
	MaxEdgeTypeBytes  = 64
	MaxIdBytes        = 256 // edge.to, chatId
	MaxFromAgentBytes = 256
)

// NewType returns the handler.Type to add to config.Config.Types.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: Dataset, DataVersion: dataVersion, Handler: itemsHandler{}},
		},
	}
}

// itemsHandler owns the agent_memory_items dataset; implementation in
// handler.go.
type itemsHandler struct {
	handler.DefaultHandler
}

func (itemsHandler) Init(_ context.Context) error { return nil }
