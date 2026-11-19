//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
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

// The wait for a function is bounded from when it was asked for: past
// it llama.cpp embeds the document, with the timeout as the reason, and
// the engine says so once.
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
	if len(n.wanted) != 0 || len(n.late) != 0 {
		t.Fatalf("waits left after every load: %v, %d timers", n.wanted, len(n.late))
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
	wake = w.DeferredWake()
	if err := w.slot.acquire(ctx, false); err != nil {
		t.Fatal(err)
	}
	w.stopChild(true)
	w.slot.release(false)
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("the child's end did not wake")
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
	w.spec.coremlDir = "/models/eg2.mlpackage"
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
