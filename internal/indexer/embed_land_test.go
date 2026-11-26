package indexer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/index"
)

// storedDoc reads a doc of the space's index; nil when it is not there.
func storedDoc(t *testing.T, s *Store, spaceId, id string) *anyenc.Value {
	t.Helper()
	coll, err := s.spaceColl(context.Background(), spaceId)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := coll.FindId(context.Background(), id)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return doc.Value()
}

// A vector lands only on the text it was embedded from: a doc rewritten
// between Pending and the landing stays pending without that vector, an
// identical re-upsert still lands and a deleted doc is not resurrected.
func TestStore_VectorLandsOnEmbeddedText(t *testing.T) {
	for _, titled := range []bool{false, true} {
		name := "plain"
		if titled {
			name = "titled"
		}
		t.Run(name, func(t *testing.T) { testVectorLandsOnEmbeddedText(t, titled) })
	}
}

func testVectorLandsOnEmbeddedText(t *testing.T, titled bool) {
	ctx := context.Background()
	s := mustStore(t, 4)
	s.SetEmbedTitles(titled)
	const sp = "space1"
	apply := func(ups []DocUpsert, dels ...string) {
		t.Helper()
		if err := s.Apply(ctx, sp, ups, dels, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A prop doc embeds only from minPropEmbedBytes up.
	long := func(s string) string { return s + ": long enough for a property value to be embedded on its own" }
	var (
		kept    = docId("o1", "chat_messages", "kept")
		same    = docId("o1", "chat_messages", "same")
		back    = docId("o1", "chat_messages", "back")
		gone    = docId("o1", "chat_messages", "gone")
		edited  = docId("o1", "chat_messages", "edited")
		reborn  = docId("o1", "chat_messages", "reborn")
		name1   = docId("o1", index.DatasetProp, index.NamePropRecordId)
		name2   = docId("o2", index.DatasetProp, index.NamePropRecordId)
		winKept = docId("o2", "editor_blocks", "win_kept")
		winEdit = docId("o2", "editor_blocks", "win_edit")
		chunk0  = docId("o3", "email_messages", "r")
		chunk1  = chunkDocId(chunk0, 1)
		short   = docId("o4", index.DatasetProp, "description")
	)
	apply([]DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "kept", "kept text", 1)},
		{Entry: entry("chat", "o1", "chat_messages", "same", "same text", 2)},
		{Entry: entry("chat", "o1", "chat_messages", "back", "back text", 3)},
		{Entry: entry("chat", "o1", "chat_messages", "gone", "gone text", 4)},
		{Entry: entry("chat", "o1", "chat_messages", "edited", "old text", 5)},
		{Entry: entry("chat", "o1", "chat_messages", "reborn", "old text", 6)},
		{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, long("old name"), 7)},
		{Entry: entry("basic", "o2", index.DatasetProp, index.NamePropRecordId, long("kept name"), 8)},
		{Entry: entry("basic", "o2", "editor_blocks", "win_kept", "kept window", 9)},
		{Entry: entry("basic", "o2", "editor_blocks", "win_edit", "old window", 10)},
		{Entry: entry("email", "o3", "email_messages", "r", "chunk zero", 11)},
		{Entry: entry("email", "o3", "email_messages", "r", "old chunk one", 11), Chunk: 1},
		{Entry: entry("basic", "o4", index.DatasetProp, "description", long("old description"), 12)},
	})
	ids, texts, err := s.Pending(ctx, sp, 100)
	if err != nil || len(ids) != 13 {
		t.Fatalf("pending = %v, %v; want all 13 docs", ids, err)
	}
	titles, err := s.DocTitles(ctx, sp, ids)
	if err != nil || (titles != nil) != titled {
		t.Fatalf("titles = %q, %v", titles, err)
	}

	// What lands while the batch embeds: an append, a cut and a change
	// of case among the rewrites.
	rewritten := map[string]string{
		edited:  "old text, appended",
		reborn:  "new text",
		name1:   long("new name"),
		winEdit: "old",
		chunk1:  "Old chunk one",
	}
	apply([]DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "same", "same text", 20)},
		{Entry: entry("chat", "o1", "chat_messages", "back", "another text", 21)},
		{Entry: entry("chat", "o1", "chat_messages", "edited", rewritten[edited], 22)},
		{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, rewritten[name1], 23)},
		{Entry: entry("basic", "o2", "editor_blocks", "win_edit", rewritten[winEdit], 24)},
		{Entry: entry("email", "o3", "email_messages", "r", rewritten[chunk1], 25), Chunk: 1},
		{Entry: entry("basic", "o4", index.DatasetProp, "description", "too short to embed", 26)},
	}, gone, reborn)
	apply([]DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "back", "back text", 30)},
		{Entry: entry("chat", "o1", "chat_messages", "reborn", rewritten[reborn], 31)},
	})

	stale, fresh := []float32{1, 0, 0, 0}, []float32{0, 1, 0, 0}
	vecs := func(n int, vec []float32) [][]float32 {
		out := make([][]float32, n)
		for i := range out {
			out[i] = vec
		}
		return out
	}
	landed := []string{kept, same, back, name2, winKept, chunk0}
	n, err := s.SetTitledVectors(ctx, sp, ids, texts, titles, vecs(len(ids), stale))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(landed) {
		t.Errorf("landed %d docs, want the %d whose text stands", n, len(landed))
	}
	for _, id := range landed {
		v := storedDoc(t, s, sp, id)
		if v == nil || !slices.Equal(v.GetVectorF32("vector"), stale) || v.Get("pending") != nil {
			t.Errorf("%q: want its vector landed and the mark cleared, got %v", id, v)
		}
	}
	if v := storedDoc(t, s, sp, gone); v != nil {
		t.Errorf("the deleted doc is back: %v", v)
	}
	// Rewritten into a text that does not embed: no mark, and no vector.
	if v := storedDoc(t, s, sp, short); v == nil || v.Get("vector") != nil || v.Get("pending") != nil {
		t.Errorf("%q: want neither a vector nor the mark, got %v", short, v)
	}
	for id, text := range rewritten {
		v := storedDoc(t, s, sp, id)
		if v == nil || docText(v) != text || v.Get("pending") == nil {
			t.Errorf("%q: want %q still pending, got %v", id, text, v)
		} else if v.Get("vector") != nil {
			t.Errorf("%q carries the vector of a text it no longer holds", id)
		}
	}
	ids, texts, err = s.Pending(ctx, sp, 100)
	if err != nil || len(ids) != len(rewritten) {
		t.Fatalf("pending = %q, %v; want the %d rewritten docs", ids, err, len(rewritten))
	}

	// The next round embeds the new texts.
	if titles, err = s.DocTitles(ctx, sp, ids); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SetTitledVectors(ctx, sp, ids, texts, titles, vecs(len(ids), fresh)); err != nil || n != len(ids) {
		t.Fatalf("landed %d, %v; want all %d", n, err, len(ids))
	}
	for _, id := range ids {
		if v := storedDoc(t, s, sp, id); !slices.Equal(v.GetVectorF32("vector"), fresh) {
			t.Errorf("%q: want the vector of its new text", id)
		}
	}
	if n, err := s.PendingCount(ctx, sp); err != nil || n != 0 {
		t.Fatalf("%d docs pending, %v; want none", n, err)
	}
}

