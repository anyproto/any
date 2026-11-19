//go:build llamacpp && !android && !ios

package indexer

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// observed gives the worker a logger whose lines the test reads.
func observed(w *workerEmbedder) *observer.ObservedLogs {
	core, logs := observer.New(zapcore.DebugLevel)
	w.lg = logger.CtxLogger{Logger: zap.New(core)}
	return logs
}

func routingLines(logs *observer.ObservedLogs) []map[string]any {
	var out []map[string]any
	for _, e := range logs.FilterMessage("local embedder bulk routing").All() {
		out = append(out, e.ContextMap())
	}
	return out
}

// The llama.cpp texts of a frame are counted by reason, read as the
// frame's one reason is: the child's miss when the Neural Engine path is
// planned, the timeout once bulk work gave up, else the GPU's.
func TestWorkerEmbedder_FrameReasons(t *testing.T) {
	w, _ := aneWorker(t)
	rt := &workerRoutes{ANE: 3, Llama: 4, Miss: EmbedReasonTooLong,
		Misses: map[string]int{EmbedReasonFunctionLoading: 2, EmbedReasonTooLong: 1}}
	got := w.frameReasons(rt)
	// gpuLayers is 0 in this worker: the text with no miss is the GPU's.
	want := map[string]int{EmbedReasonFunctionLoading: 2, EmbedReasonTooLong: 1, EmbedReasonGPUDisabled: 1}
	if len(got) != len(want) {
		t.Fatalf("reasons %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("reasons %v, want %v", got, want)
		}
	}
	w.giveUpBulk()
	if got = w.frameReasons(rt); got[EmbedReasonReadinessTimeout] != 2 || got[EmbedReasonFunctionLoading] != 0 {
		t.Fatalf("after giving up: %v, want the loading texts as a readiness timeout", got)
	}
	w.mu.Lock()
	w.coremlDisabled = true
	w.mu.Unlock()
	if got = w.frameReasons(rt); len(got) != 1 || got[EmbedReasonANEOff] != 4 {
		t.Fatalf("neural engine demoted: %v, want every text as ane_off", got)
	}
	if got = w.frameReasons(&workerRoutes{ANE: 3}); got != nil {
		t.Fatalf("a frame with no llama.cpp text: %v", got)
	}
	// A frame that names its last miss and counts none.
	w.mu.Lock()
	w.coremlDisabled = false
	w.mu.Unlock()
	if got = w.frameReasons(&workerRoutes{Llama: 2, Miss: EmbedReasonTooLong}); got[EmbedReasonTooLong] != 1 || got[EmbedReasonGPUDisabled] != 1 {
		t.Fatalf("a frame with only its last miss: %v", got)
	}
}

// Bulk frames off the Neural Engine are logged with their counts, per
// reason, at most once per period; work with nothing to say stays
// counted, and a child that goes says what is still counted.
func TestWorkerEmbedder_LogsBulkRouting(t *testing.T) {
	w, _ := aneWorker(t)
	logs := observed(w)
	w.routed.every = time.Hour

	// All on the Neural Engine: nothing to say, even with the child gone.
	w.noteBulk(routeTally{ane: 64})
	w.logBulk(true)
	if lines := routingLines(logs); len(lines) != 0 {
		t.Fatalf("logged %v for a batch all on the neural engine", lines)
	}

	// The first batch off it is said at once, with the work before it.
	w.noteBulk(routeTally{ane: 60, coreml: 1, deferred: 2,
		llama:   map[string]int{EmbedBackendGPU: 3},
		reasons: map[string]int{EmbedReasonTooLong: 2, EmbedReasonANEFailed: 1}})
	lines := routingLines(logs)
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1: %v", len(lines), lines)
	}
	l := lines[0]
	if l["ane"] != int64(124) || l["coreml"] != int64(1) || l["llama"] != int64(3) || l["deferred"] != int64(2) ||
		l["llamaReasons"] != "ane_failed=1 too_long=2" || l["llamaBackends"] != "gpu" {
		t.Fatalf("line %v", l)
	}

	// Within the period nothing more is logged, neither for a batch nor
	// for a report from the child; the counts run on.
	w.noteBulk(routeTally{ane: 10, deferred: 1, llama: map[string]int{EmbedBackendGPU: 1}, reasons: map[string]int{EmbedReasonTooLong: 1}})
	w.logBulk(false)
	w.noteBulk(routeTally{deferred: 4})
	if n := len(routingLines(logs)); n != 1 {
		t.Fatalf("%d lines within the period, want 1", n)
	}
	// The period over: one line for all of it, with a window that is the
	// time since the first batch counted.
	time.Sleep(20 * time.Millisecond)
	w.routed.mu.Lock()
	w.routed.every = 0
	w.routed.mu.Unlock()
	w.logBulk(false)
	lines = routingLines(logs)
	if len(lines) != 2 {
		t.Fatalf("%d lines after the period, want 2: %v", len(lines), lines)
	}
	l = lines[1]
	if l["ane"] != int64(10) || l["llama"] != int64(1) || l["deferred"] != int64(5) || l["llamaReasons"] != "too_long=1" {
		t.Fatalf("line %v", l)
	}
	for _, field := range []string{"window", "deferredFor"} {
		if d, _ := l[field].(time.Duration); d < 20*time.Millisecond || d > time.Minute {
			t.Fatalf("%s = %v, want the time since the first batch", field, l[field])
		}
	}
	// Nothing counted since: a report, or the child going, logs nothing.
	w.logBulk(false)
	w.logBulk(true)
	if n := len(routingLines(logs)); n != 2 {
		t.Fatalf("%d lines with nothing counted, want 2", n)
	}
	// A child that goes says what was counted, whatever the period.
	w.routed.mu.Lock()
	w.routed.every = time.Hour
	w.routed.mu.Unlock()
	w.noteBulk(routeTally{deferred: 1})
	if n := len(routingLines(logs)); n != 2 {
		t.Fatalf("%d lines within the period, want 2", n)
	}
	w.logBulk(true)
	if lines = routingLines(logs); len(lines) != 3 || lines[2]["deferred"] != int64(1) {
		t.Fatalf("lines after the child went: %v", lines)
	}
}

