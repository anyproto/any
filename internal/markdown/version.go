package markdown

import (
	"context"
	"strconv"

	"github.com/zeebo/xxh3"

	"github.com/anyproto/any-sync-sdk/space"
)

// Document is a rendered body and the version it was read at.
//
// Version is opaque to callers: the space's rebuild generation, the
// highest _applySeq across the object's records in the collection
// (tombstones and nested blocks included), and a hash of the body. Any
// write to the collection moves it. A rebuild of the SDK store
// renumbers _applySeq under a new generation, and the hash keeps a
// version from matching a different body if the sequence ever rewinds
// without one, as after restoring an older store backup.
type Document struct {
	Content string
	Version string
}

// ConflictError is Set's answer to an ifVersion the document has moved
// past. Current is the latest body and version read.
type ConflictError struct {
	Current Document
}

func (e ConflictError) Error() string {
	return "markdown: the body changed"
}

// readDocument renders objectId's body and its version from one read of
// the collection, and returns the top-level blocks it rendered and the
// collection's highest _applySeq.
func readDocument(ctx context.Context, sp space.Space, objectId, collection, gen string) (Document, []existingBlock, uint64, error) {
	existing, seq, err := listTopLevel(ctx, sp, objectId, collection)
	if err != nil {
		return Document{}, nil, 0, err
	}
	content := Join(renderExisting(existing))
	return Document{Content: content, Version: formatVersion(gen, seq, content)}, existing, seq, nil
}

func formatVersion(gen string, seq uint64, content string) string {
	return gen + "." + strconv.FormatUint(seq, 10) + "." + strconv.FormatUint(xxh3.HashString(content), 36)
}
