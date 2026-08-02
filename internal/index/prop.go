package index

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"
)

// DatasetProp is the VIRTUAL dataset name the prop chunker writes under
// (the middle segment of its doc ids). No SDK handler registers a
// dataset called "prop", so it can't collide with real dataset chunks.
const DatasetProp = "prop"

// MetaIndexKey is the property-definition meta key controlling value
// indexing. Three states: absent/empty → indexed under the default
// ScopeProps; a scope slug (e.g. "agent") → indexed under that scope;
// MetaIndexNone → excluded (opt-out for blobs and noisy enums).
const MetaIndexKey = "index"

// MetaIndexNone is the MetaIndexKey value that excludes a property
// from indexing. The literal word — an empty string means "default".
const MetaIndexNone = "none"

// Reserved RecordIds for the always-indexed built-in `any` properties.
// Both are valid base58, so a collision with a real hash-derived propId
// is theoretically possible — and harmless: it would merge two text
// sources under one doc id.
const (
	NamePropRecordId        = "name"
	DescriptionPropRecordId = "description"
)

// propCatalogTTL bounds how stale the per-space indexed-property
// snapshot may get: newly flagged properties are picked up within the
// TTL (and only affect rows written afterwards anyway — "index from
// the next change").
const propCatalogTTL = 30 * time.Second

// PropChunker indexes property VALUES from the shared `objects`
// collection: one entry per (object, indexed property), doc id
// objectId:prop:propId. User properties index BY DEFAULT under the
// dedicated scope ScopeProps with self-describing entry text
// ("<prop name>: <value>"); a property definition's
// meta["index"] = "<scope>" overrides the scope and
// meta["index"] = "none" opts out (see the SDK's PropertyDraft.Meta).
// The built-in any.name and any.description are always indexed under
// scope "basic", raw (no name prefix). Ungated: it runs for every
// object.
type PropChunker struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	cache map[string]*propCatalog // spaceId → snapshot
	// excludeTypes: objects carrying any of these type ids are skipped
	// entirely (no name/description/value entries) — for diagnostic
	// objects whose names would leak noise into search. Empty = index
	// every object.
	excludeTypes map[string]bool
}

// indexedProp is one catalog row: where the value lives, the scope its
// entries carry, and the display name prefixed onto the entry text.
type indexedProp struct {
	typeId string
	propId string
	name   string
	scope  string
	kind   space.PropertyKind // String, Array or Number — others never index
}

type propCatalog struct {
	resolvedAt time.Time
	props      []indexedProp
}

// NewPropChunker constructs the chunker with an empty catalog cache.
// excludeTypeIds names types whose objects are skipped entirely (e.g. the
// a diagnostic type — see PropChunker.excludeTypes).
func NewPropChunker(excludeTypeIds ...string) *PropChunker {
	excl := make(map[string]bool, len(excludeTypeIds))
	for _, id := range excludeTypeIds {
		excl[id] = true
	}
	return &PropChunker{ttl: propCatalogTTL, now: time.Now, cache: map[string]*propCatalog{}, excludeTypes: excl}
}

func (c *PropChunker) Dataset() string { return DatasetProp }
func (c *PropChunker) TypeId() string  { return "" } // ungated

// Invalidate drops the space's catalog snapshot (test/ops hook — the
// next ChunksSince re-resolves immediately instead of waiting out the
// TTL).
func (c *PropChunker) Invalidate(spaceId string) {
	c.mu.Lock()
	delete(c.cache, spaceId)
	c.mu.Unlock()
}

// catalog returns the space's indexed-property snapshot, re-resolving
// past the TTL. A failed resolve never installs a snapshot — the error
// propagates and the advance loop retries with backoff.
func (c *PropChunker) catalog(ctx context.Context, sp space.Space) ([]indexedProp, error) {
	spaceId := sp.Id()
	c.mu.Lock()
	cached := c.cache[spaceId]
	c.mu.Unlock()
	if cached != nil && c.now().Sub(cached.resolvedAt) < c.ttl {
		return cached.props, nil
	}

	types, err := sp.Types().List(ctx)
	if err != nil {
		return nil, err
	}
	var props []indexedProp
	for _, t := range types {
		if t.BuiltIn {
			continue // any.name/any.description are hardcoded below
		}
		defs, err := sp.Types().Properties(ctx, t.Id)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			if p, ok := resolveIndexedProp(t.Id, d); ok {
				props = append(props, p)
			}
		}
	}

	c.mu.Lock()
	c.cache[spaceId] = &propCatalog{resolvedAt: c.now(), props: props}
	c.mu.Unlock()
	return props, nil
}

