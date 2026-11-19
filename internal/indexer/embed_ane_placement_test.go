//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"encoding/json"
	"errors"
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
// does: the first axis for a text under six tokens, else the second.
type planFn struct {
	err      error
	constant bool
	calls    int
	closed   bool
}

func (f *planFn) embed(tokens []int32, out []float32) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	clear(out)
	if len(tokens) < 6 || f.constant {
		out[0] = 1
	} else {
		out[1] = 1
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

var (
	onEngine  = coreml.Placement{NeuralEngine: 6000, CPU: 14}
	offEngine = coreml.Placement{CPU: 6014, CPUCore: []string{"ios18.matmul"}}
)

func newTestBackend(t *testing.T, dir, host string) *testBackend {
	t.Helper()
	tb := &testBackend{plans: map[string]coreml.Placement{}}
	b := newCoreMLBackend(&coreMLPackage{name: "m"}, filepath.Join(dir, "m.mlpackage"), 4, t.Logf)
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
	if got := fn.(*planFn).calls; got != 2 {
		t.Fatalf("%d predictions on a load, want 2", got)
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
	raw, err := os.ReadFile(updated.record)
	if err != nil {
		t.Fatal(err)
	}
	var rec placementRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Version != placementVersion || rec.Host != "Chip, macOS build 1B2" || !rec.Functions["s128"].On {
		t.Fatalf("record %s", raw)
	}
	if stages, _ := filepath.Glob(updated.record + ".*"); len(stages) != 0 {
		t.Fatalf("staging files left: %v", stages)
	}

	nameless := newTestBackend(t, t.TempDir(), "")
	if _, err := nameless.audit(128); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nameless.record); !os.IsNotExist(err) {
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
	if _, err := os.Stat(b.record); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict: %v", err)
	}
}

// A function that opens but does not predict the model's vectors is not
// loaded, and is closed.
func TestCoreMLBackend_LoadValidates(t *testing.T) {
	for name, broken := range map[string]*planFn{
		"a failed prediction": {err: errors.New("predict failed")},
		"one vector for all":  {constant: true},
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
}

// A function serves as soon as it is loaded, as Core ML; it is the
// Neural Engine's once its audit passed. Audits run after every queued
// load, one per function.
func TestNeuralEngine_AuditsAfterLoads(t *testing.T) {
	f := &fakeANE{auditGate: make(chan struct{})}
	n, _ := newTestANE(t, f)
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	if _, ok := n.embed(tokensOf(10)); !ok || n.lastPlaced() {
		t.Fatalf("under the audit: served %v, placed %v; want served and not placed", ok, n.lastPlaced())
	}
	// Asked for while the audit runs: both load before the next audit.
	n.embed(tokensOf(200))
	n.embed(tokensOf(400))
	close(f.auditGate)
	n.loads.Wait()
	waitOrder(t, f, "load 128", "audit 128", "load 256", "load 512", "audit 256", "audit 512")
	if _, ok := n.embed(tokensOf(10)); !ok || !n.lastPlaced() {
		t.Fatalf("after the audit: served %v, placed %v", ok, n.lastPlaced())
	}
	// A reload after an idle release is not audited again.
	n.mu.Lock()
	n.fns[256].close()
	delete(n.fns, 256)
	n.mu.Unlock()
	loadFor(n, 200)
	waitOrder(t, f, "load 128", "audit 128", "load 256", "load 512", "audit 256", "audit 512", "load 256")
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
