package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anyproto/any/internal/api"
)

func procEvent(typ, identity, id string, self bool, data processEventData) *api.Event {
	payload, _ := json.Marshal(data)
	return &api.Event{
		Type:   typ,
		Scope:  api.EventScopeDevice,
		Target: id,
		Data:   payload,
		Sender: &api.EventSender{Identity: identity, Self: self},
	}
}

// testRegistry returns a registry on a manually advanced clock.
func testRegistry() (*processRegistry, *time.Time) {
	now := time.Unix(1_700_000_000, 0)
	r := newProcessRegistry()
	r.now = func() time.Time { return now }
	return r, &now
}

// A lone progress heartbeat fully materializes a process (descriptor
// folded into every frame), and later frames win field-by-field.
func TestProcessRegistry_MaterializeAndUpsert(t *testing.T) {
	r, _ := testRegistry()

	r.apply(procEvent(api.EventProcessProgress, "acctA", "p1", false,
		processEventData{Kind: "agent.run", Title: "Run", Done: 3, Total: 10, Message: "step 3"}))
	got := r.list()
	if len(got) != 1 {
		t.Fatalf("list = %d entries, want 1", len(got))
	}
	p := got[0]
	if p.State != api.ProcessStateRunning || p.Kind != "agent.run" || p.Title != "Run" ||
		p.Done != 3 || p.Total != 10 || p.Message != "step 3" || p.Self {
		t.Errorf("materialized = %+v", p)
	}

	// started resets the row (owner restart).
	r.apply(procEvent(api.EventProcessStarted, "acctA", "p1", false,
		processEventData{Kind: "agent.run", Title: "Run"}))
	if p := r.list()[0]; p.Done != 0 || p.State != api.ProcessStateRunning {
		t.Errorf("after re-start = %+v, want done reset", p)
	}

	// terminal failed carries the error.
	r.apply(procEvent(api.EventProcessFailed, "acctA", "p1", false,
		processEventData{Kind: "agent.run", Title: "Run", Error: &api.ProcessError{Code: "boom", Message: "it broke"}}))
	if p := r.list()[0]; p.State != api.ProcessStateFailed || p.Error == nil || p.Error.Code != "boom" {
		t.Errorf("after failed = %+v", p)
	}
}

// process.cancel is a directive, not a state change — it never creates
// or mutates a row. Non-process types are ignored entirely.
func TestProcessRegistry_IgnoresCancelAndForeignTypes(t *testing.T) {
	r, _ := testRegistry()
	cancel, _ := json.Marshal(processCancelData{Identity: "acctA"})
	r.apply(&api.Event{Type: api.EventProcessCancel, Scope: api.EventScopeDevice, Target: "p1",
		Data: cancel, Sender: &api.EventSender{Identity: "acctB"}})
	r.apply(&api.Event{Type: "ui.open_space", Scope: api.EventScopeDevice, Target: "p1",
		Sender: &api.EventSender{Identity: "acctA"}})
	// No target (process id) → ignored.
	r.apply(&api.Event{Type: api.EventProcessStarted, Scope: api.EventScopeDevice,
		Sender: &api.EventSender{Identity: "acctA"}})
	if got := r.list(); len(got) != 0 {
		t.Fatalf("list = %+v, want empty", got)
	}
}

// A terminal error never survives a state change: progress after
// failed resurrects the row to running with the error cleared, and a
// later non-failed terminal clears it too.
func TestProcessRegistry_ErrorCleared(t *testing.T) {
	r, _ := testRegistry()
	fail := processEventData{Kind: "k", Title: "t", Error: &api.ProcessError{Code: "x", Message: "m"}}
	r.apply(procEvent(api.EventProcessFailed, "a", "p1", false, fail))
	r.apply(procEvent(api.EventProcessProgress, "a", "p1", false, processEventData{Kind: "k", Title: "t", Done: 1}))
	if p := r.list()[0]; p.State != api.ProcessStateRunning || p.Error != nil {
		t.Errorf("after resurrect = %+v, want running with no error", p)
	}
	r.apply(procEvent(api.EventProcessFailed, "a", "p1", false, fail))
	r.apply(procEvent(api.EventProcessDone, "a", "p1", false, processEventData{Kind: "k", Title: "t"}))
	if p := r.list()[0]; p.State != api.ProcessStateDone || p.Error != nil {
		t.Errorf("after done = %+v, want done with no error", p)
	}
}

