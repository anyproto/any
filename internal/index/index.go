// Package index defines the consumer-side chunker contract: the bridge
// between the SDK's per-space change feed and the search indexer
// (internal/indexer). Chunkers turn one object's records into a stream
// of IndexEntry values ordered by ApplySeq — the SDK's per-space,
// peer-local, monotonic apply counter (advances on every apply that
// mutates a record, including non-DAG ones). Record deletions are
// first-class: a deleted record yields a removal entry (Data == "");
// structural removals (object deletion, type detach) are derived by the
// indexer from the shared objects row and applied as primary-key prefix
// deletes. See docs/13-index.md for the full contract.
package index

import (
	"context"

	"github.com/anyproto/any-sync-sdk/space"
)

// Conventional scope slugs. Scopes are an OPEN set — any slug passing
// ValidScope works (property meta flags carry arbitrary scopes); these
// constants are just the established vocabulary.
const (
	ScopeBasic = "basic" // editor blocks, object names/descriptions
	ScopeChat  = "chat"  // chat messages
	// ScopeProps is the default scope for user property values (see
	// PropChunker). FTS-only: the indexer never embeds props-scope docs —
	// short "Name: value" entries embed badly and would pollute vector
	// recall. A meta.index override into another scope opts back in.
	ScopeProps = "props"
)

// ApplySeqField is the reserved record field carrying the per-space
// applySeq watermark. Mirrors the SDK's crdt.ApplySeqField — duplicated
// here so the index package doesn't depend on an SDK internal. It is the
// record-level twin of space.ObjectChange.ApplySeq, so the chunker
// window and the change-index cursor share one ordering axis.
const ApplySeqField = "_applySeq"

// IndexEntry is one unit handed to the indexer: the text of a single
// record, tagged with enough identity to address it (Scope + ObjectId +
// Dataset + RecordId) and ordered by ApplySeq.
//
// An empty Data is a record-level removal signal: "this record is gone
// (or has nothing to index), remove it." Removing a never-indexed
// record is a no-op on the indexer side, so removals are safe to emit
// unconditionally. Structural removals — a whole object (deletion) or
// one object's dataset (type detach) — are NOT expressed as entries:
// the indexer derives them from the shared objects row inside the same
// ChangedSince window and applies them as id-prefix deletes in the same
// page transaction.
type IndexEntry struct {
	Scope    string
	ObjectId string
	Dataset  string
	RecordId string
	Data     string // text to index; empty = remove this record from the index
	Title    string // optional BM25F-boosted field (heading / signature / summary);
	// its terms should also appear in Data — Title only adds ranking weight,
	// and the content hash (embed-skip) is over Data alone.
	ApplySeq uint64 // peer-local, per-space monotonic apply counter
	// Links are the edges found in the record(s) this entry covers —
	// the link sink's input (docs/13-index.md § Links). Each carries
	// its own source place: a coalesced editor window reports the
	// links of every member block under that block's id. A streaming
	// chunker's entry replaces its record's edges (nil = the record
	// links nothing); a reconciling chunker's set replaces the
	// collection's edges. Not part of the content hash.
	Links []LinkEntry
}

// Reconciler is an optional chunker capability for datasets whose index
// unit spans MULTIPLE records — e.g. coalesced editor windows, where one
// index doc concatenates several blocks. An incremental per-record stream
// (ChunksSince) can't express such a unit: a window's text needs sibling
// records below the cursor, and a deleted record's position is wiped from
// its tombstone, so the affected window can't be located incrementally.
//
// Reconcile returns the object's FULL current doc set for the dataset.
// The indexer diffs it against what is already stored (by content hash):
// docs that vanished are deleted, new/changed docs are upserted, and
// unchanged docs are left untouched — so their vectors are preserved and
// not needlessly re-embedded. The indexer prefers Reconcile over
// ChunksSince when a chunker implements this interface.
type Reconciler interface {
	Chunker
	// Reconcile returns every current index entry for objectId on this
	// chunker's dataset. since is the indexer's cursor (the current
	// implementation rebuilds the full set and ignores it; a future
	// incremental implementation could use it to scope work).
	Reconcile(ctx context.Context, sp space.Space, objectId string, since uint64) ([]IndexEntry, error)
}

