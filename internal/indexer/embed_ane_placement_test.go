//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/anyproto/any/internal/indexer/coreml"
)

// planFn is a function whose vector depends on its text, as a model's
// does: an axis per first word for a text under six tokens, another for
// a longer one.
type planFn struct {
	err       error
	failFirst int     // predictions that fail before the rest succeed
	constant  bool    // one vector whatever the text
	scale     float32 // the vectors' length; 0 = 1
	blind     bool    // the vector depends on the text's length alone
	calls     int
	closed    bool
}

func (f *planFn) embed(tokens []int32, out []float32) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if f.calls <= f.failFirst {
		return errors.New("a passing failure")
	}
	clear(out)
	one := f.scale
	if one == 0 {
		one = 1
	}
	switch {
	case f.constant:
		out[0] = one
	case len(tokens) >= 6:
		out[2] = one
	case f.blind || tokens[1] == 1001:
		out[0] = one
	default:
		out[1] = one
	}
	return nil
}

func (f *planFn) close() { f.closed = true }

// testBackend is a Core ML backend over fake calls, on host, whose record
// sits in dir. It lists the functions it planned and opened.
type testBackend struct {
	*coreMLBackend
	planned, opened []string
	plans           map[string]coreml.Placement
	planErr         error
	fns             []*planFn
}

var testPackage = &coreMLPackage{name: "m", lengths: []int{128, 256, 512, 1024, 2048}}

var (
	onEngine  = coreml.Placement{NeuralEngine: 6000, CPU: 14}
	offEngine = coreml.Placement{CPU: 6014, CPUCore: []string{"ios18.matmul"}}
)

func newTestBackend(t *testing.T, dir, host string) *testBackend {
	t.Helper()
	tb := &testBackend{plans: map[string]coreml.Placement{}}
	b := newCoreMLBackend(testPackage, filepath.Join(dir, "m.mlpackage"), 4, t.Logf)
	b.host = host
	b.plan = func(compiled, function string) (coreml.Placement, error) {
		tb.planned = append(tb.planned, function)
		if tb.planErr != nil {
			return coreml.Placement{}, tb.planErr
		}
		if p, ok := tb.plans[function]; ok {
			return p, nil
		}
		return onEngine, nil
	}
	b.open = func(compiled, function string, length, dim int) (aneFunction, error) {
		tb.opened = append(tb.opened, function)
		fn := &planFn{}
		tb.fns = append(tb.fns, fn)
		return fn, nil
	}
	b.readPlacements() // what prepare does once the package is there
	tb.coreMLBackend = b
	return tb
}

// A load opens the function and has it predict, with no compute plan.
// The plan is the audit's, once: its verdict is kept for the process and
// written beside the compiled model, where the next process finds it.
func TestCoreMLBackend_LoadsWithoutAPlan(t *testing.T) {
	dir := t.TempDir()
	b := newTestBackend(t, dir, "Chip, macOS build 1A1")
	fn, info, err := b.load(128)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.planned) != 0 {
		t.Fatalf("planned %v on a load", b.planned)
	}
	if got := fn.(*planFn).calls; got != 3 {
		t.Fatalf("%d predictions on a load, want 3", got)
	}
	if !strings.Contains(info, "open ") || !strings.Contains(info, "validate ") || !strings.Contains(info, "placement not verified yet") {
		t.Fatalf("load info %q, want the open and validate times and an unverified placement", info)
	}
	if b.placed(128) {
		t.Fatal("placed before an audit")
	}

	info, err = b.audit(128)
	if err != nil || !strings.Contains(info, "plan ") || !strings.Contains(info, "6000 operations on the neural engine") {
		t.Fatalf("audit: %q, %v", info, err)
	}
	if !b.placed(128) {
		t.Fatal("not placed after an audit that passed")
	}
	if _, info, err = b.load(128); err != nil || !strings.Contains(info, "placement verified") {
		t.Fatalf("reload: %q, %v", info, err)
	}
	if !reflect.DeepEqual(b.planned, []string{"s128"}) {
		t.Fatalf("planned %v, want s128 once", b.planned)
	}

	// The next process on the same machine setup plans nothing.
	next := newTestBackend(t, dir, "Chip, macOS build 1A1")
	if !next.placed(128) || next.placed(256) {
		t.Fatalf("record: s128 placed %v, s256 placed %v; want only s128", next.placed(128), next.placed(256))
	}
	if _, info, err = next.load(128); err != nil || !strings.Contains(info, "placement on record, plan skipped") {
		t.Fatalf("load from the record: %q, %v", info, err)
	}
	if len(next.planned) != 0 {
		t.Fatalf("planned %v with a record", next.planned)
	}
	// A second function's audit keeps the first in the record.
	if _, err = next.audit(256); err != nil {
		t.Fatal(err)
	}
	third := newTestBackend(t, dir, "Chip, macOS build 1A1")
	if !third.placed(128) || !third.placed(256) {
		t.Fatal("the record lost a function")
	}
}

