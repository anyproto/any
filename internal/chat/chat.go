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
//	  "createdAt":        {"$date": "<RFC 3339>"},           // server-stamped
//	  "modifiedAt":       {"$date": "<RFC 3339>"},           // bumped on edit
//	  "replyToMessageId": "<msgId>",                         // optional
//	  "agent": {                                             // optional, create-only
//	    "name": "<display label>", "debugLink": "<any://…>", "done": <bool>
//	  },
//	  "text":             "<markdown>",                      // ≤ MaxTextBytes
//	  "mentions":         ["<accountId>", ...],              // server-derived
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
// `mentions` is server-DERIVED, never client-supplied (spoofable both
// ways otherwise: silent-ping griefing and notification suppression —
// the text is the source of truth). At change materialization the
// handler parses the message text for mention links
// (`any://m/<spaceId>/<identity>`, anyuri.ExtractMentions) and stamps
// the deduped identity array; when the message is a reply
// (`replyToMessageId`), the replied-to message's creator is folded in
// — a reply IS a ping to the original author, and folding it in at
// write time keeps every consumer (badge counter, push, "mentions of
// me" query) a single indexed field check. Edits re-derive the array
// from the new text (the reply fold-in re-reads via ctx.Get; the
// replied-to creator is immutable so this is replica-deterministic).
// Absent when the message mentions nobody — the sparse idx_mentions
// index stays proportional to mentioning messages. v1 does not
// distinguish reply-derived entries from text mentions: a ping is a
// ping. Self-mentions are not filtered — the array is an objective
// fact of the message; "not my own messages" is a client-side
// creator != me filter (and read-tracking never badges self-authored
// changes anyway).
//
// Server-stamped fields land via sink.Derive in BeforeCreate /
// BeforeModify; they are intentionally invisible to client payloads.
// The handler rejects payloads that try to set them directly.
package chat

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// Module is the module slug a type names in a part's dataset
// declaration (`{"module": "chat", "shared": true}`). Chat is
// shared-only: an object carries at most one chat collection — the
// canonical Dataset — which is what keeps a single read frontier and a
// single push group per object.
const Module = "chat"

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
	FieldMentions         = "mentions"
	FieldReactions        = "reactions"
	FieldAgent            = "agent"
	FieldAttachments      = "attachments"
	FieldContext          = "context"
	FieldControl          = "control"
)

// Agent sub-record keys.
const (
	FieldAgentName      = "name"
	FieldAgentDebugLink = "debugLink"
	FieldAgentDone      = "done"
	FieldAgentOutcome   = "outcome"
)

// Attachment sub-record keys. Each attachment in the attachments map
// is itself a small object; ids in the outer map are opaque short
// strings the client chooses.
const (
	FieldAttachmentType = "type"
	FieldAttachmentLink = "link"
)

// Sub-keys of the `control` group — a client-side signal to the agent
// serving the chat ({kind, hard?}; api.ChatMessageControl). `break`
// asks the run in flight to stop.
const (
	FieldControlKind = "kind"
	FieldControlHard = "hard"
)

// Sub-keys of the `context` group — the sender's view at send time
// ({spaceId, objectId?, view?}; api.ChatMessageContext).
const (
	FieldContextSpaceId  = "spaceId"
	FieldContextObjectId = "objectId"
	FieldContextView     = "view"
)

// dataVersion is the on-the-wire stamp pinned to writes on this
// dataset. Bump only when validation logic changes in a way that must
// reject older writers. v2: `fromAgent` (string) replaced by the
// `agent` {name, debugLink, done} group. v3: the create-only `context`
// group ({spaceId, objectId?, view?}) — a v2 handler rejects a create
// carrying it as field_not_allowed. v4: the optional `agent.outcome`
// sub-field (how the run behind a done:true bubble ended when not
// normally — `interrupted`, `error`; opaque to the server). v5: the
// create-only `control` group ({kind, hard?}) — a signal to the agent
// (`break`), a message that needs no text.
const dataVersion = "chat_messages-v5"