// MultiReconciler is a DynamicChunker whose index unit spans multiple
// records on SEVERAL collections at once (a module's canonical
// collection plus namespaced instances). ReconcileAll returns the full
// current entry set per active collection; the indexer diffs each
// collection's stored docs (prefix objectId:<collection>:) against its
// set exactly as Reconcile does for one dataset. Reconciles reports
// whether the chunker takes this path at all — a streaming module
// chunker returns false and is served through ChunksSince.
type MultiReconciler interface {
	DynamicChunker
	Reconciles() bool
	ReconcileAll(ctx context.Context, sp space.Space, objectId string, since uint64) (map[string][]IndexEntry, error)
}

// DynamicChunker is an optional Chunker capability for chunkers whose
// dataset set is defined at runtime (schema-driven datasets). The
// worker replaces the static Dataset()/TypeId() gate for such a
// chunker: it prefix-evicts objectId:<ds>: for every name returned by
// EvictDatasets, then runs ChunksSince as usual — the chunker
// self-gates its active set, so an evicted dataset is never also
// streamed in the same page.
type DynamicChunker interface {
	Chunker
	// EvictDatasets returns the runtime dataset names to structurally
	// evict for an object with the given any.types set: catalog
	// datasets whose owning type is not attached (the DetachType path),
	// plus names retired since process start (definition removed).
	EvictDatasets(ctx context.Context, sp space.Space, attached map[string]bool) ([]string, error)
}

// TextEvictor is an optional DynamicChunker capability for datasets
// whose TEXT docs must go while their edges stay: the worker
// prefix-evicts `objectId:<ds>:` on the text collection only for every
// name returned, and the chunker keeps streaming the dataset for its
// links (docs/13-index.md § Links). A dataset that lost its search
// mapping but keeps link fields is the case.
type TextEvictor interface {
	EvictText(ctx context.Context, sp space.Space, attached map[string]bool) []string
}

// CatalogInvalidator is an optional Chunker capability for chunkers
// holding a per-space catalog snapshot with a TTL (the prop chunker):
// the worker calls Invalidate when a type object changed in the feed,
// so a property added a moment ago is in the catalog when the values
// written right after it are extracted.
type CatalogInvalidator interface {
	Invalidate(spaceId string)
}

// WholeCollectionLinks is an optional Chunker capability for streaming
// chunkers whose every stream carries the record's complete edge set
// for the whole collection — the prop chunker emits one entry per
// catalog property per row, so a property that left the catalog
// (definition removed) simply stops being emitted. The worker then
// replaces the collection's edges on every stream instead of only the
// streamed records', and the removed property's edges fall out.
type WholeCollectionLinks interface {
	LinksReplaceCollection()
}

// Chunker streams the IndexEntry values for one dataset on one object.
type Chunker interface {
	// Dataset is the dataset this chunker writes — the middle segment
	// of the index doc id (objectId:dataset:recordId). May be virtual
	// (see DatasetProp): it only has to be unique among chunkers,
	// colon-free, and stable.
	Dataset() string
	// TypeId is the any.types entry gating this chunker: the indexer
	// runs ChunksSince only while the type is attached to the object,
	// and prefix-evicts objectId:<dataset>: when it is not — covering
	// DetachType. Empty = ungated, runs for every object.
	TypeId() string
	// ChunksSince streams every entry of objectId with ApplySeq > since,
	// ascending by ApplySeq. Cleared/deleted records yield removal
	// entries (Data == ""). yield is called once per entry; a yield
	// error stops the stream and is returned.
	ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error
}
