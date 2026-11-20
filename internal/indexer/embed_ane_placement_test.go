//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	collapse  bool    // one vector for every long text
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
	case len(tokens) >= 6 && (f.collapse || tokens[len(tokens)-2] < 20000):
		out[2] = one
	case len(tokens) >= 6:
		out[3] = one
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
	state           coreml.Power
	stateErr        error
}

func (tb *testBackend) placed(length int) bool {
	placed, _ := tb.audited(length)
	return placed
}

// unsettled: the function's audit is on record as ended without a verdict.
func (tb *testBackend) unsettled(length int) bool {
	placed, known := tb.audited(length)
	return known && !placed
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
	b.power = func() (coreml.Power, error) { return tb.state, tb.stateErr }
	b.adopt() // what prepare does once the package is there
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
	if got := b.fns[0].calls; got != 4 || fn == nil {
		t.Fatalf("%d predictions on a load, want 4", got)
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

// A refusal holds for the process that found it and is never written:
// the next process loads the function and plans it for itself. A record
// that stood for the function goes with the refusal.
func TestCoreMLBackend_RefusalIsNotRecorded(t *testing.T) {
	dir := t.TempDir()
	const host = "Chip, macOS build 1A1"
	for name, plan := range map[string]coreml.Placement{
		"core operators on the cpu":            offEngine,
		"too few operations wherever they run": {Unknown: 80},
		"after a record that stood for it":     offEngine,
		"after a record of an audit left open": offEngine,
	} {
		b := newTestBackend(t, dir, host)
		switch name {
		case "after a record that stood for it":
			if _, err := b.audit(512); err != nil {
				t.Fatal(err)
			}
		case "after a record of an audit left open":
			b.abandon(512)
		}
		b = newTestBackend(t, dir, host)
		b.plans["s512"] = plan
		if _, err := b.audit(512); !errors.Is(err, errOffNeuralEngine) {
			t.Fatalf("%s: audit = %v, want a refusal", name, err)
		}
		if placed, known := b.audited(512); placed || known {
			t.Fatalf("%s: placed %v, known %v after a refusal", name, placed, known)
		}
		if files, _ := filepath.Glob(b.record + "*"); len(files) != 0 {
			t.Fatalf("%s: on disk after a refusal: %v", name, files)
		}
		next := newTestBackend(t, dir, host)
		next.plans["s512"] = plan
		if _, _, err := next.load(512); err != nil {
			t.Fatalf("%s: load in the next process: %v", name, err)
		}
		if _, err := next.audit(512); !errors.Is(err, errOffNeuralEngine) || !reflect.DeepEqual(next.planned, []string{"s512"}) {
			t.Fatalf("%s: the next process's audit = %v, planned %v; want its own plan and refusal", name, err, next.planned)
		}
	}
}

// A record holds for one chip and OS build, and carries it in its name:
// another setup audits for itself, and coming back to the first finds
// its verdict. With no setup to name, nothing is written.
func TestCoreMLBackend_RecordIsPerHostSetup(t *testing.T) {
	dir := t.TempDir()
	old := newTestBackend(t, dir, "Chip, macOS build 1A1")
	old.abandon(128)

	updated := newTestBackend(t, dir, "Chip, macOS build 1B2")
	if placed, known := updated.audited(128); placed || known {
		t.Fatalf("after an OS update: placed %v, known %v; want unaudited", placed, known)
	}
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
	if *rec.Host != "Chip, macOS build 1B2" || *rec.Function != "s128" || *rec.Verdict != verdictOn || *rec.NeuralEngine != 6000 {
		t.Fatalf("record %s", raw)
	}
	if name := filepath.Base(updated.recordPath(128)); !strings.Contains(name, ".v"+strconv.Itoa(placementVersion)+".s128.") {
		t.Fatalf("record named %s, want the records' version in it", name)
	}
	if stages, _ := filepath.Glob(updated.recordPath(128) + ".*"); len(stages) != 0 {
		t.Fatalf("staging files left: %v", stages)
	}
	if back := newTestBackend(t, dir, "Chip, macOS build 1A1"); !back.unsettled(128) {
		t.Fatal("the first setup's record did not survive the second setup's audit")
	}

	nameless := newTestBackend(t, t.TempDir(), "")
	if _, err := nameless.audit(128); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(nameless.record + "*"); len(files) != 0 {
		t.Fatalf("records without a host setup: %v", files)
	}
	if !nameless.placed(128) {
		t.Fatal("the audit does not hold for the process")
	}
}

// A plan Core ML fails to give is no verdict and nothing is recorded:
// the next process reads it again.
func TestCoreMLBackend_AuditErrorIsNoVerdict(t *testing.T) {
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	b.planErr = errors.New("MLModelErrorDomain error 0")
	if _, err := b.audit(128); err == nil || errors.Is(err, errOffNeuralEngine) {
		t.Fatalf("audit: %v, want the plan's own error", err)
	}
	if placed, known := b.audited(128); placed || known {
		t.Fatalf("placed %v, known %v without a plan", placed, known)
	}
	if _, err := os.Stat(b.recordPath(128)); !os.IsNotExist(err) {
		t.Fatalf("a record without a verdict: %v", err)
	}
}

// A plan Core ML does not answer in time ends the audit without a
// verdict, which is recorded: no later process waits for that plan again.
// So does an audit the engine gave up; a verdict that still comes
// replaces it.
func TestCoreMLBackend_AuditWithoutAVerdictIsRecorded(t *testing.T) {
	dir := t.TempDir()
	const host = "Chip, macOS build 1A1"
	b := newTestBackend(t, dir, host)
	done := make(chan struct{})
	b.planErr = &coreml.LatePlanError{Function: "s128", Waited: time.Minute, Done: done}
	var late *coreml.LatePlanError
	if _, err := b.audit(128); !errors.As(err, &late) || late.Done != (<-chan struct{})(done) {
		t.Fatalf("audit = %v, want the late plan's error", err)
	}
	b.abandon(256)
	next := newTestBackend(t, dir, host)
	if !next.unsettled(128) || !next.unsettled(256) || next.unsettled(512) {
		t.Fatalf("records: s128 %v, s256 %v, s512 %v; want the first two without a verdict",
			next.unsettled(128), next.unsettled(256), next.unsettled(512))
	}
	if _, info, err := next.load(128); err != nil || !strings.Contains(info, "audit ended without a verdict, plan skipped") {
		t.Fatalf("load: %q, %v", info, err)
	}
	// The abandoned audit returns after all: its verdict stands, and an
	// abandon that comes after a verdict changes nothing.
	b.planErr = nil
	if _, err := b.audit(256); err != nil {
		t.Fatal(err)
	}
	b.abandon(256)
	other := newTestBackend(t, dir, host)
	other.abandon(256) // it read the verdict; and the file is another process's
	if last := newTestBackend(t, dir, host); !b.placed(256) || !last.placed(256) {
		t.Fatalf("s256 placed: here %v, on record %v", b.placed(256), last.placed(256))
	}
}

// A record's staging file left by a process that died writing it is
// removed once it is old, by the next process that prepares.
func TestCoreMLBackend_StaleRecordStagesAreRemoved(t *testing.T) {
	dir := t.TempDir()
	b := newTestBackend(t, dir, "Chip, macOS build 1A1")
	stale, fresh := stagePath(b.recordPath(128), 4242), stagePath(b.recordPath(256), 4243)
	for _, file := range []string{stale, fresh} {
		if err := os.WriteFile(file, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * aneStageMaxAge)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := b.audit(512); err != nil {
		t.Fatal(err)
	}
	newTestBackend(t, dir, "Chip, macOS build 1A1")
	if _, err := os.Stat(stale); err == nil {
		t.Error("an abandoned staging file of a record was kept")
	}
	for _, file := range []string{fresh, b.recordPath(512)} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%s was removed", filepath.Base(file))
		}
	}
}

// No audit should start in Low Power Mode or under serious thermal
// pressure; a system that does not say is taken as calm.
func TestCoreMLBackend_Strain(t *testing.T) {
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	for _, c := range []struct {
		state coreml.Power
		err   error
		want  string
	}{
		{coreml.Power{}, nil, ""},
		{coreml.Power{Thermal: coreml.ThermalFair}, nil, ""},
		{coreml.Power{LowPower: true}, nil, "low power mode"},
		{coreml.Power{Thermal: coreml.ThermalSerious}, nil, "thermal pressure"},
		{coreml.Power{LowPower: true}, coreml.ErrUnsupported, ""},
	} {
		b.state, b.stateErr = c.state, c.err
		if got := b.strain(); got != c.want {
			t.Errorf("%+v, %v: strain = %q, want %q", c.state, c.err, got, c.want)
		}
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
		"one vector for every long text":           {collapse: true},
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
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
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
	// The server is told only when no Core ML call is left in flight.
	if log.has(aneOffStatus) {
		t.Fatal("the off line was written under a prediction")
	}
	f.resume <- struct{}{}
	if !<-served {
		t.Fatal("the prediction in flight was lost")
	}
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
		t.Fatalf("closed %v, want s128 once its prediction returned", closed)
	}
	if !log.has(aneOffStatus) {
		t.Fatalf("no off line after the prediction: %v", log.lines)
	}
}

// An audit does not hold the loader: a function asked for under it loads
// beside it, and its documents wait only for that load.
func TestNeuralEngine_LoadsBesideAnAudit(t *testing.T) {
	f := &fakeANE{auditGate: make(chan struct{})}
	n, clock := newTestANE(t, f)
	release := sync.OnceFunc(func() { close(f.auditGate) })
	defer release() // before the engine's cleanup, which waits for the auditor
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	if _, _, deferred := n.embedBulk(tokensOf(200)); !deferred {
		t.Fatal("a document for a function not loaded yet was not deferred")
	}
	// The audit is still running: the load is published anyway.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		n.mu.Lock()
		published := n.fns[256] != nil
		n.mu.Unlock()
		if published {
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
	release()
	n.loads.Wait()
	waitOrder(t, f, "load 128", "audit 128", "load 256", "audit 256")
}

// A function refused by its audit while it loads again, after an idle
// release, is not published by that load.
func TestNeuralEngine_ReloadDoesNotRepublishARefusedFunction(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("6014 operations on the cpu"))
	f := &fakeANE{auditErr: map[int]error{128: refused}, auditGate: make(chan struct{})}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	releaseAudit := sync.OnceFunc(func() { close(f.auditGate) })
	defer releaseAudit()
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	// Released as idle under its audit, then asked for again.
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	releaseLoad := sync.OnceFunc(func() { close(f.gate) })
	defer releaseLoad()
	n.mu.Lock()
	n.fns[128].close()
	delete(n.fns, 128)
	n.requestLocked(128)
	n.mu.Unlock()
	<-f.started // the reload is inside the backend
	releaseAudit()
	for deadline := time.Now().Add(10 * time.Second); !log.has("s128 not used: "); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the audit never refused the function")
		}
	}
	releaseLoad()
	n.loads.Wait()
	n.mu.Lock()
	published := n.fns[128] != nil
	n.mu.Unlock()
	if published {
		t.Fatal("the reload published a function its audit refused")
	}
	if _, _, closed, _ := f.snapshot(); len(closed) != 2 {
		t.Fatalf("closed %v, want the released handle and the reloaded one", closed)
	}
	if _, ok := n.embed(tokensOf(10)); ok {
		t.Fatal("a text ran on the refused function")
	}
}