// A function planned off the Neural Engine is refused by its audit, and
// by every later load on that machine setup without opening it.
func TestCoreMLBackend_RecordsARefusal(t *testing.T) {
	dir := t.TempDir()
	b := newTestBackend(t, dir, "Chip, macOS build 1A1")
	b.plans["s512"] = offEngine
	if _, err := b.audit(512); !errors.Is(err, errOffNeuralEngine) {
		t.Fatalf("audit of a function planned on the cpu: %v", err)
	}
	if _, _, err := b.load(512); !errors.Is(err, errOffNeuralEngine) {
		t.Fatalf("load after the refusal: %v", err)
	}
	next := newTestBackend(t, dir, "Chip, macOS build 1A1")
	_, _, err := next.load(512)
	if !errors.Is(err, errOffNeuralEngine) || !strings.Contains(err.Error(), "on record") {
		t.Fatalf("load in the next process: %v", err)
	}
	if len(next.opened) != 0 || len(next.planned) != 0 {
		t.Fatalf("opened %v, planned %v; want a refusal from the record alone", next.opened, next.planned)
	}
}

// The record holds for one chip and OS build: another setup audits
// again and its record replaces the old one. With no setup to name,
// nothing is written.
func TestCoreMLBackend_RecordIsPerHostSetup(t *testing.T) {
	dir := t.TempDir()
	old := newTestBackend(t, dir, "Chip, macOS build 1A1")
	old.plans["s128"] = offEngine
	_, _ = old.audit(128)

	updated := newTestBackend(t, dir, "Chip, macOS build 1B2")
	if _, _, err := updated.load(128); err != nil {
		t.Fatalf("load after an OS update: %v", err)
	}
	updated.plans["s128"] = onEngine
	if _, err := updated.audit(128); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(updated.recordPath(128))
	if err != nil {
		t.Fatal(err)
	}
	var rec placementRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if *rec.Version != placementVersion || *rec.Host != "Chip, macOS build 1B2" || *rec.Function != "s128" || !*rec.On {
		t.Fatalf("record %s", raw)
	}
	if stages, _ := filepath.Glob(updated.recordPath(128) + ".*"); len(stages) != 0 {
		t.Fatalf("staging files left: %v", stages)
	}

	nameless := newTestBackend(t, t.TempDir(), "")
	if _, err := nameless.audit(128); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nameless.recordPath(128)); !os.IsNotExist(err) {
		t.Fatalf("a record without a host setup: %v", err)
	}
	if !nameless.placed(128) {
		t.Fatal("the audit does not hold for the process")
	}
}

// A plan that cannot be read is no verdict: nothing is recorded.
func TestCoreMLBackend_AuditErrorIsNoVerdict(t *testing.T) {
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	b.planErr = errors.New("no answer")
	if _, err := b.audit(128); err == nil || errors.Is(err, errOffNeuralEngine) {
		t.Fatalf("audit: %v, want the plan's own error", err)
	}
	if b.placed(128) {
		t.Fatal("placed without a plan")
	}
	if _, err := os.Stat(b.recordPath(128)); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict: %v", err)
	}
}

