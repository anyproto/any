package index

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"
)

// DatasetSchemaVirtual is the SchemaChunker's VIRTUAL Dataset() name.
// Never used in doc ids — every entry carries its real runtime dataset
// name — but it must stay unique among chunkers, and (like DatasetProp)
// reserved against user dataset names so runtime doc ids can't collide
// with a chunker's namespace.
const DatasetSchemaVirtual = "schema"

// SchemaChunker indexes records of RUNTIME-DEFINED datasets by their
// schema's x-search {title, text, scope} mapping: one registered
// chunker covers every dataset any type in the space declares with a
// search annotation. Entries carry the real dataset name (doc ids
// objectId:<dataset>:<recordId>) under the declared scope — absent
// defaults to "basic" (runtime dataset records are user content on
// par with editor blocks), an invalid slug makes the dataset
// unsearchable (a broken override must not silently land in the
// default scope — the resolveIndexedProp stance).
//
// Unlike PropChunker there is NO catalog TTL cache: Space.Datasets()
// is an atomic in-memory snapshot the SDK refreshes synchronously when
// a definitions change applies, so a fresh read is never stale relative
// to the applySeq window being processed — and a cached one could skip
// records in the primary define-then-write flow while the cursor
// advances past them.
//
// It implements DynamicChunker: per object, datasets whose owning type
// is not attached are structurally evicted by the worker (the
// DetachType path), and dataset names that vanish from the catalog
// (definition removed) are remembered for the process lifetime and
// evicted the same way as objects get dirty. Restart drops the retired
// set — stale docs of a removed definition survive until a boot-time
// sweep exists (docs/13-index.md § Removal semantics).
type SchemaChunker struct {
	mu   sync.Mutex
	skip map[string]bool // static + virtual dataset names, never treated as runtime
	// known / retired: per space, dataset names seen in the catalog and
	// names that later vanished from it (definition removed).
	known   map[string]map[string]bool
	retired map[string]map[string]bool
	// pending: per space, the catalog resolved by the last EvictDatasets,
	// handed to the paired ChunksSince (the worker calls them
	// back-to-back per object) so one advance step costs one resolve,
	// not two. One-shot: ChunksSince consumes it; an unpaired call
	// resolves fresh. Safe because each space has a single advance
	// goroutine.
	pending map[string]*schemaCatalog
}

// schemaCatalog is one resolved catalog snapshot.
type schemaCatalog struct {
	searchable []schemaDataset
	// unsearchable: runtime names in the catalog WITHOUT a usable
	// x-search mapping. Always evicted — covers a cleared/removed
	// x-search annotation (previously indexed docs would otherwise go
	// stale forever); for the never-searchable majority the prefix
	// delete is an idempotent single seek.
	unsearchable []string
}

// schemaDataset is one resolved runtime dataset with a search mapping.
// textFields holds the mapped text keys in declaration order (the
// `text` leaf is a bare field key or an array of keys).
type schemaDataset struct {
	name       string
	typeId     string
	titleField string
	textFields []string
	scope      string
}

// NewSchemaChunker constructs the chunker. staticDatasets are the
// compiled-in dataset names (indexed or not) to never treat as runtime;
// the virtual chunker names are always skipped on top of them.
func NewSchemaChunker(staticDatasets ...string) *SchemaChunker {
	skip := map[string]bool{DatasetProp: true, DatasetSchemaVirtual: true}
	for _, name := range staticDatasets {
		skip[name] = true
	}
	return &SchemaChunker{
		skip:    skip,
		known:   map[string]map[string]bool{},
		retired: map[string]map[string]bool{},
		pending: map[string]*schemaCatalog{},
	}
}

func (c *SchemaChunker) Dataset() string { return DatasetSchemaVirtual }
func (c *SchemaChunker) TypeId() string  { return "" } // self-gated per dataset

// resolve returns the space's current catalog and, as a side effect,
// folds catalog disappearances into the retired set.
func (c *SchemaChunker) resolve(sp space.Space) *schemaCatalog {
	searchable, unsearchable := parseSchemaDatasets(sp.Datasets(), c.skip)
	allNames := make([]string, 0, len(searchable)+len(unsearchable))
	for _, ds := range searchable {
		allNames = append(allNames, ds.name)
	}
	allNames = append(allNames, unsearchable...)
	c.trackRetired(sp.Id(), allNames)
	return &schemaCatalog{searchable: searchable, unsearchable: unsearchable}
}

