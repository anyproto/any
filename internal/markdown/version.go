package markdown

import (
	"context"
	"strconv"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// Document is a rendered body and the version it was read at.
//
// Version is opaque to callers: the space's rebuild generation plus the
// highest _applySeq across the object's records in the collection,
// tombstones and nested blocks included. Any write to the collection
// moves it, and a rebuild of the SDK store renumbers _applySeq under a
// new generation, so a version from before the rebuild never matches.
type Document struct {
	Content string
	Version string
}

// ConflictError is Set's answer to an ifVersion the document has moved
// past. Current is the body and version the check read.
type ConflictError struct {
	Current Document
}

func (e ConflictError) Error() string {
	return "markdown: the body changed after ifVersion"
}

func formatVersion(gen string, seq uint64) string {
	return gen + "." + strconv.FormatUint(seq, 10)
}

// savedVersion is the version of the body Set just wrote: the highest
// _applySeq stamped after the read, provided every record stamped since
// is one this save wrote. A record another writer touched in between is
// not in the saved body, so the read version comes back instead and the
// caller's next ifVersion conflicts. A save that wrote nothing keeps the
// read version without a second query.
func savedVersion(ctx context.Context, sp space.Space, objectId, collection, gen string, readSeq uint64, res SetResult) (string, error) {
	if len(res.Inserted)+len(res.Updated)+len(res.Deleted) == 0 {
		return formatVersion(gen, readSeq), nil
	}
	var stamps []stamp
	err := index.RecordsSince(ctx, sp.Query(objectId, collection), readSeq, func(rec *anyenc.Value, seq uint64) error {
		stamps = append(stamps, stamp{id: rec.GetString("id"), seq: seq})
		return nil
	})
	if err != nil {
		return "", err
	}
	return formatVersion(gen, savedSeq(readSeq, stamps, res)), nil
}

type stamp struct {
	id  string
	seq uint64
}

// savedSeq picks savedVersion's sequence from the records stamped after
// readSeq. A foreign write to one of this save's own blocks hides behind
// the block's id: the same block is last-writer-wins on every markdown
// path.
func savedSeq(readSeq uint64, stamps []stamp, res SetResult) uint64 {
	own := make(map[string]struct{}, len(res.Inserted)+len(res.Updated)+len(res.Deleted))
	for _, ids := range [][]string{res.Inserted, res.Updated, res.Deleted} {
		for _, id := range ids {
			own[id] = struct{}{}
		}
	}
	seq := readSeq
	for _, s := range stamps {
		if _, ok := own[s.id]; !ok {
			return readSeq
		}
		seq = max(seq, s.seq)
	}
	return seq
}