// handlerVersion is this handler's LOCAL logic version — bumped when a
// change to the handler makes rows already materialized on disk wrong,
// so the SDK rebuilds them from the DAG (the SDK's docs/08-versioning.md).
// It never leaves the device; the wire DataVersion above is what gates
// peers. v2: the derived createdAt / modifiedAt stamps and the reaction
// leaves are datetime instants, not epoch numbers.
const handlerVersion = 2

// Validation limits. Conservative; revisit if real usage hits them.
const (
	MaxTextBytes         = 32 * 1024 // ~heart's 8000 utf-16 cps × 4
	MaxReplyIdBytes      = 256
	MaxEmojiBytes        = 64
	MaxAgentNameBytes    = 256
	MaxDebugLinkBytes    = 2 * 1024
	MaxAgentOutcomeBytes = 64

	MaxAttachments         = 32
	MaxAttachmentIdBytes   = 64
	MaxAttachmentTypeBytes = 64
	MaxAttachmentLinkBytes = 2 * 1024

	MaxContextIdBytes   = 256
	MaxContextViewBytes = 64
	MaxControlKindBytes = 64

	// MaxMentions caps the derived mentions array (post-dedup,
	// first-occurrence order wins). MaxTextBytes already bounds real
	// mentions far below this; the cap is a defense against
	// pathological link-stuffing, not a product limit.
	MaxMentions = 64
)

// NewModule returns the handler.Module to add to config.Config.Modules
// so the SDK serves the canonical chat_messages collection with the
// message handler on every controller. SharedOnly: the one declaration
// shape is `{"module": "chat", "shared": true}`; namespaced chat
// instances are refused. Reserved: only the server's own catalog
// install (`system:general-chat/v1`, docs/16-chat.md) declares it —
// a client part, dataset or bundle naming the module is refused, and
// the install root is the type's only carrier. Opening the module to
// clients waits on per-collection push topics and read tracking
// (docs/07-roadmap.md).
//
//	cfg := config.Config{
//	    Modules: []handler.Module{ editor.NewModule(), chat.NewModule() },
//	    ...
//	}
func NewModule() handler.Module {
	return handler.Module{
		Name:           Module,
		Canonical:      Dataset,
		SharedOnly:     true,
		Reserved:       true,
		DataVersion:    dataVersion,
		HandlerVersion: handlerVersion,
		// Unread counters materialized onto the chat object's row —
		// local-scope (device-derived from the synced read frontier,
		// never written by clients or peers) — plus the account-scoped
		// per-chat push preference (client-written, account-synced).
		// They live in the module's namespace (`chat.*`), granted to a
		// row when one of its types declares the module. See reading.go.
		Properties: []handler.PropertyDecl{
			{Id: PropUnreadCount, Name: "Unread Messages", Kind: handler.PropertyKindNumber, Scope: handler.ScopeLocal,
				Description: "Unread messages in the chat for this account."},
			{Id: PropUnreadMentions, Name: "Unread Mentions", Kind: handler.PropertyKindNumber, Scope: handler.ScopeLocal,
				Description: "Unread mentions of this account in the chat."},
			{Id: PropUnreadReactionsCount, Name: "Unread Reactions", Kind: handler.PropertyKindNumber, Scope: handler.ScopeLocal,
				Description: "Unseen reactions to this account's messages in the chat."},
			{Id: PropNotifyMode, Name: "Notify Mode", Kind: handler.PropertyKindString, Scope: handler.ScopeAccount,
				Description: "Push preference for the chat: all, mentions or none; absent inherits the space setting."},
		},
		New: func(handler.ModuleInstance) handler.Dataset {
			return handler.Dataset{
				Handler:      messagesHandler{},
				Schema:       datasetSchema(),
				Indexes:      messagesHandler{}.Indexes(),
				ReadTracking: readTracking(),
				// No version history for chat: clients render live records
				// only (edits show the current text, deletes tombstone) —
				// nothing reads a per-message timeline, so the index rows
				// would be dead weight at chat write volume. The DAG keeps
				// everything; flipping this later only costs a backfill.
				SkipHistory: true,
			}
		},
	}
}

