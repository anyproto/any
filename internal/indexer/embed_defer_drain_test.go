package indexer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/index"
)

// deferEmbedder leaves texts that start with "long" without a vector
// until released, as an embedder whose runtime for them is not ready.
type deferEmbedder struct {
	axisEmbedder
	mu    sync.Mutex
	ready bool
	wake  chan struct{}
	calls int
	sent  []string
	// releaseAt, when set, is the call that releases the texts as it
	// returns, in the middle of a pass.
	releaseAt int
	held      int // HoldBulk calls not released
	maxHeld   int
	onCall    func(call int) // runs at the start of each call, unlocked
}

func (e *deferEmbedder) HoldBulk() func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held++
	e.maxHeld = max(e.maxHeld, e.held)
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.held--
	}
}

func (e *deferEmbedder) EmbedDocsRouted(_ context.Context, _, texts []string) ([][]float32, EmbedRoute, error) {
	e.mu.Lock()
	call, onCall := e.calls+1, e.onCall
	e.mu.Unlock()
	if onCall != nil {
		onCall(call)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.sent = append(e.sent, texts...)
	out := make([][]float32, len(texts))
	rt := EmbedRoute{Backends: []string{EmbedBackendANE}}
	for i, t := range texts {
		if strings.HasPrefix(t, "long") && !e.ready {
			rt.Deferred++
			continue
		}
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	if rt.Deferred == len(texts) {
		rt.Backends = nil
	}
	if e.releaseAt > 0 && e.calls == e.releaseAt {
		e.releaseLocked()
	}
	return out, rt, nil
}

func (e *deferEmbedder) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releaseLocked()
}

func (e *deferEmbedder) releaseLocked() {
	e.ready = true
	if e.wake != nil {
		close(e.wake)
		e.wake = make(chan struct{})
	}
}

func (e *deferEmbedder) counts() (calls int, sent []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls, append([]string(nil), e.sent...)
}

// wakingEmbedder is a deferEmbedder that says when to send again.
type wakingEmbedder struct{ *deferEmbedder }

func (e wakingEmbedder) DeferredWake() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.wake
}

// rows are the distinct phase[backends] states of the last deferDrain's
// process row, in order. Read after its drain returned.
var rows []string

func deferDrain(t *testing.T, emb Embedder, every time.Duration, texts []string) (*spaceWorker, *Store, func() []string) {
	t.Helper()
	ctx := context.Background()
	st := mustStore(t, 4)
	var mu sync.Mutex
	var phases []string
	rows = nil
	ix := &Indexer{store: st, opts: Options{
		Embedder:      emb,
		EmbedBatch:    2,
		PendingEvery:  every,
		AnnounceAfter: -1,
		OnProcess: func(u ProcessUpdate) {
			mu.Lock()
			defer mu.Unlock()
			if u.Embedding != nil && (len(phases) == 0 || phases[len(phases)-1] != u.Embedding.Phase) {
				phases = append(phases, u.Embedding.Phase)
			}
			if u.Embedding != nil {
				row := u.Embedding.Phase + "[" + strings.Join(u.Embedding.Backends, ",") + "]"
				if len(rows) == 0 || rows[len(rows)-1] != row {
					rows = append(rows, row)
				}
			}
		},
	}.withDefaults()}
	for i, text := range texts {
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("m%02d", i), text, uint64(i+1))}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	return &spaceWorker{ix: ix, sp: staticIdSpace{}, embedCh: make(chan struct{}, 1)}, st, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), phases...)
	}
}

// writeNudged lands one chat text as an advance does: applied, then
// named to the embed loop if it went pending.
func writeNudged(t *testing.T, w *spaceWorker, st *Store, recordId, text string, seq uint64) {
	up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", recordId, text, seq)}
	_, marked, err := st.ApplyPageMarked(context.Background(), "sp1", []DocUpsert{up}, nil, nil, nil, &LinkOps{})
	if err != nil {
		t.Error(err)
	}
	if len(marked) > 0 {
		w.nudgeEmbed(marked)
	}
}

func waitPending(t *testing.T, st *Store, want int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		n, err := st.PendingCount(context.Background(), "sp1")
		if err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d docs pending, want %d", n, want)
		}
	}
}

