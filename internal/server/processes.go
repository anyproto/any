package server

import (
	"cmp"
	"encoding/json"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anyproto/any/internal/api"
)

// Process-view staleness (docs/22-processes.md § Heartbeat and
// staleness). Owners re-broadcast progress at least every
// processHeartbeat even when idle; a running entry not heard from for
// processStaleTTL is dropped (owner presumed dead), a terminal entry
// lingers processTerminalTTL so consumers can observe the outcome.
const (
	processHeartbeat   = 15 * time.Second
	processStaleTTL    = 45 * time.Second
	processTerminalTTL = 60 * time.Second
)

// processKey is the composite process identity: id uniqueness is
// publisher-local, and identity comes from the (signature-verified on
// network scopes) event sender — remote collision/spoofing is
// structurally impossible.
type processKey struct {
	identity string
	id       string
}

// processEventData is the data payload of the process.* state events.
// The descriptor (kind/title/target) is folded into every frame so a
// single heartbeat fully materializes a process on a device that
// missed process.started.
type processEventData struct {
	Kind    string            `json:"kind,omitempty"`
	Title   string            `json:"title,omitempty"`
	Target  string            `json:"target,omitempty"`
	Done    int64             `json:"done,omitempty"`
	Total   int64             `json:"total,omitempty"`
	Message string            `json:"message,omitempty"`
	Error   *api.ProcessError `json:"error,omitempty"`
}

// processCancelData is the data payload of process.cancel: the
// identity half of the composite key, so an owner ignores cancels
// addressed at someone else's same-named process.
type processCancelData struct {
	Identity string `json:"identity"`
}

type processEntry struct {
	proc api.Process
	seen time.Time // last event for this key, local clock — the expiry basis
}

// processRegistry is the in-memory last-event-wins view behind
// GET /v1/processes. It observes every hub publish through a
// synchronous tap (local emits and bridged network broadcasts alike),
// keeps no history and persists nothing — a restart forgets every
// process by design. Expiry is swept lazily on apply and list; there
// is no background janitor.
type processRegistry struct {
	mu      sync.Mutex
	entries map[processKey]*processEntry
	now     func() time.Time // injectable clock for expiry tests
}

func newProcessRegistry() *processRegistry {
	return &processRegistry{entries: make(map[processKey]*processEntry), now: time.Now}
}

// apply folds one bus event into the view. Non-process types,
// process.cancel (a directive, not a state change), events without a
// sender and events without a target (the process id) are ignored.
// Data is decoded best-effort — the ev/ namespace is open to every
// space member, so a malformed payload degrades to an id-only row
// rather than being trusted or crashing anything.
func (r *processRegistry) apply(ev *api.Event) {
	var state string
	switch ev.Type {
	case api.EventProcessStarted, api.EventProcessProgress:
		state = api.ProcessStateRunning
	case api.EventProcessDone:
		state = api.ProcessStateDone
	case api.EventProcessFailed:
		state = api.ProcessStateFailed
	case api.EventProcessCancelled:
		state = api.ProcessStateCancelled
	default:
		return
	}
	if ev.Sender == nil || ev.Target == "" {
		return
	}
	var data processEventData
	_ = json.Unmarshal(ev.Data, &data)
	// Sanitize before storing: rows can materialize from hand-emitted
	// or remote payloads the register endpoint never validated, and
	// progress/finish re-emit the stored descriptor — nothing
	// grammar-violating or oversized may enter the view.
	if data.Kind != "" && !eventTargetRe.MatchString(data.Kind) {
		data.Kind = ""
	}
	if data.Target != "" && !eventTargetRe.MatchString(data.Target) {
		data.Target = ""
	}
	data.Title = clipUTF8(data.Title, processTitleMaxLen)
	data.Message = clipUTF8(data.Message, processMessageMaxLen)
	if data.Error != nil {
		data.Error.Message = clipUTF8(data.Error.Message, processMessageMaxLen)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	key := processKey{identity: ev.Sender.Identity, id: ev.Target}
	e := r.entries[key]
	// started (re)starts the view row; anything else upserts
	// last-event-wins (a lone progress heartbeat materializes a row a
	// late joiner never saw started for).
	if e == nil || ev.Type == api.EventProcessStarted {
		e = &processEntry{proc: api.Process{
			Identity:  ev.Sender.Identity,
			Self:      ev.Sender.Self,
			Id:        ev.Target,
			StartedAt: now.Unix(),
		}}
		r.entries[key] = e
	}
	p := &e.proc
	p.Scope = ev.Scope
	p.SpaceId = ev.SpaceId
	p.State = state
	if data.Kind != "" {
		p.Kind = data.Kind
	}
	if data.Title != "" {
		p.Title = data.Title
	}
	if data.Target != "" {
		p.Target = data.Target
	}
	if ev.Type == api.EventProcessProgress {
		p.Done = data.Done
		p.Total = data.Total
		p.Message = data.Message
	}
	// Only failed carries an error; every other event clears it, so a
	// row resurrected to running (a late heartbeat after finish) or
	// re-finished after a failure never shows a stale outcome.
	if ev.Type == api.EventProcessFailed {
		p.Error = data.Error
	} else {
		p.Error = nil
	}
	p.UpdatedAt = now.Unix()
	e.seen = now
	r.sweepLocked(now)
}

// clipUTF8 bounds s to max bytes without bisecting a rune.
func clipUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// expired reports whether e is past its staleness budget at t.
func (e *processEntry) expired(t time.Time) bool {
	ttl := processStaleTTL
	if e.proc.State != api.ProcessStateRunning {
		ttl = processTerminalTTL
	}
	return t.Sub(e.seen) > ttl
}

func (r *processRegistry) sweepLocked(now time.Time) {
	for key, e := range r.entries {
		if e.expired(now) {
			delete(r.entries, key)
		}
	}
}

// list snapshots the live view, expired entries swept, ordered by
// first-seen time then key so the output is stable across calls.
func (r *processRegistry) list() []api.Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.now())
	out := make([]api.Process, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.proc)
	}
	slices.SortFunc(out, func(a, b api.Process) int {
		if c := cmp.Compare(a.StartedAt, b.StartedAt); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Identity, b.Identity); c != 0 {
			return c
		}
		return cmp.Compare(a.Id, b.Id)
	})
	return out
}

// get returns the live entry for one composite key, if any.
func (r *processRegistry) get(identity, id string) (api.Process, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[processKey{identity: identity, id: id}]
	if e == nil || e.expired(r.now()) {
		return api.Process{}, false
	}
	return e.proc, true
}

// byId returns every live entry sharing one publisher-local id,
// across identities — the cancel-resolution set.
func (r *processRegistry) byId(id string) []api.Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var out []api.Process
	for key, e := range r.entries {
		if key.id == id && !e.expired(now) {
			out = append(out, e.proc)
		}
	}
	slices.SortFunc(out, func(a, b api.Process) int { return cmp.Compare(a.Identity, b.Identity) })
	return out
}
