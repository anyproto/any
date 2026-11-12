package indexer

import (
	"context"
	"errors"
	"slices"
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
}

func (e *editingEmbedder) EmbedsTitles() bool { return true }

func (e *editingEmbedder) EmbedTitledDocs(ctx context.Context, _, texts []string) ([][]float32, error) {
	e.once.Do(e.edit)
	return e.EmbedDocs(ctx, texts)
}

// A doc rewritten or renamed while its batch embeds takes no vector,
// stays pending and is not reported as landed; the next drain lands it.
// The round is three batches, so each lands against its own texts.
func TestEmbedDrain_SkipsDocChangedMidRound(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &editingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{
		Embedder:         emb,
		EmbedBatch:       2,
		EmbedConcurrency: 3,
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
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m1", "old message", 3)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m2", "second message", 4)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m3", "third message", 5)},
		DocUpsert{Entry: entry("chat", "o2", "chat_messages", "m4", "fourth message", 6)},
	)
	var editErr error
	emb.edit = func() {
		editErr = st.Apply(ctx, sp, []DocUpsert{
			{Entry: entry("basic", "o1", index.DatasetProp, index.NamePropRecordId, "New name", 7)},
			{Entry: entry("chat", "o2", "chat_messages", "m1", "new message", 8)},
		}, nil, nil)
	}
	drain := func() (landed int) {
		t.Helper()
		if err := w.drainRounds(ctx, func(n, _ int) { landed += n }); err != nil {
			t.Fatal(err)
		}
		return landed
	}
	n := drain()
	if editErr != nil {
		t.Fatal(editErr)
	}
	if n != 3 {
		t.Errorf("round reported %d docs landed, want the 3 untouched messages", n)
	}
	changed := []string{docId("o1", "editor_blocks", "win_a"), docId("o2", "chat_messages", "m1")}
	ids, _, err := st.Pending(ctx, sp, 10)
	slices.Sort(ids)
	if err != nil || !slices.Equal(ids, changed) {
		t.Fatalf("pending = %q, %v; want %q", ids, err, changed)
	}
	for _, id := range changed {
		if v := storedDoc(t, st, sp, id); v.Get("vector") != nil {
			t.Errorf("%q carries a vector embedded before it changed", id)
		}
	}
	if n := drain(); n != 2 {
		t.Fatalf("next round reported %d docs landed, want 2", n)
	}
	if n, err := st.PendingCount(ctx, sp); err != nil || n != 0 {
		t.Fatalf("%d docs pending, %v; want none", n, err)
	}
}