// A function that opens but does not predict the model's vectors is not
// loaded, and is closed.
func TestCoreMLBackend_LoadValidates(t *testing.T) {
	for name, broken := range map[string]*planFn{
		"a failed prediction":                      {err: errors.New("predict failed")},
		"one vector for all":                       {constant: true},
		"one vector for all, short of unit length": {constant: true, scale: 0.995},
		"a vector by length alone":                 {blind: true},
		"too many failures in a row":               {failFirst: 3 * aneMaxFailures},
	} {
		b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
		b.open = func(string, string, int, int) (aneFunction, error) { return broken, nil }
		if _, _, err := b.load(128); err == nil {
			t.Errorf("%s: loaded", name)
		}
		if !broken.closed {
			t.Errorf("%s: left open", name)
		}
	}
	if err := validateFunction(&fakeFn{parent: &fakeANE{notUnit: true}, length: 128}, 128, 4); err == nil {
		t.Error("a vector that is not unit length passed")
	}
	// One failed prediction is not the end of the function, as in serving.
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	flaky := &planFn{failFirst: 1}
	b.open = func(string, string, int, int) (aneFunction, error) { return flaky, nil }
	if _, _, err := b.load(128); err != nil || flaky.closed {
		t.Errorf("a function whose first prediction failed: %v, closed %v", err, flaky.closed)
	}
}

// The last function refused while it predicts: the path goes off, and
// the function is closed only when its prediction has returned.
func TestNeuralEngine_LastRefusalUnderAPrediction(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("6014 operations on the cpu"))
	f := &fakeANE{auditErr: map[int]error{128: refused}, auditGate: make(chan struct{}),
		predicting: make(chan int), resume: make(chan struct{})}
	n, _ := newTestANE(t, f)
	n.lengths = []int{128}
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	served := make(chan bool)
	go func() {
		_, ok := n.embed(tokensOf(10))
		served <- ok
	}()
	<-f.predicting
	close(f.auditGate)
	n.loads.Wait()
	n.mu.Lock()
	off := n.off
	n.mu.Unlock()
	if !off {
		t.Fatal("the path is on with its only function refused")
	}
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("closed %v under a prediction", closed)
	}
	f.resume <- struct{}{}
	if !<-served {
		t.Fatal("the prediction in flight was lost")
	}
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
		t.Fatalf("closed %v, want s128 once its prediction returned", closed)
	}
}

// A function on record as refused is known from the start: a document
// that needs it goes to llama.cpp while the loader is busy elsewhere,
// and a refused first function still settles the path's first wait.
func TestNeuralEngine_RecordedRefusalIsNotWaitedFor(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("on record"))
	f := &fakeANE{refusals: map[int]error{512: refused}}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	if _, ok, deferred := n.embedBulk(tokensOf(200)); ok || !deferred {
		t.Fatalf("a document for the loading s256: served %v, deferred %v", ok, deferred)
	}
	<-f.started // the loader is inside s256
	if _, ok, deferred := n.embedBulk(tokensOf(400)); ok || deferred || n.lastMiss() != EmbedReasonANEFailed {
		t.Fatalf("a document for the refused s512: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
	close(f.gate)
	n.loads.Wait()
	waitOrder(t, f, "load 128", "audit 128", "load 256", "audit 256")

	f = &fakeANE{refusals: map[int]error{128: refused}}
	log := newLogLines()
	n, _ = newTestANE(t, f)
	n.logf = log.logf
	n.start()
	n.loads.Wait()
	if !log.has("s128 not used: ") {
		t.Fatalf("no line about the refused first function: %v", log.lines)
	}
	if loaded, _, _, _ := f.snapshot(); len(loaded) != 0 {
		t.Fatalf("loaded %v, want nothing", loaded)
	}
}

// An audit does not hold the loader: a function asked for under it loads
// beside it, and its documents wait only for that load.
func TestNeuralEngine_LoadsBesideAnAudit(t *testing.T) {
	f := &fakeANE{auditGate: make(chan struct{})}
	n, clock := newTestANE(t, f)
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("a document for a function not loaded yet was not deferred")
	}
	// The audit is still running: the load lands anyway.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		if loaded, _, _, _ := f.snapshot(); len(loaded) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("s256 did not load while s128 was audited")
		}
	}
	clock.add(n.wait - time.Second)
	if _, ok, deferred := n.embedBulk(tokensOf(200)); !ok || deferred {
		t.Fatalf("under the audit: served %v, deferred %v, miss %q", ok, deferred, n.lastMiss())
	}
	close(f.auditGate)
	n.loads.Wait()
	waitOrder(t, f, "load 128", "audit 128", "load 256", "audit 256")
}

