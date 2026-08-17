package api

// Process is one row of the live process view (GET /v1/processes) —
// the server's last-event-wins picture of a long-running operation
// built from `process.*` events on the event bus. Nothing is
// persisted: a server restart forgets every process, remote processes
// materialize from their periodic broadcasts, and entries expire when
// their owner stops heartbeating. See docs/22-processes.md.
//
// Processes are keyed (identity, id): id uniqueness is
// publisher-local, and identity comes from the server-stamped (for
// network scopes signature-verified) event sender — a remote peer can
// neither collide with nor spoof another publisher's process. Self is
// true when the process belongs to this account (any of its devices).
//
// Scope/SpaceId/Target mirror the registering envelope: scope is
// where the process broadcasts (device/account/space), Target is the
// process's subject (objectId, runId, …) — distinct from the
// envelope target, which carries the process id. State is one of the
// ProcessState* values; Error is set when State is failed. Done/Total
// are the owner's progress counters (Total 0 = unknown).
//
// StartedAt/UpdatedAt are unix seconds of local observation — this
// device's clock, not the owner's. Display/expiry quality only.
type Process struct {
	Identity  string        `json:"identity"`
	Self      bool          `json:"self"`
	Id        string        `json:"id"`
	Kind      string        `json:"kind"`
	Title     string        `json:"title"`
	Scope     string        `json:"scope"`
	SpaceId   string        `json:"spaceId,omitempty"`
	Target    string        `json:"target,omitempty"`
	State     string        `json:"state"`
	Done      int64         `json:"done"`
	Total     int64         `json:"total,omitempty"`
	Message   string        `json:"message,omitempty"`
	Error     *ProcessError `json:"error,omitempty"`
	StartedAt int64         `json:"startedAt"`
	UpdatedAt int64         `json:"updatedAt"`
}

// ProcessError is the terminal failure payload of a process — carried
// by the process.failed event and surfaced on the failed Process row.
type ProcessError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// Process State values — a closed set. Only running processes are
// heartbeat-kept; terminal states linger briefly in the view and then
// expire.
const (
	ProcessStateRunning   = "running"
	ProcessStateDone      = "done"
	ProcessStateFailed    = "failed"
	ProcessStateCancelled = "cancelled"
)

// Process event types — the `process.*` corner of the event bus
// vocabulary the helper endpoints emit (envelope target = process id).
// EventProcessCancel is a directive addressed at the owner, not a
// state change: the owner reacts and emits the terminal event.
const (
	EventProcessStarted   = "process.started"
	EventProcessProgress  = "process.progress"
	EventProcessDone      = "process.done"
	EventProcessFailed    = "process.failed"
	EventProcessCancelled = "process.cancelled"
	EventProcessCancel    = "process.cancel"
)

// ProcessRegisterRequest is the body of POST /v1/processes. Id names
// the process (publisher-local uniqueness; the event-target grammar
// [A-Za-z0-9._-]{1,128} applies — it becomes a topic segment).
// Re-registering an id restarts the process view. Scope routes the
// broadcasts (device/account/space; SpaceId required iff space);
// Target optionally names the process's subject. Strict-bound.
type ProcessRegisterRequest struct {
	Id      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Scope   string `json:"scope"`
	SpaceId string `json:"spaceId,omitempty"`
	Target  string `json:"target,omitempty"`
}

// ProcessProgressRequest is the body of POST /v1/processes/:id/progress.
// Done/Total are free-unit counters (Total 0/absent = unknown);
// Message is a short human-readable status line. Owners re-POST
// progress at least every 15s as heartbeat even when idle — a running
// process not heard from for 45s expires from the view. Strict-bound.
type ProcessProgressRequest struct {
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

// ProcessFinishRequest is the body of POST /v1/processes/:id/finish.
// Status is done, failed or cancelled; Error is required iff failed.
// Strict-bound.
type ProcessFinishRequest struct {
	Status string        `json:"status"`
	Error  *ProcessError `json:"error,omitempty"`
}

// ProcessCancelRequest is the body of POST /v1/processes/:id/cancel.
// Identity disambiguates when more than one publisher runs a process
// with the same id (the composite key); with a single match it may be
// omitted. Strict-bound.
type ProcessCancelRequest struct {
	Identity string `json:"identity,omitempty"`
}

// ProcessListResponse is the body of GET /v1/processes — the live
// view, expired entries swept.
type ProcessListResponse struct {
	Processes []Process `json:"processes"`
}