// A reload discarded because the last function was refused meanwhile is
// closed before the off line is written: closing it is a Core ML call.
func TestNeuralEngine_OffLineComesAfterTheDiscardedReloadIsClosed(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("6014 operations on the cpu"))
	f := &fakeANE{auditErr: map[int]error{128: refused}, auditGate: make(chan struct{})}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	n.lengths = []int{128}
	releaseAudit := sync.OnceFunc(func() { close(f.auditGate) })
	defer releaseAudit()
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	releaseLoad := sync.OnceFunc(func() { close(f.gate) })
	defer releaseLoad()
	n.mu.Lock()
	n.fns[128].close()
	delete(n.fns, 128)
	n.requestLocked(128)
	n.mu.Unlock()
	<-f.started
	releaseAudit()
	for deadline := time.Now().Add(10 * time.Second); !log.has("s128 placement audit finished"); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the audit never finished")
		}
	}
	offAtClose := make(chan bool, 1)
	f.mu.Lock()
	f.onClose = func(int) { offAtClose <- log.has(aneOffStatus) }
	f.mu.Unlock()
	releaseLoad()
	n.loads.Wait()
	select {
	case said := <-offAtClose:
		if said {
			t.Fatal("the off line was written before the discarded handle was closed")
		}
	default:
		t.Fatal("the discarded handle was not closed")
	}
	if !log.has(aneOffStatus) {
		t.Fatalf("no off line once the handle was closed: %v", log.lines)
	}
	// The load the server saw begin is said to be over, before the off line.
	if at := log.index("s128 " + aneLoadDiscarded); at < 0 || at > log.index(aneOffStatus) {
		t.Fatalf("the discarded load's line is at %d, the off line at %d: %v", at, log.index(aneOffStatus), log.lines)
	}
}

