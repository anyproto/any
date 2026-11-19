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
	up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", "m90", "a new short text", 90)}
	if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
		t.Fatal(err)
	}
	waitPending(t, st, 2)
	w.embedCh <- struct{}{}
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
	want := []string{"embedding[]", "embedding[ane]", "embedding[]", "preparing[]", "embedding[]", "embedding[ane]"}
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

// nullEmbedder answers with no vector for its second text and declares
// nothing, as an HTTP embedder whose response has a null in it.
type nullEmbedder struct {
	axisEmbedder
	routed bool
}

func (e nullEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := e.axisEmbedder.EmbedDocs(ctx, texts)
	if len(vecs) > 1 {
		vecs[1] = nil
	}
	return vecs, err
}

type nullRoutedEmbedder struct{ nullEmbedder }

func (e nullRoutedEmbedder) EmbedDocsRouted(ctx context.Context, _, texts []string) ([][]float32, EmbedRoute, error) {
	vecs, err := e.EmbedDocs(ctx, texts)
	return vecs, EmbedRoute{Backends: []string{EmbedBackendRemote}}, err
}

// A missing vector the embedder did not declare deferred is an error:
// the drain lands what came before it and fails, it does not wait.
func TestEmbedDrainRefusesAnUndeclaredMissingVector(t *testing.T) {
	for name, emb := range map[string]Embedder{
		"an embedder without routes": nullEmbedder{axisEmbedder: axisEmbedder{dim: 4}},
		"a routed embedder":          nullRoutedEmbedder{nullEmbedder{axisEmbedder: axisEmbedder{dim: 4}}},
	} {
		w, st, phases := deferDrain(t, emb, time.Hour, []string{"a", "b"})
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
		waitPending(t, st, 1)
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
		keep     int
		ok       bool
	}{
		{[][]float32{a, a}, 0, 2, true},
		{[][]float32{a, nil, a}, 1, 3, true},
		{[][]float32{a, nil, a}, 0, 1, false},
		{[][]float32{nil, a, nil}, 1, 0, false},
		{[][]float32{a, {}}, 0, 1, false},
		{[][]float32{a, a}, 1, 2, false}, // a deferral declared, none missing
		{[][]float32{a, nil}, 2, 1, false},
		{nil, 0, 0, true},
	} {
		got, err := declaredVectors(tc.vecs, tc.deferred)
		if len(got) != tc.keep || (err == nil) != tc.ok {
			t.Errorf("declaredVectors(%v, %d) = %d vectors, %v; want %d, ok %v", tc.vecs, tc.deferred, len(got), err, tc.keep, tc.ok)
		}
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
// start it over once it landed as much as that resends: a row that sorts
// ahead of the offset does not wait for the end of the queue.
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
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", "a-first", "new", 90)}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Error(err)
		}
		w.embedCh <- struct{}{}
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
			up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("a%03d", 900-inserted), "new", uint64(100+inserted))}
			if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
				t.Error(err)
			}
		}
		select {
		case w.embedCh <- struct{}{}:
		default:
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

// The store says whether a page left a doc newly pending: a text that
// embeds, or the windows of an object whose name it changed or removed.
func TestStore_ApplyPageMarked(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	st.SetEmbedTitles(true)
	body := "a paragraph long enough to be embedded as a document of this object"
	vec := []float32{1, 0, 0, 0}
	apply := func(ups []DocUpsert, dels []string) bool {
		t.Helper()
		_, marked, err := st.ApplyPageMarked(ctx, "sp1", ups, dels, nil, nil, &LinkOps{})
		if err != nil {
			t.Fatal(err)
		}
		return marked
	}
	name := entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Consoles", 1)
	window := entry("basic", "obj1", "editor_blocks", "win_a", body, 2)
	props := entry("basic", "obj1", "chat_messages", "m1", body, 3)
	props.Scope = index.ScopeProps
	short := entry("basic", "obj1", index.DatasetProp, "p1", "short", 4)

	if !apply([]DocUpsert{{Entry: name}, {Entry: window}}, nil) {
		t.Fatal("a text that embeds marked nothing")
	}
	ids, _, _ := st.Pending(ctx, "sp1", 10)
	if err := st.SetVectors(ctx, "sp1", ids, slices.Repeat([][]float32{vec}, len(ids))); err != nil {
		t.Fatal(err)
	}
	if apply([]DocUpsert{{Entry: props}, {Entry: short}}, nil) {
		t.Fatal("full-text-only writes marked a doc pending")
	}
	if apply([]DocUpsert{{Entry: name}}, nil) {
		t.Fatal("the same name again marked a doc pending")
	}
	// The name removed, beside a write that embeds nothing.
	if !apply([]DocUpsert{{Entry: short}}, []string{docId("obj1", index.DatasetProp, index.NamePropRecordId)}) {
		t.Fatal("removing the name marked nothing, with the object's window to re-embed")
	}
	if n, _ := st.PendingCount(ctx, "sp1"); n != 1 {
		t.Fatalf("%d docs pending after the name went, want the window", n)
	}
}
