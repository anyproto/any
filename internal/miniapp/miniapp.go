// Package miniapp registers the built-in `mini_app` type: a small embeddable
// HTML/JS app whose source, runtime state, and readme live in a structured
// dataset record rather than being parsed out of markdown blocks.
//
// Mirrors internal/program: one built-in type, raw DefaultHandler datasets,
// per-path $set/$unset writes via POST /v1/spaces/:id/modify and reads via
// POST /v1/spaces/:id/query with dataset=mini_app. The single record per
// object uses id "main" (same convention as program_source).
//
// Record shape (dataset `mini_app`, record "main"):
//
//	{ "source": "<full HTML>", "state": "<json text|''>", "readme": "<markdown>" }
//
// The display name lives on the object itself (any.name); there is no separate
// name property. Source/state are edited independently via atomic per-path
// $set, so updating state never rewrites source.
package miniapp

import "github.com/anyproto/any-sync-sdk/handler"

const (
	TypeId      = "mini_app"
	Name        = "Mini App"
	Description = "Embeddable HTML/JS mini app with source, state, and readme"

	// Dataset is the per-object dataset holding the single "main" record.
	Dataset = "mini_app"

	// Record is the conventional single record id (one mini app per object).
	Record = "main"

	// Field keys inside the record.
	FieldSource = "source"
	FieldState  = "state"
	FieldReadme = "readme"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go). DefaultHandler stores raw values with no
// transformation — source HTML and state JSON round-trip byte-for-byte.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{
			{Name: Dataset, DataVersion: "1", Handler: handler.DefaultHandler{}},
		},
	}
}
