// Package bin registers the built-in `bin` type: the marker of an object
// moved to the bin.
//
// Move to bin = attach the type, restore = detach it, both through the
// existing POST …/properties/:objectId/{attach,detach}/:typeId — no wire
// surface of its own. The server stamps `movedAt` (an instant) and
// `movedBy` (the account identity, the encoding `author` / `modifiedBy`
// use) on attach and clears both on detach, in the same change as the
// membership op (internal/server/handlers_properties.go). Hidden, no
// parts. Clients filter carriers out of ordinary lists
// (`{"any.types": {"$nin": ["bin"]}}`); a permanent delete stays
// DELETE …/objects/:id.
//
// The stamps are ordinary synced properties: the server writes them,
// but nothing stops a peer from writing them directly, so a reader
// treats an absent stamp as unknown, never as "not moved".
package bin

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// TypeId is reserved — content-addressable user type ids never
	// produce it, and the xKey guard refuses user types claiming it.
	TypeId      = "bin"
	Name        = "Bin"
	Description = "Moved to the bin: attach to move, detach to restore"

	// PropMovedAt is when the object was moved (server clock, an
	// instant); PropMovedBy the account that moved it.
	PropMovedAt = "movedAt"
	PropMovedBy = "movedBy"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go).
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Hidden:      true,
		Properties: []handler.PropertyDecl{
			{Id: PropMovedAt, Name: "Moved At", Kind: handler.PropertyKindDatetime, XFormat: map[string]any{"type": "datetime"},
				Description: "Instant the object was moved to the bin."},
			{Id: PropMovedBy, Name: "Moved By", Kind: handler.PropertyKindString,
				Description: "Account identity that moved the object to the bin."},
		},
	}
}