// Texts the embedder defers do not hold the queue: every other text
// lands, past them and across pages, then the drain waits for the
// embedder's word without sending anything, and lands the rest.
func TestEmbedDrainGoesPastDeferredTexts(t *testing.T) {
	texts := []string{"long 1", "long 2", "long 3", "a", "b", "long 4", "c", "d", "e", "long 5"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, phases := deferDrain(t, emb, time.Hour, texts)
	done := make(chan error, 1)
	go func() { done <- w.drainPending(context.Background()) }()

	waitPending(t, st, 5) // the five short texts landed
	time.Sleep(100 * time.Millisecond)
	calls, sent := emb.counts()
	select {
	case err := <-done:
		t.Fatalf("the drain ended with texts deferred: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if again, _ := emb.counts(); again != calls {
		t.Fatalf("%d calls while waiting, %d before: the drain is not waiting", again, calls)
	}
	if len(sent) != len(texts) {
		t.Fatalf("sent %v, want each text once before the wait", sent)
	}
	if got := phases(); !slices.Equal(got, []string{EmbedPhaseEmbedding, EmbedPhasePreparing}) {
		t.Fatalf("phases %v, want embedding then preparing", got)
	}

	emb.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain did not end after the embedder's word")
	}
	waitPending(t, st, 0)
	if _, sent = emb.counts(); len(sent) != len(texts)+5 {
		t.Fatalf("sent %d texts, want the five deferred ones once more", len(sent))
	}
	if got := phases(); !slices.Equal(got, []string{EmbedPhaseEmbedding, EmbedPhasePreparing, EmbedPhaseEmbedding}) {
		t.Fatalf("phases %v, want embedding again after the wait", got)
	}
}

// An embedder that defers and never says when is asked again on the
// loop's own period: the drain neither ends nor spins.
func TestEmbedDrainRetriesDeferredTextsOnThePeriod(t *testing.T) {
	const every = 150 * time.Millisecond
	emb := &deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	w, st, _ := deferDrain(t, emb, every, []string{"long 1", "a", "long 2"})
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- w.drainPending(context.Background()) }()
	waitPending(t, st, 2)
	time.Sleep(3 * every)
	if calls, _ := emb.counts(); calls > 2+2*int(time.Since(start)/every+1) {
		t.Fatalf("%d calls in %s with a %s period: the drain spins", calls, time.Since(start), every)
	}
	emb.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain did not pick the deferred texts up")
	}
	waitPending(t, st, 0)
}

// A drain that waits for deferred texts leaves with its context.
func TestEmbedDrainLeavesTheDeferredWait(t *testing.T) {
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 1", "a"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the drain ended without an error and a text pending")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain outlived its context")
	}
}

func TestStore_PendingAfter(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	for _, i := range []int{3, 0, 4, 1, 2} { // written out of order
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("m%d", i), fmt.Sprintf("text %d", i), uint64(i+1))}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, _, err := st.Pending(ctx, "sp1", 10)
	if err != nil || len(all) != 5 || !slices.IsSorted(all) {
		t.Fatalf("Pending: %v, %v; want the five docs in id order", all, err)
	}
	ids, texts, err := st.PendingAfter(ctx, "sp1", all[1], 2)
	if err != nil || !slices.Equal(ids, all[2:4]) || len(texts) != 2 {
		t.Fatalf("PendingAfter(%q, 2) = %v, %v, %v; want %v", all[1], ids, texts, err, all[2:4])
	}
	// A doc written ahead of the cursor, or deleted behind it, moves nothing.
	up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", "a-first", "new", 9)}
	if err := st.Apply(ctx, "sp1", []DocUpsert{up}, []string{all[0]}, nil); err != nil {
		t.Fatal(err)
	}
	if ids, _, err = st.PendingAfter(ctx, "sp1", all[1], 2); err != nil || !slices.Equal(ids, all[2:4]) {
		t.Fatalf("PendingAfter after writes around the cursor = %v, %v; want %v", ids, err, all[2:4])
	}
	if ids, _, err = st.PendingAfter(ctx, "sp1", all[4], 2); err != nil || len(ids) != 0 {
		t.Fatalf("PendingAfter past the queue = %v, %v", ids, err)
	}
}

// The embedder's word in the middle of a pass is heard between pages: a
// text left behind is sent again then, not after everything behind it.
func TestEmbedDrainHearsTheWakeBetweenPages(t *testing.T) {
	texts := []string{"long 1", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{}), releaseAt: 2}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	if err := w.drainPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPending(t, st, 0)
	_, sent := emb.counts()
	again, last := -1, -1
	for i, text := range sent {
		if text == "long 1" && i > 0 {
			again = i
		}
		if text == "j" {
			last = i
		}
	}
	if again < 0 || last < 0 || again > last {
		t.Fatalf("sent %v, want the deferred text again before the end of the queue", sent)
	}
}

// A drain waiting for deferred texts takes the nudge for new ones: they
// do not wait for a function that is not theirs.
func TestEmbedDrainTakesTheNudgeWhileWaiting(t *testing.T) {
	ctx := context.Background()
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 1", "a"})
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1)
	time.Sleep(50 * time.Millisecond) // the drain is waiting now
	writeNudged(t, w, st, "m90", "a new short text", 90)
	waitPending(t, st, 1) // the new text landed; the long one still waits
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitPending(t, st, 0)
}

