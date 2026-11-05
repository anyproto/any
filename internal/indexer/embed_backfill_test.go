package indexer

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// A run without an embedder writes text docs with neither a vector nor
// the pending mark. BackfillPending queues exactly the docs an upsert
// with an embedder would have marked, once; the embed stamp records
// that on the cursor row.
func TestStore_BackfillPending(t *testing.T) {
	ctx := context.Background()
	st, err := OpenStoreInMemory(ctx, 0, false) // no embedder: nothing is marked
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const sp = "sp1"

	up := func(scope, dataset, recordId, data string) DocUpsert {
		return DocUpsert{Entry: index.IndexEntry{Scope: scope, ObjectId: "O", Dataset: dataset, RecordId: recordId, Data: data, ApplySeq: 1}}
	}
	ups := []DocUpsert{
		up(index.ScopeBasic, "editor_blocks", "b1", "a paragraph of text"),
		up(index.ScopeBasic, index.DatasetProp, "name", "Object name"), // a short name: title-only, as at upsert
		up(index.ScopeProps, index.DatasetProp, "status", "done"),      // props scope: never embedded
		up(index.ScopeBasic, index.DatasetProp, "p1", "short value"),   // under the prop length floor
		up(index.ScopeBasic, index.DatasetProp, "p2", strings.Repeat("long description ", 8)),
		up(index.ScopeChat, "chat_messages", "m1", "already embedded"),
	}
	ups[5].Vector = []float32{0.1, 0.2}
	if _, err := st.ApplyPage(ctx, sp, ups, nil, nil, nil, &LinkOps{}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PendingCount(ctx, sp); err != nil || n != 0 {
		t.Fatalf("pending before the backfill = %d (%v), want 0", n, err)
	}

	n, err := st.BackfillPending(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	ids, _, err := st.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	want := []string{"O:editor_blocks:b1", "O:prop:p2"}
	if n != len(want) || strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("backfill marked %d docs, pending = %v, want %v", n, ids, want)
	}
	if n, err := st.BackfillPending(ctx, sp); err != nil || n != 0 {
		t.Fatalf("second sweep marked %d docs (%v), want 0", n, err)
	}

	// The stamp: absent on a fresh row, set, cleared — and clearing it on
	// a space never indexed creates no row.
	if stamped, err := st.EmbedStamped(ctx, sp); err != nil || stamped {
		t.Fatalf("fresh row stamped = %v (%v)", stamped, err)
	}
	if err := st.StampEmbed(ctx, sp, true); err != nil {
		t.Fatal(err)
	}
	if stamped, err := st.EmbedStamped(ctx, sp); err != nil || !stamped {
		t.Fatalf("stamped = %v (%v) after StampEmbed(true)", stamped, err)
	}
	if err := st.StampEmbed(ctx, sp, false); err != nil {
		t.Fatal(err)
	}
	if stamped, err := st.EmbedStamped(ctx, sp); err != nil || stamped {
		t.Fatalf("stamped = %v (%v) after StampEmbed(false)", stamped, err)
	}
	if err := st.StampEmbed(ctx, "never-indexed", false); err != nil {
		t.Fatal(err)
	}
	coll, err := st.db.Collection(ctx, cursorsCollection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coll.FindId(ctx, "never-indexed"); !errors.Is(err, anystore.ErrDocNotFound) {
		t.Fatalf("clearing the stamp created a cursor row for a space never indexed (err %v)", err)
	}
}

// idSpace is the only part of a space the backfill step reads.
type idSpace struct {
	space.Space
	id string
}

func (s idSpace) Id() string { return s.id }

type noopEmbedder struct{}

func (noopEmbedder) EmbedDocs(context.Context, []string) ([][]float32, error) { return nil, nil }
func (noopEmbedder) EmbedQuery(context.Context, string) ([]float32, error)    { return nil, nil }
func (noopEmbedder) Dim(context.Context) (int, error)                         { return 0, nil }

// The worker over one on-disk index through an embedder on, off, on
// cycle: the first run with an embedder stamps the space; a run without
// one forgets the stamp while its docs land unmarked and a rewritten doc
// loses its vector; the next run with an embedder queues exactly those,
// keeps the vectors that survived, and nudges the embed loop once.
func TestSpaceWorker_BackfillPending(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	const sp = "sp1"

	open := func(embedder bool) *Store {
		st, err := OpenStore(ctx, dbPath, 0, embedder)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	doc := func(recordId, text string, seq uint64) DocUpsert {
		return DocUpsert{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "O", Dataset: "editor_blocks", RecordId: recordId, Data: text, ApplySeq: seq}}
	}
	land := func(st *Store, ups ...DocUpsert) {
		if _, err := st.ApplyPage(ctx, sp, ups, nil, nil, nil, &LinkOps{}); err != nil {
			t.Fatal(err)
		}
	}
	worker := func(st *Store, emb Embedder) *spaceWorker {
		return newSpaceWorker(New(nil, nil, st, Options{Embedder: emb}), idSpace{id: sp})
	}
	pending := func(st *Store) []string {
		ids, _, err := st.Pending(ctx, sp, 10)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(ids)
		return ids
	}
	stamped := func(st *Store) bool {
		on, err := st.EmbedStamped(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
	hasVector := func(st *Store, id string) bool {
		coll, err := st.spaceColl(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		d, err := coll.FindId(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d.Value().Get("vector") != nil
	}

	// An embedder: A and B land marked and get embedded; the open stamps
	// the space and has nothing to queue.
	st := open(true)
	land(st, doc("a", "first", 1), doc("b", "second", 2))
	if err := st.SetVectors(ctx, sp, []string{"O:editor_blocks:a", "O:editor_blocks:b"}, [][]float32{{1}, {1}}); err != nil {
		t.Fatal(err)
	}
	w := worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if !stamped(st) || len(pending(st)) != 0 || len(w.embedCh) != 0 {
		t.Fatalf("with an embedder: stamped = %v, pending = %v, nudges = %d", stamped(st), pending(st), len(w.embedCh))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// No embedder: the open forgets the stamp; C lands unmarked and A,
	// rewritten, loses its vector.
	st = open(false)
	worker(st, nil).backfillPending(ctx)
	if stamped(st) {
		t.Fatal("a run without an embedder left the stamp in place")
	}
	land(st, doc("a", "first, edited", 3), doc("c", "third", 4))
	if got := pending(st); len(got) != 0 || hasVector(st, "O:editor_blocks:a") {
		t.Fatalf("without an embedder: pending = %v, a has vector = %v", got, hasVector(st, "O:editor_blocks:a"))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The embedder is back: A and C are queued, B keeps its vector, the
	// space is stamped and the embed loop nudged once.
	st = open(true)
	defer st.Close()
	w = worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if got, want := pending(st), []string{"O:editor_blocks:a", "O:editor_blocks:c"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after re-enabling: pending = %v, want %v", got, want)
	}
	if !hasVector(st, "O:editor_blocks:b") || !stamped(st) || len(w.embedCh) != 1 {
		t.Fatalf("after re-enabling: b has vector = %v, stamped = %v, nudges = %d", hasVector(st, "O:editor_blocks:b"), stamped(st), len(w.embedCh))
	}
	// Stamped: the next start neither sweeps nor nudges.
	w = worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if len(w.embedCh) != 0 {
		t.Fatal("a stamped space nudged the embed loop")
	}
}