// parseSchemaDatasets splits a catalog snapshot into the searchable
// runtime datasets (x-search declared) and the unsearchable runtime
// name set. Runtime entries are the type-owned names outside the
// static skip set; only searchable ones are streamed, but every name is
// tracked for retirement and unsearchable ones are always evicted (see
// schemaCatalog).
func parseSchemaDatasets(list []space.DatasetSchema, skip map[string]bool) (searchable []schemaDataset, unsearchable []string) {
	for _, ds := range list {
		if ds.TypeId == "" || skip[ds.Name] || strings.Contains(ds.Name, ":") {
			continue
		}
		var doc struct {
			Search struct {
				Title string          `json:"title"`
				Text  json.RawMessage `json:"text"`
				Scope string          `json:"scope"`
			} `json:"x-search"`
		}
		if err := json.Unmarshal(ds.JSONSchema, &doc); err != nil {
			// Malformed schema doc — not indexed, evicted unconditionally.
			unsearchable = append(unsearchable, ds.Name)
			continue
		}
		textFields, ok := parseSearchTextFields(doc.Search.Text)
		if !ok || (doc.Search.Title == "" && len(textFields) == 0) {
			// Malformed text mapping or no search annotation — same
			// stance as a malformed doc.
			unsearchable = append(unsearchable, ds.Name)
			continue
		}
		scope := doc.Search.Scope
		if scope == "" {
			scope = ScopeBasic
		} else if !ValidScope(scope) {
			// A broken scope override must not silently land in the
			// default scope (the resolveIndexedProp stance).
			unsearchable = append(unsearchable, ds.Name)
			continue
		}
		searchable = append(searchable, schemaDataset{
			name:       ds.Name,
			typeId:     ds.TypeId,
			titleField: doc.Search.Title,
			textFields: textFields,
			scope:      scope,
		})
	}
	return searchable, unsearchable
}

// parseSearchTextFields decodes the discovery doc's `x-search.text`
// wire forms: absent/empty-string → no text mapping, a bare string →
// one key, an array of strings → the keys in order. Anything else
// (wrong JSON type, non-string elements) reports !ok — the dataset is
// treated like a malformed doc. Empty keys inside a valid array cannot
// appear in a validated declaration and are dropped defensively.
func parseSearchTextFields(raw json.RawMessage) (fields []string, ok bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, true
		}
		return []string{s}, true
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, false
	}
	for _, k := range keys {
		if k != "" {
			fields = append(fields, k)
		}
	}
	return fields, true
}

// trackRetired diffs the current runtime name set against everything
// seen for the space: vanished names accumulate as retired (definition
// removed), re-appearing names un-retire.
func (c *SchemaChunker) trackRetired(spaceId string, allNames []string) {
	current := make(map[string]bool, len(allNames))
	for _, name := range allNames {
		current[name] = true
	}
	c.mu.Lock()
	known := c.known[spaceId]
	if known == nil {
		known = map[string]bool{}
		c.known[spaceId] = known
	}
	for name := range known {
		if !current[name] {
			if c.retired[spaceId] == nil {
				c.retired[spaceId] = map[string]bool{}
			}
			c.retired[spaceId][name] = true
		}
	}
	for name := range current {
		known[name] = true
		delete(c.retired[spaceId], name)
	}
	c.mu.Unlock()
}

// EvictDatasets implements DynamicChunker: searchable catalog datasets
// whose owning type is not attached to this object, every unsearchable
// runtime name (x-search absent or cleared), and every retired name.
// The resolved catalog is stashed for the paired ChunksSince.
func (c *SchemaChunker) EvictDatasets(ctx context.Context, sp space.Space, attached map[string]bool) ([]string, error) {
	cat := c.resolve(sp)
	var out []string
	for _, ds := range cat.searchable {
		if !attached[ds.typeId] {
			out = append(out, ds.name)
		}
	}
	out = append(out, cat.unsearchable...)
	spaceId := sp.Id()
	c.mu.Lock()
	for name := range c.retired[spaceId] {
		out = append(out, name)
	}
	c.pending[spaceId] = cat
	c.mu.Unlock()
	return out, nil
}

