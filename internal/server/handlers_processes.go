package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// processTitleMaxLen / processMessageMaxLen bound the free-text fields
// — display strings, not payloads (bulk data belongs in `data` of
// plain events).
const (
	processTitleMaxLen   = 256
	processMessageMaxLen = 1024
)

// processRegister handles POST /v1/processes — register a process and
// emit process.started on its scope. Re-registering an id restarts
// the view row (the supported owner-restart path). The response is
// the bus publish reply: how many local subscribers matched.
// See docs/22-processes.md.
//
//	@Summary	Register a process
//	@Tags		processes
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.ProcessRegisterRequest	true	"process"
//	@Success	200		{object}	api.EventPublishResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Router		/processes [post]
func (d *deps) processRegister(c echo.Context) error {
	req, ok := bindBodyStrict[api.ProcessRegisterRequest](c, "")
	if !ok {
		return nil
	}
	if req.Id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "id required", nil)
	}
	if !validTargetToken(c, "id", req.Id) {
		return nil
	}
	if req.Kind == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "kind required", nil)
	}
	if !validTargetToken(c, "kind", req.Kind) {
		return nil
	}
	if req.Title == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "title required", nil)
	}
	if len(req.Title) > processTitleMaxLen {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"title must be at most 256 bytes", nil)
	}
	if !validateEventScope(c, req.Scope, req.SpaceId) {
		return nil
	}
	if req.Target != "" && !validTargetToken(c, "target", req.Target) {
		return nil
	}
	return d.emitProcess(c, api.EventProcessStarted, req.Scope, req.SpaceId, req.Id,
		processEventData{Kind: req.Kind, Title: req.Title, Target: req.Target})
}

// processProgress handles POST /v1/processes/:id/progress — emit a
// process.progress frame (also the heartbeat: owners re-POST at least
// every 15s even when idle). Absent fields keep their current values —
// the server folds the stored state into the frame, so a bare
// heartbeat never wipes total/message and every emitted frame carries
// the full picture. The process must be live in the view under this
// account's identity — after a restart or staleness expiry the owner
// re-registers first.
//
//	@Summary	Report process progress (heartbeat)
//	@Tags		processes
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string						true	"process id"
//	@Param		body	body		api.ProcessProgressRequest	true	"progress"
//	@Success	200		{object}	api.EventPublishResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Router		/processes/{id}/progress [post]
func (d *deps) processProgress(c echo.Context) error {
	req, ok := bindBodyStrict[api.ProcessProgressRequest](c, "")
	if !ok {
		return nil
	}
	if (req.Done != nil && *req.Done < 0) || (req.Total != nil && *req.Total < 0) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"done and total must be non-negative", nil)
	}
	if req.Message != nil && len(*req.Message) > processMessageMaxLen {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"message must be at most 1024 bytes", nil)
	}
	id := c.Param("id")
	if !validTargetToken(c, "id", id) {
		return nil
	}
	// Fold atomically under the registry lock (absent fields carry the
	// stored state forward; explicit zero/empty clears) — a concurrent
	// heartbeat and update serialize instead of reverting each other.
	p, ok := d.processes().mergeOwn(d.sdk.Account().Id(), id, req.Done, req.Total, req.Message)
	if !ok {
		return writeError(c, http.StatusNotFound, "process.not_found",
			"no live process "+id+" — register it first (POST /v1/processes)", nil)
	}
	return d.emitProcess(c, api.EventProcessProgress, p.Scope, p.SpaceId, p.Id,
		processEventData{Kind: p.Kind, Title: p.Title, Target: p.Target,
			Done: p.Done, Total: p.Total, Message: p.Message})
}

