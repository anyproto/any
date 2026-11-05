package indexer

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// A run without an embedder writes text docs with neither a vector nor
// the pending mark. BackfillPending queues exactly the docs an upsert
// with an embedder would have marked, page by page, and finds nothing
// the second time.
func TestStore_BackfillPending(t *testing.T) {
	ctx := context.Background()
	st, err := OpenStoreInMemory(ctx, 0, false) // no embedder: nothing is marked
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const sp = "sp1"
	// Two docs per page, so the sweep has to turn pages.
	prev := backfillPage
	backfillPage = 2
	t.Cleanup(func() { backfillPage = prev })

	up := func(scope, dataset, recordId, data string) DocUpsert {
		return DocUpsert{Entry: index.IndexEntry{Scope: scope, ObjectId: "O", Dataset: dataset, RecordId: recordId, Data: data, ApplySeq: 1}}
	}
	longName := strings.Repeat("A name long enough to embed ", 3)
	ups := []DocUpsert{
		up(index.ScopeBasic, "editor_blocks", "b1", "a paragraph of text"),
		up(index.ScopeBasic, index.DatasetProp, "name", "Object name"), // a short name: title-only, as at upsert
		up(index.ScopeProps, index.DatasetProp, "status", "done"),      // props scope: never embedded
		up(index.ScopeBasic, index.DatasetProp, "p1", "short value"),   // under the prop length floor
		up(index.ScopeBasic, index.DatasetProp, "p2", strings.Repeat("long description ", 8)),
		up(index.ScopeChat, "chat_messages", "m1", "already embedded"),
		{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "P", Dataset: index.DatasetProp, RecordId: "name", Data: longName, ApplySeq: 1}}, // a long name: text in the name field
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
	want := []string{"O:editor_blocks:b1", "O:prop:p2", "P:prop:name"}
	if n != len(want) || strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("backfill marked %d docs, pending = %v, want %v", n, ids, want)
	}
	if n, err := st.BackfillPending(ctx, sp); err != nil || n != 0 {
		t.Fatalf("second sweep marked %d docs (%v), want 0", n, err)
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
// cycle: with an embedder the advance moves the embed cursor with the
// cursor and the open has nothing to sweep; a run without one advances
// the cursor only, lands docs unmarked and a rewritten doc loses its
// vector; the next run with an embedder queues exactly those, keeps the
// vectors that survived, nudges the embed loop once and catches the
// embed cursor up. A run without an embedder writes nothing of its own.
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
	// land is what an advance page does: docs, then the cursor.
	land := func(st *Store, ups ...DocUpsert) {
		if _, err := st.ApplyPage(ctx, sp, ups, nil, nil, nil, &LinkOps{}); err != nil {
			t.Fatal(err)
		}
		if err := st.AdvanceCursor(ctx, sp, ups[len(ups)-1].Entry.ApplySeq, "gen1"); err != nil {
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
	needed := func(st *Store) bool {
		n, _, err := st.EmbedBackfillNeeded(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		return n
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

	// An embedder: A and B land marked and get embedded, the advance
	// moves the embed cursor, the open has nothing to sweep.
	st := open(true)
	land(st, doc("a", "first", 1), doc("b", "second", 2))
	if err := st.SetVectors(ctx, sp, []string{"O:editor_blocks:a", "O:editor_blocks:b"}, [][]float32{{1}, {1}}); err != nil {
		t.Fatal(err)
	}
	if needed(st) {
		t.Fatal("an advance with an embedder left the embed cursor behind")
	}
	w := worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if len(pending(st)) != 0 || len(w.embedCh) != 0 {
		t.Fatalf("with an embedder: pending = %v, nudges = %d", pending(st), len(w.embedCh))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// No embedder: the open writes nothing; C lands unmarked and A,
	// rewritten, loses its vector; the cursor moves on without the embed
	// cursor.
	st = open(false)
	worker(st, nil).backfillPending(ctx)
	if needed(st) {
		t.Fatal("a run without an embedder changed the cursor row at open")
	}
	land(st, doc("a", "first, edited", 3), doc("c", "third", 4))
	if got := pending(st); len(got) != 0 || hasVector(st, "O:editor_blocks:a") || !needed(st) {
		t.Fatalf("without an embedder: pending = %v, a has vector = %v, needed = %v", got, hasVector(st, "O:editor_blocks:a"), needed(st))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The embedder is back: A and C are queued, B keeps its vector, the
	// embed loop is nudged once and the embed cursor catches up.
	st = open(true)
	defer st.Close()
	w = worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if got, want := pending(st), []string{"O:editor_blocks:a", "O:editor_blocks:c"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after re-enabling: pending = %v, want %v", got, want)
	}
	if !hasVector(st, "O:editor_blocks:b") || needed(st) || len(w.embedCh) != 1 {
		t.Fatalf("after re-enabling: b has vector = %v, needed = %v, nudges = %d", hasVector(st, "O:editor_blocks:b"), needed(st), len(w.embedCh))
	}
	// Caught up: the next start neither sweeps nor nudges.
	w = worker(st, noopEmbedder{})
	w.backfillPending(ctx)
	if len(w.embedCh) != 0 {
		t.Fatal("a caught-up space nudged the embed loop")
	}
}

// A sweep that fails leaves the embed cursor behind, and the pages the
// advance lands afterwards do not catch it up, so the next start sweeps
// again and finds the doc the failed sweep missed.
func TestSpaceWorker_BackfillPending_FailedSweepSurvivesAdvance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	const sp = "sp1"
	doc := func(recordId, text string, seq uint64) DocUpsert {
		return DocUpsert{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "O", Dataset: "editor_blocks", RecordId: recordId, Data: text, ApplySeq: seq}}
	}
	land := func(st *Store, up DocUpsert) {
		if _, err := st.ApplyPage(ctx, sp, []DocUpsert{up}, nil, nil, nil, &LinkOps{}); err != nil {
			t.Fatal(err)
		}
		if err := st.AdvanceCursor(ctx, sp, up.Entry.ApplySeq, "gen1"); err != nil {
			t.Fatal(err)
		}
	}

	// No embedder: A lands unmarked.
	st, err := OpenStore(ctx, dbPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	land(st, doc("a", "first", 1))
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// An embedder: the sweep fails (cancelled ctx), then a page lands.
	st, err = OpenStore(ctx, dbPath, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := newSpaceWorker(New(nil, nil, st, Options{Embedder: noopEmbedder{}}), idSpace{id: sp})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	w.backfillPending(cancelled)
	land(st, doc("b", "second", 2))
	if needed, _, err := st.EmbedBackfillNeeded(ctx, sp); err != nil || !needed {
		t.Fatalf("after a failed sweep and an advance: needed = %v (%v), want true", needed, err)
	}

	// The next start sweeps and finds A.
	w = newSpaceWorker(New(nil, nil, st, Options{Embedder: noopEmbedder{}}), idSpace{id: sp})
	w.backfillPending(ctx)
	ids, _, err := st.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if want := []string{"O:editor_blocks:a", "O:editor_blocks:b"}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("pending after the retried sweep = %v, want %v", ids, want)
	}
	if needed, _, err := st.EmbedBackfillNeeded(ctx, sp); err != nil || needed {
		t.Fatalf("after the retried sweep: needed = %v (%v), want false", needed, err)
	}
}
