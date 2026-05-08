package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cheggaaa/mb/v3"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// keepaliveInterval is the gap between SSE comment-frame heartbeats.
// Idle middleboxes (proxies, load balancers) commonly close streams
// after 30–60s of silence; 25s keeps us comfortably under that. On
// loopback v1 it is belt-and-braces — cheap, future-proof.
const keepaliveInterval = 25 * time.Second

// subscribeObject handles GET /v1/spaces/:spaceId/objects/:objectId/subscribe.
// Streams CRDT apply events for the given (objectId, dataset) over
// Server-Sent Events. The dataset is required as a query param —
// matches Space.Subscribe(ctx, objectId, dataset).
func (d *deps) subscribeObject(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	dataset := c.QueryParam("dataset")
	if dataset == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "dataset query param required", nil)
	}

	sub, err := sp.Subscribe(c.Request().Context(), objectId, dataset)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId":  sp.Id(),
			"objectId": objectId,
			"dataset":  dataset,
		})
	}
	defer sub.Close()

	return d.streamSSE(c, sub)
}

// subscribeProperties handles GET /v1/spaces/:spaceId/properties/subscribe.
// Firehose for the per-space `objects` dataset (every object's
// property-value changes), powered by Space.SubscribeProperties.
func (d *deps) subscribeProperties(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	sub, err := sp.SubscribeProperties(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	defer sub.Close()

	return d.streamSSE(c, sub)
}

// streamSSE pumps events from sub's mailbox to the client as SSE
// frames until the client disconnects, the SDK closes the
// subscription, or the server starts shutting down. Each frame is
// `event: changes` with a JSON array body — Mailbox.Wait coalesces
// events accumulated during the previous write into a single batch
// for free, so a slow client/network produces fewer, larger frames
// rather than head-of-line stalls.
//
// streamsWG is bumped for the lifetime of the loop so server.Run can
// wait for in-flight streams to drain before exiting.
//
// On graceful shutdown the client receives a terminal `event: closed`
// frame so it knows to reconnect rather than treat the EOF as a
// transport error.
func (d *deps) streamSSE(c echo.Context, sub space.Subscription) error {
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
	w.Flush()

	// waitCtx fires on either client disconnect or server shutdown.
	// Mailbox.Wait selects on it and returns with whatever events were
	// already queued, so the streaming loop drains promptly without
	// burying late-arrival events under a final keepalive tick.
	waitCtx, cancelWait := mergeCtx(c.Request().Context(), d.shutdownCtx)
	defer cancelWait()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	mailbox := sub.Mailbox()
	var lastDropped uint64

	// One in-flight waiter at a time. The keepalive branch must NOT
	// respawn — that would orphan the previous goroutine on
	// mailbox.Wait(waitCtx), which only unblocks on event arrival or
	// ctx cancel. With keepaliveInterval=25s an idle SSE stream
	// would otherwise leak one goroutine per tick.
	batchCh := waitBatch(mailbox, waitCtx)

	for {
		select {
		case res := <-batchCh:
			if res.err != nil {
				return d.streamSSEFinish(w, res.err)
			}
			// Spawn the next waiter immediately so we don't miss
			// events that land while we're writing this batch.
			batchCh = waitBatch(mailbox, waitCtx)
			if len(res.events) == 0 {
				continue
			}
			// Surface drops alongside the batch so clients know a
			// re-Query is in order. Cumulative count is the source of
			// truth; emit only when it grows.
			if dropped := sub.Dropped(); dropped > lastDropped {
				lastDropped = dropped
				if err := writeSSEEvent(w, "lagged", "", api.SubscribeLagged{Total: dropped}); err != nil {
					return nil
				}
			}
			if err := writeBatch(w, res.events); err != nil {
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

// streamSSEFinish handles the terminal frame and return value when
// Mailbox.Wait returns an error. mb.ErrClosed means the SDK released
// the subscription (space close, sdk close); a context error means
// either the client hung up or the server is shutting down. The
// shutdown signal wins precedence over a coincident client cancel —
// readers asked us to tell them so they can distinguish reconnectable
// closes from hard transport errors.
func (d *deps) streamSSEFinish(w http.ResponseWriter, err error) error {
	switch {
	case errors.Is(err, mb.ErrClosed):
		_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: api.SubscribeClosedSDKClosed})
		flush(w)
	case d.shutdownCtx != nil && d.shutdownCtx.Err() != nil:
		_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: api.SubscribeClosedServerShutdown})
		flush(w)
	default:
		// client disconnect — nothing to send
	}
	return nil
}

type batchResult struct {
	events []space.Event
	err    error
}

// waitBatch runs Mailbox.Wait in a goroutine and surfaces the result
// via a one-shot channel. We have to spawn here because Mailbox.Wait
// blocks; the streaming loop needs to interleave the wait with a
// keepalive ticker, and Go's select can't take a function call.
func waitBatch(mailbox *mb.MB[space.Event], ctx context.Context) <-chan batchResult {
	out := make(chan batchResult, 1)
	go func() {
		evs, err := mailbox.Wait(ctx)
		out <- batchResult{events: evs, err: err}
	}()
	return out
}

// writeBatch emits the events as a single `event: changes` SSE frame
// with the JSON body `[{event}, {event}, ...]`. No SSE `id:` line —
// dedup uses the per-event VersionId in the payload compared against
// the per-record `_ver` stamps in queried records, not a transport-
// layer cursor.
func writeBatch(w http.ResponseWriter, events []space.Event) error {
	payload := make([]api.SubscribeEvent, len(events))
	for i, ev := range events {
		payload[i] = subscribeEventToAPI(ev)
	}
	return writeSSEEvent(w, "changes", "", payload)
}

// subscribeEventToAPI projects an SDK Event onto its wire shape.
// Op.Payload is a *anyenc.Value — MarshalTo on that produces anyenc's
// binary encoding (NOT JSON), so we route through FastJson(arena) to
// get a *fastjson.Value whose MarshalTo emits real JSON. Records with
// Deleted=true ship Ops empty; the consumer drops the record from its
// local state.
func subscribeEventToAPI(ev space.Event) api.SubscribeEvent {
	out := api.SubscribeEvent{
		SpaceId:   ev.SpaceId,
		ObjectId:  ev.ObjectId,
		Dataset:   ev.Dataset,
		VersionId: string(ev.VersionId),
	}
	if len(ev.Records) == 0 {
		return out
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	out.Records = make([]api.SubscribeEventRecord, len(ev.Records))
	for i, rec := range ev.Records {
		dst := api.SubscribeEventRecord{
			Id:      rec.Id,
			Variant: rec.Variant,
			Deleted: rec.Deleted,
		}
		if !rec.Deleted && len(rec.Ops) > 0 {
			dst.Ops = make([]api.SubscribeEventOp, len(rec.Ops))
			for j, op := range rec.Ops {
				dst.Ops[j] = api.SubscribeEventOp{
					Type: string(op.Type),
					Path: op.Path,
				}
				if op.Payload != nil {
					dst.Ops[j].Payload = op.Payload.FastJson(fa).MarshalTo(nil)
				}
			}
		}
		out.Records[i] = dst
	}
	return out
}

// writeSSEEvent emits one SSE frame: an event line, an optional id
// line, and a single data line carrying the JSON-encoded payload.
// Newlines inside the payload would split the data field; json.Marshal
// escapes them to \n so a single data line is always safe.
func writeSSEEvent(w http.ResponseWriter, event, id string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// mergeCtx returns a child context that is canceled when either
// parent cancels. Used to merge the per-request context (client
// disconnect) with the server-wide shutdown context. b may be nil —
// tests build deps without booting Run.
func mergeCtx(a, b context.Context) (context.Context, context.CancelFunc) {
	if b == nil {
		return context.WithCancel(a)
	}
	ctx, cancel := context.WithCancel(a)
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