// editingEmbedder embeds under titles and runs edit once, while the
// first round's batches embed — an advance page landing mid-round.
type editingEmbedder struct {
	axisEmbedder
	edit func()
	once sync.Once
	mu   sync.Mutex
	sent []string // "<text>@<title>", as sent
}

func (e *editingEmbedder) EmbedsTitles() bool { return true }

func (e *editingEmbedder) EmbedTitledDocs(ctx context.Context, titles, texts []string) ([][]float32, error) {
	e.once.Do(e.edit)
	e.mu.Lock()
	for i, text := range texts {
		e.sent = append(e.sent, text+"@"+titles[i])
	}
	e.mu.Unlock()
	return e.EmbedDocs(ctx, texts)
}

// A doc rewritten or renamed while its batch embeds takes no vector from
// it: the drain reads the head once more and embeds it as it is now,
// though the page was the last and no nudge named a renamed window.
func TestEmbedDrain_SkipsDocChangedMidRound(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &editingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{
		Embedder:         emb,
		EmbedBatch:       2,
		EmbedConcurrency: 4,
		AnnounceAfter:    -1,
	}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	const sp = "sp1"
	apply := func(ups ...DocUpsert) {
		t.Helper()
		if err := st.Apply(ctx, sp, ups, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	apply(
		DocUpsert{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, "Old name", 1)},
		DocUpsert{Entry: entry("basic", "o1", "editor_blocks", "win_a", "a window", 2)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m1", "first message", 3)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m2", "second message", 4)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m3", "third message", 5)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m4", "fourth message", 6)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m5", "fifth message", 7)},
	)
	var editErr error
	emb.edit = func() {
		editErr = st.Apply(ctx, sp, []DocUpsert{
			{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, "New name", 8)},
			{Entry: entry("chat", "o2", "chat_messages", "m1", "first message, edited", 9)},
			{Entry: entry("chat", "o2", "chat_messages", "m2", "second message, edited", 10)},
		}, nil, nil)
	}
	drain := func() (landed int) {
		t.Helper()
		if err := w.drainRounds(ctx, false, func(n, _ int) { landed += n }, nil); err != nil {
			t.Fatal(err)
		}
		return landed
	}
	n := drain()
	if editErr != nil {
		t.Fatal(editErr)
	}
	if n != 6 {
		t.Errorf("the drain reported %d docs landed, want each of the 6 once", n)
	}
	if n, err := st.PendingCount(ctx, sp); err != nil || n != 0 {
		t.Fatalf("%d docs pending, %v; want none", n, err)
	}
	// The changed ones went out twice, the second time as they are now:
	// what was embedded before the change did not land.
	slices.Sort(emb.sent)
	want := []string{
		"a window@New name", "a window@Old name",
		"fifth message@", "first message, edited@", "first message@", "fourth message@",
		"second message, edited@", "second message@", "third message@",
	}
	if !slices.Equal(emb.sent, want) {
		t.Fatalf("sent %q, want %q", emb.sent, want)
	}
}

