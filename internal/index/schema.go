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
// schema's x-search {title, text} mapping: one registered chunker
// covers every dataset any type in the space declares with a search
// annotation. Entries carry the real dataset name (doc ids
// objectId:<dataset>:<recordId>) under scope "basic" — runtime dataset
// records are user content on par with editor blocks.
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
}

// schemaDataset is one resolved runtime dataset with a search mapping.
type schemaDataset struct {
	name       string
	typeId     string
	titleField string
	textField  string
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
	}
}

func (c *SchemaChunker) Dataset() string { return DatasetSchemaVirtual }
func (c *SchemaChunker) TypeId() string  { return "" } // self-gated per dataset

// resolve returns the space's current searchable runtime datasets and,
// as a side effect, folds catalog disappearances into the retired set.
func (c *SchemaChunker) resolve(sp space.Space) []schemaDataset {
	searchable, allNames := parseSchemaDatasets(sp.Datasets(), c.skip)
	c.trackRetired(sp.Id(), allNames)
	return searchable
}

// parseSchemaDatasets splits a catalog snapshot into the searchable
// runtime datasets (x-search declared) and the full runtime name set.
// Runtime entries are the type-owned names outside the static skip set;
// only searchable ones are streamed, but ALL names are tracked for
// retirement — a dataset without search never indexed anything, so its
// retirement eviction is a harmless idempotent seek.
func parseSchemaDatasets(list []space.DatasetSchema, skip map[string]bool) (searchable []schemaDataset, allNames []string) {
	for _, ds := range list {
		if ds.TypeId == "" || skip[ds.Name] || strings.Contains(ds.Name, ":") {
			continue
		}
		allNames = append(allNames, ds.Name)
		var doc struct {
			Search struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"x-search"`
		}
		if err := json.Unmarshal(ds.JSONSchema, &doc); err != nil {
			continue // malformed schema doc — nothing to index
		}
		if doc.Search.Title == "" && doc.Search.Text == "" {
			continue // no search annotation — not indexed
		}
		searchable = append(searchable, schemaDataset{
			name:       ds.Name,
			typeId:     ds.TypeId,
			titleField: doc.Search.Title,
			textField:  doc.Search.Text,
		})
	}
	return searchable, allNames
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

// EvictDatasets implements DynamicChunker: catalog datasets whose
// owning type is not attached to this object, plus every retired name.
func (c *SchemaChunker) EvictDatasets(ctx context.Context, sp space.Space, attached map[string]bool) ([]string, error) {
	var out []string
	for _, ds := range c.resolve(sp) {
		if !attached[ds.typeId] {
			out = append(out, ds.name)
		}
	}
	c.mu.Lock()
	for name := range c.retired[sp.Id()] {
		out = append(out, name)
	}
	c.mu.Unlock()
	return out, nil
}

// ChunksSince streams the object's runtime-dataset entries past the
// cursor: per active dataset (owning type attached), every record with
// ApplySeq > since maps x-search.title → Title and title+text → Data.
// Tombstones and records whose mapped fields render empty yield removal
// entries (Data == ""). Datasets the worker just evicted (type not
// attached) are never streamed in the same page.
func (c *SchemaChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error {
	dss := c.resolve(sp)
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
				Scope:    ScopeBasic,
				ObjectId: objectId,
				Dataset:  ds.name,
				RecordId: string(rec.GetStringBytes("id")),
				ApplySeq: seq,
			}
			if !IsDeleted(rec) {
				title := renderSearchValue(fieldValue(rec, ds.titleField))
				text := renderSearchValue(fieldValue(rec, ds.textField))
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
