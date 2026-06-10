package index

import (
	"context"
	"strings"
	"sync"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

// DatasetObjects is the per-space shared `objects` collection — one row
// per regular object, holding computed property values. Agent memory
// objects live here (they're regular objects carrying the agent_memory
// type), so the agent chunker reads it via QueryObjects rather than a
// bespoke per-object dataset.
const DatasetObjects = "objects"

// MemoryTypeXKey is the client-side type key identifying agent memory
// objects. The SDK assigns each type a content-addressed Id; clients map
// to it via this stable XKey.
const MemoryTypeXKey = "agent_memory"

// memoryPropXKeys are the string-valued properties whose values feed the
// index, joined newline-separated after the object name.
var memoryPropXKeys = []string{"context", "keywords", "entities"}

// AgentMemoryChunker indexes agent_memory-typed objects from the shared
// `objects` dataset under scope "agent". The indexed text is the object
// name plus the context / keywords / entities property values.
//
// Type and property ids are content-addressed and immutable, so a
// resolved (typeId, propIds) tuple is cached per space forever. Absence
// is never cached: a not-yet-created type or a still-incomplete property
// set re-resolves on the next call, so the chunker picks them up once
// the client defines them.
type AgentMemoryChunker struct {
	mu    sync.Mutex
	cache map[string]*resolved // spaceId -> resolved ids
}

// NewAgentMemoryChunker constructs the chunker with an empty cache.
func NewAgentMemoryChunker() *AgentMemoryChunker {
	return &AgentMemoryChunker{cache: map[string]*resolved{}}
}

func (c *AgentMemoryChunker) Scope() string   { return ScopeAgent }
func (c *AgentMemoryChunker) Dataset() string { return DatasetObjects }

// resolved holds the per-space ids the chunker resolves once. props maps
// each wanted XKey to its propId; only resolved props are present.
type resolved struct {
	typeId string
	props  map[string]string // xKey -> propId
}

func (r *resolved) complete() bool {
	return r.typeId != "" && len(r.props) == len(memoryPropXKeys)
}

// resolve returns the agent_memory typeId and the wanted propIds for the
// space, caching positives. Returns ("", nil, nil) when the type isn't
// defined yet — the caller then emits no live entries (but still emits
// tombstones for deletions). Re-resolves while the props map is
// incomplete so late-added properties get picked up.
func (c *AgentMemoryChunker) resolve(ctx context.Context, sp space.Space) (string, map[string]string, error) {
	spaceId := sp.Id()

	c.mu.Lock()
	cached := c.cache[spaceId]
	c.mu.Unlock()
	if cached != nil && cached.complete() {
		return cached.typeId, cached.props, nil
	}

	typeId := ""
	props := map[string]string{}
	if cached != nil {
		typeId = cached.typeId
		for k, v := range cached.props {
			props[k] = v
		}
	}

	if typeId == "" {
		types, err := sp.Types().List(ctx)
		if err != nil {
			return "", nil, err
		}
		for _, t := range types {
			if t.BuiltIn {
				continue
			}
			if t.XKey == MemoryTypeXKey {
				typeId = t.Id
				break
			}
		}
	}
	if typeId == "" {
		// Type absent — don't cache absence; live rows yield nothing.
		return "", nil, nil
	}

	if len(props) < len(memoryPropXKeys) {
		defs, err := sp.Types().Properties(ctx, typeId)
		if err != nil {
			return "", nil, err
		}
		for _, d := range defs {
			if d.Kind != space.PropertyKindString {
				continue
			}
			for _, want := range memoryPropXKeys {
				if d.XKey == want {
					props[want] = d.Id
				}
			}
		}
	}

	c.mu.Lock()
	c.cache[spaceId] = &resolved{typeId: typeId, props: props}
	c.mu.Unlock()
	return typeId, props, nil
}

// ChunksSince streams agent-memory index entries for objectId past the
// cursor. Per row:
//   - deleted → always a tombstone entry (Data ""), regardless of type:
//     the tombstone's any.types is wiped so memory-ness is unknowable,
//     and removing a never-indexed id is an indexer-side no-op.
//   - live row missing the agent_memory type (or type undefined) → also
//     a tombstone entry. The row may have *been* a memory before a
//     DetachType, and the indexer can't tell "row didn't stream" from
//     "streamed but not a memory" — emitting the idempotent removal
//     here lets the indexer apply all entries uniformly.
//   - else one entry whose Data is name + context + keywords + entities.
func (c *AgentMemoryChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error {
	typeId, props, err := c.resolve(ctx, sp)
	if err != nil {
		return err
	}

	q := sp.QueryObjects().Filter(map[string]any{"id": objectId})
	return RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := IndexEntry{
			Scope:    ScopeAgent,
			ObjectId: objectId,
			Dataset:  DatasetObjects,
			RecordId: objectId,
			AddSeq:   seq,
		}
		if IsDeleted(rec) {
			entry.Data = "" // tombstone — remove from index
			return yield(entry)
		}
		if typeId == "" || !hasType(rec, typeId) {
			entry.Data = "" // live but not (or no longer) a memory → idempotent removal
			return yield(entry)
		}
		entry.Data = memoryData(rec, typeId, props)
		return yield(entry)
	})
}

// hasType reports whether rec's any.types array contains typeId.
func hasType(rec *anyenc.Value, typeId string) bool {
	if rec == nil {
		return false
	}
	for _, v := range rec.GetArray("any", "types") {
		if string(v.GetStringBytes()) == typeId {
			return true
		}
	}
	return false
}

// memoryData joins the object name and the context / keywords / entities
// property values with newlines, skipping empties. Property values live
// at rec[typeId][propId]; the object name at rec["any"]["name"]. Missing
// props and missing name are simply skipped — order is name first, then
// the memoryPropXKeys order.
func memoryData(rec *anyenc.Value, typeId string, props map[string]string) string {
	if rec == nil {
		return ""
	}
	parts := make([]string, 0, len(memoryPropXKeys)+1)
	if name := string(rec.GetStringBytes("any", "name")); name != "" {
		parts = append(parts, name)
	}
	for _, xKey := range memoryPropXKeys {
		propId, ok := props[xKey]
		if !ok {
			continue
		}
		if val := string(rec.GetStringBytes(typeId, propId)); val != "" {
			parts = append(parts, val)
		}
	}
	return strings.Join(parts, "\n")
}
