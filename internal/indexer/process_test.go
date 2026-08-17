//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
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

// One embed drain reports started → progress (cumulative Done) →
// done; an empty drain reports nothing; a failing embedder turns the
// terminal report into failed with the error message.
func TestEmbedDrainOnProcess(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	var updates []ProcessUpdate
	ix := &Indexer{store: st, opts: Options{
		Embedder:  unitEmbedder{dim: 4},
		OnProcess: func(u ProcessUpdate) { updates = append(updates, u) },
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
	if u := updates[0]; u.Phase != ProcessStarted || u.SpaceId != "sp1" || u.Done != 0 {
		t.Errorf("first = %+v, want started", u)
	}
	if u := updates[len(updates)-1]; u.Phase != ProcessDone || u.Done != 2 {
		t.Errorf("last = %+v, want done with Done=2", u)
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
