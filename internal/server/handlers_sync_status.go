package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// statusForwardBuffer is the per-stream channel depth between the
// SDK's dispatcher callback and the SSE writer. State-flip events
// are sparse (one per object on a real transition), so a small
// buffer is plenty. On overflow the forwarder drops the event and
// bumps a counter; the next successful frame is preceded by a
// `lagged` frame so clients know to re-GET.
const statusForwardBuffer = 16

// syncStatusSpaceGet handles GET /v1/spaces/:spaceId/sync-status.
//
//	@Summary	Get space sync status
//	@Tags		sync
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.SpaceSyncStatusResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/sync-status [get]
func (d *deps) syncStatusSpaceGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	return c.JSON(http.StatusOK, spaceSyncStatusToAPI(sp.SyncStatus().Space()))
}

// syncStatusObjectGet handles GET /v1/spaces/:spaceId/sync-status/objects/:objectId.
//
//	@Summary	Get object sync status
//	@Tags		sync
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Success	200			{object}	api.ObjectSyncStatusResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/sync-status/objects/{objectId} [get]
func (d *deps) syncStatusObjectGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	return c.JSON(http.StatusOK, objectSyncStatusToAPI(sp.SyncStatus().Object(objectId)))
}

// syncStatusSubscribe handles GET /v1/sync-status/subscribe.
//
//	@Summary	Subscribe to account-wide sync status (SSE)
//	@Tags		sync
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/sync-status/subscribe [get]
func (d *deps) syncStatusSubscribe(c echo.Context) error {
	events := make(chan space.SpaceSyncStatus, statusForwardBuffer)
	var dropped atomic.Uint64
	cancelSub := d.sdk.Spaces().SubscribeStatus(func(s space.SpaceSyncStatus) {
		select {
		case events <- s:
		default:
			dropped.Add(1)
		}
	})
	defer cancelSub()

	return d.streamStatusSSE(c, &dropped, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case s := <-events:
				if err := emit("status", spaceSyncStatusToAPI(s)); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

// syncStatusObjectSubscribe handles GET /v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe.
//
//	@Summary	Subscribe to object sync status (SSE)
//	@Tags		sync
//	@Produce	text/event-stream
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Object ID"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/sync-status/objects/{objectId}/subscribe [get]
func (d *deps) syncStatusObjectSubscribe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	events := make(chan space.ObjectSyncStatus, statusForwardBuffer)
	var dropped atomic.Uint64
	cancelSub := sp.SyncStatus().SubscribeObject(objectId, func(s space.ObjectSyncStatus) {
		select {
		case events <- s:
		default:
			dropped.Add(1)
		}
	})
	defer cancelSub()

	return d.streamStatusSSE(c, &dropped, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case s := <-events:
				if err := emit("status", objectSyncStatusToAPI(s)); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

// streamStatusSSE is the shared SSE driver for sync-status streams.
// Differs from the dataset-backed subscribe primitive in two ways:
//
//   - source is a typed Go channel fed from an SDK callback (not a
//     mailbox.MB), so there is no per-stream batching;
//   - terminal frame uses the same reason set as /subscribe so
//     clients can share the reconnection switch.
//
// pump is called once with the merged wait-context and an emit()
// closure. pump is responsible for selecting on ctx.Done and
// returning when it fires.
func (d *deps) streamStatusSSE(c echo.Context, dropped *atomic.Uint64, pump func(ctx context.Context, emit func(string, any) error) error) error {
	if d.streamsWG != nil {
		d.streamsWG.Add(1)
		defer d.streamsWG.Done()
	}

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSEEvent(w, "ready", "", api.SyncStatusReady{}); err != nil {
		return nil
	}
	w.Flush()

	waitCtx, cancelWait := mergeCtx(c.Request().Context(), d.shutdownCtx)
	defer cancelWait()

	var lastDropped uint64
	emit := func(event string, payload any) error {
		if dropped != nil {
			if cur := dropped.Load(); cur > lastDropped {
				lastDropped = cur
				if err := writeSSEEvent(w, "lagged", "", api.SyncStatusLagged{Total: cur}); err != nil {
					return err
				}
			}
		}
		if err := writeSSEEvent(w, event, "", payload); err != nil {
			return err
		}
		w.Flush()
		return nil
	}

	// Keepalive: state events are sparse, so an idle stream would
	// otherwise sit silent indefinitely. Stop alongside the pump.
	stopKeepalive := make(chan struct{})
	go func() {
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-stopKeepalive:
				return
			case <-waitCtx.Done():
				return
			case <-t.C:
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flush(w)
			}
		}
	}()
	defer close(stopKeepalive)

	_ = pump(waitCtx, emit)

	// Terminal frame mirrors /subscribe — share the same reason set
	// so clients can switch on it identically. Client-disconnect
	// case writes nothing (peer is gone).
	if d.shutdownCtx != nil && d.shutdownCtx.Err() != nil {
		_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: api.SubscribeClosedServerShutdown})
		flush(w)
	}
	return nil
}

func spaceSyncStatusToAPI(s space.SpaceSyncStatus) api.SpaceSyncStatusResponse {
	return api.SpaceSyncStatusResponse{
		SpaceId:      s.SpaceId,
		State:        s.State.String(),
		Synced:       s.Synced,
		Total:        s.Total,
		NetworkPeers: s.NetworkPeers,
		LastSyncedAt: s.LastSyncedAt,
	}
}

func objectSyncStatusToAPI(s space.ObjectSyncStatus) api.ObjectSyncStatusResponse {
	return api.ObjectSyncStatusResponse{
		ObjectId:   s.ObjectId,
		State:      s.State.String(),
		LastSyncAt: s.LastSyncAt,
	}
}
