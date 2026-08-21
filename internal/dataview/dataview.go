// Package dataview registers the built-in `data_view` type — saved
// views over a set of objects. The type attaches to ANY object,
// including a type object (a view "on a type" hosts its records there),
// and owns one dataset holding one record per view.
//
// A view is name + icon + layout + a query + column settings. The
// query and the column settings stay OPAQUE to the server: clients own
// the filter/sort/groupBy vocabulary and decide what a rule naming a
// deleted property means. Validating property references here would
// turn a deleted property into a write failure instead of a rule the
// client marks invalid.
//
// Why built-in: views are shared client vocabulary, not app data —
// any-ui, mobile and desktop must land on the SAME records or a view
// saved in one client is invisible in the next. A client-minted user
// type passes each peer's local check-then-create and merges, leaving
// a space with several parallel "Views" types (the proliferation the
// `page` type solved for documents). A registered type exists in every
// space by construction.
//
// Iteration 1 is the SHARED tier: one set of views, visible to
// everyone with space access. Account-private and device-private
// tiers need scoped DATASETS — the SDK scopes fields, and its account
// mirror covers `objects` rows only.
//
// Writes go through POST /v1/spaces/:id/modify, reads through
// /query[/subscribe] with dataset=data_views. No bespoke endpoints:
// the record shape carries no server semantics worth an endpoint.
package dataview

import "github.com/anyproto/any-sync-sdk/handler"

const (
	// TypeId is reserved — content-addressable user type ids never
	// produce it, and the server's xKey guard rejects user types
	// claiming it.
	TypeId      = "data_view"
	Name        = "Data View"
	Description = "Saved views over a set of objects: one record per view (layout + query + column settings)"

	// Dataset holds one record per saved view.
	Dataset = "data_views"

	// DataVersion is stamped on every change and gated by peers.
	DataVersion = "data_views-v1"
)

// Record field names.
const (
	FieldName   = "name"
	FieldIcon   = "icon"
	FieldPos    = "pos"
	FieldLayout = "layout"

	// FieldQuery is the /query body shape verbatim — filter / sort /
	// groupBy — plus a `type` discriminator, `"plain"` today. The
	// discriminator exists so an aggregation-backed view lands later
	// without a migration.
	FieldQuery = "query"

	// FieldLayoutSettings is the synced column state (visible / order,
	// and widths as a shared baseline).
	FieldLayoutSettings = "layoutSettings"

	// FieldLocalSettings is this device's override of layoutSettings,
	// same vocabulary. Column-drag autosave lands here so the churn
	// never syncs; the client merges local over synced. Scope is
	// per top-level field, so the override cannot live inside
	// layoutSettings.
	FieldLocalSettings = "localSettings"

	FieldCreator    = "creator"
	FieldCreatedAt  = "createdAt"
	FieldModifiedAt = "modifiedAt"
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go).
//
// Handler is nil: the declaration below is complete enough for the
// SDK's generic schema handler — required-on-create, mutability,
// apply-time stamps, user-supplied ids and the delete gate all come
// from it, with no bespoke code to keep in step.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Datasets: []handler.Dataset{{
			Name:        Dataset,
			DataVersion: DataVersion,
			Schema:      datasetSchema(),
		}},
	}
}

// datasetSchema declares the view record.
//
// Dynamic: the schema is enforced on every peer at apply time, so a
// closed keyspace would silently drop a newer client's undeclared key
// on an older peer. The declared vocabulary is the contract; dynamic is
// the forward-compat escape hatch.
//
// IdUser: the default view is a fixed record id plus upsert, never
// create-on-open — two devices opening a fresh object would otherwise
// race two "All" views. Concurrent creates of the same id by DIFFERENT
// members take arrival-order-dependent creation verdicts (the SDK's
// IdUser contract); content still converges LWW.
//
// MutableByAnyone / DeleteByAnyone: a shared view is space furniture —
// any member with write permission retunes or removes it, and readers
// are already fenced by the ACL. Author-only would freeze a departed
// member's view forever.
func datasetSchema() handler.Schema {
	// Opaque object payloads: an object shape with no declared
	// properties accepts any keys, so this pins "must be an object"
	// and nothing more.
	object := func() *handler.FieldShape { return handler.Leaf(handler.PropertyKindObject) }
	str := func() *handler.FieldShape { return handler.Leaf(handler.PropertyKindString) }

	return handler.Schema{
		Dynamic:  true,
		IdRule:   handler.IdUser,
		DeleteBy: handler.DeleteByAnyone,
		Fields: []handler.Field{
			{Id: FieldName, Name: "Name", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone},
			{Id: FieldIcon, Name: "Icon", Schema: str(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone},
			{Id: FieldPos, Name: "Position", Schema: str(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone},
			{Id: FieldLayout, Name: "Layout", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone},
			{Id: FieldQuery, Name: "Query", Schema: object(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone},
			{Id: FieldLayoutSettings, Name: "Layout Settings", Schema: object(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone},
			{Id: FieldLocalSettings, Name: "Local Settings", Schema: object(), Scope: handler.ScopeLocal, MutableBy: handler.MutableByAnyone},
			{Id: FieldCreator, Name: "Creator", Schema: str(), Stamp: handler.StampCreator},
			// Kind number matches what the SDK stamps today; a
			// time-stamped field's kind is coerced to datetime once the
			// SDK stores instants natively.
			{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindNumber), Stamp: handler.StampCreateTime},
			{Id: FieldModifiedAt, Name: "Modified At", Schema: handler.Leaf(handler.PropertyKindNumber), Stamp: handler.StampModifyTime},
		},
	}
}