func TestANEAuditing(t *testing.T) {
	for status, want := range map[string][2]string{
		"s256 placement audit started":                {"s256", ""},
		"s256 placement audit finished":               {"", "s256"},
		"s256 placement verified (plan 1s)":           {"", ""},
		"s256 not used: core ml plans it off":         {"", ""},
		"s256 not used any more: 3 failures in a row": {"", ""},
		"off, llama.cpp embeds everything: no engine": {"", ""},
		"s128 ready (open 1s)":                        {"", ""},
		"starting":                                    {"", ""},
	} {
		if started, finished := aneAuditing(status); started != want[0] || finished != want[1] {
			t.Errorf("aneAuditing(%q) = %q, %q; want %q, %q", status, started, finished, want[0], want[1])
		}
	}
}

// With several documents to a decode, llama.cpp takes only the ones the
// Neural Engine path leaves to it: a text behind a miss is routed on its
// own, and each miss is counted under its own reason.
func TestLocal_LlamaGroupClassifiesEachText(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{256: errors.New("refused")}}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	loadFor(n, 200) // s256 fails to load
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	l := &Local{ane: n, nCtx: 2048, batchDocs: 4}
	toks := func(n int) []llama.Token { return make([]llama.Token, n) }

	// s256 failed, s512 is loading: the 400-token text waits.
	tokenized := [][]llama.Token{toks(200), toks(400)}
	lens := []int{200, 400}
	if _, ok, _ := l.embedANE(tokenized[0], true); ok {
		t.Fatal("served by a failed function")
	}
	end, misses := l.llamaGroup(tokenized, lens, 0, true)
	if end != 1 || !reflect.DeepEqual(misses, []string{EmbedReasonANEFailed}) {
		t.Fatalf("group ends at %d with %v, want the failed text alone", end, misses)
	}
	<-f.started
	// Texts that all fall back share the decode, each with its reason; a
	// text a loaded function serves ends the group.
	tokenized = [][]llama.Token{toks(200), toks(100000), toks(10), toks(200)}
	lens = []int{200, 600, 10, 200}
	_, _, _ = l.embedANE(tokenized[0], true)
	end, misses = l.llamaGroup(tokenized, lens, 0, true)
	if end != 2 || !reflect.DeepEqual(misses, []string{EmbedReasonANEFailed, EmbedReasonTooLong}) {
		t.Fatalf("group ends at %d with %v, want the two fallback texts", end, misses)
	}
	close(f.gate)
	n.loads.Wait()
	// With no Neural Engine path the group is llama.cpp's own.
	if end, misses = (&Local{nCtx: 2048, batchDocs: 4}).llamaGroup(tokenized, lens, 0, true); end != 4 || misses != nil {
		t.Fatalf("without the path: group ends at %d with %v", end, misses)
	}
}

