package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

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
	if !eventTargetRe.MatchString(req.Id) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"id must be 1-128 chars of [A-Za-z0-9._-]", nil)
	}
	if req.Kind == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "kind required", nil)
	}
	if !eventTargetRe.MatchString(req.Kind) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"kind must be 1-128 chars of [A-Za-z0-9._-]", nil)
	}
	if req.Title == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "title required", nil)
	}
	if len(req.Title) > processTitleMaxLen {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"title must be at most 256 bytes", nil)
	}
	switch req.Scope {
	case api.EventScopeDevice, api.EventScopeAccount, api.EventScopeSpace:
	case "":
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"scope required (device, account or space)", nil)
	default:
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"scope must be device, account or space", nil)
	}
	if req.Scope == api.EventScopeSpace && req.SpaceId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"spaceId required for scope space", nil)
	}
	if req.Scope != api.EventScopeSpace && req.SpaceId != "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"spaceId only valid with scope space", nil)
	}
	if req.Target != "" && !eventTargetRe.MatchString(req.Target) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"target must be 1-128 chars of [A-Za-z0-9._-]", nil)
	}
	return d.emitProcess(c, api.EventProcessStarted, req.Scope, req.SpaceId, req.Id,
		processEventData{Kind: req.Kind, Title: req.Title, Target: req.Target})
}

// processProgress handles POST /v1/processes/:id/progress — emit a
// process.progress frame (also the heartbeat: owners re-POST at least
// every 15s even when idle). The process must be live in the view
// under this account's identity — after a restart or staleness expiry
// the owner re-registers first.
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
	if req.Done < 0 || req.Total < 0 {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"done and total must be non-negative", nil)
	}
	if len(req.Message) > processMessageMaxLen {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"message must be at most 1024 bytes", nil)
	}
	p, err := d.ownProcess(c)
	if err != nil {
		return err
	}
	return d.emitProcess(c, api.EventProcessProgress, p.Scope, p.SpaceId, p.Id,
		processEventData{Kind: p.Kind, Title: p.Title, Target: p.Target,
			Done: req.Done, Total: req.Total, Message: req.Message})
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
	p, err := d.ownProcess(c)
	if err != nil {
		return err
	}
	return d.emitProcess(c, typ, p.Scope, p.SpaceId, p.Id,
		processEventData{Kind: p.Kind, Title: p.Title, Target: p.Target, Error: req.Error})
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
	if !eventTargetRe.MatchString(id) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"id must be 1-128 chars of [A-Za-z0-9._-]", nil)
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
// — the precondition for progress/finish. 404 process.not_found when
// absent or expired (the owner re-registers).
func (d *deps) ownProcess(c echo.Context) (api.Process, error) {
	id := c.Param("id")
	if !eventTargetRe.MatchString(id) {
		return api.Process{}, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"id must be 1-128 chars of [A-Za-z0-9._-]", nil)
	}
	p, found := d.processes().get(d.sdk.Account().Id(), id)
	if !found {
		return api.Process{}, writeError(c, http.StatusNotFound, "process.not_found",
			"no live process "+id+" — register it first (POST /v1/processes)", nil)
	}
	return p, nil
}

// emitProcess publishes one process.* event on scope and folds it into
// the local view. Device scope reaches the view through the hub tap;
// network scopes apply directly after a confirmed publish, because the
// SDK's Self loopback re-enters the hub only when a local interest
// covers the topic — the local view must not depend on interests.
func (d *deps) emitProcess(c echo.Context, typ, scope, spaceId, id string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	ev := api.Event{
		Type:    typ,
		Scope:   scope,
		SpaceId: spaceId,
		Target:  id,
		Data:    payload,
		Sender:  &api.EventSender{Identity: d.sdk.Account().Id(), Self: true},
	}
	switch scope {
	case api.EventScopeDevice:
		n := d.eventsHub().publish(ev)
		return c.JSON(http.StatusOK, api.EventPublishResponse{Subscribers: n})
	case api.EventScopeAccount:
		return d.emitProcessNetwork(c, ev, d.sdk.PubSub())
	default: // space
		sp, err := d.sdk.Spaces().Get(c.Request().Context(), spaceId)
		if err != nil {
			return spaceError(c, err, spaceId)
		}
		return d.emitProcessNetwork(c, ev, sp.PubSub())
	}
}

func (d *deps) emitProcessNetwork(c echo.Context, ev api.Event, ps space.PubSubAPI) error {
	n, err := d.networkPublish(c.Request().Context(), ev, ps)
	if err != nil {
		return pubsubError(c, err)
	}
	d.processes().apply(&ev)
	return c.JSON(http.StatusOK, api.EventPublishResponse{Subscribers: n})
}
