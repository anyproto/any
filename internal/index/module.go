package index

import (
	"context"
	"sort"
	"sync"

	"github.com/anyproto/any-sync-sdk/space"
)

// ModuleStream streams one collection's entries past the cursor —
// the per-record shape (chat messages).
type ModuleStream func(ctx context.Context, sp space.Space, objectId, collection string, since uint64, yield func(IndexEntry) error) error

// ModuleReconcile renders one collection's full current entry set —
// the coalesced shape (editor windows), diffed against the store by
// the indexer.
type ModuleReconcile func(ctx context.Context, sp space.Space, objectId, collection string) ([]IndexEntry, error)

// ModuleChunker indexes every collection a dataset module serves in a
// space: the module's shared canonical collection and each namespaced
// `<typeId>_<key>` instance a type declares. One registered chunker per
// module, resolved per space from Space.Datasets (the `Module` of each
// discovered dataset) — the same in-memory snapshot the SDK refreshes
// synchronously when a declaration applies, so a read is never stale
// relative to the applySeq window being processed.
//
// The gate is collection ownership: an object holds a collection when
// it carries one of the collection's Owners — the one declaring type of
// a namespaced instance, any of the types sharing a canonical one. The
// worker structurally evicts objectId:<collection>: for every
// collection the object cannot hold (DynamicChunker), plus collections
// that vanished from the catalog since process start (a part removed —
// the SchemaChunker's retired set, same restart caveat: docs/13-index.md
// § Removal semantics). Entries carry the real collection as their
// dataset, so doc ids stay per collection; Dataset() is the module's
// virtual name.
//
// Exactly one of stream / reconcile is set. A reconciling module
// implements MultiReconciler: the worker diffs each collection's stored
// docs against the returned set, so an edit re-embeds only the windows
// that changed.
type ModuleChunker struct {
	module    string
	stream    ModuleStream
	reconcile ModuleReconcile

	mu      sync.Mutex
	known   map[string]map[string]bool // space → collections seen
	retired map[string]map[string]bool // space → collections gone since
	pending map[string]*moduleCatalog  // space → catalog handed from EvictDatasets to the paired stream
}

// moduleCatalog is one resolved snapshot: collection → owners.
type moduleCatalog struct {
	owners map[string][]string
	names  []string // sorted
}

// NewModuleChunker constructs a chunker for module. Pass either a
// ModuleStream or a ModuleReconcile as the shape.
func NewModuleChunker(module string, shape any) *ModuleChunker {
	c := &ModuleChunker{
		module:  module,
		known:   map[string]map[string]bool{},
		retired: map[string]map[string]bool{},
		pending: map[string]*moduleCatalog{},
	}
	switch fn := shape.(type) {
	case ModuleStream:
		c.stream = fn
	case ModuleReconcile:
		c.reconcile = fn
	default:
		panic("index: NewModuleChunker: shape must be a ModuleStream or a ModuleReconcile")
	}
	return c
}

// Dataset is the module's virtual name — unique among chunkers, never
// a doc-id segment (entries carry their collection).
func (c *ModuleChunker) Dataset() string { return c.module }

// TypeId is empty: gating is per collection through EvictDatasets.
func (c *ModuleChunker) TypeId() string { return "" }

// Module returns the module slug this chunker indexes.
func (c *ModuleChunker) Module() string { return c.module }

func (c *ModuleChunker) resolve(sp space.Space) *moduleCatalog {
	cat := &moduleCatalog{owners: map[string][]string{}}
	for _, ds := range sp.Datasets() {
		if ds.Module != c.module {
			continue
		}
		cat.owners[ds.Name] = ds.Owners
		cat.names = append(cat.names, ds.Name)
	}
	sort.Strings(cat.names)
	c.trackRetired(sp.Id(), cat.names)
	return cat
}

