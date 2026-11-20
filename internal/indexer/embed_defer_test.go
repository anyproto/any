//go:build llamacpp && !android && !ios

package indexer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/anyproto/any/internal/config"
)

// logLines collects a neural engine's status lines.
type logLines struct {
	mu    chan struct{}
	lines []string
}

func newLogLines() *logLines { return &logLines{mu: make(chan struct{}, 1)} }

func (l *logLines) logf(format string, args ...any) {
	l.mu <- struct{}{}
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
	<-l.mu
}

// all is a copy of the lines so far.
func (l *logLines) all() []string {
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	return append([]string(nil), l.lines...)
}

// index is the position of the first line that holds part; -1 = none.
func (l *logLines) index(part string) int {
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	for i, line := range l.lines {
		if strings.Contains(line, part) {
			return i
		}
	}
	return -1
}

func (l *logLines) has(part string) bool {
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	for _, line := range l.lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}

// A document of bulk work whose function is loading is neither embedded
// nor left to llama.cpp; a query in the same state is llama.cpp's. Every
// other miss stays a miss for both.
func TestNeuralEngine_BulkDefersWhileLoading(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 8)}
	n, _ := newTestANE(t, f)
	n.start()
	if got := <-f.started; got != 128 {
		t.Fatalf("loading s%d first, want s128", got)
	}
	bulk := func(tokens int) (served, deferred bool, miss string) {
		t.Helper()
		_, ok, d := n.embedBulk(tokensOf(tokens))
		return ok, d, n.lastMiss()
	}

	// The first function is loading: a document waits, a query does not.
	if ok, deferred, _ := bulk(10); ok || !deferred {
		t.Fatalf("a document under the first load: served %v, deferred %v", ok, deferred)
	}
	if _, ok := n.embed(tokensOf(10)); ok || n.lastMiss() != EmbedReasonFunctionLoading {
		t.Fatalf("a query under the first load: served %v, miss %q", ok, n.lastMiss())
	}
	// Longer than every function: llama.cpp's, at once.
	if ok, deferred, miss := bulk(100000); ok || deferred || miss != EmbedReasonTooLong {
		t.Fatalf("too long: served %v, deferred %v, miss %q", ok, deferred, miss)
	}

	// s128 loads; a 200-token document waits for s256, a short one runs.
	f.gate <- struct{}{}
	n.loads.Wait()
	if ok, deferred, _ := bulk(10); !ok || deferred {
		t.Fatalf("a document that fits a loaded function: served %v, deferred %v", ok, deferred)
	}
	if ok, deferred, _ := bulk(200); ok || !deferred {
		t.Fatalf("a document whose function loads: served %v, deferred %v", ok, deferred)
	}
	if got := <-f.started; got != 256 {
		t.Fatalf("loading s%d, want s256", got)
	}
	f.gate <- struct{}{}
	n.loads.Wait()
	if ok, deferred, _ := bulk(200); !ok || deferred {
		t.Fatalf("after the load: served %v, deferred %v", ok, deferred)
	}

	// A function that failed to load is not waited for.
	f.mu.Lock()
	f.loadErr = map[int]error{512: errRefused}
	f.mu.Unlock()
	if ok, deferred, _ := bulk(400); ok || !deferred {
		t.Fatalf("a document whose function is about to fail: served %v, deferred %v", ok, deferred)
	}
	<-f.started
	f.gate <- struct{}{}
	n.loads.Wait()
	if ok, deferred, miss := bulk(400); ok || deferred || miss != EmbedReasonANEFailed {
		t.Fatalf("a failed function: served %v, deferred %v, miss %q", ok, deferred, miss)
	}
	// The path off: llama.cpp embeds everything.
	n.close()
	if ok, deferred, miss := bulk(10); ok || deferred || miss != EmbedReasonANEOff {
		t.Fatalf("path off: served %v, deferred %v, miss %q", ok, deferred, miss)
	}
}

var errRefused = errors.New("refused")

// A larger loaded function serves a document whose own function loads.
func TestNeuralEngine_BulkUsesLargerLoadedFunction(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	loadFor(n, 400) // s512, after s128
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	if _, ok, deferred := n.embedBulk(tokensOf(200)); !ok || deferred {
		t.Fatalf("served %v, deferred %v; want s512 to serve it while s256 loads", ok, deferred)
	}
	<-f.started
	close(f.gate)
	n.loads.Wait()
}

