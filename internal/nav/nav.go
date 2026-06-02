// Package nav defines the virtual built-in `nav` type used to organise
// objects into a folder/item tree.
//
// Storage is plain property values on the per-space `objects` collection
// — no dedicated dataset. nav registers with the SDK as a property-only
// type (NewType, no dataset handlers) so write-time schema validation
// resolves and enforces nav.* values. Every object's row carries a
// `nav` namespace alongside `any` and any user types:
//
//	{
//	  "id":  "obj_abc",
//	  "any": { "name": "...", "types": ["nav", ...] },
//	  "nav": {
//	    "type":     1,        // 1 = item, 2 = folder
//	    "parentId": "obj_root_or_other",
//	    "pos":      "lexid"   // sorts siblings inside a folder
//	  }
//	}
//
// The `nav` type is injected at the server boundary — Types().List
// surfaces it and POST /v1/spaces/:spaceId/objects auto-stamps a default
// nav record on every new object. Tree views are pure queries against
// the existing /v1/spaces/:spaceId/objects/query endpoint.
package nav

import (
	"fmt"

	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/lexid"

	"github.com/anyproto/any/internal/api"
)

// TypeId is the literal namespace key used in property paths
// ("nav.type", "nav.parentId", "nav.pos") and in any.types when nav is
// listed alongside user types. Reserved — user-derived type ids are
// content-addressable and never produce this string.
const TypeId = "nav"

// Display metadata used by the typeList/typeGet handlers when they
// inject the virtual nav type into Types() responses.
const (
	Name        = "Nav"
	Description = "Position in the folder tree (built-in)"
)

// Property keys on the nav namespace. These are literal strings — no
// content-addressable propIds — because nav is a virtual built-in
// (analogous to the SDK's `any` type, which uses "name", "description"
// etc. directly).
const (
	PropType     = "type"
	PropParentId = "parentId"
	PropPos      = "pos"
)

// NavType discriminates folder rows from item rows. Stored as a number
// so filtering by `nav.type == 2` (find folders) is efficient and
// language-agnostic. Two values is enough — the type system is closed.
const (
	TypeItem   = 1
	TypeFolder = 2
)

// RootParentId is the sentinel for "no parent — top-level row". Empty
// string keeps queries simple: `{"nav.parentId": ""}` lists the root.
const RootParentId = ""

// lexidGen mirrors the parameters used by blocks.lexids and by the
// SDK's any-sync tree allocator (CharsAllNoEscape, blockSize=4,
// stepSize=100). Same shape anytype-heart's storestate uses for its
// per-doc block ordering — keeps allocator alphabets consistent.
var lexidGen = lexid.Must(lexid.CharsAllNoEscape, 4, 100)

// NextPos returns a lexid that sorts strictly after `prev`. Pass an
// empty string when the folder is empty — you'll get Middle(), which
// leaves headroom on both sides for later inserts. Otherwise returns
// Next(prev), so passing the folder's current max appends a new row
// at the tail. Mirrors anytype-heart's pattern (Middle on empty,
// Next(prev) thereafter).
func NextPos(prev string) string {
	if prev == "" {
		return lexidGen.Middle()
	}
	return lexidGen.Next(prev)
}

// PrevPos returns a lexid that sorts strictly before `next`. Empty
// `next` means "the folder is empty" → Middle(). Use this to prepend
// a row at the head of a folder.
func PrevPos(next string) string {
	if next == "" {
		return lexidGen.Middle()
	}
	return lexidGen.Prev(next)
}

// PosBetween allocates a lexid strictly between `prev` and `next`.
// Either may be empty: empty `prev` means "before everything else
// known", empty `next` means "after everything else known". Used by
// the UI when the user drags a row to a specific position — the
// caller supplies the bounding siblings.
func PosBetween(prev, next string) (string, error) {
	switch {
	case prev == "" && next == "":
		return lexidGen.Middle(), nil
	case next == "":
		return lexidGen.Next(prev), nil
	case prev == "":
		return lexidGen.Prev(next), nil
	default:
		id, err := lexidGen.NextBefore(prev, next)
		if err != nil {
			return "", fmt.Errorf("nav: PosBetween(prev=%q, next=%q): %w", prev, next, err)
		}
		return id, nil
	}
}

// TypeInfo returns the wire-shape entry for the virtual nav type. Used
// by handlers_types.go to inject nav into TypesListResponse and
// TypeGet.
func TypeInfo() api.TypeInfo {
	return api.TypeInfo{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		BuiltIn:     true,
	}
}

// PropertyDefs returns the hardcoded property defs for the nav type.
// Used by handlers_types.go on GET /v1/spaces/:spaceId/types/nav/properties
// so the UI can render an editor for nav fields uniformly with user
// types and the `any` built-in.
func PropertyDefs() []api.PropertyDef {
	return []api.PropertyDef{
		{Id: PropType, Name: "Type", Kind: api.PropertyKindNumber},
		{Id: PropParentId, Name: "Parent", Kind: api.PropertyKindString},
		{Id: PropPos, Name: "Position", Kind: api.PropertyKindString},
	}
}

// NewType returns the SDK handler.Type registration for nav: a
// property-only type with no dataset handlers — its entire footprint is
// the three values it contributes to the shared `objects` namespace.
// Registering it lets the SDK resolve nav's schema and enforce nav.*
// writes (kind + known-property) instead of rejecting them as an
// unknown type. Add it to config.Config.Types alongside the
// dataset-owning types.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Properties: []handler.PropertyDecl{
			{Id: PropType, Name: "Type", Kind: handler.PropertyKindNumber},
			{Id: PropParentId, Name: "Parent", Kind: handler.PropertyKindString},
			{Id: PropPos, Name: "Position", Kind: handler.PropertyKindString},
		},
	}
}
