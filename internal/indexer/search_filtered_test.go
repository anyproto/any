//go:build fts

package indexer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync-sdk/space"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// Real any-store queries with deliberately different container/record
// authors. The fixture substitutes only SDK discovery, not filter execution.
func TestSearch_FilteredRecordCreator(t *testing.T) {
	ctx := context.Background()
	st := linkStore(t)
	objects, err := st.db.Collection(ctx, "test_objects")
	require.NoError(t, err)
	records, err := st.db.Collection(ctx, "test_records")
	require.NoError(t, err)
	require.NoError(t, objects.Insert(ctx, anyenc.MustParseJson(`{"id":"chat1","any":{"name":"Chat","types":["chatType"]},"author":"alice"}`)))
	for _, doc := range []string{
		`{"id":"from-bob","text":"quasar message","creator":"bob","createdAt":{"$date":"2026-09-02T00:00:00Z"}}`,
		`{"id":"from-alice","text":"quasar message","creator":"alice","createdAt":{"$date":"2026-09-01T00:00:00Z"}}`,
		`{"id":"unknown","text":"quasar message"}`,
		`{"id":"empty","text":"","creator":"bob","createdAt":{"$date":"2026-09-03T00:00:00Z"}}`,
	} {
		require.NoError(t, records.Insert(ctx, anyenc.MustParseJson(doc)))
	}
	sp := &searchFixtureSpace{objects: objects, records: records}
	ix := New(nil, index.NewRegistry(), st, Options{})
	ix.spacesAPI = searchFixtureService{sp: sp}
	var ups []DocUpsert
	for i, id := range []string{"from-bob", "from-alice", "unknown", "deleted"} {
		ups = append(ups, DocUpsert{Entry: index.IndexEntry{Scope: "chat", ObjectId: "chat1", Dataset: "chat_messages", RecordId: id, Data: "quasar message", ApplySeq: uint64(i + 1)}})
	}
	require.NoError(t, st.Apply(ctx, "sp1", ups, nil, nil))
	req := api.SearchRequest{Query: "quasar", Filter: &api.SearchFilter{Kinds: []string{"record"}, Creator: "bob", TypeIds: []string{"chatType"}}, Scopes: []string{"chat"}}
	res, err := ix.Search(ctx, "sp1", req)
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "from-bob", res.Hits[0].RecordId)
	require.Equal(t, "bob", res.Hits[0].Creator)
	require.Equal(t, 1, sp.recordReads, "batch one query per dataset, not per hit")
	req.Filter.Creator = "alice"
	res, err = ix.Search(ctx, "sp1", req)
	require.NoError(t, err)
	require.Len(t, res.Hits, 1, "owner alice must not admit Bob's messages")
	require.Equal(t, "from-alice", res.Hits[0].RecordId)
	req.Filter.Creator = ""
	res, err = ix.Search(ctx, "sp1", req)
	require.NoError(t, err)
	require.Len(t, res.Hits, 3, "stale deleted hit is removed, unknown author still searchable without creator chip")
	req.Query, req.Filter.Creator, req.Sort, req.Limit = "", "bob", "created", 1
	res, err = ix.Search(ctx, "sp1", req)
	require.NoError(t, err)
	require.Equal(t, "empty", res.Hits[0].RecordId, "chip-only browse includes attachment-only messages with empty text")
	require.True(t, *res.HasNext)
	req.Offset = 1
	res, err = ix.Search(ctx, "sp1", req)
	require.NoError(t, err)
	require.Equal(t, "from-bob", res.Hits[0].RecordId)
	require.False(t, *res.HasNext)
	for _, deleted := range []error{space.ErrObjectNotFound, space.ErrObjectDeleted} {
		sp.recordErr = fmt.Errorf("open owner: %w", deleted)
		res, err = ix.Search(ctx, "sp1", req)
		require.NoError(t, err)
		require.Empty(t, res.Hits, "owner deleted between metadata and record read is omitted")
	}
	sp.recordErr = errors.New("storage unavailable")
	_, err = ix.Search(ctx, "sp1", req)
	require.ErrorIs(t, err, sp.recordErr, "unexpected query failures must remain visible")
}

func TestStore_SearchRelatedObjectsUncapped(t *testing.T) {
	ctx := context.Background()
	st := linkStore(t)
	var links LinkOps
	for i := range 1105 {
		links.Ups = append(links.Ups, edge(fmt.Sprintf("source-%04d", i), "editor_blocks", "b", "link", "any://o/sp2/selected/editor_blocks/part"))
		links.Seqs = append(links.Seqs, 1)
	}
	_, err := st.ApplyPage(ctx, "sp1", nil, nil, nil, nil, &links)
	require.NoError(t, err)
	_, err = st.ApplyPage(ctx, "sp2", nil, nil, nil, nil, &LinkOps{
		Ups: []index.LinkEntry{
			edge("selected", "editor_blocks", "b", "link", "any://o/sp1/outgoing"),
			edge("selected", "editor_blocks", "c", "link", "any://o/sp3/not-this-space"),
		}, Seqs: []uint64{1, 1},
	})
	require.NoError(t, err)
	got, err := st.relatedObjects(ctx, "sp1", api.SearchObjectRef{SpaceId: "sp2", ObjectId: "selected"})
	require.NoError(t, err)
	require.Len(t, got, 1106, "incoming and outgoing object union exceeds both HTTP and store link-list caps")
	require.True(t, got["source-1104"])
	require.True(t, got["outgoing"])
	require.False(t, got["not-this-space"])
}

type searchFixtureSpace struct {
	space.Space
	objects, records anystore.Collection
	recordReads      int
	recordErr        error
}

func (s *searchFixtureSpace) Id() string                { return "sp1" }
func (s *searchFixtureSpace) QueryObjects() space.Query { return searchFixtureQuery{coll: s.objects} }
func (s *searchFixtureSpace) Query(string, string) space.Query {
	s.recordReads++
	return searchFixtureQuery{coll: s.records, err: s.recordErr}
}
func (s *searchFixtureSpace) Datasets() []space.DatasetSchema {
	return []space.DatasetSchema{{Name: "chat_messages", Module: "chat", Owners: []string{"chatType"}, JSONSchema: []byte(`{}`)}}
}

type searchFixtureService struct {
	space.Service
	sp space.Space
}

func (s searchFixtureService) Get(context.Context, string) (space.Space, error) { return s.sp, nil }

type searchFixtureQuery struct {
	space.Query
	coll   anystore.Collection
	filter query.Filter
	err    error
}

func (q searchFixtureQuery) Filter(f any) space.Query {
	var err error
	q.filter, err = query.ParseCondition(f)
	if err != nil {
		panic(err)
	}
	return q
}
func (q searchFixtureQuery) Iter(ctx context.Context) (space.Iterator, error) {
	if q.err != nil {
		return nil, q.err
	}
	it, err := q.coll.Find(q.filter).Iter(ctx)
	if err != nil {
		return nil, err
	}
	return searchFixtureIterator{it}, nil
}

type searchFixtureIterator struct{ anystore.Iterator }

func (i searchFixtureIterator) Doc() (*anyenc.Value, error) {
	doc, err := i.Iterator.Doc()
	if err != nil {
		return nil, err
	}
	return doc.Value(), nil
}