func (c *ModuleChunker) trackRetired(spaceId string, current []string) {
	cur := make(map[string]bool, len(current))
	for _, n := range current {
		cur[n] = true
	}
	c.mu.Lock()
	known := c.known[spaceId]
	if known == nil {
		known = map[string]bool{}
		c.known[spaceId] = known
	}
	for name := range known {
		if !cur[name] {
			if c.retired[spaceId] == nil {
				c.retired[spaceId] = map[string]bool{}
			}
			c.retired[spaceId][name] = true
		}
	}
	for name := range cur {
		known[name] = true
		delete(c.retired[spaceId], name)
	}
	c.mu.Unlock()
}

// holds reports whether an object carrying `attached` may hold the
// collection: one of its owners is attached.
func holds(owners []string, attached map[string]bool) bool {
	for _, o := range owners {
		if attached[o] {
			return true
		}
	}
	return false
}

// EvictDatasets returns the collections to structurally evict for an
// object with the given any.types: every collection of the module none
// of whose owners is attached, plus collections retired from the
// catalog.
func (c *ModuleChunker) EvictDatasets(ctx context.Context, sp space.Space, attached map[string]bool) ([]string, error) {
	cat := c.resolve(sp)
	var out []string
	for _, name := range cat.names {
		if !holds(cat.owners[name], attached) {
			out = append(out, name)
		}
	}
	spaceId := sp.Id()
	c.mu.Lock()
	for name := range c.retired[spaceId] {
		out = append(out, name)
	}
	c.pending[spaceId] = cat
	c.mu.Unlock()
	return out, nil
}

// active returns the collections the object may hold, resolving the
// catalog handed over by the paired EvictDatasets (or fresh).
func (c *ModuleChunker) active(ctx context.Context, sp space.Space, objectId string) ([]string, error) {
	c.mu.Lock()
	cat := c.pending[sp.Id()]
	delete(c.pending, sp.Id())
	c.mu.Unlock()
	if cat == nil {
		cat = c.resolve(sp)
	}
	if len(cat.names) == 0 {
		return nil, nil
	}
	row, err := objectRow(ctx, sp, objectId)
	if err != nil {
		return nil, err
	}
	if row == nil || IsDeleted(row) {
		return nil, nil // structural eviction is the indexer's job
	}
	attached := map[string]bool{}
	for _, v := range row.GetArray("any", "types") {
		attached[string(v.GetStringBytes())] = true
	}
	var out []string
	for _, name := range cat.names {
		if holds(cat.owners[name], attached) {
			out = append(out, name)
		}
	}
	return out, nil
}

// ChunksSince streams every active collection's entries (stream
// shape). A reconciling module answers through ReconcileAll instead;
// this path then renders its full sets, which the worker treats as
// a plain stream only when it does not implement MultiReconciler.
func (c *ModuleChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error {
	colls, err := c.active(ctx, sp, objectId)
	if err != nil {
		return err
	}
	for _, coll := range colls {
		if c.stream != nil {
			if err := c.stream(ctx, sp, objectId, coll, since, yield); err != nil {
				return err
			}
			continue
		}
		entries, err := c.reconcile(ctx, sp, objectId, coll)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := yield(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReconcileAll returns, per active collection, the full current entry
// set (reconcile shape). Only meaningful for reconciling modules; the
// worker checks MultiReconciler membership through Reconciles.
func (c *ModuleChunker) ReconcileAll(ctx context.Context, sp space.Space, objectId string, since uint64) (map[string][]IndexEntry, error) {
	colls, err := c.active(ctx, sp, objectId)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]IndexEntry, len(colls))
	for _, coll := range colls {
		entries, err := c.reconcile(ctx, sp, objectId, coll)
		if err != nil {
			return nil, err
		}
		out[coll] = entries
	}
	return out, nil
}

// Reconciles reports whether this chunker diffs full sets per
// collection (reconcile shape) rather than streaming records.
func (c *ModuleChunker) Reconciles() bool { return c.reconcile != nil }