// A function serves as soon as it is loaded, as Core ML; it is the
// Neural Engine's once its audit passed. An audit starts when the loader
// has nothing to load, one per function, and says when it is over.
func TestNeuralEngine_AuditsAfterLoads(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 8)}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	n.start()
	n.mu.Lock()
	n.requestLocked(256)
	n.mu.Unlock()
	<-f.started
	f.gate <- struct{}{} // s128 lands; s256 is queued behind it
	<-f.started
	if _, ok := n.embed(tokensOf(10)); !ok || n.lastPlaced() {
		t.Fatalf("before its audit: served %v, placed %v; want served and not placed", ok, n.lastPlaced())
	}
	f.mu.Lock()
	audits := len(f.audits)
	f.mu.Unlock()
	if audits != 0 {
		t.Fatal("an audit started with a load still queued")
	}
	f.gate <- struct{}{}
	n.loads.Wait()
	waitOrder(t, f, "load 128", "load 256", "audit 128", "audit 256")
	if _, ok := n.embed(tokensOf(10)); !ok || !n.lastPlaced() {
		t.Fatalf("after the audit: served %v, placed %v", ok, n.lastPlaced())
	}
	for _, line := range []string{"s128 placement audit started", "s128 placement audit finished", "s256 placement audit finished"} {
		if !log.has(line) {
			t.Fatalf("no %q among %v", line, log.lines)
		}
	}
	// A reload after an idle release is not audited again.
	f.mu.Lock()
	f.gate, f.started = nil, nil
	f.mu.Unlock()
	n.mu.Lock()
	n.fns[256].close()
	delete(n.fns, 256)
	n.mu.Unlock()
	loadFor(n, 200)
	waitOrder(t, f, "load 128", "load 256", "audit 128", "audit 256", "load 256")
}

func waitOrder(t *testing.T, f *fakeANE, want ...string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		f.mu.Lock()
		got := append([]string(nil), f.order...)
		f.mu.Unlock()
		if reflect.DeepEqual(got, want) {
			return
		}
		if len(got) > len(want) || time.Now().After(deadline) {
			t.Fatalf("backend calls %v, want %v", got, want)
		}
	}
}

// With every placement on record nothing is audited and the functions
// are the Neural Engine's from the first text.
func TestNeuralEngine_RecordedPlacementSkipsTheAudit(t *testing.T) {
	f := &fakeANE{recorded: true}
	n, _ := newTestANE(t, f)
	loadFor(n, 200)
	if _, ok := n.embed(tokensOf(200)); !ok || !n.lastPlaced() {
		t.Fatalf("served %v, placed %v", ok, n.lastPlaced())
	}
	waitOrder(t, f, "load 128", "load 256")
}

// An audit that finds a function off the Neural Engine retires it: it is
// closed, llama.cpp takes its texts, and the path goes off with the last
// one. A function that is predicting is closed when it returns.
func TestNeuralEngine_AuditRefusalRetiresTheFunction(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("6014 operations on the cpu"))
	f := &fakeANE{auditErr: map[int]error{128: refused}, auditGate: make(chan struct{}),
		predicting: make(chan int), resume: make(chan struct{})}
	n, _ := newTestANE(t, f)
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	served := make(chan bool)
	go func() {
		_, ok := n.embed(tokensOf(10))
		served <- ok
	}()
	<-f.predicting
	close(f.auditGate) // the refusal lands under the prediction
	n.loads.Wait()
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("closed %v under a prediction", closed)
	}
	f.resume <- struct{}{}
	if !<-served {
		t.Fatal("the prediction in flight was lost")
	}
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
		t.Fatalf("closed %v, want s128 once its prediction returned", closed)
	}
	if _, ok := n.embed(tokensOf(10)); ok || n.lastMiss() != EmbedReasonANEFailed {
		t.Fatalf("after the refusal: served %v, miss %q", ok, n.lastMiss())
	}
	n.loads.Wait()

	// Every function refused: the path is off.
	f = &fakeANE{auditErr: map[int]error{128: refused, 256: refused, 512: refused}}
	n, _ = newTestANE(t, f)
	n.start()
	n.loads.Wait()
	loadFor(n, 200)
	loadFor(n, 400)
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(10)); ok || n.lastMiss() != EmbedReasonANEOff {
		t.Fatalf("every function refused: served %v, miss %q", ok, n.lastMiss())
	}
}

// An audit that fails without a verdict leaves the function serving as
// Core ML, and is not run again in the process.
func TestNeuralEngine_AuditErrorKeepsTheFunction(t *testing.T) {
	f := &fakeANE{auditErr: map[int]error{128: errors.New("no answer")}}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(10)); !ok || n.lastPlaced() {
		t.Fatalf("served %v, placed %v; want served, not placed", ok, n.lastPlaced())
	}
	n.mu.Lock()
	n.requestLocked(128) // loaded: nothing to do
	n.mu.Unlock()
	n.loads.Wait()
	waitOrder(t, f, "load 128", "audit 128")
}

