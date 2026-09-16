// Package miniapp registers the built-in `miniapp` collection: the
// marker an object carries to run an installed bundle.
//
// The sidebar is the list of members. `bundle` — the id of the
// installed bundle the object runs (the registry record id,
// docs/03-api.md § Bundles) — is what a client needs to know what to
// open; absent, the member is an ordinary object the user pinned.
// `pos` orders the sidebar (lexid, client-allocated), `hidden` takes a
// member out of it without uninstalling anything. A collection, not a
// type: an app root keeps its own type slot (a definition marker, or
// the type a pinned object has). Hidden: a capability an object opts
// into, not a class a user picks.
package miniapp

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// Id is reserved — content-addressable user ids never produce it,
	// and the xKey guard refuses user definitions claiming it.
	Id          = "miniapp"
	Name        = "Mini app"
	Description = "Runs an installed bundle: `bundle` names the install"

	// PropBundle is the installed bundle's id, e.g. `system:wiki/v1`.
	PropBundle = "bundle"
	// PropPos is the carrier's sidebar position (lexid string).
	PropPos = "pos"
	// PropHidden takes the carrier out of the sidebar.
	PropHidden = "hidden"
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
			{Id: PropBundle, Name: "Bundle", Kind: handler.PropertyKindString,
				Description: "Id of the installed bundle this object is the app of; absent on an object the user pinned."},
			{Id: PropPos, Name: "Position", Kind: handler.PropertyKindString,
				Description: "Sidebar position, a lexid string allocated by the client."},
			{Id: PropHidden, Name: "Hidden", Kind: handler.PropertyKindBoolean, XFormat: map[string]any{"type": "checkbox"},
				Description: "True hides the app from the sidebar without uninstalling it."},
		},
	}
}