// The child refuses an idle stop while the loader is inside Core ML,
// whose work the stop would lose, and says when that has run too long.
func TestNeuralEngine_QuiesceIsRefusedUnderALoad(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 1), recorded: true}
	log := newLogLines()
	n, clock := newTestANE(t, f)
	n.logf = log.logf
	release := sync.OnceFunc(func() { close(f.gate) })
	defer release()
	n.start()
	<-f.started
	if !log.has("s128 " + aneLoading) {
		t.Fatalf("no line for the load that began: %v", log.lines)
	}
	if ok, overdue := n.quiesce(); ok || overdue {
		t.Fatalf("under a load: quiesced %v, overdue %v", ok, overdue)
	}
	clock.add(n.auditMax)
	if ok, overdue := n.quiesce(); ok || !overdue {
		t.Fatalf("under a load past the bound: quiesced %v, overdue %v", ok, overdue)
	}
	release()
	n.loads.Wait()
	if ok, overdue := n.quiesce(); !ok || overdue {
		t.Fatalf("with nothing running: quiesced %v, overdue %v", ok, overdue)
	}
}

// The path's off line waits for the Core ML call in flight, and the
// server may stop an idle child only between audits.
func TestNeuralEngine_QuiesceAndTheOffLine(t *testing.T) {
	f := &fakeANE{auditGate: make(chan struct{})}
	n, clock := newTestANE(t, f)
	release := sync.OnceFunc(func() { close(f.auditGate) })
	defer release()
	n.start()
	waitOrder(t, f, "load 128", "audit 128")
	if ok, overdue := n.quiesce(); ok || overdue {
		t.Fatalf("under an audit: quiesced %v, overdue %v", ok, overdue)
	}
	// An audit that runs longer than any does is said to be overdue.
	clock.add(n.auditMax)
	for range 2 {
		if ok, overdue := n.quiesce(); ok || !overdue {
			t.Fatalf("under an audit past its bound: quiesced %v, overdue %v", ok, overdue)
		}
	}
	// It is given up there, once: no later child audits that function.
	f.mu.Lock()
	abandoned := append([]int(nil), f.abandoned...)
	f.mu.Unlock()
	if !reflect.DeepEqual(abandoned, []int{128}) {
		t.Fatalf("abandoned %v, want the overdue s128 once", abandoned)
	}
	release()
	n.loads.Wait()
	if ok, overdue := n.quiesce(); !ok || overdue {
		t.Fatalf("with nothing running: quiesced %v, overdue %v", ok, overdue)
	}
	// Once it agreed to stop, no audit starts.
	loadFor(n, 200)
	waitOrder(t, f, "load 128", "audit 128", "load 256")
}