// A batch left pending whole clears the runtimes of the batch before it
// from the row, and the drain holds the embedder until it returns.
func TestEmbedDrainRowAndHoldUnderDeferral(t *testing.T) {
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"a", "b", "long 1", "long 2"})
	done := make(chan error, 1)
	go func() { done <- w.drainPending(context.Background()) }()
	waitPending(t, st, 2)
	time.Sleep(50 * time.Millisecond)
	emb.mu.Lock()
	held := emb.held
	emb.mu.Unlock()
	if held != 1 {
		t.Fatalf("%d holds on the embedder while the drain waits, want 1", held)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := []string{"embedding[]", "embedding[ane]", "embedding[]", "preparing[]", "embedding[ane]"}
	if !slices.Equal(rows, want) {
		t.Fatalf("row states %v, want %v", rows, want)
	}
	emb.mu.Lock()
	held = emb.held
	emb.mu.Unlock()
	if held != 0 {
		t.Fatalf("%d holds left after the drain", held)
	}
}

// The hold starts with the start delay and ends with a cancelled drain.
func TestEmbedDrainHoldsTheEmbedderUnderTheDelay(t *testing.T) {
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, _, _ := deferDrain(t, emb, time.Hour, []string{"a"})
	w.ix.embedFrom = time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		emb.mu.Lock()
		held := emb.held
		emb.mu.Unlock()
		if held == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the drain never held the embedder under its start delay")
		}
	}
	cancel()
	<-done
	emb.mu.Lock()
	defer emb.mu.Unlock()
	if emb.held != 0 {
		t.Fatalf("%d holds left after the drain was cancelled", emb.held)
	}
}

// nullEmbedder answers with no vector for the text "null" and declares
// nothing, as an HTTP embedder whose response has a null in it.
type nullEmbedder struct{ axisEmbedder }

func (e nullEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := e.axisEmbedder.EmbedDocs(ctx, texts)
	for i, text := range texts {
		if text == "null" {
			vecs[i] = nil
		}
	}
	return vecs, err
}

type nullRoutedEmbedder struct{ nullEmbedder }

func (e nullRoutedEmbedder) EmbedDocsRouted(ctx context.Context, _, texts []string) ([][]float32, EmbedRoute, error) {
	vecs, err := e.EmbedDocs(ctx, texts)
	return vecs, EmbedRoute{Backends: []string{EmbedBackendRemote}}, err
}

// A missing vector the embedder did not declare deferred is an error,
// and only that text's: the vectors beside it land, the pass goes on
// past it, and the drain ends in the error instead of waiting.
func TestEmbedDrainRefusesAnUndeclaredMissingVector(t *testing.T) {
	for name, emb := range map[string]Embedder{
		"an embedder without routes": nullEmbedder{axisEmbedder{dim: 4}},
		"a routed embedder":          nullRoutedEmbedder{nullEmbedder{axisEmbedder{dim: 4}}},
	} {
		// The missing one first in its batch, and more batches behind it.
		w, st, phases := deferDrain(t, emb, time.Hour, []string{"null", "b", "c", "d", "e"})
		done := make(chan error, 1)
		go func() { done <- w.drainPending(context.Background()) }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: the drain ended without an error", name)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the drain waits on a vector that is not coming", name)
		}
		if n, _ := st.PendingCount(context.Background(), "sp1"); n != 1 {
			t.Errorf("%s: %d docs pending, want the one without a vector", name, n)
		}
		if got := phases(); slices.Contains(got, EmbedPhasePreparing) {
			t.Errorf("%s: phases %v, want no preparing", name, got)
		}
	}
}

func TestDeclaredVectors(t *testing.T) {
	a := []float32{1}
	for _, tc := range []struct {
		vecs     [][]float32
		deferred int
		missing  int
		ok       bool
	}{
		{[][]float32{a, a}, 0, 0, true},
		{[][]float32{a, nil, a}, 1, 1, true},
		{[][]float32{a, nil, a}, 0, 1, false},
		{[][]float32{nil, a, nil}, 1, 2, false},
		{[][]float32{a, {}}, 0, 1, false},
		{[][]float32{a, a}, 1, 0, false}, // a deferral declared, none missing
		{[][]float32{a, nil}, 2, 1, false},
		{nil, 0, 0, true},
	} {
		got, err := declaredVectors(tc.vecs, tc.deferred)
		if got != tc.missing || (err == nil) != tc.ok {
			t.Errorf("declaredVectors(%v, %d) = %d missing, %v; want %d, ok %v", tc.vecs, tc.deferred, got, err, tc.missing, tc.ok)
		}
	}
}

