// Package miniapp registers the built-in `miniapp` type: the marker an
// object carries to run an installed bundle.
//
// One property, `bundle` — the id of the installed bundle the object
// runs (the registry record id, docs/03-api.md § Bundles) — which is
// what a client needs to know what to open. No parts and nothing else
// in the first iteration. Hidden: a capability an object opts into, not
// a class a user picks.
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
			{Id: PropBundle, Name: "Bundle", Kind: handler.PropertyKindString},
		},
	}
}
