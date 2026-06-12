// Package chat defines the built-in `chat` type — a CRDT-backed
// stream of messages stored as one record per message on a per-object
// `chat_messages` dataset. Mirrors the markdown package's shape: a
// registered handler.Type plus aggregating helpers in api.go.
//
// Wire / storage shape of one message record:
//
//	{
//	  "id":               "<auto-derived from changeId>",
//	  "_ver": { "id": "<VersionId of creating change>", ... },  // SDK-managed
//	  "creator":          "<accountId>",                     // server-stamped
//	  "createdAt":        <unix-seconds>,                    // server-stamped
//	  "modifiedAt":       <unix-seconds>,                    // bumped on edit
//	  "replyToMessageId": "<msgId>",                         // optional
//	  "agent": {                                             // optional, create-only
//	    "name": "<display label>", "debugLink": "<any://…>", "done": <bool>
//	  },
//	  "text":             "<markdown>",                      // ≤ MaxTextBytes
//	  "attachments":      {                                  // optional, create-only
//	    "<id>": { "type": "<link|image|…>", "link": "<url>" }
//	  },
//	  "reactions":        { "<emoji>": { "<accountId>": <changeTimestamp> } }
//	}
//
// `attachments` is a UI-side affordance — a map keyed by short opaque
// ids (≤ MaxAttachmentIdBytes, [A-Za-z0-9_-]+) carrying {type, link}.
// Create-only — the handler rejects $set on the attachments path
// post-create — so once a message lands, its attachments are
// immutable (matches reactions' append-only stance, simpler to reason
// about across peers).
//
// `agent` marks the message as authored by an agent acting on the
// signer's behalf (vs the signer typing it directly). It is NOT
// cryptographically verified — `creator` is still the change signer;
// the group is a UI hint, useful e.g. as a "human said this, please
// respond" trigger for an agent subscribed to agent-less messages.
// Fields: `name` (required display label), `debugLink` (optional
// drill-down into the run's debug page, by convention
// `any://<spaceId>/<objectId>[#turn_<n>]` — opaque to the server) and
// `done` (required liveness bool: false while the producing run is
// still going, true on terminal messages; clients key their typing
// indicator on it). Create-only and immutable as a group, like
// attachments.
//
// Chronological order is `_ver.id` — the SDK's creation-version
// marker, set once when the record is created (newRecord) and never
// updated by subsequent modifies. Same role heart's `_o.id` plays.
// The chat handler does NOT stamp a parallel order field; sort by
// `_ver.id` and pagination cursors translate to message-id → _ver.id
// lookups at the API layer.
//
// Reactions are stored emoji-keyed at the first segment, identity-
// keyed at the second: `reactions.<emoji>.<accountId> = <ts>`. The
// timestamp is the triggering change's clock, server-derived (the
// client's payload value is overwritten via sink.Derive). Two
// invariants follow:
//
//   - Authorization on react ops is a single path-segment compare:
//     path[2] (the accountId leaf) must equal ctx.Change.Creator.
//     Anyone can toggle their own slot; nobody can toggle someone
//     else's.
//   - The emoji and accountId are both first-class storage keys, so a
//     single CRDT $set/$unset on the leaf path adds or removes one
//     identity's reaction with one emoji — no array shuffling.
//
// On the wire the handler keeps the storage shape unchanged; the API
// layer rolls it up to `{emoji: [accountId, ...]}` (sorted by
// timestamp ascending) because that's what clients render.
//
// Server-stamped fields land via sink.Derive in BeforeCreate /
// BeforeModify; they are intentionally invisible to client payloads.
// The handler rejects payloads that try to set them directly.
package chat

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// TypeId is the type identifier callers register chat objects under.
// Reserved — content-addressable user-derived type ids never produce
// this string.
const TypeId = "chat"

// Display metadata used when surfacing the built-in via Types.List.
const (
	Name        = "Chat"
	Description = "Message stream, one record per message"
)

// Dataset is the per-object dataset that holds the message records.
const Dataset = "chat_messages"