// The wait for a function is bounded from the first document deferred
// for it: past it llama.cpp embeds the document, with the timeout as the
// reason, and the engine says so once.
func TestNeuralEngine_BulkWaitIsBounded(t *testing.T) {
	f := &fakeANE{}
	log := newLogLines()
	n, clock := newTestANE(t, f)
	n.logf = log.logf
	n.wait = 150 * time.Millisecond
	loadFor(n, 10)
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()

	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || !deferred {
		t.Fatalf("served %v, deferred %v; want a wait", ok, deferred)
	}
	<-f.started
	clock.add(n.wait - time.Millisecond)
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("stopped waiting before the bound")
	}
	clock.add(time.Millisecond)
	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || deferred || n.lastMiss() != EmbedReasonReadinessTimeout {
		t.Fatalf("past the bound: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
	for deadline := time.Now().Add(5 * time.Second); !log.has("s256 still loading after"); {
		if time.Now().After(deadline) {
			t.Fatal("the engine never reported the late function")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The load lands: the function serves, and the next wait starts over.
	close(f.gate)
	n.loads.Wait()
	if _, ok, _ := n.embedBulk(tokensOf(200)); !ok {
		t.Fatal("not served after the load")
	}
	if len(n.owed) != 0 || len(n.late) != 0 {
		t.Fatalf("waits left after a document got its function: %v, %d timers", n.owed, len(n.late))
	}
}

// A document that embeds on a larger function does not end the wait for
// the one it was meant for: that one is still loading.
func TestNeuralEngine_BulkOnALargerFunctionKeepsTheSmallerOnesWait(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 2)
	f.mu.Unlock()
	release := sync.OnceFunc(func() { close(f.gate) })
	defer release()
	n.mu.Lock()
	n.requestLocked(512) // ahead of s256 in the loader
	n.mu.Unlock()
	<-f.started
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("not deferred")
	}
	f.gate <- struct{}{} // s512 lands; s256 is next, and held
	<-f.started
	if _, ok, _ := n.embedBulk(tokensOf(200)); !ok {
		t.Fatal("not served by the larger function")
	}
	n.mu.Lock()
	owed := n.owed[256]
	n.mu.Unlock()
	if owed.IsZero() {
		t.Fatal("embedding on s512 ended the wait for s256, which is still loading")
	}
}

// The loader takes the smallest queued function first, whichever was
// asked for first.
func TestNeuralEngine_LoadsTheSmallestQueuedFirst(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 4)}
	n, _ := newTestANE(t, f)
	n.start()
	if got := <-f.started; got != 128 {
		t.Fatalf("first load is s%d", got)
	}
	// Both are queued behind the load in flight, the larger one first.
	for _, tokens := range []int{400, 200} {
		if _, _, deferred := n.embedBulk(tokensOf(tokens)); !deferred {
			t.Fatalf("%d-token document not deferred", tokens)
		}
	}
	f.gate <- struct{}{}
	got := <-f.started
	close(f.gate)
	n.loads.Wait()
	if got != 256 {
		t.Fatalf("the loader took s%d ahead of the smaller s256", got)
	}
}