// processFinish handles POST /v1/processes/:id/finish — emit the
// terminal event (process.done / process.failed / process.cancelled).
// error is required iff status is failed. Terminal entries linger
// briefly in the view, then expire.
//
//	@Summary	Finish a process
//	@Tags		processes
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string						true	"process id"
//	@Param		body	body		api.ProcessFinishRequest	true	"outcome"
//	@Success	200		{object}	api.EventPublishResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Router		/processes/{id}/finish [post]
func (d *deps) processFinish(c echo.Context) error {
	req, ok := bindBodyStrict[api.ProcessFinishRequest](c, "")
	if !ok {
		return nil
	}
	var typ string
	switch req.Status {
	case api.ProcessStateDone:
		typ = api.EventProcessDone
	case api.ProcessStateFailed:
		typ = api.EventProcessFailed
	case api.ProcessStateCancelled:
		typ = api.EventProcessCancelled
	case "":
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"status required (done, failed or cancelled)", nil)
	default:
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"status must be done, failed or cancelled", nil)
	}
	if req.Status == api.ProcessStateFailed && (req.Error == nil || req.Error.Message == "") {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"error {message} required for status failed", nil)
	}
	if req.Status != api.ProcessStateFailed && req.Error != nil {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"error only valid with status failed", nil)
	}
	if req.Error != nil && len(req.Error.Message) > processMessageMaxLen {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"error message must be at most 1024 bytes", nil)
	}
	p, ok := d.ownProcess(c)
	if !ok {
		return nil
	}
	// The terminal frame carries the final counters (done/total/message
	// serialize unconditionally — omitting them would zero the row on
	// every observer).
	return d.emitProcess(c, typ, p.Scope, p.SpaceId, p.Id,
		processEventData{Kind: p.Kind, Title: p.Title, Target: p.Target,
			Done: p.Done, Total: p.Total, Message: p.Message, Error: req.Error})
}

// processCancel handles POST /v1/processes/:id/cancel — emit
// process.cancel toward the owner on the process's own scope. The
// owner (which may be a remote device or a space member) reacts and
// emits the terminal event; cancel itself changes no state. identity
// disambiguates when several publishers run the same id.
//
//	@Summary	Request process cancellation
//	@Tags		processes
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string						true	"process id"
//	@Param		body	body		api.ProcessCancelRequest	false	"owner selector"
//	@Success	200		{object}	api.EventPublishResponse
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/processes/{id}/cancel [post]
func (d *deps) processCancel(c echo.Context) error {
	req, ok := bindBodyStrict[api.ProcessCancelRequest](c, "")
	if !ok {
		return nil
	}
	id := c.Param("id")
	if !validTargetToken(c, "id", id) {
		return nil
	}
	var p api.Process
	if req.Identity != "" {
		var found bool
		if p, found = d.processes().get(req.Identity, id); !found {
			return writeError(c, http.StatusNotFound, "process.not_found",
				"no live process "+id+" for that identity", nil)
		}
	} else {
		switch matches := d.processes().byId(id); len(matches) {
		case 0:
			return writeError(c, http.StatusNotFound, "process.not_found",
				"no live process "+id, nil)
		case 1:
			p = matches[0]
		default:
			identities := make([]string, len(matches))
			for i, m := range matches {
				identities[i] = m.Identity
			}
			return writeError(c, http.StatusConflict, "process.ambiguous",
				"several publishers run a process "+id+" — pass identity to pick one",
				map[string]any{"identities": identities})
		}
	}
	return d.emitProcess(c, api.EventProcessCancel, p.Scope, p.SpaceId, p.Id,
		processCancelData{Identity: p.Identity})
}

// processesList handles GET /v1/processes — the live last-event-wins
// view, expired entries swept. Remote coverage: account-scope
// processes of this account's other devices always (standing
// interest); space-scope processes of other members only while some
// local subscriber holds that space's event interest.
//
//	@Summary	List live processes
//	@Tags		processes
//	@Produce	json
//	@Success	200	{object}	api.ProcessListResponse
//	@Router		/processes [get]
func (d *deps) processesList(c echo.Context) error {
	return c.JSON(http.StatusOK, api.ProcessListResponse{Processes: d.processes().list()})
}

// ownProcess resolves :id against this account's identity in the view
// — the precondition for progress/finish. False means the error
// response is already written: 404 process.not_found when absent or
// expired (the owner re-registers), 400 on bad id grammar. Same
// (value, ok) shape as bindBodyStrict.
func (d *deps) ownProcess(c echo.Context) (api.Process, bool) {
	id := c.Param("id")
	if !validTargetToken(c, "id", id) {
		return api.Process{}, false
	}
	p, found := d.processes().get(d.sdk.Account().Id(), id)
	if !found {
		_ = writeError(c, http.StatusNotFound, "process.not_found",
			"no live process "+id+" — register it first (POST /v1/processes)", nil)
		return api.Process{}, false
	}
	return p, true
}

// emitProcess builds one process.* event (envelope target = process
// id) and hands it to the shared scope dispatch. The local view is fed
// by the hub tap on device scope and by publishNetworkEvent's direct
// apply on the network scopes — same paths as every other bus publish.
func (d *deps) emitProcess(c echo.Context, typ, scope, spaceId, id string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	return d.publishScoped(c, api.Event{
		Type:    typ,
		Scope:   scope,
		SpaceId: spaceId,
		Target:  id,
		Data:    payload,
		Sender:  &api.EventSender{Identity: d.sdk.Account().Id(), Self: true},
	})
}
