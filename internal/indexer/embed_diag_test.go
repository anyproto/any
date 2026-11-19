//go:build llamacpp && !android && !ios

package indexer

import (
	"strings"
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
}

// Bulk batches whose texts ran on llama.cpp or were left pending are
// logged with their counts: per reason, and for the deferred ones how
// many and for how long, closed off when the child reports.
func TestWorkerEmbedder_LogsBulkRouting(t *testing.T) {
	w, _ := aneWorker(t)
	logs := observed(w)

	// All on the Neural Engine: nothing to say.
	w.noteBulk(true, routeTally{ane: 64})
	w.logBulk(false)
	if lines := routingLines(logs); len(lines) != 0 {
		t.Fatalf("logged %v for a batch all on the neural engine", lines)
	}
	// Calls that are not bulk work are not counted.
	w.noteBulk(false, routeTally{llama: map[string]int{EmbedBackendGPU: 5}, reasons: map[string]int{"": 5}})
	w.logBulk(false)
	if lines := routingLines(logs); len(lines) != 0 {
		t.Fatalf("logged %v for a call outside bulk work", lines)
	}

	w.noteBulk(true, routeTally{ane: 60, coreml: 1, deferred: 2,
		llama:   map[string]int{EmbedBackendGPU: 3},
		reasons: map[string]int{EmbedReasonTooLong: 2, EmbedReasonANEFailed: 1}})
	w.noteBulk(true, routeTally{ane: 10, deferred: 1, llama: map[string]int{EmbedBackendGPU: 1}, reasons: map[string]int{EmbedReasonTooLong: 1}})
	time.Sleep(20 * time.Millisecond)
	w.logBulk(false)
	lines := routingLines(logs)
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1: %v", len(lines), lines)
	}
	l := lines[0]
	if l["ane"] != int64(70) || l["coreml"] != int64(1) || l["llama"] != int64(4) || l["deferred"] != int64(3) ||
		l["llamaReasons"] != "ane_failed=1 too_long=3" || l["llamaBackends"] != "gpu" || l["deferredSentAgain"] != false {
		t.Fatalf("line %v", l)
	}
	if d, _ := l["deferredFor"].(time.Duration); d < 20*time.Millisecond {
		t.Fatalf("deferredFor %v, want the time since the first deferred text", l["deferredFor"])
	}

	// Nothing new: no line. The child reports: the deferred texts are
	// summed up once more, as sent again, and the count starts over.
	w.logBulk(false)
	if n := len(routingLines(logs)); n != 1 {
		t.Fatalf("%d lines with nothing new, want 1", n)
	}
	w.wakeDeferred()
	lines = routingLines(logs)
	if len(lines) != 2 || lines[1]["deferred"] != int64(3) || lines[1]["deferredSentAgain"] != true || lines[1]["llama"] != int64(0) {
		t.Fatalf("lines after the child's report: %v", lines)
	}
	w.wakeDeferred()
	if n := len(routingLines(logs)); n != 2 {
		t.Fatalf("%d lines after a second report with nothing deferred, want 2", n)
	}
}

// A batch through the child ends up in the same counts.
func TestWorkerEmbedder_BulkRoutingFromTheChild(t *testing.T) {
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_DEFER=long", "ANY_EMBED_WORKER_ROUTES=0,1,"+EmbedReasonTooLong)
	logs := observed(w)
	if _, _, err := w.EmbedDocsRouted(t.Context(), nil, []string{"a", "long one", "b"}); err != nil {
		t.Fatal(err)
	}
	w.logBulk(false)
	lines := routingLines(logs)
	if len(lines) != 1 || lines[0]["deferred"] != int64(1) || lines[0]["llama"] != int64(3) ||
		!strings.Contains(lines[0]["llamaReasons"].(string), "=3") {
		t.Fatalf("lines %v, want one with 1 deferred text and the frames' llama.cpp texts", lines)
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

func TestBulkOpenedBy(t *testing.T) {
	for _, tc := range []struct {
		delayed, prepared bool
		want              string
	}{
		{false, false, "no_wait"},
		{true, false, "start_delay"},
		{true, true, "embedder_ready"},
		{false, true, "embedder_ready"},
	} {
		if got := bulkOpenedBy(tc.delayed, tc.prepared); got != tc.want {
			t.Errorf("bulkOpenedBy(%v, %v) = %q, want %q", tc.delayed, tc.prepared, got, tc.want)
		}
	}
}