// resolveIndexedProp maps one property definition to its catalog row.
// ok=false when the definition doesn't index: opted out
// (meta.index "none"), an invalid scope override (a broken override
// must not silently land in the default scope), or a text-less kind.
func resolveIndexedProp(typeId string, d space.PropertyDef) (indexedProp, bool) {
	scope := d.Meta[MetaIndexKey]
	switch {
	case scope == MetaIndexNone:
		return indexedProp{}, false
	case scope == "":
		scope = ScopeProps
	case !ValidScope(scope):
		return indexedProp{}, false
	}
	switch d.Kind {
	case space.PropertyKindString, space.PropertyKindArray, space.PropertyKindNumber:
	default:
		return indexedProp{}, false // booleans/null/object carry no discoverable text
	}
	name := d.Name
	if name == "" {
		name = d.XKey
	}
	return indexedProp{typeId: typeId, propId: d.Id, name: name, scope: scope, kind: d.Kind}, true
}

// ChunksSince streams the object's property entries past the cursor.
// Per live row it emits, unconditionally:
//   - any.name / any.description under scope "basic";
//   - one entry per catalog property — value rendered when its type is
//     attached, Data "" otherwise (idempotent record-level eviction of
//     cleared values and detached-type props).
//
// Tombstoned rows yield nothing — structural eviction is the indexer's
// job (it never calls chunkers for deleted objects).
func (c *PropChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error {
	props, err := c.catalog(ctx, sp)
	if err != nil {
		return err
	}
	q := sp.QueryObjects().Filter(query.Key{Path: []string{"id"}, Filter: query.NewComp(query.CompOpEq, objectId)})
	return RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		if IsDeleted(rec) {
			return nil
		}
		attached := map[string]bool{}
		for _, v := range rec.GetArray("any", "types") {
			attached[string(v.GetStringBytes())] = true
		}
		// Diagnostic objects are excluded
		// from search wholesale — their name/description carry debug
		// content, not knowledge. They are never indexed, so there is
		// nothing to evict here.
		for ex := range c.excludeTypes {
			if attached[ex] {
				return nil
			}
		}
		entry := IndexEntry{Scope: ScopeBasic, ObjectId: objectId, Dataset: DatasetProp, ApplySeq: seq}

		entry.RecordId = NamePropRecordId
		entry.Data = string(rec.GetStringBytes("any", "name"))
		if err := yield(entry); err != nil {
			return err
		}
		entry.RecordId = DescriptionPropRecordId
		entry.Data = string(rec.GetStringBytes("any", "description"))
		if err := yield(entry); err != nil {
			return err
		}

		for _, p := range props {
			e := IndexEntry{Scope: p.scope, ObjectId: objectId, Dataset: DatasetProp, RecordId: p.propId, ApplySeq: seq}
			if attached[p.typeId] {
				// Entry text is self-describing — "<prop name>: <value>"
				// — so property-NAME search works and bare numbers get
				// context. Valueless rows stay empty (a removal signal):
				// the prefix alone would index the name on every object
				// of the type.
				if v := renderPropValue(rec.Get(p.typeId, p.propId), p.kind); v != "" && p.name != "" {
					e.Data = p.name + ": " + v
				} else {
					e.Data = v
				}
			}
			if err := yield(e); err != nil {
				return err
			}
		}
		return nil
	})
}

// renderPropValue turns a property value into indexable text: strings
// as-is, numbers in canonical JSON rendering (distinctive numerals are
// real discovery anchors), arrays as a newline join of their string and
// number elements (other elements skipped), anything else (or absent)
// empty.
func renderPropValue(v *anyenc.Value, kind space.PropertyKind) string {
	if v == nil {
		return ""
	}
	switch kind {
	case space.PropertyKindString:
		if v.Type() == anyenc.TypeString {
			return string(v.GetStringBytes())
		}
	case space.PropertyKindNumber:
		if v.Type() == anyenc.TypeNumber {
			return renderNumber(v.GetFloat64())
		}
	case space.PropertyKindArray:
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

// renderNumber is the canonical JSON rendering (integers without a
// decimal point, shortest round-trip floats). NaN/Inf — unreachable
// through anyenc — render empty.
func renderNumber(f float64) string {
	b, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(b)
}