// ChunksSince streams the object's runtime-dataset entries past the
// cursor: per active dataset (owning type attached), every record with
// ApplySeq > since maps x-search.title → Title and title + the joined
// text fields → Data.
// Tombstones and records whose mapped fields render empty yield removal
// entries (Data == ""). Datasets the worker just evicted (type not
// attached) are never streamed in the same page.
func (c *SchemaChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error {
	// Consume the catalog the paired EvictDatasets resolved (one-shot);
	// an unpaired call resolves fresh.
	c.mu.Lock()
	cat := c.pending[sp.Id()]
	delete(c.pending, sp.Id())
	c.mu.Unlock()
	if cat == nil {
		cat = c.resolve(sp)
	}
	dss := cat.searchable
	if len(dss) == 0 {
		return nil
	}
	row, err := objectRow(ctx, sp, objectId)
	if err != nil {
		return err
	}
	if row == nil || IsDeleted(row) {
		return nil // structural eviction is the indexer's job
	}
	attached := map[string]bool{}
	for _, v := range row.GetArray("any", "types") {
		attached[string(v.GetStringBytes())] = true
	}
	for _, ds := range dss {
		if !attached[ds.typeId] {
			continue // evicted by the worker via EvictDatasets
		}
		err := RecordsSince(ctx, sp.Query(objectId, ds.name), since, func(rec *anyenc.Value, seq uint64) error {
			e := IndexEntry{
				Scope:    ds.scope,
				ObjectId: objectId,
				Dataset:  ds.name,
				RecordId: string(rec.GetStringBytes("id")),
				ApplySeq: seq,
			}
			if !IsDeleted(rec) {
				title := renderSearchValue(fieldValue(rec, ds.titleField))
				text := renderTextFields(rec, ds.textFields)
				e.Title = title
				e.Data = joinTitleText(title, text)
			}
			return yield(e)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// objectRow reads the object's shared-collection row, tombstones
// included (nil when the row doesn't exist at all).
func objectRow(ctx context.Context, sp space.Space, objectId string) (*anyenc.Value, error) {
	it, err := sp.QueryObjects().
		Filter(query.Key{Path: []string{"id"}, Filter: query.NewComp(query.CompOpEq, objectId)}).
		Projection(space.ProjectionOpts{IncludeDeleted: true}).
		Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	if !it.Next() {
		return nil, it.Err()
	}
	return it.Doc()
}

// fieldValue resolves a mapped field on the record; an empty mapping
// resolves to nothing.
func fieldValue(rec *anyenc.Value, field string) *anyenc.Value {
	if field == "" {
		return nil
	}
	return rec.Get(field)
}

// renderSearchValue turns a mapped field value into indexable text by
// its ACTUAL type — the SDK does not validate x-search fields against
// declared kinds (dynamic datasets may map undeclared fields): strings
// as-is, numbers canonical, arrays a newline join of string/number
// elements, anything else empty.
func renderSearchValue(v *anyenc.Value) string {
	if v == nil {
		return ""
	}
	switch v.Type() {
	case anyenc.TypeString:
		return string(v.GetStringBytes())
	case anyenc.TypeNumber:
		return renderNumber(v.GetFloat64())
	case anyenc.TypeArray:
		var parts []string
		for _, el := range v.GetArray() {
			switch el.Type() {
			case anyenc.TypeString:
				parts = append(parts, string(el.GetStringBytes()))
			case anyenc.TypeNumber:
				parts = append(parts, renderNumber(el.GetFloat64()))
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// renderTextFields renders each mapped text field and joins the
// non-empty values with a blank line in mapping order — one body per
// record. A missing/empty field contributes nothing, same as an empty
// single-field `text`.
func renderTextFields(rec *anyenc.Value, fields []string) string {
	var parts []string
	for _, f := range fields {
		if v := renderSearchValue(fieldValue(rec, f)); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n\n")
}

// joinTitleText builds Data so Title's terms also appear in it (the
// IndexEntry contract — Title only adds ranking weight, and the content
// hash is over Data alone).
func joinTitleText(title, text string) string {
	switch {
	case title == "":
		return text
	case text == "":
		return title
	default:
		return title + "\n" + text
	}
}
