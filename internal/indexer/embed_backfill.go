package indexer

import (
	"context"
	"errors"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
)

// The cursor row's `embed` field is the cursor up to which every doc the
// upsert rule embeds carries a vector or the pending mark. A store that
// marks docs (an embedder configured, or index.vector.dim set) moves it
// with the cursor while the two agree (AdvanceCursor); a store that
// marks nothing advances the cursor alone and leaves it behind, which is
// what tells the next run with an embedder to sweep, and only a finished
// sweep catches it up. Nothing is ever erased, so a failed write or
// sweep only costs another sweep.

// backfillPage is how many docs one sweep page reads, and so the most
// one write transaction marks.
var backfillPage = 500

// EmbedBackfillNeeded reports whether docs may have landed without the
// pending mark since the space was last swept or advanced with an
// embedder, and the cursor to stamp once a sweep is done.
func (s *Store) EmbedBackfillNeeded(ctx context.Context, spaceId string) (needed bool, seq uint64, err error) {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return false, 0, err
	}
	doc, err := coll.FindId(ctx, spaceId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	v := doc.Value()
	seq = uint64(v.GetInt("seq"))
	return uint64(v.GetInt("embed")) != seq, seq, nil
}

// StampEmbed records seq as the cursor up to which every doc the upsert
// rule embeds carries a vector or the pending mark (merged into the
// cursor row).
func (s *Store) StampEmbed(ctx context.Context, spaceId string, seq uint64) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
		v.Set("embed", a.NewNumberInt(int(seq)))
		return v, true, nil
	})
	_, err = coll.UpsertId(ctx, spaceId, mod)
	return err
}

// BackfillPending marks every doc of the space that has neither a
// vector nor the pending mark and that the upsert rule embeds. It walks
// the collection in id order one page at a time; a page's docs are
// pulled and its iterator closed before the page's marks are written,
// so memory stays bounded and no iterator is open across a write.
// Returns how many docs it marked, also on failure.
func (s *Store) BackfillPending(ctx context.Context, spaceId string) (marked int, err error) {
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return 0, err
	}
	last := ""
	for {
		if err := ctx.Err(); err != nil {
			return marked, err
		}
		ids, next, more, err := unembeddedPage(ctx, coll, last)
		if err != nil {
			return marked, err
		}
		n, err := markPendingBatch(ctx, coll, ids)
		marked += n
		if err != nil {
			return marked, err
		}
		if !more {
			return marked, nil
		}
		last = next
	}
}

// unembeddedPage reads up to backfillPage docs after id last, in id
// order, and returns the ids among them to mark, the last id read, and
// whether the collection goes on past it.
func unembeddedPage(ctx context.Context, coll anystore.Collection, last string) (ids []string, next string, more bool, err error) {
	var filter any
	if last != "" {
		filter = query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGt, last)}
	}
	iter, err := coll.Find(filter).Sort("id").Limit(uint(backfillPage)).Iter(ctx)
	if err != nil {
		return nil, "", false, err
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, "", false, err
		}
		v := doc.Value()
		n++
		next = string(v.GetStringBytes("id"))
		if v.Get("vector") != nil || v.GetInt("pending") == 1 {
			continue
		}
		// The text lives in data, or in the name field for a name doc.
		text := len(v.GetStringBytes("data"))
		if text == 0 {
			text = len(v.GetStringBytes(nameField))
		}
		if !embedRule(string(v.GetStringBytes("scope")), string(v.GetStringBytes("dataset")), text) {
			continue
		}
		ids = append(ids, next)
	}
	return ids, next, n == backfillPage, iter.Err()
}

// markPendingBatch sets the pending mark on ids in one write
// transaction, update-only: a doc deleted or embedded meanwhile is left
// alone and not counted.
func markPendingBatch(ctx context.Context, coll anystore.Collection, ids []string) (marked int, err error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := coll.WriteTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
		if v.Get("vector") != nil {
			return v, false, nil
		}
		v.Set("pending", a.NewNumberInt(1))
		return v, true, nil
	})
	for _, id := range ids {
		res, err := coll.UpdateId(tx.Context(), id, mod)
		if err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
			return 0, err
		}
		marked += res.Modified
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return marked, nil
}