// A text whose function failed goes to the next larger one, which is
// loaded for it and then kept as the smallest in use; llama.cpp gets it
// only when no function that holds it is left.
func TestNeuralEngine_FailedFunctionsTextsTakeTheNextOne(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{128: errors.New("broken")}}
	n, clock := newTestANE(t, f)
	n.idle = time.Minute
	n.start()
	n.loads.Wait()
	if _, ok, deferred := n.embedBulk(tokensOf(10)); ok || !deferred {
		t.Fatalf("a document of the failed s128: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
	n.loads.Wait()
	if _, ok, _ := n.embedBulk(tokensOf(10)); !ok {
		t.Fatalf("not served after s256 loaded: miss %q", n.lastMiss())
	}
	if _, served, _, _ := f.snapshot(); !reflect.DeepEqual(served, []int{256}) {
		t.Fatalf("served by %v, want s256", served)
	}
	clock.add(2 * time.Minute)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("released %v: s256 is the smallest function left", closed)
	}
	// The last function that holds a text failed: llama.cpp embeds it.
	f.mu.Lock()
	f.loadErr[512] = errors.New("broken")
	f.mu.Unlock()
	if _, ok, deferred := n.embedBulk(tokensOf(400)); ok || !deferred {
		t.Fatalf("a 400-token document: served %v, deferred %v", ok, deferred)
	}
	n.loads.Wait()
	if _, ok, deferred := n.embedBulk(tokensOf(400)); ok || deferred || n.lastMiss() != EmbedReasonANEFailed {
		t.Fatalf("after s512 failed: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
}

// A function that finishes loading after its documents' wait ran out is
// owed to nobody: it is released as idle like any other.
func TestNeuralEngine_LateLoadIsNotKeptForever(t *testing.T) {
	f := &fakeANE{}
	log := newLogLines()
	n, clock := newTestANE(t, f)
	n.logf = log.logf
	n.wait = 50 * time.Millisecond
	loadFor(n, 10)
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	release := sync.OnceFunc(func() { close(f.gate) })
	defer release()
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("not deferred")
	}
	<-f.started
	for deadline := time.Now().Add(10 * time.Second); !log.has("s256 still loading after"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the wait never ran out")
		}
	}
	clock.add(n.wait) // its documents went to llama.cpp; then the load lands
	release()
	n.loads.Wait()
	n.mu.Lock()
	owed := len(n.owed)
	n.mu.Unlock()
	if owed != 0 {
		t.Fatal("a function that loaded late is still owed to a document")
	}
	clock.add(n.idle + time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 1 || closed[0] != 256 {
		t.Fatalf("released %v, want the late s256", closed)
	}
}

// A document deferred for a package that landed since the last look has
// the loader look again at once, not at the retry.
func TestNeuralEngine_DeferralForALandedPackageStartsTheLoader(t *testing.T) {
	f := &fakeANE{absent: true, prepErrs: []error{errANENotReady, nil}}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	f.mu.Lock()
	f.absent = false
	f.mu.Unlock()
	if _, ok, deferred := n.embedBulk(tokensOf(10)); ok || !deferred {
		t.Fatalf("served %v, deferred %v, miss %q; want a wait", ok, deferred, n.lastMiss())
	}
	n.loads.Wait()
	if _, ok, _ := n.embedBulk(tokensOf(10)); !ok {
		t.Fatalf("nothing loaded for the deferred document: miss %q", n.lastMiss())
	}
}

// A package that is on disk and still being prepared is waited for like
// a function that loads; one that is not there is llama.cpp's at once.
func TestNeuralEngine_BulkWaitsForAPackageBeingPrepared(t *testing.T) {
	f := &fakeANE{prepGate: make(chan struct{})}
	n, clock := newTestANE(t, f)
	release := sync.OnceFunc(func() { close(f.prepGate) })
	defer release() // before the engine's cleanup, which waits for the loader
	n.start()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		if _, _, _, prepares := f.snapshot(); prepares == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preparation never began")
		}
	}
	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || !deferred {
		t.Fatalf("under preparation: served %v, deferred %v, miss %q; want a wait", ok, deferred, n.lastMiss())
	}
	if _, ok := n.embed(tokensOf(200)); ok || n.lastMiss() != EmbedReasonPackageNotReady {
		t.Fatalf("a query under preparation: served %v, miss %q", ok, n.lastMiss())
	}
	// The wait is the function's own, and bounded like any.
	clock.add(n.wait)
	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || deferred || n.lastMiss() != EmbedReasonReadinessTimeout {
		t.Fatalf("past the bound: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
	release()
	n.loads.Wait()

	f = &fakeANE{absent: true, prepErrs: []error{errANENotReady}}
	n, _ = newTestANE(t, f)
	n.start()
	n.loads.Wait()
	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || deferred || n.lastMiss() != EmbedReasonPackageNotReady {
		t.Fatalf("no package: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
}

// A function loaded for a deferred document is not released as idle
// before the document came back for it, for as long as its wait runs;
// the document's deadline is the one of its first deferral.
func TestNeuralEngine_DeferredFunctionIsKeptForItsDocument(t *testing.T) {
	f := &fakeANE{}
	n, clock := newTestANE(t, f)
	n.wait = time.Hour
	loadFor(n, 10)
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("not deferred")
	}
	n.loads.Wait() // s256 is loaded; the drain is busy elsewhere
	clock.add(n.idle + time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("released %v with a document waiting for it", closed)
	}
	n.mu.Lock()
	owed := n.owed[256]
	n.mu.Unlock()
	if owed.IsZero() {
		t.Fatal("the load ended the document's wait before the document came back")
	}
	// The document comes back: served, and the function is ordinary again.
	if _, ok, _ := n.embedBulk(tokensOf(200)); !ok {
		t.Fatal("not served by the function loaded for it")
	}
	clock.add(n.idle + time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 1 || closed[0] != 256 {
		t.Fatalf("released %v, want s256 once its document was embedded", closed)
	}

	// A document that never comes back holds the function only until its
	// wait is over.
	f = &fakeANE{}
	n, clock = newTestANE(t, f)
	n.wait = 100 * time.Millisecond
	loadFor(n, 10)
	n.embedBulk(tokensOf(200))
	n.loads.Wait()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		n.mu.Lock()
		left := len(n.owed)
		n.mu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wait never ended")
		}
	}
	clock.add(n.idle + time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 1 {
		t.Fatalf("released %v, want s256 after the wait", closed)
	}
}

// The child routes a bulk frame text by text: a deferred one takes a nil
// vector and its position is reported; without bulk nothing is deferred.
func TestLocal_BulkFrameDefers(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	l := &Local{ane: n, nCtx: 2048, batchDocs: 1}
	toks := func(n int) []llama.Token { return make([]llama.Token, n) }
	tokenized := [][]llama.Token{toks(10), toks(200), toks(20), toks(300)}
	lens := []int{10, 200, 20, 300}

	vecs, err := l.embedTokenized(context.Background(), tokenized, lens, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 4 || vecs[0] == nil || vecs[1] != nil || vecs[2] == nil || vecs[3] != nil {
		t.Fatalf("vectors %v, want the two short texts embedded and the two longer left", vecs)
	}
	if rt := l.routes; rt.ANE != 2 || rt.Llama != 0 || !reflect.DeepEqual(rt.Deferred, []int{1, 3}) {
		t.Fatalf("routes %+v, want 2 on the neural engine and texts 1 and 3 deferred", rt)
	}
	<-f.started
	close(f.gate)
	n.loads.Wait()
	if vecs, err = l.embedTokenized(context.Background(), tokenized, lens, true); err != nil || vecs[1] == nil || vecs[3] == nil {
		t.Fatalf("after the load: %v, %v", vecs, err)
	}
	if rt := l.routes; rt.ANE != 4 || len(rt.Deferred) != 0 {
		t.Fatalf("routes after the load: %+v", rt)
	}
}

func TestPackVectors(t *testing.T) {
	a, b, c := []float32{1}, []float32{2}, []float32{3}
	for _, tc := range []struct {
		sparse [][]float32
		at     []int
	}{
		{[][]float32{a, b, c}, nil},
		{[][]float32{nil, a, b}, []int{0}},
		{[][]float32{a, nil, nil, b, nil}, []int{1, 2, 4}},
		{[][]float32{nil, nil}, []int{0, 1}},
	} {
		packed := packVectors(tc.sparse)
		if len(packed)+len(tc.at) != len(tc.sparse) {
			t.Fatalf("%v packed to %d vectors", tc.sparse, len(packed))
		}
		got, err := unpackVectors(packed, tc.at, len(tc.sparse))
		if err != nil || !reflect.DeepEqual(got, tc.sparse) {
			t.Errorf("round trip of %v: %v, %v", tc.sparse, got, err)
		}
	}
	for _, bad := range []struct {
		vecs [][]float32
		at   []int
		n    int
	}{
		{[][]float32{a}, nil, 2},            // a vector short
		{[][]float32{a}, []int{1, 1}, 3},    // a position twice
		{[][]float32{a}, []int{2, 1}, 3},    // descending
		{[][]float32{a, b}, []int{3}, 3},    // past the frame
		{[][]float32{a}, []int{-1}, 2},      // before it
		{[][]float32{a, b, c}, []int{0}, 3}, // a vector too many
	} {
		if got, err := unpackVectors(bad.vecs, bad.at, bad.n); err == nil {
			t.Errorf("unpackVectors(%d vectors, %v, %d) = %v, want an error", len(bad.vecs), bad.at, bad.n, got)
		}
	}
}

// Bulk work gets no vector for a text the child deferred, and the count
// of them; an ordinary document call gets every vector.
func TestWorkerEmbedder_BulkDefers(t *testing.T) {
	ctx := context.Background()
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_DEFER=long")
	texts := []string{"a", "long one", "b", "long two"}
	vecs, rt, err := w.EmbedDocsRouted(ctx, nil, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 4 || vecs[0] == nil || vecs[1] != nil || vecs[2] == nil || vecs[3] != nil {
		t.Fatalf("vectors %v, want none for the two deferred texts", vecs)
	}
	if rt.Deferred != 2 {
		t.Fatalf("route %+v, want 2 deferred", rt)
	}
	if vecs, err = w.EmbedDocs(ctx, texts); err != nil || len(vecs) != 4 || vecs[1] == nil || vecs[3] == nil {
		t.Fatalf("EmbedDocs: %v, %v; want every vector", vecs, err)
	}
	// The wait for the first function ran out: that ends the startup
	// wait, not a later function's own.
	w.giveUpBulk()
	if vecs, rt, err = w.EmbedDocsRouted(ctx, nil, texts); err != nil || vecs[1] != nil || vecs[3] != nil || rt.Deferred != 2 {
		t.Fatalf("after the startup wait ran out: %v, %+v, %v; want the two texts deferred still", vecs, rt, err)
	}
}

// The wake closes when the child reports on its functions, and when the
// child goes.
func TestWorkerEmbedder_DeferredWake(t *testing.T) {
	const delay = 200 * time.Millisecond
	ctx := context.Background()
	w, _ := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	// "starting" may still be on its way: take the wake after it.
	time.Sleep(delay / 4)
	wake := w.DeferredWake()
	select {
	case <-wake:
		t.Fatal("woke with nothing reported")
	case <-time.After(delay / 4):
	}
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("a function report did not wake")
	}
	// The child dies by itself: nothing in the worker has noticed yet.
	wake = w.DeferredWake()
	if err := w.live.Load().Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("the child's end did not wake")
	}
}

// A line that changes nothing for a deferred text does not wake the
// drain: an audit's verdict and its finished line are such lines.
func TestWorkerEmbedder_AuditLinesDoNotWake(t *testing.T) {
	const delay = 200 * time.Millisecond
	w, _ := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=audit")...)
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(delay / 4) // the lines before the audit's end are in
	wake := w.DeferredWake()
	if err := w.slot.acquire(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	c := w.child
	w.slot.release(false)
	for deadline := time.Now().Add(10 * time.Second); c.calls.Load() != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the audit's finished line never came")
		}
	}
	select {
	case <-wake:
		t.Fatal("an audit's lines woke the drain")
	default:
	}
}

// A drain's hold keeps the child from being retired as idle until it is
// released, once.
func TestWorkerEmbedder_HoldBulk(t *testing.T) {
	const idle = 50 * time.Millisecond
	ctx := context.Background()
	w, h := aneWorker(t)
	w.idleTimeout = idle
	release := w.HoldBulk()
	other := w.HoldBulk()
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	other()
	other() // a second release is not another hold's
	time.Sleep(6 * idle)
	if !hasChild(w) {
		t.Fatal("the child was retired under a hold")
	}
	release()
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child was never retired after the release")
		}
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}

	// A child with no Core ML path has nothing a drain waits for: a hold
	// does not keep it.
	plain := newTestWorker(t, &helperSpawner{modes: []string{"ok"}}, 0)
	plain.idleTimeout = idle
	defer plain.HoldBulk()()
	if _, err := plain.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(plain); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a llama.cpp-only child was held past its idle time")
		}
	}
}

