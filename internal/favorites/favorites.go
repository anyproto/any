// Package favorites declares the built-in account-level favourites
// bundle: a cross-space tree of starred objects and folders, private to
// the account, living on the tech space.
//
// The server owns the DECLARATION only — the bundle id and the
// `entries` dataset schema below — and ensures the bundle at engine
// boot (see server.ensureBuiltinBundles). It ships no endpoints and no
// handler code: the tech bundle root is its own type, the SDK's generic
// schema handler enforces the declaration on every peer, writes ride
// POST /v1/spaces/<techSpaceId>/modify | /upsert, reads
// /query[/subscribe]. Built-in for the same reason as `page` and the
// general-chat convention: shared client vocabulary — any-ui, mobile
// and third-party apps must land on the SAME records, and each of them
// re-declaring the schema would drift. Discovery is server-side:
// GET /v1/spaces/<techSpaceId>/datasets (typeId = the bundle root id).
//
// Record model — one dataset, one record per entry, ids deterministic
// from identity content:
//
//   - item: id IS the canonical `any://o/…` link (anyuri) of the
//     starred target, so star is an idempotent upsert, "is starred" a
//     point lookup, and concurrent stars on two devices the same
//     record. The pattern admits record links (a block, a chat
//     message) so those become star-able without a schema change.
//   - folder: id is `f:` + a client-minted token. The id prefix is the
//     kind discriminator — there is no `type` field.
//
// Soft delete only: un-star, folder delete and cleanup are
// `$set removed: true`; re-star / re-create is an upsert clearing it.
// CRDT tombstones are sticky, so a hard delete would ban that link or
// folder id forever — and a client cannot reliably tell a deleted
// target from one this device has not synced. `name` / `iconCid` /
// `types` on an item are a client-written MIRROR of the target's `any`
// fields (refreshed on observed difference) so entries render from the
// favourites subscribe alone — offline, and on devices that never
// loaded the target's space. Tree semantics (orphans, cycles,
// removed-folder children) are resolved read-side by clients; the
// server enforces shape, not policy. Client contract: docs/03-api.md
// § Bundles (Built-in account bundles) and docs/08-clients.md.
package favorites

import (
	"github.com/anyproto/any-sync-sdk/space"
)

const (
	// BundleId is the registry id on the tech space — permanent and
	// versioned; a breaking schema change ships as favorites/v2.
	BundleId    = "favorites/v1"
	DisplayName = "Favorites"

	// Dataset holds one record per entry (item or folder).
	Dataset = "entries"

	// FolderIdPrefix + a client-minted token forms a folder id; item
	// ids are canonical any://o/ links. IdPattern admits exactly the
	// two forms.
	FolderIdPrefix = "f:"
	IdPattern      = `^(any://o/.+|f:[A-Za-z0-9_-]{1,64})$`
	IdMaxLen       = 256
)

// Record field names.
const (
	// FieldParentId is "" for a top-level entry, else a folder's
	// record id.
	FieldParentId = "parentId"
	// FieldPos orders siblings inside a folder — a lexid, same
	// allocator parameters as nav (CharsAllNoEscape, 4, 100).
	FieldPos = "pos"
	// FieldRemoved true = soft-deleted (un-starred / folder removed);
	// absent or false = live. Never hard-delete: tombstones are sticky
	// and would ban the id forever.
	FieldRemoved = "removed"
	// FieldName: folder display name | mirrored target any.name.
	FieldName = "name"
	// FieldIconCid: mirrored target any.iconCid (folders: optional).
	FieldIconCid = "iconCid"
	// FieldTypes: mirrored target any.types (type-default icon
	// fallback).
	FieldTypes = "types"

	FieldCreator    = "creator"
	FieldCreatedAt  = "createdAt"
	FieldModifiedAt = "modifiedAt"
)

// Datasets returns the bundle's dataset declarations for
// bundles.Install. Dynamic keeps undeclared keys permitted (a newer
// client's field must not be dropped by an older peer); IdUser makes
// the id the idempotency key; DeleteByAnyone leaves hard delete
// possible for an explicit future cleanup mechanism while clients
// soft-delete by contract; MutableByAnyone throughout because every
// writer is the same account.
func Datasets() []space.DatasetDraft {
	str := func() space.PropertyKind { return space.PropertyKindString }
	return []space.DatasetDraft{{
		Name:        Dataset,
		DisplayName: "Entries",
		Description: "Favourites tree: one record per starred object (id = any://o/ link) or folder (id = f:<token>)",
		Dynamic:     true,
		IdRule:      space.IdUser,
		IdPattern:   IdPattern,
		IdMaxLen:    IdMaxLen,
		DeleteBy:    space.DeleteByAnyone,
		Fields: []space.DatasetFieldDraft{
			{Key: FieldParentId, Name: "Parent", Kind: str(), Required: true, MutableBy: space.MutableByAnyone},
			// Required: entries are read in pos order, and an absent
			// pos sorts as "" — ahead of every positioned sibling on
			// every device. A loud create failure beats a silently
			// pinned row.
			{Key: FieldPos, Name: "Position", Kind: str(), Required: true, MutableBy: space.MutableByAnyone},
			{Key: FieldRemoved, Name: "Removed", Kind: space.PropertyKindBoolean, MutableBy: space.MutableByAnyone},
			{Key: FieldName, Name: "Name", Kind: str(), MutableBy: space.MutableByAnyone},
			{Key: FieldIconCid, Name: "Icon", Kind: str(), MutableBy: space.MutableByAnyone},
			{Key: FieldTypes, Name: "Types", Kind: space.PropertyKindArray, MutableBy: space.MutableByAnyone},
			{Key: FieldCreator, Name: "Creator", Stamp: space.StampCreator},
			{Key: FieldCreatedAt, Name: "Created At", Stamp: space.StampCreateTime},
			{Key: FieldModifiedAt, Name: "Modified At", Stamp: space.StampModifyTime},
		},
	}}
}