// Rows can materialize from unvalidated remote/hand-emitted payloads;
// grammar-violating descriptor fields are dropped and oversized text
// clipped rune-safely before entering the view (progress/finish
// re-emit stored values, so nothing invalid may be stored).
func TestProcessRegistry_SanitizesData(t *testing.T) {
	r, _ := testRegistry()
	long := strings.Repeat("é", 300) // 600 bytes, multi-byte runes
	r.apply(procEvent(api.EventProcessStarted, "a", "p1", false,
		processEventData{Kind: "bad kind!", Title: long, Target: "sp ace"}))
	p := r.list()[0]
	if p.Kind != "" || p.Target != "" {
		t.Errorf("invalid kind/target stored: %+v", p)
	}
	if len(p.Title) > processTitleMaxLen || !utf8.ValidString(p.Title) || p.Title == "" {
		t.Errorf("title not clipped rune-safely: %d bytes, valid=%v", len(p.Title), utf8.ValidString(p.Title))
	}
}

// The embedder's state on an index.embed row: each frame's object
// replaces the row's, so backends a later frame leaves out are gone; a
// frame without the object keeps it; the row's end drops it; and what is
// outside the vocabulary never enters the view.
func TestProcessRegistry_Embedding(t *testing.T) {
	r, _ := testRegistry()
	frame := func(typ string, emb *api.ProcessEmbedding) {
		r.apply(procEvent(typ, "a", "index.embed.sp", false,
			processEventData{Kind: "index.embed", Title: "t", Done: 1, Embedding: emb}))
	}
	row := func() *api.ProcessEmbedding { return r.list()[0].Embedding }

	frame(api.EventProcessStarted, &api.ProcessEmbedding{Phase: "waiting"})
	if e := row(); e == nil || e.Phase != "waiting" || e.Backends != nil {
		t.Fatalf("started: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"cpu", "ane", "cpu"}, Reason: "function_loading"})
	if e := row(); e == nil || e.Phase != "embedding" || !slices.Equal(e.Backends, []string{"ane", "cpu"}) || e.Reason != "function_loading" {
		t.Fatalf("a mixed batch: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"coreml", "ane"}})
	if e := row(); e == nil || !slices.Equal(e.Backends, []string{"ane", "coreml"}) {
		t.Fatalf("verified and unverified functions: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"cpu", "ane", "cpu"}, Reason: "function_loading"})
	frame(api.EventProcessProgress, nil) // a partial frame keeps the state
	if e := row(); e == nil || len(e.Backends) != 2 {
		t.Fatalf("a frame without the object: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "preparing"})
	if e := row(); e == nil || e.Phase != "preparing" || e.Backends != nil || e.Reason != "" {
		t.Fatalf("a later frame without backends kept them: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"quantum", "gpu"}, Reason: "<script>"})
	if e := row(); e == nil || !slices.Equal(e.Backends, []string{"gpu"}) || e.Reason != "" {
		t.Fatalf("values outside the vocabulary: %+v", e)
	}
	frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "dreaming", Backends: []string{"gpu"}})
	if e := row(); e == nil || e.Phase != "embedding" {
		t.Fatalf("an unknown phase replaced the state: %+v", e)
	}
	// Every end of the row drops it, and a late heartbeat does not bring
	// it back.
	for _, end := range []string{api.EventProcessDone, api.EventProcessFailed, api.EventProcessCancelled} {
		frame(api.EventProcessProgress, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"gpu"}})
		if e := row(); e == nil {
			t.Fatalf("before %s: no state on a running row", end)
		}
		frame(end, &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"gpu"}})
		if e := row(); e != nil {
			t.Fatalf("%s kept it: %+v", end, e)
		}
		frame(api.EventProcessProgress, nil)
		if e := row(); e != nil {
			t.Fatalf("a heartbeat after %s brought it back: %+v", end, e)
		}
	}

	// It is the embed drain's: a row of another kind never has it.
	r.apply(procEvent(api.EventProcessProgress, "a", "upload.1", false,
		processEventData{Kind: "file.upload", Title: "t", Embedding: &api.ProcessEmbedding{Phase: "embedding", Backends: []string{"gpu"}}}))
	for _, p := range r.list() {
		if p.Id == "upload.1" && p.Embedding != nil {
			t.Fatalf("a %s row took the embedder's state: %+v", p.Kind, p.Embedding)
		}
	}
}

// The composite key (identity, id) keeps same-id processes of
// different publishers apart; byId returns the full resolution set.
func TestProcessRegistry_CompositeKey(t *testing.T) {
	r, _ := testRegistry()
	r.apply(procEvent(api.EventProcessStarted, "acctA", "p1", false, processEventData{Kind: "k", Title: "A"}))
	r.apply(procEvent(api.EventProcessStarted, "acctB", "p1", true, processEventData{Kind: "k", Title: "B"}))
	if got := r.list(); len(got) != 2 {
		t.Fatalf("list = %d entries, want 2", len(got))
	}
	if got := r.byId("p1"); len(got) != 2 || got[0].Identity != "acctA" || got[1].Identity != "acctB" {
		t.Errorf("byId = %+v", got)
	}
	if p, ok := r.get("acctB", "p1"); !ok || p.Title != "B" || !p.Self {
		t.Errorf("get(acctB) = %+v ok=%v", p, ok)
	}
	if _, ok := r.get("acctC", "p1"); ok {
		t.Error("get(acctC) found a process")
	}
}

// Running rows expire 45s after the last frame (missed heartbeats);
// terminal rows linger 60s so consumers observe the outcome. Sweeping
// is lazy — list/get/apply all sweep.
func TestProcessRegistry_Expiry(t *testing.T) {
	r, now := testRegistry()
	r.apply(procEvent(api.EventProcessStarted, "acctA", "run", false, processEventData{Kind: "k", Title: "R"}))
	r.apply(procEvent(api.EventProcessDone, "acctA", "fin", false, processEventData{Kind: "k", Title: "F"}))

	*now = now.Add(30 * time.Second)
	// Heartbeat keeps the running row alive.
	r.apply(procEvent(api.EventProcessProgress, "acctA", "run", false, processEventData{Kind: "k", Title: "R", Done: 1}))
	if got := r.list(); len(got) != 2 {
		t.Fatalf("at +30s list = %d entries, want 2", len(got))
	}

	*now = now.Add(31 * time.Second) // +61s: terminal past 60s, running heartbeat 31s ago
	got := r.list()
	if len(got) != 1 || got[0].Id != "run" {
		t.Fatalf("at +61s list = %+v, want only the heartbeat-kept run", got)
	}

	*now = now.Add(46 * time.Second) // 77s since last heartbeat
	if got := r.list(); len(got) != 0 {
		t.Fatalf("stale run still listed: %+v", got)
	}
}

// list order is stable: first-seen time, then identity, then id.
func TestProcessRegistry_ListOrder(t *testing.T) {
	r, now := testRegistry()
	r.apply(procEvent(api.EventProcessStarted, "b", "p2", false, processEventData{Kind: "k", Title: "t"}))
	r.apply(procEvent(api.EventProcessStarted, "a", "p3", false, processEventData{Kind: "k", Title: "t"}))
	*now = now.Add(time.Second)
	r.apply(procEvent(api.EventProcessStarted, "a", "p1", false, processEventData{Kind: "k", Title: "t"}))
	got := r.list()
	if len(got) != 3 || got[0].Identity != "a" || got[0].Id != "p3" ||
		got[1].Identity != "b" || got[2].Id != "p1" {
		t.Errorf("order = %+v", got)
	}
}

// Frames from remote publishers fold with absent-means-keep pointer
// semantics and negative counters clamped — the doc's "nothing
// invalid is ever stored or relayed" covers numbers too.
func TestProcessRegistry_CounterFoldAndClamp(t *testing.T) {
	r, _ := testRegistry()
	raw := func(typ, data string) *api.Event {
		return &api.Event{Type: typ, Scope: api.EventScopeDevice, Target: "p1",
			Data: json.RawMessage(data), Sender: &api.EventSender{Identity: "a"}}
	}
	// started carries counters (embed total, download Content-Length).
	r.apply(raw(api.EventProcessStarted, `{"kind":"k","title":"t","done":0,"total":40}`))
	if p := r.list()[0]; p.Total != 40 {
		t.Errorf("started counters dropped: %+v", p)
	}
	// A partial remote frame keeps what it doesn't mention.
	r.apply(raw(api.EventProcessProgress, `{"done":7}`))
	if p := r.list()[0]; p.Done != 7 || p.Total != 40 {
		t.Errorf("partial fold = %+v, want done=7 total kept at 40", p)
	}
	// Negative counters are clamped, not stored.
	r.apply(raw(api.EventProcessProgress, `{"done":-5,"total":-1}`))
	if p := r.list()[0]; p.Done != 0 || p.Total != 0 {
		t.Errorf("negative counters stored: %+v", p)
	}
	// Terminal frames carry final counters.
	r.apply(raw(api.EventProcessDone, `{"done":40,"total":40}`))
	if p := r.list()[0]; p.State != api.ProcessStateDone || p.Done != 40 || p.Total != 40 {
		t.Errorf("terminal counters dropped: %+v", p)
	}
}
