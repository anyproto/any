// Package page defines the built-in `page` type — the marker for "this
// object is a document".
//
// page is a pure declaration: no dataset, no properties. Everything a
// page needs already lives in other namespaces — display name and
// description on `any`, the block body on the `editor` type's
// `editor_blocks` dataset (attached on first block write via
// editor.EnsureType), tree position on `nav`, and recency on the
// derived row-root `modifiedAt`. Its entire footprint is the "page"
// entry in an object's `any.types`, which gives clients one shared,
// always-present type to file documents under.
//
// Why built-in: without it every client check-then-creates its own
// user "pages" type, and concurrent creates on different nodes each
// pass the local xKey guard then merge — real spaces accumulated up to
// five parallel "Pages" types this way (the same proliferation the
// deterministic general chat object solved for chats). A registered
// type exists in every space by construction: nothing to create,
// nothing to race, nothing to sync.
//
// Why no properties: the SDK freezes registered types' property
// definitions (AddProperty / PatchProperty / RemoveProperty return
// ErrTypeRegistered), so a built-in select/multiselect would carry a
// permanently empty, uneditable option set. Per-space columns stay a
// user-type concern until the SDK grows mutable metadata for
// registered types.
package page

import "github.com/anyproto/any-sync-sdk/handler"

// TypeId is the literal id objects carry in `any.types`. Singular by
// contract — the type marks one object as a page; it is not a
// container of pages. Reserved — user-derived type ids are
// content-addressable and never produce this string, and the server's
// xKey guard rejects user types claiming "page".
const TypeId = "page"

// Display metadata surfaced via Types().List / Get.
const (
	Name        = "Page"
	Description = "Document — name via any, block body via editor, tree position via nav"
)

// NewType returns the handler.Type registration for page: a marker
// type with no datasets and no properties. Add it to
// config.Config.Types alongside the dataset-owning types.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
	}
}