// No audit starts while the machine saves power or runs hot: it waits,
// says so once, and starts when that has passed. The functions serve
// meanwhile, and the child may be stopped as idle.
func TestNeuralEngine_AuditsWaitOutLowPowerAndHeat(t *testing.T) {
	f := &fakeANE{strained: "low power mode"}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	n.auditPoll = time.Hour
	loadFor(n, 10)
	loadFor(n, 200)
	waitOrder(t, f, "load 128", "load 256")
	if _, ok := n.embed(tokensOf(10)); !ok || n.lastPlaced() {
		t.Fatalf("served %v, placed %v; want served, not placed", ok, n.lastPlaced())
	}
	n.auditTick() // still strained
	waitOrder(t, f, "load 128", "load 256")
	log.mu <- struct{}{}
	said := 0
	for _, line := range log.lines {
		if line == "placement audits wait: low power mode" {
			said++
		}
	}
	<-log.mu
	if said != 1 {
		t.Fatalf("the wait was said %d times: %v", said, log.lines)
	}
	if ok, _ := n.quiesce(); !ok {
		t.Fatal("audits that wait kept the child from an idle stop")
	}
	n.mu.Lock()
	n.quiet = false
	n.mu.Unlock()
	f.mu.Lock()
	f.strained = ""
	f.mu.Unlock()
	n.auditTick()
	n.loads.Wait()
	waitOrder(t, f, "load 128", "load 256", "audit 128", "audit 256")
	if _, ok := n.embed(tokensOf(10)); !ok || !n.lastPlaced() {
		t.Fatalf("after the audit: served %v, placed %v", ok, n.lastPlaced())
	}
}