// Deferred texts hold no child by themselves: after a batch whose first
// frame deferred a text and whose second lost the child, the replacement
// is retired as idle like any other.
func TestWorkerEmbedder_DeferredTextsDoNotOutliveTheirChild(t *testing.T) {
	ctx := context.Background()
	// The handshake is not a request: the child dies on its second frame.
	h := &helperSpawner{modes: []string{"ok", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_DEFER=long", "ANY_EMBED_WORKER_CRASH_AFTER=1"}}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	vecs, rt, err := w.EmbedDocsRouted(ctx, nil, []string{"long one", "b"})
	if err == nil || len(vecs) != 1 || vecs[0] != nil || rt.Deferred != 1 {
		t.Fatalf("EmbedDocsRouted: %v, %+v, %v; want one deferred text, then the child's death", vecs, rt, err)
	}
	clearBackoff(w)
	if err := w.slot.acquire(ctx, false); err != nil {
		t.Fatal(err)
	}
	w.idleTimeout = 50 * time.Millisecond
	w.slot.release(false)
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the replacement was never retired as idle")
		}
	}
}

// A child is not retired as idle while it audits a placement: the
// verdict would never be written.
func TestWorkerEmbedder_AuditHoldsOffIdleRetirement(t *testing.T) {
	const idle, audit = 50 * time.Millisecond, 600 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(audit), "ANY_EMBED_WORKER_ANE=audit")...)
	w.idleTimeout = idle
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(audit / 2)
	if !hasChild(w) {
		t.Fatal("the child was retired under its audit")
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child was never retired after its audit")
		}
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A child that dies inside a placement audit is not held for it: it is
// reaped, as a failed one.
func TestWorkerEmbedder_AuditDoesNotHoldADeadChild(t *testing.T) {
	const idle, dies = 50 * time.Millisecond, 300 * time.Millisecond
	w, _ := aneWorker(t, append(delayEnv(dies), "ANY_EMBED_WORKER_ANE=auditexit")...)
	w.idleTimeout = idle
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dead child was never reaped")
		}
	}
	w.mu.Lock()
	demoted := w.coremlDisabled
	w.mu.Unlock()
	if !demoted {
		t.Fatal("a death under an audit did not count against Core ML")
	}
}

