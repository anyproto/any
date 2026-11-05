package indexer

import (
	"context"
	"errors"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any/internal/index"
)

// The cursor row's `embed` stamp: 1 when every text doc of the space
// carries a vector or the pending mark — written with an embedder
// configured, or backfilled; absent or 0 when a run without an embedder
// may have left docs with neither.
const embedStamp = 1

// backfillBatch is how many docs one backfill write transaction marks.
const backfillBatch = 500

// EmbedStamped reports whether the space's text docs are all embedded
// or marked pending.
func (s *Store) EmbedStamped(ctx context.Context, spaceId string) (bool, error) {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return false, err
	}
	doc, err := coll.FindId(ctx, spaceId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return doc.Value().GetInt("embed") == embedStamp, nil
}

// StampEmbed records on the cursor row that every text doc of the space
// is embedded or marked pending (on), or forgets it (off) — the state a
// run without an embedder leaves behind. Merged like every cursor write.
func (s *Store) StampEmbed(ctx context.Context, spaceId string, on bool) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	stamp := 0
	if on {
		stamp = embedStamp
	}
	mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
		v.Set("embed", a.NewNumberInt(stamp))
		return v, true, nil
	})
	if on {
		_, err = coll.UpsertId(ctx, spaceId, mod)
		return err
	}
	// Forgetting needs no row: a space never indexed has nothing to sweep.
	if _, err = coll.UpdateId(ctx, spaceId, mod); errors.Is(err, anystore.ErrDocNotFound) {
		return nil
	}
	return err
}

// BackfillPending marks every text doc of the space that has neither a
// vector nor the pending mark — written while no embedder was
// configured — as pending, under the upsert rule (shouldEmbed). Returns
// how many it marked. The ids are collected first and marked in
// batches, so no iterator stays open across a write.
func (s *Store) BackfillPending(ctx context.Context, spaceId string) (int, error) {
	coll, err := s.spaceColl(ctx, spaceId)
	if err != nil {
		return 0, err
	}
	ids, err := unembeddedIds(ctx, coll)
	if err != nil {
		return 0, err
	}
	for start := 0; start < len(ids); start += backfillBatch {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		end := min(start+backfillBatch, len(ids))
		if err := markPending(ctx, coll, ids[start:end]); err != nil {
			return start, err
		}
	}
	return len(ids), nil
}

func unembeddedIds(ctx context.Context, coll anystore.Collection) ([]string, error) {
	iter, err := coll.Find(nil).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var ids []string
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		v := doc.Value()
		if v.Get("vector") != nil || v.GetInt("pending") == 1 {
			continue
		}
		if !shouldEmbed(index.IndexEntry{
			Scope:   string(v.GetStringBytes("scope")),
			Dataset: string(v.GetStringBytes("dataset")),
			Data:    docText(v),
		}) {
			continue
		}
		ids = append(ids, string(v.GetStringBytes("id")))
	}
	return ids, iter.Err()
}

// markPending sets the pending mark on ids in one write transaction,
// update-only: a doc deleted or embedded meanwhile is left alone.
func markPending(ctx context.Context, coll anystore.Collection, ids []string) error {
	tx, err := coll.WriteTx(ctx)
	if err != nil {
		return err
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
		if _, err := coll.UpdateId(tx.Context(), id, mod); err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
			return err
		}
	}
	return tx.Commit()
}