// A function whose audit is on record as ended without a verdict is not
// audited again: it serves as Core ML.
func TestNeuralEngine_AuditWithoutAVerdictIsNotRunAgain(t *testing.T) {
	f := &fakeANE{unsettled: map[int]bool{128: true}}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	loadFor(n, 200)
	waitOrder(t, f, "load 128", "load 256", "audit 256")
	if _, ok := n.embed(tokensOf(10)); !ok || n.lastPlaced() {
		t.Fatalf("served %v, placed %v; want served as core ml", ok, n.lastPlaced())
	}
}

// A plan Core ML did not answer in time leaves its audit in flight until
// that call is back: the child refuses an idle stop, the audit's finished
// line and the path's off line wait for it, and no verdict is taken.
func TestNeuralEngine_LatePlanIsInFlightUntilCoreMLReturns(t *testing.T) {
	late := make(chan struct{})
	f := &fakeANE{auditLate: map[int]chan struct{}{128: late}}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	back := sync.OnceFunc(func() { close(late) })
	defer back()
	n.start()
	for deadline := time.Now().Add(10 * time.Second); !log.has("s128 placement not verified: "); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the late plan was not reported: %v", log.lines)
		}
	}
	if ok, _ := n.quiesce(); ok {
		t.Fatal("quiesced with Core ML inside a plan")
	}
	n.mu.Lock()
	n.turnOffLocked("test")
	n.mu.Unlock()
	if log.has("s128 "+aneAuditFinished) || log.has(aneOffStatus) {
		t.Fatalf("written with Core ML inside a plan: %v", log.lines)
	}
	back()
	n.loads.Wait()
	finished, off := log.index("s128 "+aneAuditFinished), log.index(aneOffStatus)
	if finished < 0 || off < finished {
		t.Fatalf("finished line at %d, off line at %d; want both, in that order: %v", finished, off, log.lines)
	}
	if n.verified[128] {
		t.Fatal("a late plan gave a verdict")
	}
}

// An audit that refuses the last function turns the path off: the off
// line comes after the audit's finished line, not inside its bracket.
func TestNeuralEngine_OffLineFollowsTheAuditThatCausedIt(t *testing.T) {
	refused := errors.Join(errOffNeuralEngine, errors.New("6014 operations on the cpu"))
	f := &fakeANE{auditErr: map[int]error{128: refused}}
	log := newLogLines()
	n, _ := newTestANE(t, f)
	n.logf = log.logf
	n.lengths = []int{128}
	n.start()
	n.loads.Wait()
	finished, off := log.index("s128 "+aneAuditFinished), log.index(aneOffStatus)
	if finished < 0 || off < finished {
		t.Fatalf("finished line at %d, off line at %d; want both, in that order: %v", finished, off, log.lines)
	}
}