// Field keys on a message record. Literal strings — no
// content-addressable propIds — to keep the registration
// hand-readable, matching nav and blocks.
//
// FieldAgent is an optional, create-only group clients use to mark a
// message as "written by an agent acting on behalf of the signer"
// rather than the signer typing it themselves. The handler does NOT
// verify the identity (signature still comes from the signer wallet);
// it's a UI-only hint, useful e.g. to subscribe to messages without
// `agent` and have an agent respond. See the package doc for the
// {name, debugLink, done} sub-fields.
const (
	FieldCreator          = "creator"
	FieldCreatedAt        = "createdAt"
	FieldModifiedAt       = "modifiedAt"
	FieldReplyToMessageId = "replyToMessageId"
	FieldText             = "text"
	FieldReactions        = "reactions"
	FieldAgent            = "agent"
	FieldAttachments      = "attachments"
)

// Agent sub-record keys.
const (
	FieldAgentName      = "name"
	FieldAgentDebugLink = "debugLink"
	FieldAgentDone      = "done"
)

// Attachment sub-record keys. Each attachment in the attachments map
// is itself a small object; ids in the outer map are opaque short
// strings the client chooses.
const (
	FieldAttachmentType = "type"
	FieldAttachmentLink = "link"
)

// dataVersion is the on-the-wire stamp pinned to writes on this
// dataset. Bump only when validation logic changes in a way that must
// reject older writers. v2: `fromAgent` (string) replaced by the
// `agent` {name, debugLink, done} group.
const dataVersion = "chat_messages-v2"

// Validation limits. Conservative; revisit if real usage hits them.
const (
	MaxTextBytes      = 32 * 1024 // ~heart's 8000 utf-16 cps × 4
	MaxReplyIdBytes   = 256
	MaxEmojiBytes     = 64
	MaxAgentNameBytes = 256
	MaxDebugLinkBytes = 2 * 1024

	MaxAttachments         = 32
	MaxAttachmentIdBytes   = 64
	MaxAttachmentTypeBytes = 64
	MaxAttachmentLinkBytes = 2 * 1024
)

// NewType returns the handler.Type to add to config.Config.Types so
// the SDK accepts writes on the chat_messages dataset.
//
//	cfg := config.Config{
//	    Types: []handler.Type{ blocks.NewType(), chat.NewType() },
//	    ...
//	}
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{{
			Name:        Dataset,
			DataVersion: dataVersion,
			Handler:     messagesHandler{},
			Schema:      datasetSchema(),
		}},
	}
}

// datasetSchema declares the chat_messages field schema for apply-time
// enforcement and discovery (Space.Datasets / GET .../datasets).
//
// Dynamic so undeclared keys stay permitted (forward-compat with clients
// that grow the record), but every known field is declared with its
// class: creator / createdAt / modifiedAt are server-stamped via
// sink.Derive → ScopeDerived (handler-only, rejected from client ops);
// text / replyToMessageId / agent / reactions / attachments are
// user/DAG-written → ScopeSynced. nav.* lives in the shared `objects`
// namespace, not here. reactions / attachments / agent carry nested
// keyspaces (emoji→accountId→ts, attachmentId→{type,link},
// {name,debugLink,done}) so they declare an unconstrained object
// shape.
func datasetSchema() handler.Schema {
	return handler.Schema{
		Dynamic: true,
		Fields: []handler.Field{
			{Id: FieldCreator, Name: "Creator", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeDerived},
			{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeDerived},
			{Id: FieldModifiedAt, Name: "Modified At", Schema: handler.Leaf(handler.PropertyKindNumber), Scope: handler.ScopeDerived},
			{Id: FieldText, Name: "Text", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldReplyToMessageId, Name: "Reply To", Schema: handler.Leaf(handler.PropertyKindString), Scope: handler.ScopeSynced},
			{Id: FieldAgent, Name: "Agent", Schema: handler.Leaf(handler.PropertyKindObject), Scope: handler.ScopeSynced},
			{Id: FieldReactions, Name: "Reactions", Schema: handler.Leaf(handler.PropertyKindObject), Scope: handler.ScopeSynced},
			{Id: FieldAttachments, Name: "Attachments", Schema: handler.Leaf(handler.PropertyKindObject), Scope: handler.ScopeSynced},
		},
	}
}

// messagesHandler is the CRDT handler owning the chat_messages
// dataset. Implementation lives in handler.go; the type is declared
// here so chat.go is self-contained for the registration story.
type messagesHandler struct {
	handler.DefaultHandler
}

func (messagesHandler) Init(_ context.Context) error { return nil }
