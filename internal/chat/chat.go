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
//	  "text":             "<markdown>",                      // ≤ MaxTextBytes
//	  "reactions":        { "<accountId>": ["<emoji>", ...] }
//	}
//
// Chronological order is `_ver.id` — the SDK's creation-version
// marker, set once when the record is created (newRecord) and never
// updated by subsequent modifies. Same role heart's `_o.id` plays.
// The chat handler does NOT stamp a parallel order field; sort by
// `_ver.id` and pagination cursors translate to message-id → _ver.id
// lookups at the API layer.
//
// Reactions are stored identity-keyed (each user owns their key) so
// authorization is a single path-segment compare against
// ctx.Change.Creator. The API layer transposes to emoji-keyed
// {emoji: [accountId, ...]} on read because that's what clients
// expect.
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
const (
	FieldCreator          = "creator"
	FieldCreatedAt        = "createdAt"
	FieldModifiedAt       = "modifiedAt"
	FieldReplyToMessageId = "replyToMessageId"
	FieldText             = "text"
	FieldReactions        = "reactions"
)

// dataVersion is the on-the-wire stamp pinned to writes on this
// dataset. Bump only when validation logic changes in a way that must
// reject older writers.
const dataVersion = "chat_messages-v1"

// Validation limits. Conservative; revisit if real usage hits them.
const (
	MaxTextBytes    = 32 * 1024 // ~heart's 8000 utf-16 cps × 4
	MaxReplyIdBytes = 256
	MaxEmojiBytes   = 64
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
		Handlers: []handler.Registration{{
			Handler:     messagesHandler{},
			DataVersion: dataVersion,
		}},
	}
}

// messagesHandler is the CRDT handler owning the chat_messages
// dataset. Implementation lives in handler.go; the type is declared
// here so chat.go is self-contained for the registration story.
type messagesHandler struct {
	handler.DefaultHandler
}

func (messagesHandler) Dataset() string              { return Dataset }
func (messagesHandler) Version() int                 { return 1 }
func (messagesHandler) Init(_ context.Context) error { return nil }
