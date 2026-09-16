// Package dataview registers the built-in `dataview` type — saved views
// over a set of objects, in two levels: a dataview object hosts many
// DATAVIEWS, each with its own VIEWS.
//
// A dataview object is an object of this type (its one type), created
// for the object it serves: `host` names that object — any object, a
// type or a collection definition included (a view "on a type" is a
// dataview object whose host is the type). The type owns two records
// datasets under one part: `dataviews` (one record per dataview — name,
// icon, pos) and `views` (one record per view — the dataview it belongs
// to, name, icon, pos, layout, a query, column settings). The added
// level is what lets one host carry several independent tables, each
// with its own set of views.
//
// A view is name + icon + layout + a query + column settings. The query
// and the column settings stay OPAQUE to the server: clients own the
// filter/sort/groupBy vocabulary and decide what a rule naming a
// deleted property means. Validating property references here would
// turn a deleted property into a write failure instead of a rule the
// client marks invalid. The same holds one level up: a view's
// `dataview` names a record in `dataviews` and is not validated against
// it — a dataview deleted under its views must not make them
// unwritable. There is no cascade; orphan views are the client's to
// delete or re-parent.
//
// Why built-in: views are shared client vocabulary, not app data —
// any-ui, mobile and desktop must land on the SAME records or a view
// saved in one client is invisible in the next. A client-minted user
// type passes each peer's local check-then-create and merges, leaving
// a space with several parallel "Views" types. A registered type exists
// in every space by construction. Hidden: a capability an object opts
// into, not a class a user picks.
//
// Iteration 1 is the SHARED tier: one set of dataviews and views,
// visible to everyone with space access. Account-private and
// device-private tiers need scoped DATASETS — the SDK scopes fields,
// and its account mirror covers `objects` rows only.
//
// Writes go through POST /v1/spaces/:id/modify, reads through
// /query[/subscribe] with dataset=dataviews or views. No bespoke
// endpoints — the record shapes carry no server semantics worth one —
// and no `dataview` module: both datasets run on the generic `records`
// handler.
package dataview

import (
	anystore "github.com/anyproto/any-store/v2"

	"github.com/anyproto/any-sync-sdk/handler"
)

const (
	// TypeId is reserved — content-addressable user type ids never
	// produce it, and the server's xKey guard rejects user types
	// claiming it.
	TypeId      = "dataview"
	Name        = "Data view"
	Description = "Saved views over a set of objects: dataviews for a host, each with its own views (layout + query + column settings)"

	// PropHost is the object the dataview object serves — the object,
	// type or collection its views are over.
	PropHost = "host"

	// PartViews is the type's single part; it owns both datasets.
	PartViews = "views"

	// DatasetDataviews holds one record per dataview on the host.
	DatasetDataviews = "dataviews"
	// DatasetViews holds one record per view; `dataview` names the
	// dataviews record it belongs to.
	DatasetViews = "views"

	// DataVersions are stamped on every change and gated by peers.
	DataVersionDataviews = "dataviews-v1"
	DataVersionViews     = "views-v1"
)

// Record field names shared by both datasets.
const (
	FieldName = "name"
	FieldIcon = "icon"
	FieldPos  = "pos"

	FieldCreator    = "creator"
	FieldCreatedAt  = "createdAt"
	FieldModifiedAt = "modifiedAt"
)

// View record fields (dataset `views`).
const (
	// FieldDataview is the id of the `dataviews` record the view belongs
	// to. Required; free to rewrite (a view moves between dataviews);
	// never validated against the collection.
	FieldDataview = "dataview"

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
)

// NewType returns the handler.Type to add to config.Config.Types (see
// internal/server/sdk.go).
//
// Handlers are nil: the declarations below are complete enough for the
// SDK's generic schema handler — required-on-create, mutability,
// apply-time stamps, user-supplied ids and the delete gate all come
// from them, with no bespoke code to keep in step.
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        Name,
		Description: Description,
		Hidden:      true,
		Properties: []handler.PropertyDecl{
			{Id: PropHost, Name: "Host", Kind: handler.PropertyKindString,
				Description: "Id of the object, type or collection the views are over."},
		},
		Datasets: []handler.Dataset{
			{
				Name:        DatasetDataviews,
				DataVersion: DataVersionDataviews,
				Schema:      dataviewsSchema(),
				// Dataviews are read in pos order.
				Indexes: []anystore.IndexInfo{{Name: "idx_pos", Fields: []string{FieldPos}}},
			},
			{
				Name:        DatasetViews,
				DataVersion: DataVersionViews,
				Schema:      viewsSchema(),
				// The documented read is one dataview's views in pos
				// order; the plain pos index serves "every view on the
				// host".
				Indexes: []anystore.IndexInfo{
					{Name: "idx_dataview_pos", Fields: []string{FieldDataview, FieldPos}},
					{Name: "idx_pos", Fields: []string{FieldPos}},
				},
			},
		},
		Parts: []handler.Part{{
			Key:      PartViews,
			Name:     "Views",
			UI:       map[string]any{"type": "table"},
			Datasets: []handler.PartDataset{{Name: DatasetDataviews}, {Name: DatasetViews}},
		}},
	}
}

