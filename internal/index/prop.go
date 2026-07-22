package index

import (
	"context"
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

// MetaIndexKey is the property-definition meta key that marks a
// property as indexable; the value is the scope slug its entries carry
// (e.g. meta["index"] = "agent").
const MetaIndexKey = "index"

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
// objectId:prop:propId. Which properties are indexed — and under which
// scope — is declared on the property definitions themselves via
// meta["index"] = "<scope>" (see the SDK's PropertyDraft.Meta); the
// built-in any.name and any.description are always indexed under scope
// "basic". Ungated: it runs for every object.
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

// indexedProp is one catalog row: where the value lives and the scope
// its entries carry.
type indexedProp struct {
	typeId string
	propId string
	scope  string
	kind   space.PropertyKind // String or Array — others never index
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
			scope := d.Meta[MetaIndexKey]
			if scope == "" || !ValidScope(scope) {
				continue
			}
			if d.Kind != space.PropertyKindString && d.Kind != space.PropertyKindArray {
				continue // only text-bearing kinds index
			}
			props = append(props, indexedProp{typeId: t.Id, propId: d.Id, scope: scope, kind: d.Kind})
		}
	}

	c.mu.Lock()
	c.cache[spaceId] = &propCatalog{resolvedAt: c.now(), props: props}
	c.mu.Unlock()
	return props, nil
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
				e.Data = renderPropValue(rec.Get(p.typeId, p.propId), p.kind)
			}
			if err := yield(e); err != nil {
				return err
			}
		}
		return nil
	})
}

// renderPropValue turns a property value into indexable text: strings
// as-is, arrays as a newline join of their string elements (non-string
// elements skipped), anything else (or absent) empty.
func renderPropValue(v *anyenc.Value, kind space.PropertyKind) string {
	if v == nil {
		return ""
	}
	switch kind {
	case space.PropertyKindString:
		if v.Type() == anyenc.TypeString {
			return string(v.GetStringBytes())
		}
	case space.PropertyKindArray:
		var parts []string
		for _, el := range v.GetArray() {
			if el.Type() == anyenc.TypeString {
				parts = append(parts, string(el.GetStringBytes()))
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
