package server

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// wireEvent is the payload an account/space-scope event travels as on
// the SDK pub/sub. Deliberately minimal: scope and spaceId are implied
// by where the message arrives, and the sender is stamped from the
// verified message signature — anything a payload claimed about its
// sender would be untrusted, so it isn't carried at all.
type wireEvent struct {
	Type   string          `json:"type"`
	Target string          `json:"target,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// eventsBridge refcounts local SSE interest into SDK pub/sub
// subscriptions: the first subscriber whose filter needs a pattern on
// a scope subscribes it, the last drops it — idle spaces cost
// nothing, and N identical filters share one SDK subscription
// (respecting the per-space pattern budget).
//
// Because the SDK fires every subscription whose pattern matches a
// message, overlapping active patterns would deliver duplicates. The
// bridge therefore keeps the full desired multiset per scope but only
// subscribes its maximal (pairwise-disjoint) cover — see
// maximalPatterns.
type eventsBridge struct {
	d *deps
	// resolve maps a scope key ("" = account) to its PubSub handle.
	// A field so tests can substitute a fake transport.
	resolve func(ctx context.Context, key string) (space.PubSubAPI, error)

	mu     sync.Mutex
	scopes map[string]*bridgeScope // key: spaceId, "" = account (tech space)
}

type bridgeScope struct {
	ps      space.PubSubAPI
	desired map[string]int    // pattern → refcount across live SSE subscribers
	active  map[string]func() // maximal cover actually subscribed → cancel
}

func newEventsBridge(d *deps) *eventsBridge {
	b := &eventsBridge{d: d, scopes: make(map[string]*bridgeScope)}
	b.resolve = func(ctx context.Context, key string) (space.PubSubAPI, error) {
		if key == "" {
			return d.sdk.PubSub(), nil
		}
		sp, err := d.sdk.Spaces().Get(ctx, key)
		if err != nil {
			return nil, err
		}
		return sp.PubSub(), nil
	}
	return b
}

// eventsNet lazily creates the bridge on first network-scope use.
func (d *deps) eventsNet() *eventsBridge {
	d.bridgeOnce.Do(func() { d.bridge = newEventsBridge(d) })
	return d.bridge
}

// acquire registers patterns on the account scope (when wantAccount)
// and on each listed space, returning a release that undoes exactly
// this acquisition. On any error the partial acquisition is rolled
// back and (nil, err) returned — the caller maps it onto the wire.
func (b *eventsBridge) acquire(ctx context.Context, wantAccount bool, spaceIds []string, patterns []string) (func(), error) {
	if len(patterns) == 0 || (!wantAccount && len(spaceIds) == 0) {
		return func() {}, nil
	}
	var keys []string
	if wantAccount {
		keys = append(keys, "")
	}
	keys = append(keys, spaceIds...)

	var acquired []string
	release := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, key := range acquired {
			b.releaseLocked(key, patterns)
		}
	}
	for _, key := range keys {
		if err := b.acquireScope(ctx, key, patterns); err != nil {
			release()
			return nil, err
		}
		acquired = append(acquired, key)
	}
	return release, nil
}

func (b *eventsBridge) acquireScope(ctx context.Context, key string, patterns []string) error {
	// Resolve the PubSub handle outside the lock — Spaces().Get can hit
	// storage on first materialization.
	ps, err := b.resolve(ctx, key)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	sc := b.scopes[key]
	if sc == nil {
		sc = &bridgeScope{ps: ps, desired: make(map[string]int), active: make(map[string]func())}
		b.scopes[key] = sc
	}
	for _, p := range patterns {
		sc.desired[p]++
	}
	if err := b.resyncLocked(key, sc); err != nil {
		for _, p := range patterns {
			if sc.desired[p]--; sc.desired[p] <= 0 {
				delete(sc.desired, p)
			}
		}
		_ = b.resyncLocked(key, sc) // best-effort restore
		return err
	}
	return nil
}

func (b *eventsBridge) releaseLocked(key string, patterns []string) {
	sc := b.scopes[key]
	if sc == nil {
		return
	}
	for _, p := range patterns {
		if sc.desired[p]--; sc.desired[p] <= 0 {
			delete(sc.desired, p)
		}
	}
	_ = b.resyncLocked(key, sc)
	if len(sc.desired) == 0 {
		delete(b.scopes, key)
	}
}

// resyncLocked diffs the scope's active subscriptions against the
// maximal cover of its desired set: cancel what's no longer maximal,
// subscribe what became maximal. Called with b.mu held.
func (b *eventsBridge) resyncLocked(key string, sc *bridgeScope) error {
	want := make(map[string]bool)
	for _, p := range maximalPatterns(sc.desired) {
		want[p] = true
	}
	for p, cancel := range sc.active {
		if !want[p] {
			cancel()
			delete(sc.active, p)
		}
	}
	for p := range want {
		if _, ok := sc.active[p]; ok {
			continue
		}
		cancel, err := sc.ps.Subscribe(p, b.deliver(key))
		if err != nil {
			return err
		}
		sc.active[p] = cancel
	}
	return nil
}

// deliver bridges one pub/sub message into the local hub. The
// callback runs on the SDK's single dispatch goroutine and must not
// block — hub.publish is non-blocking by construction. Malformed
// payloads are dropped silently: the `ev/` namespace is open to every
// space member, including non-`any` clients.
func (b *eventsBridge) deliver(key string) func(space.PubSubMessage) {
	return func(msg space.PubSubMessage) {
		var w wireEvent
		if err := json.Unmarshal(msg.Payload, &w); err != nil {
			return
		}
		if w.Type == "" || len(w.Type) > eventTypeMaxLen || !eventTypeRe.MatchString(w.Type) {
			return
		}
		if w.Target != "" && !eventTargetRe.MatchString(w.Target) {
			return
		}
		ev := api.Event{
			Type:   w.Type,
			Target: w.Target,
			Data:   w.Data,
			Sender: &api.EventSender{Identity: msg.SenderIdentity, Self: msg.Self},
		}
		if key == "" {
			ev.Scope = api.EventScopeAccount
		} else {
			ev.Scope = api.EventScopeSpace
			ev.SpaceId = msg.SpaceId
		}
		b.d.eventsHub().publish(ev)
	}
}
