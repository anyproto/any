package indexer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/index"
)

// unitEmbedder returns constant unit-ish vectors so drained docs land.
type unitEmbedder struct{ dim int }

func (u unitEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		vec := make([]float32, u.dim)
		vec[0] = 1
		out[i] = vec
	}
	return out, nil
}
func (u unitEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	vec := make([]float32, u.dim)
	vec[0] = 1
	return vec, nil
}
func (u unitEmbedder) Dim(context.Context) (int, error) { return u.dim, nil }

// staticIdSpace satisfies the one space.Space method the embed path
// touches (Id); anything else panics loudly.
type staticIdSpace struct{ space.Space }

func (staticIdSpace) Id() string { return "sp1" }

// One announced embed drain reports started → progress (cumulative
// Done) → done; an empty drain reports nothing; a failing embedder
// turns the terminal report into failed with the error message.
// AnnounceAfter -1 = immediate mode; the default gate is covered by
// TestEmbedDrainAnnounceGate.
func TestEmbedDrainOnProcess(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	var updates []ProcessUpdate
	ix := &Indexer{store: st, opts: Options{
		Embedder:      unitEmbedder{dim: 4},
		AnnounceAfter: -1,
		OnProcess:     func(u ProcessUpdate) { updates = append(updates, u) },
	}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}

	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("empty drain reported %+v", updates)
	}

	if err := st.Apply(ctx, "sp1", []DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "m1", "hello world", 1)},
		{Entry: entry("chat", "o1", "chat_messages", "m2", "second doc", 2)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(updates) < 3 {
		t.Fatalf("drain reported %+v, want started/progress/done", updates)
	}
	if u := updates[0]; u.Phase != ProcessStarted || u.Kind != ProcessKindEmbed ||
		u.SpaceId != "sp1" || u.Done != 0 || u.Total != 2 {
		t.Errorf("first = %+v, want started with Total=2 (pending count)", u)
	}
	if u := updates[len(updates)-1]; u.Phase != ProcessDone || u.Done != 2 || u.Total != 2 {
		t.Errorf("last = %+v, want done with Done=Total=2", u)
	}
	if u := updates[1]; u.Phase != ProcessProgress || u.Done != 2 {
		t.Errorf("progress = %+v, want cumulative Done=2", u)
	}

	updates = nil
	ix.opts.Embedder = &stubEmbedder{dim: 4, fail: true}
	if err := st.Apply(ctx, "sp1", []DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "m3", "third doc", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err == nil {
		t.Fatal("failing drain returned nil")
	}
	if len(updates) != 2 || updates[0].Phase != ProcessStarted ||
		updates[1].Phase != ProcessFailed || updates[1].Message == "" {
		t.Errorf("failing drain reported %+v, want started+failed{message}", updates)
	}
}

// cancellingEmbedder cancels its context on first use — simulates the
// worker being stopped (space dropped) mid-drain.
type cancellingEmbedder struct{ cancel context.CancelFunc }

func (e cancellingEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	e.cancel()
	return nil, ctx.Err()
}
func (e cancellingEmbedder) EmbedQuery(ctx context.Context, _ string) ([]float32, error) {
	return nil, ctx.Err()
}
func (e cancellingEmbedder) Dim(context.Context) (int, error) { return 4, nil }

// A drain interrupted by worker-context cancellation reports a
// terminal cancelled — never a silent exit that leaves a ghost
// running row, and never failed (shutdown isn't a failure).
func TestEmbedDrainOnProcessCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := mustStore(t, 4)
	var updates []ProcessUpdate
	ix := &Indexer{store: st, opts: Options{
		Embedder:      cancellingEmbedder{cancel: cancel},
		AnnounceAfter: -1,
		OnProcess:     func(u ProcessUpdate) { updates = append(updates, u) },
	}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}

	if err := st.Apply(context.Background(), "sp1", []DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "m1", "doomed doc", 1)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err == nil {
		t.Fatal("cancelled drain returned nil")
	}
	if len(updates) != 2 || updates[0].Phase != ProcessStarted || updates[1].Phase != ProcessCancelled {
		t.Errorf("cancelled drain reported %+v, want started+cancelled", updates)
	}
}

// fakeChanges serves a fixed ascending change list page by page.
type fakeChanges struct {
	space.ChangeIndexAPI
	changes []space.ObjectChange
}

func (f fakeChanges) Generation(context.Context) (string, error) { return "gen-1", nil }

func (f fakeChanges) ChangedSince(_ context.Context, since uint64, limit int) ([]space.ObjectChange, error) {
	var out []space.ObjectChange
	for _, ch := range f.changes {
		if ch.ApplySeq > since {
			out = append(out, ch)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

type ftsSpace struct {
	space.Space
	ch fakeChanges
}

func (s ftsSpace) Id() string                    { return "sp1" }
func (s ftsSpace) Changes() space.ChangeIndexAPI { return s.ch }

// The fts producer is gated on elapsed work (AnnounceAfter): a fast
// pass — even multi-page — stays silent under the default gate; past
// the gate (immediate mode here) it reports started → progress per
// page → done with cumulative change counts.
func TestAdvanceOnProcess(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	mk := func(n int) []space.ObjectChange {
		out := make([]space.ObjectChange, n)
		for i := range out {
			out[i] = space.ObjectChange{ObjectId: fmt.Sprintf("obj%d", i), ApplySeq: uint64(i + 1), Deleted: true}
		}
		return out
	}
	var updates []ProcessUpdate
	newWorker := func(changes []space.ObjectChange, announceAfter time.Duration) *spaceWorker {
		ix := &Indexer{store: st, reg: index.NewRegistry(), opts: Options{
			BatchLimit:    4,
			AnnounceAfter: announceAfter,
			OnProcess:     func(u ProcessUpdate) { updates = append(updates, u) },
		}.withDefaults()}
		return &spaceWorker{ix: ix, sp: ftsSpace{ch: fakeChanges{changes: changes}}}
	}

	// Default gate (3s): a fast multi-page pass is usual indexing —
	// silent.
	if err := newWorker(mk(9), 0).advance(ctx); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("fast multi-page advance reported %+v, want silence", updates)
	}

	// Cursor sits at 9 now; 9 more changes → pages of 4, 4, 1 past an
	// open gate: started at work start (before the first page lands),
	// progress per page, done.
	if err := newWorker(mk(18), -1).advance(ctx); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 5 {
		t.Fatalf("announced advance reported %+v, want started/3×progress/done", updates)
	}
	if u := updates[0]; u.Phase != ProcessStarted || u.Kind != ProcessKindFTS ||
		u.SpaceId != "sp1" || u.Done != 0 {
		t.Errorf("first = %+v, want fts started at Done=0 (work start)", u)
	}
	if u := updates[4]; u.Phase != ProcessDone || u.Done != 9 {
		t.Errorf("last = %+v, want done with Done=9", u)
	}
}

// Usual embedding — a quick drain finishing well inside AnnounceAfter
// — never reaches the view: no started, no terminal.
func TestEmbedDrainAnnounceGate(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	var updates []ProcessUpdate
	ix := &Indexer{store: st, opts: Options{
		Embedder:  unitEmbedder{dim: 4},
		OnProcess: func(u ProcessUpdate) { updates = append(updates, u) },
	}.withDefaults()} // default AnnounceAfter: 3s
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}

	if err := st.Apply(ctx, "sp1", []DocUpsert{
		{Entry: entry("chat", "o1", "chat_messages", "m1", "one quick message", 1)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("fast drain reported %+v, want silence", updates)
	}
}
