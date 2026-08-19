package indexer

import (
	"context"
	"sync"
	"time"
)

// procReporter drives one work unit's process-view reporting — the
// single template every producer (embed drain, advance pass, model
// download) shares: announce started once the elapsed-work gate
// opens, progress frames while announced, a background ticker that
// both re-checks the gate mid-operation and heartbeats through long
// silent stretches (so an announced row never staleness-expires while
// work is genuinely running), and exactly one terminal frame.
//
// Frames are emitted under the internal mutex, so ordering is strict:
// started always precedes any progress, and finish joins the ticker
// goroutine before the terminal — no frame can trail it and resurrect
// the row. A nil emit makes every method a no-op.
type procReporter struct {
	emit func(ProcessUpdate)
	tmpl ProcessUpdate // Kind / SpaceId / Name preset; phase+counters filled per frame
	gate time.Duration // announce after this much elapsed work; negative = at begin

	mu        sync.Mutex
	workStart time.Time
	announced bool
	finished  bool
	lastFrame time.Time
	done      int64
	total     int64
	message   string

	stopTick chan struct{}
	tickDone chan struct{}
}

func newProcReporter(emit func(ProcessUpdate), tmpl ProcessUpdate, gate time.Duration) *procReporter {
	return &procReporter{emit: emit, tmpl: tmpl, gate: gate}
}

// begin marks the start of the work unit, starts the gate/heartbeat
// ticker and, in immediate mode (negative gate), announces right
// away. Idempotent — producers may call it at every round boundary.
func (r *procReporter) begin() {
	if r == nil || r.emit == nil {
		return
	}
	r.mu.Lock()
	fresh := r.workStart.IsZero() && !r.finished
	if fresh {
		r.workStart = time.Now()
		r.stopTick = make(chan struct{})
		r.tickDone = make(chan struct{})
	}
	r.tryAnnounceLocked()
	r.mu.Unlock()
	if fresh {
		go r.tick()
	}
}

// progress records counters (negative done/total = keep current) and
// the status message, then emits a progress frame once announced. The
// call that opens the gate emits only started — it already carries
// these counters. Pre-begin calls just store counters, so begin's
// started frame can report state gathered before it.
func (r *procReporter) progress(done, total int64, msg string) {
	if r == nil || r.emit == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	if done >= 0 {
		r.done = done
	}
	if total >= 0 {
		r.total = total
	}
	r.message = msg
	wasAnnounced := r.announced
	if !r.tryAnnounceLocked() || !wasAnnounced {
		return
	}
	r.lastFrame = time.Now()
	r.emit(r.frameLocked(ProcessProgress, r.message))
}

// finish joins the ticker and emits the terminal frame — done on nil
// error, cancelled when ctx ended (a stopped worker isn't a failure),
// failed otherwise — carrying the final counters. Silent when the
// gate never opened: short work stays fully invisible. Idempotent.
func (r *procReporter) finish(ctx context.Context, err error) {
	if r == nil || r.emit == nil {
		return
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.finished = true
	stop := r.stopTick
	r.mu.Unlock()
	if stop != nil {
		close(stop)
		<-r.tickDone // join — no frame may trail the terminal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.announced {
		return
	}
	switch {
	case err == nil:
		r.emit(r.frameLocked(ProcessDone, ""))
	case ctx.Err() != nil:
		r.emit(r.frameLocked(ProcessCancelled, ""))
	default:
		r.emit(r.frameLocked(ProcessFailed, err.Error()))
	}
}

// tick re-checks the gate every procAnnounceTick (bounding announce
// latency when one call runs long — a big embed batch, a heavy
// chunker page, a download backoff) and heartbeats every
// procHeartbeat once announced.
func (r *procReporter) tick() {
	defer close(r.tickDone)
	t := time.NewTicker(procAnnounceTick)
	defer t.Stop()
	for {
		select {
		case <-r.stopTick:
			return
		case <-t.C:
			r.mu.Lock()
			if !r.tryAnnounceLocked() {
				r.mu.Unlock()
				continue
			}
			if time.Since(r.lastFrame) >= procHeartbeat {
				r.lastFrame = time.Now()
				r.emit(r.frameLocked(ProcessProgress, r.message))
			}
			r.mu.Unlock()
		}
	}
}

// tryAnnounceLocked emits started once the gate opens. mu held.
func (r *procReporter) tryAnnounceLocked() bool {
	if r.announced {
		return true
	}
	if r.workStart.IsZero() || r.finished || time.Since(r.workStart) < r.gate {
		return false
	}
	r.announced = true
	r.lastFrame = time.Now()
	r.emit(r.frameLocked(ProcessStarted, r.message))
	return true
}

// frameLocked renders one update from the template + counters. mu held.
func (r *procReporter) frameLocked(phase, msg string) ProcessUpdate {
	u := r.tmpl
	u.Phase = phase
	u.Done, u.Total = r.done, r.total
	u.Message = msg
	return u
}
