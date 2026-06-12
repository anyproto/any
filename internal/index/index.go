// Package index defines the consumer-side chunker contract: the bridge
// between the SDK's per-space change feed and the search indexer
// (internal/indexer). Chunkers turn one object's records into a stream
// of IndexEntry values ordered by AddSeq — the SDK's per-space,
// peer-local, monotonic delivery counter. Record deletions are
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
	ScopeAgent = "agent" // agent memory
)

// AddSeqField is the reserved record field carrying the per-space
// AddSeq watermark. Mirrors the SDK's crdt.AddSeqField — duplicated
// here so the index package doesn't depend on an SDK internal.
const AddSeqField = "_addSeq"

// IndexEntry is one unit handed to the indexer: the text of a single
// record, tagged with enough identity to address it (Scope + ObjectId +
// Dataset + RecordId) and ordered by AddSeq.
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
	AddSeq   uint64 // peer-local, per-space monotonic
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
	// ChunksSince streams every entry of objectId with AddSeq > since,
	// ascending by AddSeq. Cleared/deleted records yield removal
	// entries (Data == ""). yield is called once per entry; a yield
	// error stops the stream and is returned.
	ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error
}