// stamps are the server-derived creator / time fields both datasets
// carry; client writes to them are rejected.
func stamps() []handler.Field {
	return []handler.Field{
		{Id: FieldCreator, Name: "Creator", Schema: handler.Leaf(handler.PropertyKindString), Stamp: handler.StampCreator,
			Description: "Account identity that created the record; derived."},
		// Instants, not numbers: `{"$date": "<RFC 3339>"}` on the wire,
		// memcmp-orderable and index-keyable in the store.
		{Id: FieldCreatedAt, Name: "Created At", Schema: handler.Leaf(handler.PropertyKindDatetime), Stamp: handler.StampCreateTime,
			Description: "Instant the record was created (author's clock); derived.", XFormat: map[string]any{"type": "datetime"}},
		{Id: FieldModifiedAt, Name: "Modified At", Schema: handler.Leaf(handler.PropertyKindDatetime), Stamp: handler.StampModifyTime,
			Description: "Instant of the last edit (author's clock); derived.", XFormat: map[string]any{"type": "datetime"}},
	}
}

func str() *handler.FieldShape { return handler.Leaf(handler.PropertyKindString) }

// object pins "must be an object" and nothing more: an object shape
// with no declared properties accepts any keys.
func object() *handler.FieldShape { return handler.Leaf(handler.PropertyKindObject) }

// Declaration rules shared by dataviewsSchema and viewsSchema.
//
// Dynamic: the schema is enforced on every peer at apply time, so a
// closed keyspace would silently drop a newer client's undeclared key
// on an older peer. The declared vocabulary is the contract; dynamic is
// the forward-compat escape hatch.
//
// IdUser: the default dataview and the default view are fixed record
// ids plus upsert, never create-on-open — two devices opening a fresh
// object would otherwise race two "All" views. Concurrent creates of
// the same id by DIFFERENT members take arrival-order-dependent
// creation verdicts (the SDK's IdUser contract); content still
// converges LWW.
//
// MutableByAnyone / DeleteByAnyone: a shared view is space furniture —
// any member with write permission retunes or removes it, and readers
// are already fenced by the ACL. Author-only would freeze a departed
// member's view forever.

// dataviewsSchema declares the dataview record: a named, ordered table
// on the host.
func dataviewsSchema() handler.Schema {
	return handler.Schema{
		Dynamic:  true,
		IdRule:   handler.IdUser,
		DeleteBy: handler.DeleteByAnyone,
		Fields: append([]handler.Field{
			{Id: FieldName, Name: "Name", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Display name of the table.", XFormat: map[string]any{"type": "text"}},
			{Id: FieldIcon, Name: "Icon", Schema: str(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone,
				Description: "Display icon; the encoding is the client's."},
			// Required: dataviews are read in `pos` order, and an absent
			// pos sorts as "" — ahead of every positioned one, on every
			// peer. A loud create failure beats silently pinning a
			// record to the top of everyone's list.
			{Id: FieldPos, Name: "Position", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Lexid ordering key among the host's tables."},
		}, stamps()...),
	}
}

// viewsSchema declares the view record.
func viewsSchema() handler.Schema {
	return handler.Schema{
		Dynamic:  true,
		IdRule:   handler.IdUser,
		DeleteBy: handler.DeleteByAnyone,
		Fields: append([]handler.Field{
			{Id: FieldDataview, Name: "Dataview", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Id of the table this view belongs to; not validated."},
			{Id: FieldName, Name: "Name", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Display name of the view.", XFormat: map[string]any{"type": "text"}},
			{Id: FieldIcon, Name: "Icon", Schema: str(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone,
				Description: "Display icon; the encoding is the client's."},
			{Id: FieldPos, Name: "Position", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Lexid ordering key among the table's views."},
			{Id: FieldLayout, Name: "Layout", Schema: str(), Scope: handler.ScopeSynced, Required: true, MutableBy: handler.MutableByAnyone,
				Description: "Rendering layout slug: table, board, …"},
			{Id: FieldQuery, Name: "Query", Schema: object(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone,
				Description: "Opaque view query: type, filter / sort in the /query body shapes, client-side groupBy."},
			{Id: FieldLayoutSettings, Name: "Layout Settings", Schema: object(), Scope: handler.ScopeSynced, MutableBy: handler.MutableByAnyone,
				Description: "Opaque per-layout settings shared by every member."},
			{Id: FieldLocalSettings, Name: "Local Settings", Schema: object(), Scope: handler.ScopeLocal, MutableBy: handler.MutableByAnyone,
				Description: "Device-local overrides of layoutSettings; local wins."},
		}, stamps()...),
	}
}