// The child itself says whether it may be stopped as idle: an audit the
// server has read no report of yet is not cut short.
func TestWorkerEmbedder_ChildRefusesIdleStopUnderAnAudit(t *testing.T) {
	const idle, audit = 50 * time.Millisecond, 600 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(audit), "ANY_EMBED_WORKER_ANE=silentaudit")...)
	w.idleTimeout = idle
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(audit / 2)
	if !hasChild(w) {
		t.Fatal("the child was stopped under an audit it had not reported yet")
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child was never retired after its audit")
		}
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A child whose audit never returns is not held for it for ever: once
// the child says the audit is overdue it is stopped. Nothing failed that
// was asked of it: it costs neither runtime.
func TestWorkerEmbedder_StuckAuditDoesNotPinTheChild(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=stuckaudit", "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.idleTimeout = 50 * time.Millisecond
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child stuck in an audit was never reaped")
		}
	}
	w.mu.Lock()
	coreml, gpu := w.coremlDisabled, w.gpuDisabled
	w.mu.Unlock()
	if coreml || gpu {
		t.Fatalf("after a stuck audit: core ml off %v, gpu off %v; want neither", coreml, gpu)
	}
}

// A child stuck in an audit past its bound is replaced though it is never
// idle: under a frame every few moments, and under a drain's hold with no
// frame at all. It costs neither runtime.
func TestWorkerEmbedder_OverdueAuditDoesNotPinABusyChild(t *testing.T) {
	for _, held := range []bool{false, true} {
		h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=stuckaudit", "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
		w := newTestWorker(t, h, -1)
		w.mu.Lock()
		w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
		w.mu.Unlock()
		w.callMax = 100 * time.Millisecond
		w.askTimeout = 20 * time.Millisecond
		w.idleTimeout = time.Hour
		if held {
			w.idleTimeout = 50 * time.Millisecond
			defer w.HoldBulk()()
		}
		ctx := context.Background()
		if _, err := w.EmbedQuery(ctx, "q"); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(10 * time.Second); h.spawns() < 2 && hasChild(w); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("hold %v: the child stuck in an audit was never stopped", held)
			}
			if !held {
				if _, err := w.EmbedQuery(ctx, "q"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := w.EmbedQuery(ctx, "q"); err != nil {
			t.Fatalf("hold %v: the query after the stop: %v", held, err)
		}
		if h.spawns() != 2 {
			t.Fatalf("hold %v: spawned %d children, want the stuck one and its replacement", held, h.spawns())
		}
		w.mu.Lock()
		coreml, gpu := w.coremlDisabled, w.gpuDisabled
		w.mu.Unlock()
		if coreml || gpu {
			t.Fatalf("hold %v: core ml off %v, gpu off %v; want neither", held, coreml, gpu)
		}
	}
}

// A child held by Core ML work that is not overdue is left alone by the
// ask, and so is one whose calls have not been open for long.
func TestWorkerEmbedder_HeldChildIsKeptUntilOverdue(t *testing.T) {
	const audit = 400 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(audit), "ANY_EMBED_WORKER_ANE=audit")...)
	w.callMax = 50 * time.Millisecond
	w.askTimeout = 10 * time.Millisecond
	ctx := context.Background()
	for end := time.Now().Add(audit / 2); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := w.EmbedQuery(ctx, "q"); err != nil {
			t.Fatal(err)
		}
	}
	if h.spawns() != 1 || !hasChild(w) {
		t.Fatalf("spawned %d children; a child whose audit is not overdue was stopped", h.spawns())
	}
}

// The calls a child has open keep the time each was first seen; the
// oldest is what the server measures against the bound.
func TestOldestCall(t *testing.T) {
	open, since := map[string]bool{}, map[string]int64{}
	if got := oldestCall(open, since, 10); got != 0 {
		t.Fatalf("no call open: %d", got)
	}
	open["s128 audit"] = true
	if got := oldestCall(open, since, 20); got != 20 {
		t.Fatalf("one call: %d, want 20", got)
	}
	open["s256 load"] = true
	if got := oldestCall(open, since, 30); got != 20 {
		t.Fatalf("two calls: %d, want the first one's 20", got)
	}
	delete(open, "s128 audit")
	if got := oldestCall(open, since, 40); got != 30 {
		t.Fatalf("the older call ended: %d, want 30", got)
	}
	delete(open, "s256 load")
	open["s128 audit"] = true
	if got := oldestCall(open, since, 50); got != 50 {
		t.Fatalf("a call opened again: %d, want 50", got)
	}
}

// A child that ended while idle and is found so by the next request,
// before the idle ask, is reaped as an idle one: the request does not
// charge it to the GPU, and no backoff holds the next child.
func TestWorkerEmbedder_RequestFindsAnIdleChildDead(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=off", "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.idleTimeout = time.Hour
	ctx := context.Background()
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	_ = w.slot.acquire(ctx, false)
	c := w.child
	w.slot.release(false)
	if err := w.live.Load().Kill(); err != nil {
		t.Fatal(err)
	}
	<-c.errDone // the server has seen it end
	if _, err := w.EmbedQuery(ctx, "q"); err != nil {
		t.Fatalf("the query after an idle child's death: %v", err)
	}
	if h.spawns() != 2 {
		t.Fatalf("spawned %d children, want the dead one and its replacement", h.spawns())
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.coremlDisabled || w.gpuDisabled {
		t.Fatalf("core ml off %v, gpu off %v; want neither", w.coremlDisabled, w.gpuDisabled)
	}
}

// A request that could not be written to the child was not asked of it:
// the child is let go without a runtime charged or a backoff.
func TestWorkerEmbedder_UnwrittenRequestChargesNoRuntime(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	ctx := context.Background()
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	_ = w.slot.acquire(ctx, false)
	_ = w.child.stdin.Close() // its end of the pipe went, the server has not seen it exit
	w.slot.release(false)
	if _, err := w.EmbedQuery(ctx, "q"); !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("the query on a closed pipe: %v", err)
	}
	if gpuDisabled(w) || nextTry(w).After(time.Now()) {
		t.Fatalf("gpu off %v, next child put off %v; want neither", gpuDisabled(w), nextTry(w).After(time.Now()))
	}
	if _, err := w.EmbedQuery(ctx, "q"); err != nil || h.spawns() != 2 {
		t.Fatalf("the next query: %v, %d children", err, h.spawns())
	}
}

