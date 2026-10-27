package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// decodeClosedFrame returns the payload of the one `closed` frame in an
// SSE body.
func decodeClosedFrame(t *testing.T, body string) api.SubscribeClosed {
	t.Helper()
	_, rest, ok := strings.Cut(body, "event: closed\ndata: ")
	if !ok {
		t.Fatalf("no closed frame in body:\n%s", body)
	}
	line, _, _ := strings.Cut(rest, "\n")
	var closed api.SubscribeClosed
	if err := json.Unmarshal([]byte(line), &closed); err != nil {
		t.Fatalf("decode closed: %v (%s)", err, line)
	}
	return closed
}

// A snapshot read that outlasts the open grace must not keep the client
// waiting for headers: the stream is committed and kept warm with
// keepalives while the read runs, and a read that then fails ends the
// stream with closed{open_failed} carrying the envelope the prompt
// failure would have answered with.
func TestQuerySubscribe_SlowOpenCommitsStreamBeforeSnapshot(t *testing.T) {
	prevGrace, prevKeepalive := subscribeOpenGrace, keepaliveInterval
	subscribeOpenGrace, keepaliveInterval = time.Millisecond, time.Millisecond
	defer func() { subscribeOpenGrace, keepaliveInterval = prevGrace, prevKeepalive }()

	d := &deps{}
	e := echo.New()
	w := &fencedWriter{ResponseRecorder: httptest.NewRecorder()}
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), w)

	subscribe := func(ctx context.Context) (*space.QueryResult, error) {
		// The read is slow: it does not return until the client has
		// been fed at least one keepalive.
		deadline := time.Now().Add(5 * time.Second)
		for w.keepalives.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Microsecond)
		}
		return nil, fmt.Errorf("query: %w", space.ErrObjectNotFound)
	}
	mapErr := func(c echo.Context, err error) error {
		return sdkOpError(c, err, map[string]any{"objectId": "o1"})
	}
	if err := d.serveQuerySubscribe(c, false, recordShaper{}, subscribe, mapErr); err != nil {
		t.Fatalf("serveQuerySubscribe: %v", err)
	}
	w.returned.Store(true)

	if w.keepalives.Load() == 0 {
		t.Fatal("no keepalive reached the client before the read landed")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the stream was committed before the read failed)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	closed := decodeClosedFrame(t, w.Body.String())
	if closed.Reason != api.SubscribeClosedOpenFailed || closed.Status != http.StatusNotFound || closed.Code != "object.not_found" {
		t.Fatalf("closed = %+v, want open_failed / 404 / object.not_found", closed)
	}
	if closed.Message == "" {
		t.Fatal("closed frame carries no message")
	}
}

// With the stream committed before the SDK answers, a healthy read still
// delivers ready → snapshot, and a refused request still surfaces its
// envelope — as a closed{open_failed} frame, since the status is gone.
func TestServer_QuerySubscribe_CommittedOpen(t *testing.T) {
	prev := subscribeOpenGrace
	subscribeOpenGrace = 0
	defer func() { subscribeOpenGrace = prev }()

	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	l := setupSharedLab(t, e)
	l.seed(t, e)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	frames, _ := openStream(streamCtx, cl.StreamQuerySubscribe, l.spaceId,
		`{"objectId":"`+l.objs[1]+`","dataset":"`+l.samples+`","sort":["score"],"limit":50}`)
	if snap := readSnapshot(t, frames); len(snap.Records) == 0 {
		t.Fatal("snapshot is empty")
	}
	streamCancel()

	// A deleted object: the SDK refuses the subscribe after its
	// snapshot fence, past every request-boundary check.
	gone := l.objs[2]
	doJSONExpect(t, e, http.MethodDelete, l.base()+"/objects/"+gone, http.StatusNoContent)
	rec := doJSON(t, e, http.MethodPost, l.base()+"/query/subscribe",
		`{"objectId":"`+gone+`","dataset":"`+l.samples+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	closed := decodeClosedFrame(t, rec.Body.String())
	if closed.Reason != api.SubscribeClosedOpenFailed || closed.Status != http.StatusNotFound || closed.Code != "object.not_found" {
		t.Fatalf("closed = %+v, want open_failed / 404 / object.not_found", closed)
	}
}
