// Package miniapp registers the built-in `miniapp` type: the marker an
// object carries to run an installed bundle.
//
// The sidebar is the list of carriers. `bundle` — the id of the
// installed bundle the object runs (the registry record id,
// docs/03-api.md § Bundles) — is what a client needs to know what to
// open; absent, the carrier is an ordinary object the user pinned.
// `pos` orders the sidebar (lexid, client-allocated), `hidden` takes a
// carrier out of it without uninstalling anything. No parts. Hidden: a
// capability an object opts into, not a class a user picks.
package miniapp

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// TypeId is reserved — content-addressable user type ids never
	// produce it, and the xKey guard refuses user types claiming it.
	TypeId      = "miniapp"
	Name        = "Mini app"
	Description = "Runs an installed bundle: `bundle` names the install"

	// PropBundle is the installed bundle's id, e.g. `system:wiki/v1`.
	PropBundle = "bundle"
	// PropPos is the carrier's sidebar position (lexid string).
	PropPos = "pos"
	// PropHidden takes the carrier out of the sidebar.
	PropHidden = "hidden"
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
			{Id: PropBundle, Name: "Bundle", Kind: handler.PropertyKindString,
				Description: "Id of the installed bundle this object is the app of; absent on an object the user pinned."},
			{Id: PropPos, Name: "Position", Kind: handler.PropertyKindString,
				Description: "Sidebar position, a lexid string allocated by the client."},
			{Id: PropHidden, Name: "Hidden", Kind: handler.PropertyKindBoolean, XFormat: map[string]any{"type": "checkbox"},
				Description: "True hides the app from the sidebar without uninstalling it."},
		},
	}
}