// A doc that takes no vector on every read does not keep the drain at the
// queue's head: one more read, then the drain ends and the doc waits for
// the next one.
func TestEmbedDrain_RereadsTheHeadOnlyOnce(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &renamingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	const sp = "sp1"
	name := func(n int) DocUpsert {
		return DocUpsert{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, fmt.Sprintf("Name %d", n), uint64(10+n))}
	}
	if err := st.Apply(ctx, sp, []DocUpsert{name(0), {Entry: entry("basic", "o1", "editor_blocks", "win_a", "a window", 2)}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	emb.rename = func(call int) {
		if err := st.Apply(ctx, sp, []DocUpsert{name(call)}, nil, nil); err != nil {
			t.Error(err)
		}
	}
	if err := w.drainRounds(ctx, false, func(int, int) {}, nil); err != nil {
		t.Fatal(err)
	}
	if emb.calls != 2 {
		t.Fatalf("the window was sent %d times, want the first read and one more", emb.calls)
	}
	if n, err := st.PendingCount(ctx, sp); err != nil || n != 1 {
		t.Fatalf("%d docs pending, %v; want the window", n, err)
	}
}

// Nor does it keep the docs behind it from the embedder when it fills a
// page by itself: after the one more read the drain reads on past it.
func TestEmbedDrain_ReadsPastAHeadThatKeepsChanging(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &renamingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{Embedder: emb, EmbedBatch: 1, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	const sp = "sp1"
	name := func(n int) DocUpsert {
		return DocUpsert{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, fmt.Sprintf("Name %d", n), uint64(10+n))}
	}
	if err := st.Apply(ctx, sp, []DocUpsert{name(0),
		{Entry: entry("basic", "o1", "editor_blocks", "win_a", "a window", 2)},
		{Entry: entry("chat", "o2", "chat_messages", "m1", "a stable message", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ids, _, err := st.PendingAfter(ctx, sp, "", 1)
	if err != nil || len(ids) != 1 || !strings.Contains(ids[0], "win_a") {
		t.Fatalf("the head of the queue is %v, %v; want the window", ids, err)
	}
	emb.rename = func(call int) {
		if call > 20 { // the window lands, and the drain ends
			t.Error("the drain never left the head of the queue")
			return
		}
		if err := st.Apply(ctx, sp, []DocUpsert{name(call)}, nil, nil); err != nil {
			t.Error(err)
		}
	}
	if err := w.drainRounds(ctx, false, func(int, int) {}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _, err = st.PendingAfter(ctx, sp, "", 10)
	if err != nil || len(ids) != 1 || !strings.Contains(ids[0], "win_a") {
		t.Fatalf("pending after the drain: %v, %v; want the window alone", ids, err)
	}
}

// renamingEmbedder renames an object under every one of its calls.
type renamingEmbedder struct {
	axisEmbedder
	rename func(call int)
	calls  int
}

func (e *renamingEmbedder) EmbedsTitles() bool { return true }

func (e *renamingEmbedder) EmbedTitledDocs(ctx context.Context, _, texts []string) ([][]float32, error) {
	e.calls++
	e.rename(e.calls)
	return e.EmbedDocs(ctx, texts)
}