// The server follows a child's Core ML calls by its lines: its
// preparation, its loads and its audits, each from the line that opens it
// to the one that ends it.
func TestANECalls(t *testing.T) {
	open := map[string]bool{}
	for _, step := range []struct {
		status string
		want   []string
	}{
		{"starting", []string{"prepare"}},
		{"s256 still loading after 10m0s: llama.cpp embeds its texts until it is ready", []string{"prepare"}},
		{"package compiled in 40s", nil},
		{"starting", []string{"prepare"}},
		{"s128 loading", []string{"s128 load"}},
		{"s128 ready (open 1s)", nil},
		{"s128 placement audit started", []string{"s128 audit"}},
		{"s256 loading", []string{"s128 audit", "s256 load"}},
		{"s128 failed: predict", []string{"s128 audit", "s256 load"}},
		{"s128 placement not verified: no answer", []string{"s128 audit", "s256 load"}},
		{"s256 not used: it does not predict", []string{"s128 audit"}},
		{"s512 loading", []string{"s128 audit", "s512 load"}},
		{"s512 loaded and discarded", []string{"s128 audit"}},
		// An audit outlives the path's off line when its plan is late.
		{"off, llama.cpp embeds everything: no engine", []string{"s128 audit"}},
		{"s256 placement audit finished", []string{"s128 audit"}},
		{"s128 placement audit finished", nil},
		{"starting", []string{"prepare"}},
		{"off, llama.cpp embeds everything: this machine has no neural engine", nil},
	} {
		aneCalls(open, step.status)
		var got []string
		for call := range open {
			got = append(got, call)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("after %q: open %v, want %v", step.status, got, step.want)
		}
	}
}

// Only a line that changes what a deferred text can do wakes the drain.
func TestANEWakes(t *testing.T) {
	for status, want := range map[string]bool{
		"s256 ready (open 1s)":                                                         true,
		"s256 not used: core ml plans it off":                                          true,
		"s256 not used any more: 3 failures in a row":                                  true,
		"s256 still loading after 10m0s: llama.cpp embeds its texts until it is ready": true,
		"off, llama.cpp embeds everything: no engine":                                  true,
		"starting":                               false,
		"package compiled in 40s":                false,
		"placement audits wait: low power mode":  false,
		"s256 loading":                           false,
		"s256 loaded and discarded":              false,
		"s256 failed: predict":                   false,
		"s256 released after 5m0s unused":        false,
		"s256 placement audit started":           false,
		"s256 placement audit finished":          false,
		"s256 placement verified (plan 1s)":      false,
		"s256 placement not verified: no answer": false,
		"s256 placement not recorded: denied":    false,
	} {
		if got := aneWakes(status); got != want {
			t.Errorf("aneWakes(%q) = %v, want %v", status, got, want)
		}
	}
}