// quietRouting holds the routing line back until something forces it.
func quietRouting(w *workerEmbedder) {
	w.routed.mu.Lock()
	w.routed.every, w.routed.last = time.Hour, time.Now()
	w.routed.mu.Unlock()
}

// The frames of a batch through the child end up in the same counts,
// and only where a Neural Engine path is configured.
func TestWorkerEmbedder_BulkRoutingFromTheChild(t *testing.T) {
	env := []string{"ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_DEFER=long", "ANY_EMBED_WORKER_MISS=" + EmbedReasonTooLong}
	w, _ := aneWorker(t, env...)
	logs := observed(w)
	quietRouting(w)
	if _, _, err := w.EmbedDocsRouted(t.Context(), nil, []string{"a", "long one", "b"}); err != nil {
		t.Fatal(err)
	}
	// An ordinary document call is not bulk work.
	if _, err := w.EmbedDocs(t.Context(), []string{"c"}); err != nil {
		t.Fatal(err)
	}
	w.logBulk(true)
	lines := routingLines(logs)
	if len(lines) != 1 || lines[0]["deferred"] != int64(1) || lines[0]["llama"] != int64(2) || lines[0]["llamaReasons"] != "too_long=2" {
		t.Fatalf("lines %v, want one with 1 deferred text and 2 too long for llama.cpp", lines)
	}

	plain := newTestWorker(t, &helperSpawner{modes: []string{"ok"}, env: env}, 0)
	logs = observed(plain)
	if _, _, err := plain.EmbedDocsRouted(t.Context(), nil, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	plain.logBulk(true)
	if lines := routingLines(logs); len(lines) != 0 {
		t.Fatalf("logged %v with no neural engine path", lines)
	}
}

// A frame answered before the child died is in the line the death
// writes, and the batches of the replacement, which runs llama.cpp alone
// after the demotion, are still counted, as ane_off.
func TestWorkerEmbedder_BulkRoutingAcrossACrash(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_DEFER=long",
		"ANY_EMBED_WORKER_MISS=" + EmbedReasonTooLong, "ANY_EMBED_WORKER_CRASH_AFTER=1"}}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = filepath.Join(t.TempDir(), "eg2.mlpackage")
	w.mu.Unlock()
	logs := observed(w)
	quietRouting(w)
	if _, _, err := w.EmbedDocsRouted(t.Context(), nil, []string{"long one", "b"}); err == nil {
		t.Fatal("want the child's death")
	}
	lines := routingLines(logs)
	if len(lines) != 1 || lines[0]["deferred"] != int64(1) {
		t.Fatalf("lines after the death: %v, want the deferred text of the frame before it", lines)
	}
	clearBackoff(w)
	quietRouting(w)
	if _, _, err := w.EmbedDocsRouted(t.Context(), nil, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	w.logBulk(true)
	if lines = routingLines(logs); len(lines) != 2 || lines[1]["llama"] != int64(1) || lines[1]["llamaReasons"] != "ane_off=1" {
		t.Fatalf("lines after the demotion: %v, want the replacement's text as ane_off", lines)
	}
}

// A function's ready line carries how long ago it was asked for.
func TestNeuralEngine_ReadyLineSaysHowLongItWasAskedFor(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 1)}
	log := newLogLines()
	n, clock := newTestANE(t, f)
	n.logf = log.logf
	n.start()
	<-f.started
	clock.add(42 * time.Second)
	close(f.gate)
	n.loads.Wait()
	if !log.has("s128 ready (fake; asked for 42s ago)") {
		t.Fatalf("lines %v", log.lines)
	}
}