// The child that replaces one found dead inside a Core ML call starts
// without the Neural Engine, also when the restart wait is over by the
// time it is spawned.
func TestWorkerEmbedder_ReplacementOfADeathInCoreMLRunsWithoutIt(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=loading", "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.idleTimeout = time.Hour
	// Every look at the clock is a minute on: past any restart wait.
	clock := time.Now()
	w.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	ctx := context.Background()
	if _, err := w.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	_ = w.slot.acquire(ctx, false)
	c := w.child
	w.slot.release(false)
	for deadline := time.Now().Add(10 * time.Second); c.calls.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child's load never showed")
		}
	}
	if err := w.live.Load().Kill(); err != nil {
		t.Fatal(err)
	}
	<-c.errDone
	if _, err := w.EmbedQuery(ctx, "q"); err != nil {
		t.Fatalf("the query after the death: %v", err)
	}
	if specs := h.specsCopy(); len(specs) != 2 || specs[1].coremlDir != "" || specs[1].gpuLayers == 0 {
		t.Fatalf("spawned %d children, the last with core ml %q and gpu layers %d; want it without core ml, on the gpu",
			len(specs), specs[len(specs)-1].coremlDir, specs[len(specs)-1].gpuLayers)
	}
}

// idleDeath runs a child with a Core ML path in the helper's mode, kills
// it once idle and returns what its death cost: a runtime, a backoff.
func idleDeath(t *testing.T, mode string) (coremlOff, gpuOff, backoff bool) {
	t.Helper()
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=" + mode, "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.idleTimeout = 200 * time.Millisecond
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // its lines are in
	if err := w.live.Load().Kill(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dead child was never reaped")
		}
	}
	backoff = nextTry(w).After(time.Now())
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.coremlDisabled, w.gpuDisabled, backoff
}

// A child with a Core ML path found dead by the idle ask had nothing
// asked of it: its death costs neither runtime, whether its path was on
// or off, unless it died inside a Core ML call of its own, which costs
// the Neural Engine path and never the GPU.
func TestWorkerEmbedder_IdleDeathCostsNoRuntime(t *testing.T) {
	for mode, wantCoreMLOff := range map[string]bool{
		"off":     false, // the path was off
		"loaded":  false, // the path was on, nothing in flight
		"loading": true,  // inside a load
		"1":       true,  // inside the preparation
	} {
		coremlOff, gpuOff, backoff := idleDeath(t, mode)
		if coremlOff != wantCoreMLOff || gpuOff {
			t.Errorf("%s: core ml off %v, gpu off %v; want core ml off %v and the gpu kept", mode, coremlOff, gpuOff, wantCoreMLOff)
		}
		if backoff && !wantCoreMLOff {
			t.Errorf("%s: a death that cost nothing put off the next child", mode)
		}
	}
}

// A child that does not answer the idle ask is stopped after the ask's
// own short bound, not the request timeout: a search behind the ask gets
// a fresh child, and no runtime is charged.
func TestWorkerEmbedder_IdleAskIsBounded(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=loaded", "ANY_EMBED_WORKER_MUTE=1",
		"ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.reqTimeout = time.Minute
	w.askTimeout = 200 * time.Millisecond
	w.idleTimeout = 100 * time.Millisecond
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the ask is on the wire
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := w.EmbedQuery(ctx, "q"); err != nil {
		t.Fatalf("a search behind an unanswered idle ask: %v", err)
	}
	if h.spawns() != 2 {
		t.Fatalf("spawned %d children, want the mute one and its replacement", h.spawns())
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.coremlDisabled || w.gpuDisabled {
		t.Fatalf("after an unanswered idle ask: core ml off %v, gpu off %v; want neither", w.coremlDisabled, w.gpuDisabled)
	}
}

// A child that dies in an audit died in Core ML, whatever its path said
// before: Core ML is what the next child goes without, not the GPU.
func TestWorkerEmbedder_DeathUnderAnAuditIsCoreMLs(t *testing.T) {
	const idle, dies = 50 * time.Millisecond, 300 * time.Millisecond
	h := &helperSpawner{modes: []string{"ok"}, env: append(delayEnv(dies), "ANY_EMBED_WORKER_ANE=offauditexit",
		"ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)")}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	w.idleTimeout = idle
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dead child was never reaped")
		}
	}
	w.mu.Lock()
	coreml, gpu := w.coremlDisabled, w.gpuDisabled
	w.mu.Unlock()
	if !coreml || gpu {
		t.Fatalf("after a death under an audit: core ml off %v, gpu off %v; want core ml alone", coreml, gpu)
	}
}

// A child without a Core ML path that died while idle is reaped as an
// idle one: its death does not count against the GPU.
func TestWorkerEmbedder_IdleDeathWithoutCoreMLKeepsTheGPU(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"}}
	w := newTestWorker(t, h, -1)
	w.idleTimeout = 100 * time.Millisecond
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := w.live.Load().Kill(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); hasChild(w); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dead child was never reaped")
		}
	}
	if gpuDisabled(w) {
		t.Fatal("an idle child's death turned GPU offload off")
	}
}

