package server

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakePubSub records Subscribe/cancel calls per pattern.
type fakePubSub struct {
	mu     sync.Mutex
	active map[string]int // pattern → live subscription count
	cbs    map[string][]func(space.PubSubMessage)
	err    error
}

func newFakePubSub() *fakePubSub {
	return &fakePubSub{active: map[string]int{}, cbs: map[string][]func(space.PubSubMessage){}}
}

func (f *fakePubSub) Publish(ctx context.Context, topic string, payload []byte) error { return f.err }

func (f *fakePubSub) Subscribe(pattern string, cb func(space.PubSubMessage)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.active[pattern]++
	f.cbs[pattern] = append(f.cbs[pattern], cb)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.active[pattern]--
	}, nil
}

func (f *fakePubSub) activePatterns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p, n := range f.active {
		if n > 0 {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// dispatch fires msg at every live callback whose pattern matches the
// message topic — a stand-in for the SDK's per-subscription dispatch
// (a concrete topic is a wildcard-free pattern, so subsumption is
// exactly the match test).
func (f *fakePubSub) dispatch(msg space.PubSubMessage) {
	f.mu.Lock()
	var cbs []func(space.PubSubMessage)
	for p, list := range f.cbs {
		if f.active[p] > 0 && patternSubsumes(p, msg.Topic) {
			cbs = append(cbs, list...)
		}
	}
	f.mu.Unlock()
	for _, cb := range cbs {
		cb(msg)
	}
}

func testBridge(d *deps, fakes map[string]*fakePubSub) *eventsBridge {
	b := newEventsBridge(d)
	b.resolve = func(_ context.Context, key string) (space.PubSubAPI, error) {
		if ps, ok := fakes[key]; ok {
			return ps, nil
		}
		return nil, space.ErrPubSubInvalidTopic // stand-in "unknown space"
	}
	return b
}

func TestEventsBridgeRefcount(t *testing.T) {
	d := &deps{}
	acc := newFakePubSub()
	b := testBridge(d, map[string]*fakePubSub{"": acc})
	ctx := context.Background()

	// Two subscribers sharing a pattern → one SDK subscription.
	rel1, err := b.acquire(ctx, true, nil, []string{"ev/process/>"})
	if err != nil {
		t.Fatal(err)
	}
	rel2, err := b.acquire(ctx, true, nil, []string{"ev/process/>"})
	if err != nil {
		t.Fatal(err)
	}
	if got := acc.activePatterns(); !slices.Equal(got, []string{"ev/process/>"}) {
		t.Fatalf("active = %v, want one shared ev/process/>", got)
	}

	// A narrower pattern subsumed by the active one adds nothing.
	rel3, err := b.acquire(ctx, true, nil, []string{"ev/process/progress/*"})
	if err != nil {
		t.Fatal(err)
	}
	if got := acc.activePatterns(); !slices.Equal(got, []string{"ev/process/>"}) {
		t.Fatalf("active = %v, want the broad pattern only", got)
	}

	// Dropping both broad refs re-subscribes the narrow survivor.
	rel1()
	rel2()
	if got := acc.activePatterns(); !slices.Equal(got, []string{"ev/process/progress/*"}) {
		t.Fatalf("active = %v, want the narrow pattern after broad release", got)
	}

	rel3()
	if got := acc.activePatterns(); got != nil {
		t.Fatalf("active = %v, want none after full release", got)
	}
	if len(b.scopes) != 0 {
		t.Fatalf("scopes not cleaned up: %v", b.scopes)
	}
}

// A failed re-subscribe on the release path (broad pattern released,
// narrower survivor must be re-subscribed) does not strand the
// remaining subscribers: the bridge retries in the background until
// the desired set is active again.
func TestEventsBridgeResyncRetry(t *testing.T) {
	old := bridgeResyncRetry
	bridgeResyncRetry = 10 * time.Millisecond
	defer func() { bridgeResyncRetry = old }()

	d := &deps{}
	acc := newFakePubSub()
	b := testBridge(d, map[string]*fakePubSub{"": acc})
	ctx := context.Background()

	relStanding, err := b.acquire(ctx, true, nil, []string{"ev/process/>"})
	if err != nil {
		t.Fatal(err)
	}
	relBroad, err := b.acquire(ctx, true, nil, []string{"ev/>"})
	if err != nil {
		t.Fatal(err)
	}
	if got := acc.activePatterns(); !slices.Equal(got, []string{"ev/>"}) {
		t.Fatalf("active = %v, want the broad cover", got)
	}

	// Subscribe fails while the broad pattern is released — the
	// standing interest cannot be re-subscribed right away.
	acc.mu.Lock()
	acc.err = space.ErrPubSubTooManyPatterns
	acc.mu.Unlock()
	relBroad()
	if got := acc.activePatterns(); got != nil {
		t.Fatalf("active = %v, want none while subscribe fails", got)
	}

	// Once the transient failure clears, the scheduled retry restores it.
	acc.mu.Lock()
	acc.err = nil
	acc.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := acc.activePatterns(); slices.Equal(got, []string{"ev/process/>"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("standing interest never restored; active = %v", acc.activePatterns())
		}
		time.Sleep(5 * time.Millisecond)
	}
	relStanding()
}

func TestEventsBridgeAcquireRollback(t *testing.T) {
	d := &deps{}
	acc := newFakePubSub()
	b := testBridge(d, map[string]*fakePubSub{"": acc}) // no "sp1" → resolve fails
	ctx := context.Background()

	if _, err := b.acquire(ctx, true, []string{"sp1"}, []string{"ev/>"}); err == nil {
		t.Fatal("acquire succeeded, want resolve error for sp1")
	}
	if got := acc.activePatterns(); got != nil {
		t.Fatalf("account interests not rolled back: %v", got)
	}
	if len(b.scopes) != 0 {
		t.Fatalf("scopes not cleaned up: %v", b.scopes)
	}
}

func TestEventsBridgeDeliver(t *testing.T) {
	d := &deps{}
	acc := newFakePubSub()
	sp := newFakePubSub()
	b := testBridge(d, map[string]*fakePubSub{"": acc, "sp1": sp})
	ctx := context.Background()

	rel, err := b.acquire(ctx, true, []string{"sp1"}, []string{"ev/>", "acc/ev/>"})
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	id, ch := d.eventsHub().subscribe(eventFilter{})
	defer d.eventsHub().unsubscribe(id)

	// Space-scope delivery: scope/spaceId derived from arrival context,
	// sender from the verified message — never from the payload.
	sp.dispatch(space.PubSubMessage{
		SpaceId: "sp1", Topic: "ev/test/ping/-",
		SenderIdentity: "peerA", Self: false,
		Payload: []byte(`{"type":"test.ping","data":{"n":1}}`),
	})
	ev := <-ch
	if ev.Scope != "space" || ev.SpaceId != "sp1" || ev.Type != "test.ping" {
		t.Errorf("space event = %+v", ev)
	}
	if ev.Sender == nil || ev.Sender.Identity != "peerA" || ev.Sender.Self {
		t.Errorf("sender = %+v, want peerA/self=false", ev.Sender)
	}

	// Account-scope delivery: no spaceId leaks (tech space id is private).
	acc.dispatch(space.PubSubMessage{
		SpaceId: "tech-space-id", Topic: "ev/test/ping/-",
		SenderIdentity: "me", Self: true,
		Payload: []byte(`{"type":"test.ping"}`),
	})
	ev = <-ch
	if ev.Scope != "account" || ev.SpaceId != "" || ev.Sender == nil || !ev.Sender.Self {
		t.Errorf("account event = %+v", ev)
	}

	// Malformed and grammar-violating payloads are dropped.
	sp.dispatch(space.PubSubMessage{SpaceId: "sp1", Topic: "ev/x/-", Payload: []byte(`not json`)})
	sp.dispatch(space.PubSubMessage{SpaceId: "sp1", Topic: "ev/x/-", Payload: []byte(`{"type":"Bad/Type"}`)})
	select {
	case ev := <-ch:
		t.Fatalf("malformed payload delivered: %+v", ev)
	default:
	}
}
