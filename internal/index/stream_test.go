package index

import (
	"context"
	"errors"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakeQuery records the chained options and hands back a fakeIterator
// over a fixed record set. Only the methods RecordsSince touches are
// meaningful; the rest satisfy space.Query.
type fakeQuery struct {
	recs []*anyenc.Value

	projection space.ProjectionOpts
	filters    []any
	sorts      []any
	iter       *fakeIterator
}

func (q *fakeQuery) Filter(f any) space.Query { q.filters = append(q.filters, f); return q }
func (q *fakeQuery) Sort(s ...any) space.Query { q.sorts = append(q.sorts, s...); return q }
func (q *fakeQuery) Limit(int) space.Query     { return q }
func (q *fakeQuery) Offset(int) space.Query    { return q }
func (q *fakeQuery) Projection(o space.ProjectionOpts) space.Query {
	q.projection = o
	return q
}
func (q *fakeQuery) Iter(context.Context) (space.Iterator, error) {
	q.iter = &fakeIterator{recs: q.recs}
	return q.iter, nil
}
func (q *fakeQuery) All(context.Context) ([]*anyenc.Value, error)  { return q.recs, nil }
func (q *fakeQuery) One(context.Context) (*anyenc.Value, error)    { return nil, space.ErrNotFound }
func (q *fakeQuery) Count(context.Context) (int, error)            { return len(q.recs), nil }
func (q *fakeQuery) Snapshot(context.Context, space.QueryOpts) (*space.QueryResult, error) {
	return nil, nil
}
func (q *fakeQuery) Subscribe(context.Context, space.QueryOpts) (*space.QueryResult, error) {
	return nil, nil
}

type fakeIterator struct {
	recs   []*anyenc.Value
	i      int
	closed bool
}

func (it *fakeIterator) Next() bool {
	if it.i >= len(it.recs) {
		return false
	}
	it.i++
	return true
}
func (it *fakeIterator) Doc() (*anyenc.Value, error) { return it.recs[it.i-1], nil }
func (it *fakeIterator) Err() error                  { return nil }
func (it *fakeIterator) Close() error                { it.closed = true; return nil }

func recWithSeq(arena *anyenc.Arena, id string, seq int) *anyenc.Value {
	r := arena.NewObject()
	r.Set("id", arena.NewString(id))
	r.Set(ApplySeqField, arena.NewNumberInt(seq))
	return r
}

func TestRecordsSince_ChainsOptionsAndParsesSeq(t *testing.T) {
	arena := &anyenc.Arena{}
	q := &fakeQuery{recs: []*anyenc.Value{
		recWithSeq(arena, "a", 5),
		recWithSeq(arena, "b", 9),
	}}

	type got struct {
		id  string
		seq uint64
	}
	var seen []got
	err := RecordsSince(context.Background(), q, 3, func(rec *anyenc.Value, seq uint64) error {
		seen = append(seen, got{id: string(rec.GetStringBytes("id")), seq: seq})
		return nil
	})
	if err != nil {
		t.Fatalf("RecordsSince: %v", err)
	}

	// IncludeDeleted projection applied.
	if !q.projection.IncludeDeleted {
		t.Errorf("IncludeDeleted projection not applied")
	}
	// Window filter on _applySeq applied (typed any-store/query filter).
	if len(q.filters) != 1 {
		t.Fatalf("want 1 filter, got %d", len(q.filters))
	}
	fk, ok := q.filters[0].(query.Key)
	if !ok || len(fk.Path) != 1 || fk.Path[0] != ApplySeqField {
		t.Errorf("filter %#v does not window on %s", q.filters[0], ApplySeqField)
	}
	// Sort on _applySeq applied.
	if len(q.sorts) != 1 || q.sorts[0] != ApplySeqField {
		t.Errorf("want sort on %s, got %#v", ApplySeqField, q.sorts)
	}
	// Seq parsed from records.
	if len(seen) != 2 || seen[0].seq != 5 || seen[1].seq != 9 {
		t.Errorf("parsed seqs wrong: %#v", seen)
	}
	if !q.iter.closed {
		t.Errorf("iterator not closed")
	}
}

func TestRecordsSince_YieldErrorStopsAndCloses(t *testing.T) {
	arena := &anyenc.Arena{}
	q := &fakeQuery{recs: []*anyenc.Value{
		recWithSeq(arena, "a", 1),
		recWithSeq(arena, "b", 2),
		recWithSeq(arena, "c", 3),
	}}

	boom := errors.New("boom")
	calls := 0
	err := RecordsSince(context.Background(), q, 0, func(*anyenc.Value, uint64) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if calls != 1 {
		t.Errorf("yield called %d times, want 1 (stop on error)", calls)
	}
	if !q.iter.closed {
		t.Errorf("iterator not closed on yield error")
	}
}

func TestIsDeleted(t *testing.T) {
	arena := &anyenc.Arena{}
	live := arena.NewObject()
	live.Set("id", arena.NewString("x"))
	if IsDeleted(live) {
		t.Errorf("live record reported deleted")
	}
	tomb := arena.NewObject()
	tomb.Set("id", arena.NewString("x"))
	tomb.Set(deletedAtField, arena.NewNumberInt(123))
	if !IsDeleted(tomb) {
		t.Errorf("tombstone not reported deleted")
	}
	if IsDeleted(nil) {
		t.Errorf("nil reported deleted")
	}
}