// A report on another function does not settle the wait for the first:
// a refusal on record for a larger one comes before the first loads.
func TestWorkerEmbedder_LargerRefusalDoesNotSettle(t *testing.T) {
	const delay = 400 * time.Millisecond
	w, _ := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=refusedlarger")...)
	w.mu.Lock()
	w.spec.profile = config.LocalModelEmbeddingGemma2
	w.mu.Unlock()
	start := time.Now()
	if got := w.PrepareBulk(context.Background(), nil); got != BulkReady {
		t.Fatalf("PrepareBulk = %q, want the first function's own %q", got, BulkReady)
	}
	if waited := time.Since(start); waited < delay-50*time.Millisecond {
		t.Fatalf("returned after %s, before the first function's %s", waited, delay)
	}
}

// Off is final for a child's Core ML flag: a line that comes after it
// does not say Core ML is in use again.
func TestWorkerEmbedder_OffIsFinalForTheChild(t *testing.T) {
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=offthenfail")
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the three lines are in
	if err := w.slot.acquire(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	defer w.slot.release(false)
	if w.child.coreml.Load() {
		t.Fatal("a line after off put the child's Core ML flag back")
	}
}

// PrepareBulk says how its wait went.
func TestWorkerEmbedder_PrepareBulkOutcome(t *testing.T) {
	ctx := context.Background()
	for mode, want := range map[string]string{"ready": BulkReady, "failed": BulkFailed, "off": BulkOff} {
		w, _ := aneWorker(t, append(delayEnv(50*time.Millisecond), "ANY_EMBED_WORKER_ANE="+mode)...)
		if got := w.PrepareBulk(ctx, nil); got != want {
			t.Errorf("first function %s: PrepareBulk = %q, want %q", mode, got, want)
		}
		// A settled child answers the same at once.
		if got := w.PrepareBulk(ctx, nil); got != want {
			t.Errorf("first function %s, settled: PrepareBulk = %q, want %q", mode, got, want)
		}
	}
	w := newTestWorker(t, &helperSpawner{modes: []string{"ok"}}, 0)
	if got := w.PrepareBulk(ctx, nil); got != BulkNoWait {
		t.Errorf("no neural engine path: PrepareBulk = %q", got)
	}
	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=1") // never settles
	w.bulkWait = 100 * time.Millisecond
	if got := w.PrepareBulk(ctx, nil); got != BulkTimeout {
		t.Errorf("the wait ran out: PrepareBulk = %q", got)
	}
	if got := w.PrepareBulk(ctx, nil); got != BulkTimeout {
		t.Errorf("after the wait ran out: PrepareBulk = %q", got)
	}
	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=1")
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if got := w.PrepareBulk(cctx, nil); got != BulkCancelled {
		t.Errorf("cancelled: PrepareBulk = %q", got)
	}
	w, _ = aneWorker(t)
	w.newCmd = (&helperSpawner{modes: []string{"exit"}}).cmd
	if got := w.PrepareBulk(ctx, nil); got != BulkNoChild {
		t.Errorf("a child that does not start: PrepareBulk = %q", got)
	}
}

// frameDocs stands in for the Local behind serveEmbed: it defers its
// second text when asked for bulk work, and records what it was asked.
type frameDocs struct{ bulk []bool }

func (f *frameDocs) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

func (f *frameDocs) embedDocs(_ context.Context, _, texts []string, bulk bool) ([][]float32, error) {
	f.bulk = append(f.bulk, bulk)
	out := make([][]float32, len(texts))
	for i := range out {
		if !bulk || i != 1 {
			out[i] = []float32{1, 0}
		}
	}
	return out, nil
}

func (f *frameDocs) lastRoutes() workerRoutes {
	if f.bulk[len(f.bulk)-1] {
		return workerRoutes{ANE: 2, Deferred: []int{1}}
	}
	return workerRoutes{ANE: 3}
}

// The child passes a frame's bulk flag on to its embedder, and answers a
// bulk frame with the vectors it has and the positions it has none for;
// an ordinary frame gets every vector.
func TestServeEmbed_BulkAndOrdinaryFrames(t *testing.T) {
	docs := &frameDocs{}
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	texts := []string{"a", "b", "c"}
	for _, bulk := range []bool{true, false} {
		if err := serveEmbed(context.Background(), docs, w, workerReq{Op: workerOpEmbed, Role: workerRoleDoc, Texts: texts, Bulk: bulk}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(docs.bulk, []bool{true, false}) {
		t.Fatalf("the embedder was asked bulk = %v, want true then false", docs.bulk)
	}
	r := bufio.NewReader(&buf)
	var hdr workerResp
	bin, err := readFrame(r, &hdr)
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := decodeVectors(bin, hdr.N, hdr.Dim)
	if err != nil || hdr.N != 2 || hdr.Routes == nil || !reflect.DeepEqual(hdr.Routes.Deferred, []int{1}) {
		t.Fatalf("bulk frame: %d vectors, routes %+v, %v", hdr.N, hdr.Routes, err)
	}
	if sparse, err := unpackVectors(vecs, hdr.Routes.Deferred, len(texts)); err != nil || sparse[1] != nil || sparse[0] == nil || sparse[2] == nil {
		t.Fatalf("bulk frame unpacked: %v, %v", sparse, err)
	}
	hdr = workerResp{}
	if _, err = readFrame(r, &hdr); err != nil || hdr.N != 3 || len(hdr.Routes.Deferred) != 0 {
		t.Fatalf("ordinary frame: %d vectors, routes %+v, %v", hdr.N, hdr.Routes, err)
	}
}

// downloadingPackage gives a worker a Core ML package that is not on
// disk yet and a download for it the test ends by hand.
type downloadingPackage struct {
	onDisk atomic.Bool
	end    func() // the download ends, with the package on disk or not
}

func withDownloadingPackage(w *workerEmbedder) *downloadingPackage {
	done := make(chan struct{})
	p := &downloadingPackage{end: sync.OnceFunc(func() { close(done) })}
	w.pkgOnDisk = p.onDisk.Load
	w.pkgDl = &packageDownload{cancel: p.end, done: done} // Close cancels, then waits for it
	return p
}

// While the package's download runs, bulk work waits for it and then for
// the first function, as for a package being prepared. A search is not
// held meanwhile.
func TestWorkerEmbedder_PrepareBulkWaitsForThePackageDownload(t *testing.T) {
	const lands = 300 * time.Millisecond
	ctx := context.Background()
	w, _ := aneWorker(t, append(delayEnv(50*time.Millisecond), "ANY_EMBED_WORKER_ANE=ready")...)
	pkg := withDownloadingPackage(w)
	go func() {
		time.Sleep(lands)
		pkg.onDisk.Store(true) // whole, before the download is done with its licence texts
	}()
	said := 0
	start := time.Now()
	done := make(chan string, 1)
	go func() { done <- w.PrepareBulk(ctx, func() { said++ }) }()
	if _, err := w.EmbedQuery(ctx, "a search under the download"); err != nil {
		t.Fatalf("a search under the download: %v", err)
	}
	if waited := time.Since(start); waited > lands {
		t.Fatalf("the search took %s: it was held", waited)
	}
	if got := <-done; got != BulkReady {
		t.Fatalf("PrepareBulk = %q, want %q", got, BulkReady)
	}
	if waited := time.Since(start); waited < lands {
		t.Fatalf("returned after %s, before the package landed at %s", waited, lands)
	}
	if said != 1 {
		t.Fatalf("said it waits %d times, want once", said)
	}
}

// The wait for the download ends with it, and at its own bound: bulk
// work then goes on without the package, and no later call waits.
func TestWorkerEmbedder_PrepareBulkStopsWaitingForTheDownload(t *testing.T) {
	ctx := context.Background()
	w, h := aneWorker(t, "ANY_EMBED_WORKER_ANE=ready")
	pkg := withDownloadingPackage(w)
	go func() {
		time.Sleep(100 * time.Millisecond)
		pkg.end() // without the package
	}()
	start := time.Now()
	if got := w.PrepareBulk(ctx, nil); got != BulkDownloadFailed {
		t.Fatalf("a failed download: PrepareBulk = %q", got)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("returned after %s, want the download's end", waited)
	}
	if got := w.PrepareBulk(ctx, func() { t.Error("waited again after the download failed") }); got != BulkDownloadFailed {
		t.Fatalf("after a failed download: PrepareBulk = %q", got)
	}

	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=ready")
	withDownloadingPackage(w)
	w.pkgWait = 150 * time.Millisecond
	start = time.Now()
	if got := w.PrepareBulk(ctx, nil); got != BulkDownloadTimeout {
		t.Fatalf("a download past the bound: PrepareBulk = %q", got)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("returned after %s, want the %s bound", waited, w.pkgWait)
	}
	if got := w.PrepareBulk(ctx, func() { t.Error("waited again past the bound") }); got != BulkDownloadTimeout {
		t.Fatalf("past the bound: PrepareBulk = %q", got)
	}

	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=ready")
	withDownloadingPackage(w)
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if got := w.PrepareBulk(cctx, nil); got != BulkCancelled {
		t.Fatalf("cancelled under the download: PrepareBulk = %q", got)
	}

	// No download for a package that is not there: nothing to wait for.
	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=ready")
	w.pkgOnDisk = func() bool { return false }
	if got := w.PrepareBulk(ctx, func() { t.Error("waited with no download") }); got != BulkNoPackage {
		t.Fatalf("no download: PrepareBulk = %q", got)
	}
	if h.spawns() != 0 {
		t.Fatalf("spawned %d children for a package that never came", h.spawns())
	}
}

// A package that lands after the wait's last look and before its bound
// is found at the bound: bulk work goes on to the first function, it
// does not open without it.
func TestWorkerEmbedder_PackageLandingAtTheBoundIsSeen(t *testing.T) {
	w, _ := aneWorker(t, append(delayEnv(50*time.Millisecond), "ANY_EMBED_WORKER_ANE=ready")...)
	pkg := withDownloadingPackage(w)
	w.pkgWait, w.pkgPoll = 200*time.Millisecond, time.Hour // the bound is the only look
	go func() {
		time.Sleep(50 * time.Millisecond)
		pkg.onDisk.Store(true)
	}()
	if got := w.PrepareBulk(context.Background(), nil); got != BulkReady {
		t.Fatalf("PrepareBulk = %q, want the first function's %q", got, BulkReady)
	}
}

// With a model path and no package in the models directory, an eligible
// Mac has no Neural Engine path for want of the package, and says so; a
// machine that would have none anyway has nothing to wait for.
func TestNewWorkerEmbedder_SaysThePackageIsMissing(t *testing.T) {
	models := t.TempDir()
	gguf := filepath.Join(models, "model.gguf")
	if err := os.WriteFile(gguf, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, ModelPath: gguf}
	for capable, want := range map[bool]string{true: BulkNoPackage, false: BulkNoWait} {
		stubANEHost(t, capable)
		w, err := newWorkerEmbedder(cfg, models, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.PrepareBulk(context.Background(), func() { t.Error("waited with no package to come") }); got != want {
			t.Errorf("neural engine host %v: PrepareBulk = %q, want %q", capable, got, want)
		}
		_ = w.Close()
	}
}
