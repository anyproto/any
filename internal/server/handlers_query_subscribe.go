package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cheggaaa/mb/v3"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// spaceQueryObjectsSubscribe handles POST /v1/spaces/:spaceId/objects/query/subscribe.
//
// Windowed live view over the per-space `objects` collection. The
// request body matches POST /objects/query plus the Subscribe-only
// QueryOpts (mailboxCapacity, driftBudgetPercent). Response is an SSE
// stream — see docs/04-events.md § Windowed query subscriptions.
//
//	@Summary	Subscribe to a windowed objects query (SSE)
//	@Tags		objects
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		spaceId	path	string							true	"Space ID"
//	@Param		body	body	api.SpaceQueryObjectsRequest	true	"Query params"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/query/subscribe [post]
func (d *deps) spaceQueryObjectsSubscribe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	q, opts, shaper, errResp, done := buildSharedQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal, shaper)
}

// spaceQuerySubscribe handles POST /v1/spaces/:spaceId/query/subscribe.
//
// Windowed live view over a per-object dataset. Same wire as
// /objects/query/subscribe; body additionally requires objectId+dataset.
//
//	@Summary	Subscribe to a windowed per-object query (SSE)
//	@Tags		data
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		body	body	api.SpaceQueryRequest	true	"Query params (objectId+dataset required)"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/query/subscribe [post]
func (d *deps) spaceQuerySubscribe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	pq, errResp, done := buildPerObjectQuery(c, sp, d.techIndexVet(c, sp))
	if done {
		return errResp
	}
	if pq.includeDeleted {
		// the live window never carries tombstones (the SDK pins the
		// _deletedAt-missing clause on Subscribe) — refuse rather than
		// silently stream the live view under a flag that promises more
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"includeDeleted is snapshot-only: use POST …/query", map[string]any{"field": "includeDeleted"})
	}
	res, err := pq.q.Subscribe(c.Request().Context(), pq.opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId": sp.Id(), "objectId": pq.objectId, "dataset": pq.dataset,
		})
	}
	return d.streamQuerySubscribe(c, res, pq.opts.IncludeTotal, pq.shaper)
}

// streamQuerySubscribe pumps a QuerySubscription's windowed events to
// the client as SSE frames until the client disconnects, the SDK
// closes the subscription, or the server begins shutdown.
//
// Frame set:
//
//	ready    — once, after the snapshot frame is queued.
//	snapshot — once, the QueryResult.Initial materialised window.
//	changes  — JSON array of QuerySubscribeEvent batches; Mailbox.Wait
//	           coalesces concurrent events for free.
//	closed   — terminal, with a reason mapped from Sub.Err() (overflow,
//	           drifted, sdk_closed) or the engine's teardown
//	           (server_shutdown, deauthorized).
//
// The stream runs inside the engine gate (routes.go), so a teardown
// waits for it to unwind — which it does as soon as shutdownCtx fires.
func (d *deps) streamQuerySubscribe(c echo.Context, res *space.QueryResult, includeTotal bool, shaper recordShaper) error {
	defer res.Sub.Close()

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering disables nginx response buffering — harmless on
	// loopback v1, useful once anyone fronts the server with a proxy.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSEEvent(w, "ready", "", api.SubscribeReady{}); err != nil {
		return nil
	}
	if err := writeSnapshotFrame(w, res, includeTotal, shaper); err != nil {
		return nil
	}
	w.Flush()

	// waitCtx fires on client disconnect or server shutdown.
	waitCtx, cancelWait := mergeCtx(c.Request().Context(), d.shutdownCtx)
	defer cancelWait()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	mailbox := res.Sub.Events()
	batchCh := waitQueryBatch(mailbox, waitCtx)

	for {
		select {
		case bres := <-batchCh:
			if bres.err != nil {
				return d.streamQuerySubscribeFinish(w, res.Sub, bres.err)
			}
			batchCh = waitQueryBatch(mailbox, waitCtx)
			if len(bres.events) == 0 {
				continue
			}
			if err := writeQuerySubscribeBatch(w, bres.events, shaper); err != nil {
				return nil
			}
			w.Flush()
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}

// streamQuerySubscribeFinish renders the terminal frame and returns
// when the wait ends. Resolution order:
//
//   - mb.ErrClosed + Sub.Err()==ErrSubscriptionOverflow → closed{overflow}
//   - mb.ErrClosed + Sub.Err()==ErrSubscriptionDrifted  → closed{drifted}
//   - mb.ErrClosed + Sub.Err()==nil + engine teardown   → closed{server_shutdown | deauthorized}
//   - mb.ErrClosed + Sub.Err()==nil + nothing else      → closed{sdk_closed}
//   - context error + engine teardown                   → closed{server_shutdown | deauthorized}
//   - context error, no teardown                        → nothing (the client hung up)
//
// Sub.Err() returning nil on a closed mailbox covers both deliberate
// caller close and SDK-driven teardown; we lean on shutdownCtx to
// disambiguate the latter. The wait itself returns a context error
// when shutdownCtx fires before the SDK closes the mailbox — the
// normal teardown ordering — so that path emits the frame too. The
// request context is no evidence of a hang-up during a teardown: the
// gate cancels it with the engine (routes.go), racing the wait, so a
// teardown always writes the frame — to a client that did leave, the
// write fails and nothing is lost.
func (d *deps) streamQuerySubscribeFinish(w http.ResponseWriter, sub space.QuerySubscription, err error) error {
	teardown := d.shutdownCtx != nil && d.shutdownCtx.Err() != nil
	reason := ""
	switch {
	case !errors.Is(err, mb.ErrClosed):
		if !teardown {
			// The client hung up. Nothing to send.
			return nil
		}
		reason = d.engineCloseReason()
	case errors.Is(sub.Err(), space.ErrSubscriptionOverflow):
		reason = api.SubscribeClosedOverflow
	case errors.Is(sub.Err(), space.ErrSubscriptionDrifted):
		reason = api.SubscribeClosedDrifted
	case teardown:
		reason = d.engineCloseReason()
	default:
		reason = api.SubscribeClosedSDKClosed
	}
	_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: reason})
	flush(w)
	return nil
}

