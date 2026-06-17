package program

import (
	"context"
	"strings"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Scope is the index scope tool/program documentation lands under — a
// class of its own (alongside basic/chat/agent) so callers can include or
// exclude program docs from recall independently. Program SOURCE
// (program_source) is never indexed: it is code, not documentation, and
// only the description + method docs are useful search targets.
const Scope = "program"

const (
	fieldText = "text" // program_description.main.text and program_methods.*.text
	fieldName = "name" // program_methods.*.name — the method signature heading
)

// DescriptionChunker indexes the program_description dataset (one record,
// "main", field "text" — the human-facing tool description). Gated on the
// program type, scope "program".
type DescriptionChunker struct{}

// NewDescriptionChunker constructs the program-description chunker.
func NewDescriptionChunker() *DescriptionChunker { return &DescriptionChunker{} }

func (DescriptionChunker) Dataset() string { return DatasetDescription }
func (DescriptionChunker) TypeId() string  { return TypeId }

func (DescriptionChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, DatasetDescription)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    Scope,
			ObjectId: objectId,
			Dataset:  DatasetDescription,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = string(rec.GetStringBytes(fieldText))
		}
		return yield(entry)
	})
}

// MethodsChunker indexes the program_methods dataset (one record per
// method). The indexable text is the method signature (`name`) plus its
// doc body (`text`) — the signature carries the searchable identifier
// (e.g. "createType"), the body the prose. Gated on the program type,
// scope "program".
type MethodsChunker struct{}

// NewMethodsChunker constructs the program-methods chunker.
func NewMethodsChunker() *MethodsChunker { return &MethodsChunker{} }

func (MethodsChunker) Dataset() string { return DatasetMethods }
func (MethodsChunker) TypeId() string  { return TypeId }

func (MethodsChunker) ChunksSince(ctx context.Context, sp space.Space, objectId string, since uint64, yield func(index.IndexEntry) error) error {
	q := sp.Query(objectId, DatasetMethods)
	return index.RecordsSince(ctx, q, since, func(rec *anyenc.Value, seq uint64) error {
		entry := index.IndexEntry{
			Scope:    Scope,
			ObjectId: objectId,
			Dataset:  DatasetMethods,
			RecordId: string(rec.GetStringBytes("id")),
			ApplySeq: seq,
		}
		if !index.IsDeleted(rec) {
			entry.Data = methodData(rec)
		}
		return yield(entry)
	})
}

// methodData joins a method's signature heading and doc body into one
// indexable string. Empty when both are absent (treated as a removal).
func methodData(rec *anyenc.Value) string {
	name := strings.TrimSpace(string(rec.GetStringBytes(fieldName)))
	text := strings.TrimSpace(string(rec.GetStringBytes(fieldText)))
	switch {
	case name != "" && text != "":
		return name + "\n" + text
	case name != "":
		return name
	default:
		return text
	}
}