// The child counts a text by what is known of the function that
// embedded it.
func TestLocal_RoutesNameUnverifiedFunctions(t *testing.T) {
	f := &fakeANE{auditGate: make(chan struct{})}
	n, _ := newTestANE(t, f)
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	l := &Local{ane: n, nCtx: 2048, batchDocs: 1}
	tokenized := [][]llama.Token{make([]llama.Token, 10), make([]llama.Token, 20)}
	if _, err := l.embedTokenized(context.Background(), tokenized, []int{10, 20}, true); err != nil {
		t.Fatal(err)
	}
	if rt := l.routes; rt.ANE != 0 || rt.CoreML != 2 {
		t.Fatalf("routes under the audit: %+v, want 2 on core ml", rt)
	}
	close(f.auditGate)
	n.loads.Wait()
	if _, err := l.embedTokenized(context.Background(), tokenized, []int{10, 20}, true); err != nil {
		t.Fatal(err)
	}
	if rt := l.routes; rt.ANE != 2 || rt.CoreML != 0 {
		t.Fatalf("routes after the audit: %+v, want 2 on the neural engine", rt)
	}
}

// A plan that names no device for operations the verdict hangs on is no
// verdict: nothing is recorded and the function is neither placed nor
// refused.
func TestCoreMLBackend_IncompletePlanIsNoVerdict(t *testing.T) {
	for name, plan := range map[string]coreml.Placement{
		"no device for any operation":      {Unknown: 6209},
		"a core operator without a device": {NeuralEngine: 6000, CPU: 14, Unknown: 1, UnknownCore: []string{"ios18.matmul"}},
	} {
		b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
		b.plans["s128"] = plan
		if _, err := b.audit(128); err == nil || errors.Is(err, errOffNeuralEngine) {
			t.Errorf("%s: audit = %v, want an error that is not a refusal", name, err)
		}
		if b.placed(128) || b.refused(128) != nil {
			t.Errorf("%s: placed %v, refused %v", name, b.placed(128), b.refused(128))
		}
		if _, err := os.Stat(b.recordPath(128)); !os.IsNotExist(err) {
			t.Errorf("%s: a record was written: %v", name, err)
		}
	}
	// Constants without a device do not hold a verdict back.
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	b.plans["s128"] = coreml.Placement{NeuralEngine: 6000, CPU: 14, Unknown: 900}
	if _, err := b.audit(128); err != nil || !b.placed(128) {
		t.Fatalf("audit = %v, placed %v", err, b.placed(128))
	}
}