// engineCloseReason is the terminal reason a stream reports once the
// engine's ctx is cancelled. Read inside the gate, where d.eng is
// stable; server_shutdown without an engine (hand-built test deps).
func (d *deps) engineCloseReason() string {
	if d.eng != nil {
		return d.eng.getCloseReason()
	}
	return api.SubscribeClosedServerShutdown
}

type queryBatchResult struct {
	events []space.SubscriptionEvent
	err    error
}

// waitQueryBatch runs Events().Wait in a goroutine and surfaces the
// result via a one-shot channel. Mirrors the pattern used for the raw
// stream — Wait blocks, and the outer loop needs to interleave it
// with the keepalive ticker.
func waitQueryBatch(mailbox *mb.MB[space.SubscriptionEvent], ctx context.Context) <-chan queryBatchResult {
	out := make(chan queryBatchResult, 1)
	go func() {
		evs, err := mailbox.Wait(ctx)
		out <- queryBatchResult{events: evs, err: err}
	}()
	return out
}

// writeSnapshotFrame emits the initial `event: snapshot` frame
// carrying the materialised window (and Total if the caller asked for
// it). Identical shape to QueryResponse — same fields, separate type
// to keep future extensions decoupled.
func writeSnapshotFrame(w http.ResponseWriter, res *space.QueryResult, includeTotal bool, shaper recordShaper) error {
	payload := api.QuerySubscribeSnapshot{Records: shapeRecords(res.Initial, shaper)}
	if includeTotal {
		t := res.Total
		payload.Total = &t
		hm := res.HasNext
		payload.HasNext = &hm
	}
	return writeSSEEvent(w, "snapshot", "", payload)
}

// writeQuerySubscribeBatch emits one `event: changes` frame carrying
// the batch as a JSON array of QuerySubscribeEvent. Each event's
// Added/Updated records ship the full post-apply doc plus per-field
// $set/$unset ops — see api.QuerySubscribeEvent for the contract.
func writeQuerySubscribeBatch(w http.ResponseWriter, events []space.SubscriptionEvent, shaper recordShaper) error {
	payload := make([]api.QuerySubscribeEvent, len(events))
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	for i, ev := range events {
		payload[i] = api.QuerySubscribeEvent{
			VersionId: string(ev.VersionId),
			Added:     subRecordsToAPI(ev.Added, fa, shaper),
			Updated:   subRecordsToAPI(ev.Updated, fa, shaper),
			Removed:   removedRecordsToAPI(ev.Removed),
		}
	}
	return writeSSEEvent(w, "changes", "", payload)
}

// removedRecordsToAPI maps the SDK's []RemovedRecord onto the wire
// shape, serializing each RemoveReason to its stable string
// (deleted / filtered-out / displaced).
func removedRecordsToAPI(in []space.RemovedRecord) []api.RemovedRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.RemovedRecord, len(in))
	for i, r := range in {
		out[i] = api.RemovedRecord{Id: r.Id, Reason: r.Reason.String()}
	}
	return out
}

// subRecordsToAPI converts subscription records to the wire shape. The
// shaper governs both halves — the post-apply doc and the per-field
// ops — so a projected subscription cannot silently widen after the
// first update.
func subRecordsToAPI(in []space.SubRecord, fa *fastjson.Arena, shaper recordShaper) []api.QuerySubscribeRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.QuerySubscribeRecord, len(in))
	for i, r := range in {
		out[i] = api.QuerySubscribeRecord{Id: r.Id}
		if r.Doc != nil {
			out[i].Doc = shaper.record(r.Doc, fa).MarshalTo(nil)
		}
		if len(r.Ops) > 0 {
			ops := make([]api.SubscribeEventOp, 0, len(r.Ops))
			for _, op := range r.Ops {
				ops = append(ops, shaper.op(op, fa)...)
			}
			out[i].Ops = ops
		}
	}
	return out
}
