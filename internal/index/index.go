// Package index defines the consumer-side chunker contract: the bridge
// between the SDK's per-space change feed and a future full-text /
// vector search indexer. It ships the contract plus three chunkers
// (editor blocks, chat messages, agent memory) but NO indexer — phase 1
// is "handlers only." The indexer (phase 2) will cold-align via
// space.Changes().ChangedSince, hot-subscribe via Changes().Subscribe,
// then ask each dataset's chunker to stream the IndexEntry items newer
// than its persisted cursor.
//
// A chunker turns one object's records (for its dataset) into a stream
// of IndexEntry values ordered by AddSeq — the SDK's per-space,
// peer-local, monotonic delivery counter. Deletions are first-class: a
// deleted record yields a tombstone entry (Data == "") so the indexer
// can evict it. See docs/11-index.md for the full contract.
package index

import (
	"context"

	"github.com/anyproto/any-sync-sdk/space"
)

// Scope names the logical index a chunker's entries belong to. Distinct
// scopes can use different backends / ranking; the indexer keys its
// stores by scope.
const (
	ScopeBasic = "basic" // editor blocks
	ScopeChat  = "chat"  // chat messages
	ScopeAgent = "agent" // agent memory objects
)

// AddSeqField is the reserved record field carrying the per-space
// AddSeq watermark. Mirrors the SDK's crdt.AddSeqField — duplicated
// here so the index package doesn't depend on an SDK internal.
const AddSeqField = "_addSeq"

// IndexEntry is one unit handed to the indexer: the text of a single
// record, tagged with enough identity to address it (Scope + ObjectId +
// Dataset + RecordId) and ordered by AddSeq.
//
// An empty Data is a tombstone signal: "this record is gone, remove it
// from the index." Removing a never-indexed RecordId is a no-op on the
// indexer side, so tombstones are safe to emit unconditionally.
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
	// Scope is the logical index these entries belong to.
	Scope() string
	// Dataset is the per-object dataset this chunker reads.
	Dataset() string
	// ChunksSince streams every entry of objectId with AddSeq > since,
	// ascending by AddSeq. Deleted records yield a tombstone entry
	// (Data == ""). yield is called once per entry; a yield error stops
	// the stream and is returned.
	ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error
}