// Only a record with every field, for this model, setup and function,
// whose verdict the placement rule gives for its own counts, is a
// verdict. Anything else is unaudited.
func TestCoreMLBackend_RecordsAreChecked(t *testing.T) {
	const host = "Chip, macOS build 1A1"
	model := testPackage.id()
	head := `{"version":1,"model":"` + model + `","host":"` + host + `","function":"s128"`
	const refusal = `,"on":false,"neuralEngine":0,"gpu":0,"cpu":6014,"cpuCore":["ios18.matmul"]}`
	for name, raw := range map[string]string{
		"no verdict fields":                head + `}`,
		"one count alone":                  head + `,"cpu":14}`,
		"counts without the verdict":       head + `,"neuralEngine":0,"gpu":0,"cpu":14}`,
		"a count missing":                  head + `,"on":false,"gpu":0,"cpu":14}`,
		"a verdict its counts do not give": head + `,"on":true,"neuralEngine":0,"gpu":0,"cpu":14}`,
		"a count of the wrong type":        head + `,"on":false,"neuralEngine":0,"gpu":0,"cpu":"14"}`,
		"another function's record":        strings.Replace(head, "s128", "s256", 1) + refusal,
		"another model's record":           strings.Replace(head, model, "other-12345678", 1) + refusal,
		"another setup's record":           strings.Replace(head, host, "Chip, macOS build 9Z9", 1) + refusal,
		"no version":                       strings.Replace(head, `"version":1,`, "", 1) + refusal,
		"not json":                         `{"version":1,`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "m.placement.s128.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		b := newTestBackend(t, dir, host)
		if b.placed(128) || b.refused(128) != nil {
			t.Errorf("%s: placed %v, refused %v; want unaudited", name, b.placed(128), b.refused(128))
		}
		if _, _, err := b.load(128); err != nil {
			t.Errorf("%s: the function was refused: %v", name, err)
		}
	}
	// The sound refusal is read, and a file under a name that is not the
	// function's own is nobody's.
	dir := t.TempDir()
	for _, file := range []string{"m.placement.s128.json", "m.placement.s0256.json", "m.placement.512.json"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(head+refusal), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := newTestBackend(t, dir, host)
	if b.refused(128) == nil || b.refused(256) != nil || b.refused(512) != nil {
		t.Errorf("refused: s128 %v, s256 %v, s512 %v; want s128 alone", b.refused(128), b.refused(256), b.refused(512))
	}
}

// Two children that read the records before either audits, then audit
// different functions, both leave their verdict: a function's record is
// a file of its own.
func TestCoreMLBackend_TwoChildrenKeepBothVerdicts(t *testing.T) {
	dir := t.TempDir()
	a := newTestBackend(t, dir, "Chip, macOS build 1A1")
	b := newTestBackend(t, dir, "Chip, macOS build 1A1")
	a.plans["s256"] = offEngine
	// Each holds what it read at the start; neither reads again.
	if _, err := a.audit(256); !errors.Is(err, errOffNeuralEngine) {
		t.Fatal(err)
	}
	stale := b.placements
	if _, err := b.audit(512); err != nil {
		t.Fatal(err)
	}
	if _, known := stale[256]; known {
		t.Fatal("the second child read the first one's verdict: the test proves nothing")
	}
	next := newTestBackend(t, dir, "Chip, macOS build 1A1")
	if next.refused(256) == nil || !next.placed(512) {
		t.Fatalf("records: s256 refused %v, s512 placed %v; want both verdicts", next.refused(256), next.placed(512))
	}
}

// A record that is there and cannot be read is left as it is.
func TestCoreMLBackend_UnreadableRecordIsNotReplaced(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every file")
	}
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	path := b.recordPath(128)
	if err := os.WriteFile(path, []byte("kept"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := b.audit(128); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "kept" {
		t.Fatalf("the unreadable record was replaced with %s", raw)
	}
}

// What a backend's errors say names no path under the models directory,
// and neither does a status line made of any os error.
func TestStatusLinesNameNoPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secret-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	b := newTestBackend(t, root, "Chip, macOS build 1A1")
	b.open = func(compiled, _ string, _, _ int) (aneFunction, error) {
		return nil, errors.New("coreml: load s128: could not read " + compiled + "/model.mil")
	}
	_, _, err := b.load(128)
	if err == nil || strings.Contains(err.Error(), "secret-root") {
		t.Fatalf("load error %q names the models directory", err)
	}
	b.plan = func(compiled, _ string) (coreml.Placement, error) {
		_, err := os.ReadFile(filepath.Join(compiled, "missing"))
		return coreml.Placement{}, err
	}
	if _, err = b.audit(128); err == nil || strings.Contains(err.Error(), "secret-root") {
		t.Fatalf("audit error %q names the models directory", err)
	}

	// The engine's own lines, whatever backend the error comes from.
	_, pathErr := os.ReadFile(filepath.Join(root, "model.bin"))
	linkErr := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "b"))
	for name, f := range map[string]*fakeANE{
		"preparation fails": {prepErrs: []error{fmt.Errorf("install the compiled model: %w", linkErr)}},
		"a load fails":      {loadErr: map[int]error{128: pathErr}},
		"an audit fails":    {auditErr: map[int]error{128: pathErr}},
	} {
		log := newLogLines()
		n, _ := newTestANE(t, f)
		n.logf = log.logf
		n.start()
		n.loads.Wait()
		if log.has("secret-root") || len(log.lines) == 0 {
			t.Errorf("%s: lines %v name the path", name, log.lines)
		}
	}
}