// datasetSchema declares the chat_messages field schema for apply-time
// enforcement and discovery (Space.Datasets / GET .../datasets).
//
// Dynamic so undeclared keys stay permitted (forward-compat with clients
// that grow the record), but every known field is declared with its
// class: creator / createdAt / modifiedAt / mentions are server-stamped
// via sink.Derive → ScopeDerived (handler-only, rejected from client ops);
// text / replyToMessageId / agent / reactions / attachments are
// user/DAG-written → ScopeSynced; unread / unreadMention /
// unreadReactions are device-local read-tracking flags → ScopeLocal
// (written via the local-scope modify route, invisible to other
// members and other devices). a type's property values live in the shared `objects`
// namespace, not here. reactions / attachments / agent carry nested
// keyspaces (emoji→accountId→ts, attachmentId→{type,link},
// {name,debugLink,done}) so they declare an unconstrained object
// shape.
//
// Every field carries a description; a descriptor (docs/27-descriptors.md)
// only where the vocabulary names the value — instants and flags. The
// markdown text, identities, record refs and the nested objects have no
// slug yet (the text slug is a backlinks decision) and describe
// themselves in prose.
func datasetSchema() handler.Schema {
	str := func() *handler.FieldShape { return handler.Leaf(handler.PropertyKindString) }
	obj := func() *handler.FieldShape { return handler.Leaf(handler.PropertyKindObject) }
	flag := func() *handler.FieldShape { return handler.Leaf(handler.PropertyKindBoolean) }
	return handler.Schema{
		Dynamic: true,
		Fields: []handler.Field{
			{Id: FieldCreator, Name: "Creator", Schema: str(), Scope: handler.ScopeDerived,
				Description: "Account identity of the sender; derived."},
			{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindDatetime), Scope: handler.ScopeDerived,
				Description: "Instant the message was sent (sender's clock); derived.", XFormat: map[string]any{"type": "datetime"}},
			{Id: FieldModifiedAt, Name: "Modified At", Schema: handler.Leaf(handler.PropertyKindDatetime), Scope: handler.ScopeDerived,
				Description: "Instant of the last edit (sender's clock); derived.", XFormat: map[string]any{"type": "datetime"}},
			{Id: FieldMentions, Name: "Mentions", Schema: handler.Leaf(handler.PropertyKindArray), Scope: handler.ScopeDerived,
				Description: "Identities linked in the text plus the replied-to message's author; derived."},
			{Id: FieldText, Name: "Text", Schema: str(), Scope: handler.ScopeSynced,
				Description: "Message body, markdown; any:// links carry mentions and references."},
			{Id: FieldReplyToMessageId, Name: "Reply To", Schema: str(), Scope: handler.ScopeSynced,
				Description: "Id of the message this one replies to."},
			{Id: FieldAgent, Name: "Agent", Schema: obj(), Scope: handler.ScopeSynced,
				Description: "Agent authorship hint {name, debugLink, done, outcome}; set once on send."},
			{Id: FieldReactions, Name: "Reactions", Schema: obj(), Scope: handler.ScopeSynced,
				Description: "Reactions by emoji, then by identity, to the instant reacted."},
			{Id: FieldAttachments, Name: "Attachments", Schema: obj(), Scope: handler.ScopeSynced,
				Description: "Attachments by id: {type, link}."},
			{Id: FieldContext, Name: "Context", Schema: obj(), Scope: handler.ScopeSynced,
				Description: "Sender's view at send time: {spaceId, objectId, view}; set once on send."},
			{Id: FieldControl, Name: "Control", Schema: obj(), Scope: handler.ScopeSynced,
				Description: "Signal to the agent serving the chat: {kind, hard}; set once on send."},
			// Read-tracking flags — SDK-materialized, device-local,
			// filterable ({"unread": true}). See reading.go.
			{Id: FieldUnread, Name: "Unread", Schema: flag(), Scope: handler.ScopeLocal,
				Description: "Unread for this account; absent once read.", XFormat: map[string]any{"type": "checkbox"}},
			{Id: FieldUnreadMention, Name: "Unread Mention", Schema: flag(), Scope: handler.ScopeLocal,
				Description: "Unread mention of this account; absent once read.", XFormat: map[string]any{"type": "checkbox"}},
			{Id: FieldUnreadReactions, Name: "Unread Reactions", Schema: flag(), Scope: handler.ScopeLocal,
				Description: "Unseen reactions to this account's own message; absent once seen.", XFormat: map[string]any{"type": "checkbox"}},
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