// With a text left behind at the head and a new ready one written right
// behind it before every read, the cursor still moves on: the texts at
// the far end embed while the writes go on.
func TestEmbedDrainReachesTheEndUnderANudgePerPage(t *testing.T) {
	ctx := context.Background()
	texts := []string{"long 1", "z1", "z2", "z3", "z4", "z5", "z6"} // m00 … m06
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	const writes = 30
	emb.onCall = func(call int) {
		if call > writes {
			return
		}
		// Sorts after the deferred m00 and before every z.
		writeNudged(t, w, st, fmt.Sprintf("m00b%03d", call), "new", uint64(100+call))
	}
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1)
	_, sent := emb.counts()
	// Each call sends two texts: z6 is out well before the writes end.
	if at := slices.Index(sent, "z6"); at < 0 || at > writes {
		t.Fatalf("z6 was sent as text %d of %d, want it while the writes went on (%d calls)", at, len(sent), writes)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBulkOpenedFields(t *testing.T) {
	for _, tc := range []struct {
		delayed, prepared bool
		how               string
		by, embedder      string
	}{
		{false, false, "", "no_wait", ""},
		{true, false, BulkReady, "start_delay", BulkReady},
		{true, true, BulkReady, "embedder", BulkReady},
		{true, true, BulkTimeout, "embedder", BulkTimeout},
		{false, true, BulkNoChild, "embedder", BulkNoChild},
		{true, false, BulkNoPackage, "start_delay", BulkNoPackage},
	} {
		got := map[string]string{}
		for _, f := range bulkOpenedFields(tc.delayed, tc.prepared, tc.how, time.Second) {
			got[f.Key] = f.String
		}
		if got["openedBy"] != tc.by || got["embedder"] != tc.embedder {
			t.Errorf("bulkOpenedFields(%v, %v, %q) = %v; want openedBy %q, embedder %q", tc.delayed, tc.prepared, tc.how, got, tc.by, tc.embedder)
		}
	}
}

// New texts announced in the middle of a pass that left texts behind
// get a page of their own: a row that sorts ahead of the cursor does not
// wait for the end of the queue.
func TestEmbedDrainTakesTheNudgeBetweenPages(t *testing.T) {
	ctx := context.Background()
	texts := []string{"long 1", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	// On the second batch a text lands ahead of everything, with its nudge.
	emb.onCall = func(call int) {
		if call != 2 {
			return
		}
		writeNudged(t, w, st, "a-first", "new", 90)
	}
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1) // everything but the deferred text
	_, sent := emb.counts()
	at := func(text string) int { return slices.Index(sent, text) }
	if at("new") < 0 || at("k") < 0 || at("new") > at("k") {
		t.Fatalf("sent %v, want the new text before the end of the queue", sent)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// With texts left behind and new ready texts written ahead of them
// before every read, the drain still moves on: the rows behind the ones
// it left land, and so do the new ones, with the function still loading.
func TestEmbedDrainMovesOnUnderWritesAheadOfDeferredRows(t *testing.T) {
	ctx := context.Background()
	texts := []string{"long 1", "long 2", "c", "d", "e", "f"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	inserted := 0
	emb.onCall = func(call int) {
		if call > 6 {
			return
		}
		for range 2 { // two ready rows ahead of everything, and their nudge
			inserted++
			writeNudged(t, w, st, fmt.Sprintf("a%03d", 900-inserted), "new", uint64(100+inserted))
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 2) // everything but the two deferred texts
	if calls, _ := emb.counts(); calls > 40 {
		t.Fatalf("%d calls to get there", calls)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The store names the docs a page left newly pending: a text that
// embeds, and the windows of an object whose name it changed or removed.
func TestStore_ApplyPageMarked(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	body := "a paragraph long enough to be embedded as a document of this object"
	vec := []float32{1, 0, 0, 0}
	apply := func(ups []DocUpsert, dels []string) []string {
		t.Helper()
		_, marked, err := st.ApplyPageMarked(ctx, "sp1", ups, dels, nil, nil, &LinkOps{})
		if err != nil {
			t.Fatal(err)
		}
		return marked
	}
	windowId := docId("obj1", "editor_blocks", "win_a")
	name := entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Consoles", 1)
	window := entry("basic", "obj1", "editor_blocks", "win_a", body, 2)
	props := entry("basic", "obj1", "chat_messages", "m1", body, 3)
	props.Scope = index.ScopeProps
	short := entry("basic", "obj1", index.DatasetProp, "p1", "short", 4)

	if got := apply([]DocUpsert{{Entry: name}, {Entry: window}}, nil); !slices.Contains(got, windowId) {
		t.Fatalf("a text that embeds was not named: %v", got)
	}
	ids, _, _ := st.Pending(ctx, "sp1", 10)
	if err := st.SetVectors(ctx, "sp1", ids, slices.Repeat([][]float32{vec}, len(ids))); err != nil {
		t.Fatal(err)
	}
	if got := apply([]DocUpsert{{Entry: props}, {Entry: short}}, nil); len(got) != 0 {
		t.Fatalf("full-text-only writes marked %v pending", got)
	}
	if got := apply([]DocUpsert{{Entry: name}}, nil); len(got) != 0 {
		t.Fatalf("the same name again marked %v pending", got)
	}
	// The name removed, beside a write that embeds nothing.
	if got := apply([]DocUpsert{{Entry: short}}, []string{docId("obj1", index.DatasetProp, index.NamePropRecordId)}); !slices.Equal(got, []string{windowId}) {
		t.Fatalf("removing the name marked %v, want the object's window to re-embed", got)
	}
	if n, _ := st.PendingCount(ctx, "sp1"); n != 1 {
		t.Fatalf("%d docs pending after the name went, want the window", n)
	}
}

// PendingByIds answers for the docs it is asked about that still await
// embedding, each once, in the order asked.
func TestStore_PendingByIds(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	for i := range 4 {
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("m%d", i), fmt.Sprintf("text %d", i), uint64(i+1))}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, _, _ := st.Pending(ctx, "sp1", 10)
	if err := st.SetVectors(ctx, "sp1", all[1:2], [][]float32{{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	ids, texts, err := st.PendingByIds(ctx, "sp1", []string{all[3], all[1], "o1:chat_messages:gone", all[0], all[3]})
	if err != nil || !slices.Equal(ids, []string{all[3], all[0]}) || !slices.Equal(texts, []string{"text 3", "text 0"}) {
		t.Fatalf("PendingByIds = %v, %v, %v; want m3 and m0 with their texts", ids, texts, err)
	}
}

// With a backlog the embedder defers, a newly written text is embedded
// by itself: the texts left behind are not sent again for it, during the
// pass or while the drain waits.
func TestEmbedDrainDoesNotResendDeferredTextsForNewOnes(t *testing.T) {
	ctx := context.Background()
	const deferred = 100
	var texts []string
	for i := range deferred {
		texts = append(texts, fmt.Sprintf("long %d", i))
	}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, sent := emb.counts(); len(sent) >= deferred {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first pass did not finish")
		}
	}
	const writes = 10
	for i := range writes {
		writeNudged(t, w, st, fmt.Sprintf("n%02d", i), "short", uint64(1000+i))
		waitPending(t, st, deferred)
	}
	_, sent := emb.counts()
	resent, short := 0, 0
	for _, text := range sent[deferred:] {
		if text == "short" {
			short++
		} else {
			resent++
		}
	}
	if resent != 0 || short != writes {
		t.Fatalf("%d new texts were sent %d times and %d deferred texts sent again; want each new one once, alone", writes, short, resent)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Under nudges that name no doc, as a sweep's, the drain starts a pass
// over no more often than its period: it does not send the deferred
// backlog back to back.
func TestEmbedDrainStartsOverOncePerPeriodUnderUnnamedNudges(t *testing.T) {
	const deferred, every = 40, 400 * time.Millisecond
	var texts []string
	for i := range deferred {
		texts = append(texts, fmt.Sprintf("long %d", i))
	}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, _, _ := deferDrain(t, emb, every, texts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- w.drainPending(ctx) }()
	for time.Since(start) < 3*every {
		w.nudgeEmbed(nil)
		time.Sleep(10 * time.Millisecond)
	}
	_, sent := emb.counts()
	cancel()
	<-done
	// A pass at the start and one per period since, with one to spare.
	if passes := int(time.Since(start)/every) + 2; len(sent) > passes*deferred {
		t.Fatalf("%d texts sent in %s: more than %d passes over %d deferred texts with a %s period",
			len(sent), time.Since(start).Round(time.Millisecond), passes, deferred, every)
	}
}

// A new text that sorts past a page of deferred texts at the head, and
// before the cursor, is embedded at once: it does not wait for the end
// of the pass behind texts the head is full of.
func TestEmbedDrainEmbedsANewTextPastADeferredHead(t *testing.T) {
	var texts []string // m00..m03 deferred, m04..m39 ready
	for i := range 4 {
		texts = append(texts, fmt.Sprintf("long %d", i))
	}
	for i := 4; i < 40; i++ {
		texts = append(texts, fmt.Sprintf("z%02d", i))
	}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	emb.onCall = func(call int) {
		if call == 4 {
			// Sorts after the deferred head (m00..m03) and before the cursor.
			writeNudged(t, w, st, "m03x", "new", 900)
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.drainPending(context.Background()) }()
	waitPending(t, st, 4)
	_, sent := emb.counts()
	if at, last := slices.Index(sent, "new"), slices.Index(sent, "z39"); at < 0 || at > last {
		t.Fatalf("the new text was sent as text %d, the last ready one as %d: it waited for the end of the pass", at, last)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// With every pending text deferred and no word from the embedder, the
// row stays in `preparing` across the period's passes: nothing embeds.
func TestEmbedDrainRowStaysPreparingWhileEverythingIsDeferred(t *testing.T) {
	const every = 100 * time.Millisecond
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, _, _ := deferDrain(t, emb, every, []string{"long 1", "long 2", "long 3"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	time.Sleep(5*every + every/2)
	cancel()
	<-done
	if calls, _ := emb.counts(); calls < 4 {
		t.Fatalf("%d calls: the period's passes did not run", calls)
	}
	if want := []string{"embedding[]", "preparing[]"}; !slices.Equal(rows, want) {
		t.Fatalf("row states %v, want %v", rows, want)
	}
}

// titledDeferEmbedder defers texts starting with "long" and records the
// title each text was sent under.
type titledDeferEmbedder struct {
	axisEmbedder
	mu     sync.Mutex
	sent   []string
	onCall func(call int)
	calls  int
}

func (e *titledDeferEmbedder) EmbedsTitles() bool { return true }
func (e *titledDeferEmbedder) EmbedTitledDocs(ctx context.Context, _, texts []string) ([][]float32, error) {
	return e.EmbedDocs(ctx, texts)
}
func (e *titledDeferEmbedder) EmbedDocsRouted(_ context.Context, titles, texts []string) ([][]float32, EmbedRoute, error) {
	e.mu.Lock()
	e.calls++
	call, onCall := e.calls, e.onCall
	e.mu.Unlock()
	if onCall != nil {
		onCall(call)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]float32, len(texts))
	rt := EmbedRoute{Backends: []string{EmbedBackendANE}}
	for i, text := range texts {
		title := ""
		if titles != nil {
			title = titles[i]
		}
		e.sent = append(e.sent, text+"@"+title)
		if strings.HasPrefix(text, "long") {
			rt.Deferred++
			continue
		}
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	return out, rt, nil
}

// An object renamed while its pending window embeds sends no nudge: the
// window was pending already. Its vector is not landed, and with a text
// left behind the cursor is past it: the drain reads it again at once
// and embeds it under the new name, not after the period.
func TestEmbedDrainReembedsAWindowRenamedUnderItsEmbedding(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &titledDeferEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, opts: Options{Embedder: emb, EmbedBatch: 1, PendingEvery: time.Hour, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}, embedCh: make(chan struct{}, 1)}
	body := "a paragraph long enough to be embedded as a document of this object"
	ups := []DocUpsert{
		{Entry: entry("chat", "a0", "chat_messages", "m0", "long deferred", 1)},
		{Entry: entry("basic", "o5", index.DatasetProp, index.NamePropRecordId, "Old", 2)},
		{Entry: entry("basic", "o5", "editor_blocks", "win_a", body, 3)},
		{Entry: entry("chat", "z9", "chat_messages", "m1", "tail text", 4)},
	}
	if _, _, err := st.ApplyPageMarked(ctx, "sp1", ups, nil, nil, nil, &LinkOps{}); err != nil {
		t.Fatal(err)
	}
	emb.onCall = func(call int) {
		if call != 2 { // the window is the second pending doc
			return
		}
		rename := DocUpsert{Entry: entry("basic", "o5", index.DatasetProp, index.NamePropRecordId, "New", 10)}
		_, marked, err := st.ApplyPageMarked(ctx, "sp1", []DocUpsert{rename}, nil, nil, nil, &LinkOps{})
		if err != nil || len(marked) != 0 {
			t.Errorf("the rename marked %v, %v; want nothing newly pending", marked, err)
		}
	}
	dctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.drainPending(dctx) }()
	waitPending(t, st, 1) // the deferred text alone
	cancel()
	<-done
	emb.mu.Lock()
	defer emb.mu.Unlock()
	if !slices.Contains(emb.sent, body+"@New") {
		t.Fatalf("sent %v, want the window again under its new name", emb.sent)
	}
}

// writesNudged lands chat texts in one page, as an advance does, and
// names those that went pending in one nudge.
func writesNudged(t *testing.T, w *spaceWorker, st *Store, seq uint64, texts map[string]string) {
	var ups []DocUpsert
	for recordId, text := range texts {
		seq++
		ups = append(ups, DocUpsert{Entry: entry("chat", "o1", "chat_messages", recordId, text, seq)})
	}
	slices.SortFunc(ups, func(a, b DocUpsert) int { return strings.Compare(a.Entry.RecordId, b.Entry.RecordId) })
	_, marked, err := st.ApplyPageMarked(context.Background(), "sp1", ups, nil, nil, nil, &LinkOps{})
	if err != nil {
		t.Error(err)
	}
	slices.Sort(marked)
	w.nudgeEmbed(marked)
}

// Named docs are embedded in the order they were named: one that waits
// behind the doc being embedded is next, however many are named while
// each page embeds. All sort before the cursor, which does not read them.
func TestEmbedDrainKeepsNamedDocsInOrderUnderArrivals(t *testing.T) {
	ctx := context.Background()
	const arrivals = 12
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 0"})
	w.ix.opts.EmbedBatch = 1 // a doc to a page
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1)
	time.Sleep(50 * time.Millisecond) // the drain is waiting, its cursor at the end
	arrived := 0
	emb.mu.Lock()
	emb.onCall = func(int) {
		if arrived < arrivals {
			arrived++
			writeNudged(t, w, st, fmt.Sprintf("b%02d", arrived), fmt.Sprintf("later %d", arrived), uint64(500+arrived))
		}
	}
	emb.mu.Unlock()
	writesNudged(t, w, st, 100, map[string]string{"a1": "first", "a2": "second"})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, sent := emb.counts(); len(sent) >= 1+2+arrivals {
			break
		}
		if time.Now().After(deadline) {
			_, sent := emb.counts()
			t.Fatalf("sent %v, want the two named docs and %d later ones", sent, arrivals)
		}
	}
	_, sent := emb.counts()
	if first, second := slices.Index(sent, "first"), slices.Index(sent, "second"); first < 0 || second != first+1 {
		t.Fatalf("sent %v: the second named doc did not follow the first", sent)
	}
	if n := len(sent) - 1 - 2 - arrivals; n != 0 {
		t.Fatalf("sent %v: %d texts more than each new one once", sent, n)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Docs named while every page of named docs embeds do not put off the
// period: the texts left behind are still sent again once it has passed.
func TestEmbedDrainKeepsThePeriodUnderNamedNudges(t *testing.T) {
	const every = 200 * time.Millisecond
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, every, []string{"long 0"})
	until := time.Now().Add(4 * every)
	arrived := 0
	emb.onCall = func(int) {
		if time.Now().Before(until) {
			arrived++
			time.Sleep(2 * time.Millisecond)
			// Before the cursor: the pass has read to its end.
			writeNudged(t, w, st, fmt.Sprintf("a%04d", arrived), "later", uint64(500+arrived))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	time.Sleep(time.Until(until))
	cancel()
	<-done
	_, sent := emb.counts()
	again := 0
	for _, text := range sent {
		if text == "long 0" {
			again++
		}
	}
	if again < 3 || again > 6 {
		t.Fatalf("the text left behind was sent %d times in four periods of named nudges; want once a period", again)
	}
}

// A newly written text that is itself deferred is sent once, wherever it
// sorts and whichever read finds it first, and the row does not leave
// `preparing` for a page that embedded nothing.
func TestEmbedDrainSendsANewDeferredTextOnce(t *testing.T) {
	count := func(sent []string, text string) (n int) {
		for _, s := range sent {
			if s == text {
				n++
			}
		}
		return n
	}
	t.Run("cursor at the end", func(t *testing.T) {
		emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
		w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 0"})
		done := make(chan error, 1)
		go func() { done <- w.drainPending(context.Background()) }()
		waitPending(t, st, 1)
		time.Sleep(50 * time.Millisecond) // the drain is waiting
		// Sorts after everything the cursor read, then one before it.
		writeNudged(t, w, st, "m99", "long new", 99)
		waitPending(t, st, 2)
		writeNudged(t, w, st, "a00", "short", 100)
		waitPending(t, st, 2)
		time.Sleep(50 * time.Millisecond)
		_, sent := emb.counts()
		if n := count(sent, "long new"); n != 1 {
			t.Fatalf("sent %v: the new deferred text went out %d times, want once", sent, n)
		}
		if n := count(sent, "long 0"); n != 1 {
			t.Fatalf("sent %v: the text left behind went out %d times, want once", sent, n)
		}
		emb.release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := []string{"embedding[]", "preparing[]", "embedding[" + EmbedBackendANE + "]", "preparing[]", "embedding[" + EmbedBackendANE + "]"}
		if !slices.Equal(rows, want) {
			t.Fatalf("row states %v, want %v", rows, want)
		}
	})
	t.Run("ahead of the cursor", func(t *testing.T) {
		texts := []string{"long 0"} // m00 deferred, m01..m20 ready
		for i := 1; i <= 20; i++ {
			texts = append(texts, fmt.Sprintf("z%02d", i))
		}
		emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
		w, st, _ := deferDrain(t, emb, time.Hour, texts)
		emb.onCall = func(call int) {
			if call == 2 {
				writeNudged(t, w, st, "m90", "long new", 900) // past the cursor
			}
		}
		done := make(chan error, 1)
		go func() { done <- w.drainPending(context.Background()) }()
		waitPending(t, st, 2)
		time.Sleep(50 * time.Millisecond)
		_, sent := emb.counts()
		if n := count(sent, "long new"); n != 1 {
			t.Fatalf("sent %v: the new deferred text went out %d times, want once", sent, n)
		}
		emb.release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		waitPending(t, st, 0)
	})
}

// A new deferred text the cursor reaches before its name's turn is sent
// once too: the named read that follows does not send the same input.
func TestEmbedDrainSendsADeferredTextTheCursorReadFirstOnce(t *testing.T) {
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 0", "s1", "s2", "s3"})
	w.ix.opts.EmbedBatch = 1 // a doc to a page
	emb.onCall = func(call int) {
		switch call {
		case 2: // the cursor's page; the next turn is this name's
			writeNudged(t, w, st, "a00", "short", 100)
		case 3: // the named page; the cursor's next row is this one
			writeNudged(t, w, st, "m01x", "long new", 101)
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.drainPending(context.Background()) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, sent := emb.counts(); slices.Contains(sent, "s3") {
			break
		}
		if time.Now().After(deadline) {
			_, sent := emb.counts()
			t.Fatalf("sent %v: the cursor never reached the end", sent)
		}
	}
	waitPending(t, st, 2)
	_, sent := emb.counts()
	want := []string{"long 0", "s1", "short", "long new", "s2", "s3"}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	emb.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// longTitleEmbedder defers a text that starts with "long" and a text
// under a title that does, and records what it was sent.
type longTitleEmbedder struct {
	titledDeferEmbedder
}

func (e *longTitleEmbedder) EmbedDocsRouted(ctx context.Context, titles, texts []string) ([][]float32, EmbedRoute, error) {
	held := make([]string, len(texts))
	for i, text := range texts {
		held[i] = text
		if titles != nil && strings.HasPrefix(titles[i], "long") {
			held[i] = "long " + text
		}
	}
	vecs, rt, err := e.titledDeferEmbedder.EmbedDocsRouted(ctx, titles, held)
	return vecs, rt, err
}

// A window deferred by name ahead of the cursor and renamed since, which
// names nothing, is sent again when the cursor reaches it: under its new
// title it is another input, which can fit a function that is loaded.
func TestEmbedDrainSendsADeferredWindowAgainUnderANewTitle(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	emb := &longTitleEmbedder{titledDeferEmbedder{axisEmbedder: axisEmbedder{dim: 4}}}
	ix := &Indexer{store: st, opts: Options{Embedder: emb, EmbedBatch: 1, PendingEvery: time.Hour, AnnounceAfter: -1}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}, embedCh: make(chan struct{}, 1)}
	apply := func(nudge bool, ups ...DocUpsert) {
		_, marked, err := st.ApplyPageMarked(ctx, "sp1", ups, nil, nil, nil, &LinkOps{})
		if err != nil {
			t.Error(err)
		}
		if nudge {
			w.nudgeEmbed(marked)
		} else if len(marked) != 0 {
			t.Errorf("the rename marked %v; want nothing newly pending", marked)
		}
	}
	if err := st.Apply(ctx, "sp1", []DocUpsert{
		{Entry: entry("chat", "a0", "chat_messages", "m0", "long deferred", 1)},
		{Entry: entry("chat", "b1", "chat_messages", "m1", "ready", 2)},
		{Entry: entry("chat", "z9", "chat_messages", "m2", "tail text", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	body := "a paragraph long enough to be embedded as a document of this object"
	emb.onCall = func(call int) {
		switch call {
		case 2: // the cursor is at b1: the window is written past it, and named
			apply(true,
				DocUpsert{Entry: entry("basic", "o5", index.DatasetProp, index.NamePropRecordId, "long old name", 10)},
				DocUpsert{Entry: entry("basic", "o5", "editor_blocks", "win_a", body, 11)})
		case 3: // the window's own page, by name
			apply(false, DocUpsert{Entry: entry("basic", "o5", index.DatasetProp, index.NamePropRecordId, "New", 12)})
		}
	}
	dctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.drainPending(dctx) }()
	waitPending(t, st, 1) // the deferred text alone
	cancel()
	<-done
	emb.mu.Lock()
	defer emb.mu.Unlock()
	if !slices.Contains(emb.sent, "long "+body+"@long old name") || !slices.Contains(emb.sent, body+"@New") {
		t.Fatalf("sent %v, want the window under its old title and again under the new one", emb.sent)
	}
}

// The embedder's word in the middle of a pass starts it over on the
// texts left behind: the row says `preparing` from there until one of
// them embeds, not `embedding` over texts that are deferred again.
func TestEmbedDrainRowIsPreparingOverAPassStartedOver(t *testing.T) {
	texts := []string{"a", "long 1", "long 2", "long 3", "long 4", "long 5", "long 6", "long 7"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, texts)
	var during []string // the row at each call of the pass started over
	emb.onCall = func(call int) {
		switch {
		case call == 2:
			// A word that readies none of these texts.
			emb.mu.Lock()
			close(emb.wake)
			emb.wake = make(chan struct{})
			emb.mu.Unlock()
		case call > 2:
			during = append(during, rows[len(rows)-1])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if calls, _ := emb.counts(); calls >= 6 { // two pages, then four over the texts left
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass was not started over")
		}
	}
	waitPending(t, st, 7)
	cancel()
	<-done
	if len(during) == 0 || slices.ContainsFunc(during, func(row string) bool { return row != "preparing[]" }) {
		t.Fatalf("rows during the pass started over: %v, want preparing throughout", during)
	}
}
