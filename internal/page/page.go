// Package page registers the built-in `page` type: the plain document.
//
// Hidden, no properties, one part — `body` — whose dataset is the
// editor module's shared collection, so an object carrying `page` holds
// `editor_blocks` (docs/03-api.md § Parts and modules). Optional for
// clients: attach it for a plain document body, or declare your own
// document types with an editor part — both share the same collection,
// and an object carrying two such types still has one body. Nothing
// stamps it: a client decides which types its objects carry.
//
// Registered, not a bundle: the id is reserved and the declaration
// static, so every space has it by construction and no install can
// fork. The price is the registered-type freeze — no properties, no
// weight, no layout; a client that needs those declares a user type.
package page

import (
	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/internal/editor"
)

const (
	// TypeId is reserved — content-addressable user type ids never
	// produce it, and the xKey guard refuses user types claiming it.
	TypeId      = "page"
	Name        = "Page"
	Description = "A document: one body in the shared editor collection"

	// PartBody is the type's single part; its dataset is the editor's
	// canonical collection.
	PartBody = "body"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). The SDK compiles the static part into
// ownership of the editor's canonical collection on every space: the
// write gate, discovery (`owners` of `editor_blocks`) and the parts read
// follow from the declaration.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Hidden:      true,
		Parts: []handler.Part{{
			Key:      PartBody,
			Name:     "Body",
			UI:       map[string]any{"type": "document"},
			Datasets: []handler.PartDataset{{Module: editor.Module, Shared: true}},
		}},
	}
}
