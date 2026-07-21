package server

import (
	"context"
	"encoding/json"
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
	q, opts, errResp, done := buildSharedQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal)
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
	q, opts, objectId, dataset, errResp, done := buildPerObjectQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId": sp.Id(), "objectId": objectId, "dataset": dataset,
		})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal)
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
//	           drifted, sdk_closed) or shutdownCtx (server_shutdown).
//
// streamsWG is bumped for the lifetime of the loop so server.Run can
// wait for in-flight streams to drain before exiting.
func (d *deps) streamQuerySubscribe(c echo.Context, res *space.QueryResult, includeTotal bool, strip ...string) error {
	defer res.Sub.Close()

	if d.streamsWG != nil {
		d.streamsWG.Add(1)
		defer d.streamsWG.Done()
	}

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
	if err := writeSnapshotFrame(w, res, includeTotal, strip...); err != nil {
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
			if err := writeQuerySubscribeBatch(w, bres.events, strip...); err != nil {
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
// when the mailbox closes. Resolution order:
//
//   - mb.ErrClosed + Sub.Err()==ErrSubscriptionOverflow → closed{overflow}
//   - mb.ErrClosed + Sub.Err()==ErrSubscriptionDrifted  → closed{drifted}
//   - mb.ErrClosed + Sub.Err()==nil + shutdownCtx tripped → closed{server_shutdown}
//   - mb.ErrClosed + Sub.Err()==nil + nothing else      → closed{sdk_closed}
//   - context error (client gone)                       → nothing
//
// Sub.Err() returning nil on a closed mailbox covers both deliberate
// caller close and SDK-driven teardown; we lean on shutdownCtx to
// disambiguate the latter.
func (d *deps) streamQuerySubscribeFinish(w http.ResponseWriter, sub space.QuerySubscription, err error) error {
	if !errors.Is(err, mb.ErrClosed) {
		// context error — the client hung up. Nothing to send.
		return nil
	}
	reason := ""
	switch {
	case errors.Is(sub.Err(), space.ErrSubscriptionOverflow):
		reason = api.SubscribeClosedOverflow
	case errors.Is(sub.Err(), space.ErrSubscriptionDrifted):
		reason = api.SubscribeClosedDrifted
	case d.shutdownCtx != nil && d.shutdownCtx.Err() != nil:
		reason = api.SubscribeClosedServerShutdown
	default:
		reason = api.SubscribeClosedSDKClosed
	}
	_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: reason})
	flush(w)
	return nil
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
func writeSnapshotFrame(w http.ResponseWriter, res *space.QueryResult, includeTotal bool, strip ...string) error {
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(res.Initial))
	for _, doc := range res.Initial {
		if doc == nil {
			records = append(records, json.RawMessage("null"))
			continue
		}
		v := doc.FastJson(fa)
		for _, key := range strip {
			v.Del(key)
		}
		records = append(records, json.RawMessage(v.MarshalTo(nil)))
	}
	payload := api.QuerySubscribeSnapshot{Records: records}
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
func writeQuerySubscribeBatch(w http.ResponseWriter, events []space.SubscriptionEvent, strip ...string) error {
	payload := make([]api.QuerySubscribeEvent, len(events))
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	for i, ev := range events {
		payload[i] = api.QuerySubscribeEvent{
			VersionId: string(ev.VersionId),
			Added:     subRecordsToAPI(ev.Added, fa, strip...),
			Updated:   subRecordsToAPI(ev.Updated, fa, strip...),
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

// subRecordsToAPI converts subscription records to the wire shape.
// strip lists top-level fields withheld from both the doc and any op
// whose path targets them (key material on tech-space rows).
func subRecordsToAPI(in []space.SubRecord, fa *fastjson.Arena, strip ...string) []api.QuerySubscribeRecord {
	stripped := func(path []string) bool {
		if len(path) == 0 {
			return false
		}
		for _, key := range strip {
			if path[0] == key {
				return true
			}
		}
		return false
	}
	if len(in) == 0 {
		return nil
	}
	out := make([]api.QuerySubscribeRecord, len(in))
	for i, r := range in {
		out[i] = api.QuerySubscribeRecord{Id: r.Id}
		if r.Doc != nil {
			v := r.Doc.FastJson(fa)
			for _, key := range strip {
				v.Del(key)
			}
			out[i].Doc = v.MarshalTo(nil)
		}
		if len(r.Ops) > 0 {
			ops := make([]api.SubscribeEventOp, 0, len(r.Ops))
			for _, op := range r.Ops {
				if stripped(op.Path) {
					continue
				}
				path := op.Path
				if path == nil {
					path = []string{}
				}
				apiOp := api.SubscribeEventOp{
					Type: string(op.Type),
					Path: path,
				}
				if op.Payload != nil {
					pv := op.Payload.FastJson(fa)
					// A multi-field op (empty path, object payload) carries
					// the fields inline — strip there too.
					if len(op.Path) == 0 && pv.Type() == fastjson.TypeObject {
						for _, key := range strip {
							pv.Del(key)
						}
					}
					apiOp.Payload = pv.MarshalTo(nil)
				}
				ops = append(ops, apiOp)
			}
			out[i].Ops = ops
		}
	}
	return out
}