// With several documents to a decode, llama.cpp takes only the ones the
// Neural Engine path leaves to it: a text behind a miss is routed on its
// own, and each miss is counted under its own reason.
func TestLocal_LlamaGroupClassifiesEachText(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{512: errors.New("refused")}}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	loadFor(n, 400) // s512, the last that holds it, fails to load
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	l := &Local{ane: n, nCtx: 2048, batchDocs: 4}
	toks := func(n int) []llama.Token { return make([]llama.Token, n) }

	// s512 failed, s256 is loading: the 200-token text waits.
	tokenized := [][]llama.Token{toks(400), toks(200)}
	lens := []int{400, 200}
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
	tokenized = [][]llama.Token{toks(400), toks(100000), toks(10), toks(400)}
	lens = []int{400, 600, 10, 400}
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
// closed, the next function takes its texts, and the path goes off with
// the last one. A function that is predicting is closed when it returns.
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
	// Its texts go to the next function, which loads for them.
	if _, ok := n.embed(tokensOf(10)); ok || n.lastMiss() != EmbedReasonFunctionLoading {
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
// verdict: the function is neither placed nor refused, and that is
// recorded, so the next process does not read the same plan again.
func TestCoreMLBackend_IncompletePlanIsNoVerdict(t *testing.T) {
	for name, plan := range map[string]coreml.Placement{
		"no device for any operation":      {Unknown: 6209},
		"a core operator without a device": {NeuralEngine: 6000, CPU: 14, Unknown: 1, UnknownCore: []string{"ios18.matmul"}},
	} {
		dir := t.TempDir()
		b := newTestBackend(t, dir, "Chip, macOS build 1A1")
		b.plans["s128"] = plan
		if _, err := b.audit(128); err == nil || errors.Is(err, errOffNeuralEngine) {
			t.Errorf("%s: audit = %v, want an error that is not a refusal", name, err)
		}
		if !b.unsettled(128) {
			t.Errorf("%s: not held as audited without a verdict", name)
		}
		next := newTestBackend(t, dir, "Chip, macOS build 1A1")
		if _, _, err := next.load(128); err != nil || !next.unsettled(128) {
			t.Errorf("%s: the next process: load %v, without a verdict %v", name, err, next.unsettled(128))
		}
	}
	// Constants without a device do not hold a verdict back.
	b := newTestBackend(t, t.TempDir(), "Chip, macOS build 1A1")
	b.plans["s128"] = coreml.Placement{NeuralEngine: 6000, CPU: 14, Unknown: 900}
	if _, err := b.audit(128); err != nil || !b.placed(128) {
		t.Fatalf("audit = %v, placed %v", err, b.placed(128))
	}
}

// Only a record with every field, for this model, setup and function, is
// one: a placement whose counts the placement rule puts on the Neural
// Engine, or an audit without a verdict. Anything else is unaudited.
func TestCoreMLBackend_RecordsAreChecked(t *testing.T) {
	const host = "Chip, macOS build 1A1"
	model := testPackage.id()
	head := `{"model":"` + model + `","host":"` + host + `","function":"s128"`
	const on = `,"verdict":"on","neuralEngine":6000,"gpu":0,"cpu":14,"unknown":0}`
	path := func(dir string, length int) string { return newTestBackend(t, dir, host).recordPath(length) }
	for name, raw := range map[string]string{
		"no verdict":                                           head + `}`,
		"counts without the verdict":                           head + `,"neuralEngine":6000,"gpu":0,"cpu":14,"unknown":0}`,
		"a placement without its counts":                       head + `,"verdict":"on"}`,
		"a count missing":                                      head + `,"verdict":"on","gpu":0,"cpu":14,"unknown":0}`,
		"the unknown count missing":                            head + `,"verdict":"on","neuralEngine":6000,"gpu":0,"cpu":14}`,
		"a placement its counts do not give":                   head + `,"verdict":"on","neuralEngine":0,"gpu":0,"cpu":14,"unknown":0}`,
		"a count of the wrong type":                            head + `,"verdict":"on","neuralEngine":6000,"gpu":0,"cpu":"14","unknown":0}`,
		"more core operators on the cpu than operations there": head + `,"verdict":"on","neuralEngine":6000,"gpu":0,"cpu":0,"unknown":0,"cpuCore":["ios18.matmul"]}`,
		"counts that settle nothing":                           head + `,"verdict":"on","neuralEngine":60,"gpu":0,"cpu":10,"unknown":80}`,
		"a refusal":                                            head + `,"verdict":"off","neuralEngine":0,"gpu":0,"cpu":6014,"unknown":0,"cpuCore":["ios18.matmul"]}`,
		"a verdict of another kind":                            head + `,"verdict":true}`,
		"another function's record":                            strings.Replace(head, "s128", "s256", 1) + on,
		"another model's record":                               strings.Replace(head, model, "other-12345678", 1) + on,
		"another setup's record":                               strings.Replace(head, host, "Chip, macOS build 9Z9", 1) + on,
		"not json":                                             `{"model":`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(path(dir, 128), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		b := newTestBackend(t, dir, host)
		if placed, known := b.audited(128); placed || known {
			t.Errorf("%s: placed %v, known %v; want unaudited", name, placed, known)
		}
	}
	// The sound ones are read, a file under a name that is not the
	// function's own is nobody's, and neither is another version's.
	dir := t.TempDir()
	own := path(dir, 128)
	version := ".v" + strconv.Itoa(placementVersion) + "."
	for file, raw := range map[string]string{
		own: head + on,
		strings.Replace(own, ".s128.", ".s0256.", 1): head + on,
		strings.Replace(own, ".s128.", ".512.", 1):   head + on,
		path(dir, 1024): strings.Replace(head, "s128", "s1024", 1) + `,"verdict":"none"}`,
		strings.Replace(path(dir, 2048), version, ".v1.", 1): strings.Replace(head, "s128", "s2048", 1) + on,
		strings.Replace(path(dir, 2048), version, ".", 1):    strings.Replace(head, "s128", "s2048", 1) + on,
	} {
		if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := newTestBackend(t, dir, host)
	if !b.placed(128) || b.placed(256) || b.placed(512) || !b.unsettled(1024) || b.placed(2048) {
		t.Errorf("placed: s128 %v, s256 %v, s512 %v, s2048 %v; s1024 without a verdict %v; want s128 placed and s1024 left open",
			b.placed(128), b.placed(256), b.placed(512), b.placed(2048), b.unsettled(1024))
	}
}

// Two children that read the records before either audits, then audit
// different functions, both leave their verdict: a function's record is
// a file of its own.
func TestCoreMLBackend_TwoChildrenKeepBothVerdicts(t *testing.T) {
	dir := t.TempDir()
	a := newTestBackend(t, dir, "Chip, macOS build 1A1")
	b := newTestBackend(t, dir, "Chip, macOS build 1A1")
	// Each holds what it read at the start; neither reads again.
	if _, err := a.audit(256); err != nil {
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
	if !next.placed(256) || !next.placed(512) {
		t.Fatalf("records: s256 placed %v, s512 placed %v; want both verdicts", next.placed(256), next.placed(512))
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
	clean := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: no error", what)
		}
		for _, part := range []string{"secret-root", "m.mlmodelc", "m.mlpackage"} {
			if strings.Contains(err.Error(), part) {
				t.Errorf("%s: %q names %s", what, err, part)
			}
		}
	}
	b := newTestBackend(t, root, "Chip, macOS build 1A1")
	b.open = func(compiled, _ string, _, _ int) (aneFunction, error) {
		return nil, errors.New("coreml: load s128: could not read " + compiled + "/model.mil")
	}
	_, _, err := b.load(128)
	clean("a load error", err)
	b.plan = func(compiled, _ string) (coreml.Placement, error) {
		_, err := os.ReadFile(filepath.Join(compiled, "missing"))
		return coreml.Placement{}, err
	}
	_, err = b.audit(128)
	clean("an audit error", err)
	// A prediction of a function that loaded fine.
	failing := &planFn{}
	b.open = func(string, string, int, int) (aneFunction, error) { return failing, nil }
	fn, _, err := b.load(128)
	if err != nil {
		t.Fatal(err)
	}
	failing.err = errors.New("coreml: s128 predict: file://" + b.compiled + "/weights/weight.bin is gone, and so is " + b.dir)
	clean("a serving error", fn.embed(tokensOf(10), make([]float32, 4)))

	// The engine's own lines, whatever backend the error comes from.
	_, pathErr := os.ReadFile(filepath.Join(root, "model.bin"))
	linkErr := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "b"))
	for name, f := range map[string]*fakeANE{
		"preparation fails": {prepErrs: []error{fmt.Errorf("install the compiled model: %w", linkErr)}},
		"a load fails":      {loadErr: map[int]error{128: pathErr}},
		"an audit fails":    {auditErr: map[int]error{128: pathErr}},
		// A copy between volumes: the destination, and inside it the source.
		"a copy fails": {prepErrs: []error{fmt.Errorf("install the compiled model: %w", &fs.PathError{Op: "CopyFS",
			Path: filepath.Join(root, "m.mlmodelc.1.tmp"),
			Err:  &fs.PathError{Op: "open", Path: "/var/folders/secret-tmp/model.mil", Err: fs.ErrPermission}})}},
		"a prediction fails": {embedErr: map[int]error{0: pathErr}},
	} {
		log := newLogLines()
		n, _ := newTestANE(t, f)
		n.logf = log.logf
		n.start()
		n.loads.Wait()
		n.embed(tokensOf(10))
		n.loads.Wait()
		if log.has("secret-root") || log.has("secret-tmp") || len(log.lines) == 0 {
			t.Errorf("%s: lines %v name the path", name, log.lines)
		}
	}
}
