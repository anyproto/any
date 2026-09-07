package indexer

import (
	"context"

	"github.com/anyproto/any/anyuri"
)

// Link reads over the index (docs/13-index.md § Links). Every read is
// a primary-key or indexed seek on the space's link collection; the
// worker's page transaction is the only writer.

// LinkQuery narrows a link read.
type LinkQuery struct {
	// Kinds keeps only these edge kinds (empty = all).
	Kinds []string
	// Limit bounds the read (0 = the store cap).
	Limit int
}

// Backlinks returns the edges pointing at target in spaceId. When
// target names an object (an `o` reference without a record, or a
// `p` reference to one of its values), every edge to the object OR
// any of its records / values is returned — the caller splits them on
// Target.IsPart(). A record target, an identity or a file returns the
// edges to exactly that target.
func (ix *Indexer) Backlinks(ctx context.Context, spaceId string, target anyuri.URI, q LinkQuery) ([]LinkDoc, error) {
	key, byObject := backlinkKey(target)
	return ix.store.Backlinks(ctx, spaceId, key, byObject, q.Kinds, q.Limit)
}

// backlinkKey picks the index the read seeks: the object key for a
// whole-object target, the exact canonical key otherwise.
func backlinkKey(target anyuri.URI) (key string, byObject bool) {
	if target.Kind == anyuri.KindObject && target.RecordId == "" {
		if ok, has := target.ObjectKey(); has {
			return ok, true
		}
	}
	return target.String(), false
}

// Links returns the edges whose source is the object, one of its
// datasets or one of its records — forward links, "what does this
// link to".
func (ix *Indexer) Links(ctx context.Context, spaceId, objectId, dataset, recordId string, q LinkQuery) ([]LinkDoc, error) {
	prefix := objectId + ":"
	if dataset != "" {
		prefix += dataset + ":"
		if recordId != "" {
			prefix += recordId + ":"
		}
	}
	return ix.store.Links(ctx, spaceId, prefix, q.Kinds, q.Limit)
}

// SpaceBacklinks is one space's share of an account-wide read.
type SpaceBacklinks struct {
	SpaceId string
	Links   []LinkDoc
}

// BacklinksAll runs Backlinks over every indexed space — the device
// holds only spaces this account is a member of, so the result is
// access-filtered by construction. Spaces are read in name order; a
// space that fails to read is skipped.
func (ix *Indexer) BacklinksAll(ctx context.Context, target anyuri.URI, q LinkQuery) ([]SpaceBacklinks, error) {
	spaces, err := ix.store.LinkSpaces(ctx)
	if err != nil {
		return nil, err
	}
	key, byObject := backlinkKey(target)
	var out []SpaceBacklinks
	for _, sp := range spaces {
		docs, err := ix.store.Backlinks(ctx, sp, key, byObject, q.Kinds, q.Limit)
		if err != nil {
			continue
		}
		if len(docs) > 0 {
			out = append(out, SpaceBacklinks{SpaceId: sp, Links: docs})
		}
	}
	return out, nil
}
