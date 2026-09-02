package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
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
	// resolve maps a scope key ("" = account) to its PubSub handle on
	// the SDK the caller is working against — passed in, never read
	// off deps, so an engine goroutine outliving a drain cannot touch
	// a cleared field. A field so tests can substitute a fake transport.
	resolve func(ctx context.Context, sdk *anysyncsdk.SDK, key string) (space.PubSubAPI, error)

	mu     sync.Mutex
	scopes map[string]*bridgeScope // key: spaceId, "" = account (tech space)
	// gen counts detaches. An acquisition resolves its PubSub handle
	// outside the lock; one that raced a detach must not seed the next
	// engine's scope with the previous engine's handle.
	gen uint64
}

// errBridgeDetached: the engine went away between resolving a scope's
// PubSub handle and registering the interest.
var errBridgeDetached = errors.New("events bridge detached")

// detach drops every pub/sub interest and scope: the engine behind the
// handles is being torn down. Every SSE subscriber has already left
// (the gate drained first), so a late release finds no scope and is a
// no-op; the next engine's acquisitions build fresh scopes through
// resolve, which reads the live SDK.
func (b *eventsBridge) detach() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sc := range b.scopes {
		for _, cancel := range sc.active {
			cancel()
		}
	}
	b.scopes = make(map[string]*bridgeScope)
	b.gen++
}

type bridgeScope struct {
	ps      space.PubSubAPI
	desired map[string]int    // pattern → refcount across live SSE subscribers
	active  map[string]func() // maximal cover actually subscribed → cancel
	// retryScheduled dedups pending resync retries after a failed
	// re-subscribe (see scheduleResyncRetryLocked).
	retryScheduled bool
}

// bridgeResyncRetry is the delay before retrying a failed re-subscribe
// on the release path. Without it a transient SDK error there would
// strand the remaining subscribers — including the standing process
// interest, which has no reconnect path — with no active interest
// until restart. Variable for tests.
var bridgeResyncRetry = 5 * time.Second

func newEventsBridge(d *deps) *eventsBridge {
	b := &eventsBridge{d: d, scopes: make(map[string]*bridgeScope)}
	b.resolve = func(ctx context.Context, sdk *anysyncsdk.SDK, key string) (space.PubSubAPI, error) {
		if sdk == nil {
			return nil, errBridgeDetached
		}
		if key == "" {
			return sdk.PubSub(), nil
		}
		sp, err := sdk.Spaces().Get(ctx, key)
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
func (b *eventsBridge) acquire(ctx context.Context, sdk *anysyncsdk.SDK, wantAccount bool, spaceIds []string, patterns []string) (func(), error) {
	if len(patterns) == 0 || (!wantAccount && len(spaceIds) == 0) {
		return func() {}, nil
	}
	// A request whose engine is going away (its ctx is cancelled with
	// the engine's) must not seed a scope with that engine's handle.
	if err := ctx.Err(); err != nil {
		return nil, err
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
		if err := b.acquireScope(ctx, sdk, key, patterns); err != nil {
			release()
			return nil, err
		}
		acquired = append(acquired, key)
	}
	return release, nil
}

func (b *eventsBridge) acquireScope(ctx context.Context, sdk *anysyncsdk.SDK, key string, patterns []string) error {
	b.mu.Lock()
	gen := b.gen
	b.mu.Unlock()
	// Resolve the PubSub handle outside the lock — Spaces().Get can hit
	// storage on first materialization.
	ps, err := b.resolve(ctx, sdk, key)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen {
		return errBridgeDetached
	}
	sc := b.scopes[key]
	if sc == nil {
		sc = &bridgeScope{ps: ps, desired: make(map[string]int), active: make(map[string]func())}
		b.scopes[key] = sc
	}
	for _, p := range patterns {
		sc.desired[p]++
	}
	if err := b.resyncLocked(key, sc); err != nil {
		b.releaseLocked(key, patterns) // roll back, sweeping the scope if now empty
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
	// A failed re-subscribe here would strand the remaining subscribers
	// with no active interest and no wire signal — log and keep
	// retrying in the background until the desired set is satisfied.
	if err := b.resyncLocked(key, sc); err != nil {
		handlerLog.Error("events bridge resubscribe failed; retrying",
			zap.String("spaceId", key), zap.Error(err))
		// Captured here, inside a gated request, where the engine ctx
		// is stable — the timer must not read the live field.
		b.scheduleResyncRetryLocked(key, sc, b.d.backgroundCtx())
	}
	if len(sc.desired) == 0 {
		delete(b.scopes, key)
	}
}

// scheduleResyncRetryLocked arms one delayed resync for the scope
// (deduped via retryScheduled), rescheduling itself while the resync
// keeps failing. Stops when the scope is swept, the desired set is
// satisfied, or the engine it was armed under goes away. Called with
// b.mu held.
func (b *eventsBridge) scheduleResyncRetryLocked(key string, sc *bridgeScope, engCtx context.Context) {
	if sc.retryScheduled {
		return
	}
	sc.retryScheduled = true
	time.AfterFunc(bridgeResyncRetry, func() {
		if engCtx.Err() != nil {
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		sc := b.scopes[key]
		if sc == nil {
			return
		}
		sc.retryScheduled = false
		if err := b.resyncLocked(key, sc); err != nil {
			handlerLog.Warn("events bridge resync retry failed; will retry",
				zap.String("spaceId", key), zap.Error(err))
			b.scheduleResyncRetryLocked(key, sc, engCtx)
		}
	})
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
		// A self-owned type is only trusted from its acc/ namespace —
		// a payload claiming one on a plain ev/ topic (reachable via a
		// broad local interest) would bypass the ownership guarantee.
		if selfOwnedEventTypes[w.Type] && !strings.HasPrefix(msg.Topic, eventAccTopicPrefix) {
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
