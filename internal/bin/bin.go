// Package bin registers the built-in `bin` collection: the marker of an
// object moved to the bin.
//
// Move to bin = attach the collection, restore = detach it, both
// through POST/DELETE …/properties/:objectId/collections/bin — no wire
// surface of its own. The server stamps `movedAt` (an instant) and
// `movedBy` (the account identity, the encoding `author` / `modifiedBy`
// use) on attach and clears both on detach, in the same change as the
// membership op (internal/server/handlers_properties.go). Hidden.
// Clients filter members out of ordinary lists
// (`{"any.collections": {"$nin": ["bin"]}}`); a permanent delete stays
// DELETE …/objects/:id.
//
// The stamps are ordinary synced properties: the server writes them,
// but nothing stops a peer from writing them directly, so a reader
// treats an absent stamp as unknown, never as "not moved".
package bin

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// Id is reserved — content-addressable user ids never produce it,
	// and the xKey guard refuses user definitions claiming it.
	Id          = "bin"
	Name        = "Bin"
	Description = "Moved to the bin: attach to move, detach to restore"

	// PropMovedAt is when the object was moved (server clock, an
	// instant); PropMovedBy the account that moved it.
	PropMovedAt = "movedAt"
	PropMovedBy = "movedBy"
)

// NewCollection returns the handler.Collection to add to
// config.Config.Collections (see internal/server/sdk.go).
func NewCollection() handler.Collection {
	return handler.Collection{
		Id:          Id,
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
