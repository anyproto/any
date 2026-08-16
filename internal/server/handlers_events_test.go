package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

func TestServer_EventsPublishValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	cases := []struct {
		name string
		body string
		code string
	}{
		{"missing type", `{"scope":"device"}`, "request.missing_field"},
		{"bad type", `{"type":"Process/Progress","scope":"device"}`, "request.invalid_field"},
		{"missing scope", `{"type":"test.ping"}`, "request.missing_field"},
		{"bad scope", `{"type":"test.ping","scope":"global"}`, "request.invalid_field"},
		{"space scope needs spaceId", `{"type":"test.ping","scope":"space"}`, "request.missing_field"},
		{"spaceId only with space scope", `{"type":"test.ping","scope":"device","spaceId":"sp1"}`, "request.invalid_field"},
		{"bad target", `{"type":"test.ping","scope":"device","target":"a/b"}`, "request.invalid_field"},
		{"sender is server-stamped", `{"type":"test.ping","scope":"device","sender":{"identity":"x","self":true}}`, "request.unknown_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, "/v1/events", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.code) {
				t.Errorf("body %s does not carry code %q", rec.Body.String(), tc.code)
			}
		})
	}

	t.Run("oversized data", func(t *testing.T) {
		big := strings.Repeat("x", eventDataLimit)
		rec := doJSON(t, e, http.MethodPost, "/v1/events",
			`{"type":"test.ping","scope":"device","data":{"blob":"`+big+`"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "events.payload_too_large") {
			t.Errorf("body %s does not carry events.payload_too_large", rec.Body.String())
		}
	})

	t.Run("network scopes 501 until the pubsub bridge", func(t *testing.T) {
		for _, body := range []string{
			`{"type":"test.ping","scope":"account"}`,
			`{"type":"test.ping","scope":"space","spaceId":"sp1"}`,
		} {
			rec := doJSON(t, e, http.MethodPost, "/v1/events", body)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status=%d, want 501; body=%s", rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("no subscribers still succeeds", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, "/v1/events", `{"type":"test.ping","scope":"device"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var out api.EventPublishResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Subscribers != 0 {
			t.Errorf("subscribers = %d, want 0", out.Subscribers)
		}
	})
}

func TestServer_EventsSubscribeParamValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	for _, q := range []string{"scope=global", "type=Process.*", "type=*", "target=a/b"} {
		rec := doJSON(t, e, http.MethodGet, "/v1/events/subscribe?"+q, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d, want 400; body=%s", q, rec.Code, rec.Body.String())
		}
	}
}

// TestServer_Events_RoundTrip drives the full device-scope path:
// filtered SSE subscribe → publish → delivery with a server-stamped
// sender → non-matching publish reports 0 subscribers → shutdown emits
// the terminal closed frame.
func TestServer_Events_RoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frames := make(chan client.SSEFrame, 16)
	go func() {
		_ = cl.StreamEvents(ctx, url.Values{"type": {"ui.*"}}, func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-ctx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready (data=%s)", got.Event, got.Data)
	}

	pub, err := cl.EventsPublish(ctx, api.EventPublishRequest{
		Type:  api.EventUIOpenSpace,
		Scope: api.EventScopeDevice,
		Data:  json.RawMessage(`{"spaceId":"sp1"}`),
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.Subscribers != 1 {
		t.Errorf("subscribers = %d, want 1", pub.Subscribers)
	}

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "event" {
		t.Fatalf("frame = %q, want event (data=%s)", got.Event, got.Data)
	}
	var ev api.Event
	if err := json.Unmarshal(got.Data, &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if ev.Type != api.EventUIOpenSpace || ev.Scope != api.EventScopeDevice {
		t.Errorf("event = %+v, want type %s scope device", ev, api.EventUIOpenSpace)
	}
	if ev.Sender == nil || ev.Sender.Identity == "" || !ev.Sender.Self {
		t.Errorf("sender = %+v, want stamped {identity, self:true}", ev.Sender)
	}

	// The subscriber's type filter excludes this one.
	pub, err = cl.EventsPublish(ctx, api.EventPublishRequest{Type: "process.progress", Scope: api.EventScopeDevice})
	if err != nil {
		t.Fatalf("publish non-matching: %v", err)
	}
	if pub.Subscribers != 0 {
		t.Errorf("non-matching subscribers = %d, want 0", pub.Subscribers)
	}

	d.cancelShutdown()
	got = waitFrame(t, frames, 5*time.Second)
	if got.Event != "closed" {
		t.Fatalf("post-shutdown frame = %q, want closed (data=%s)", got.Event, got.Data)
	}
	var closed api.SubscribeClosed
	if err := json.Unmarshal(got.Data, &closed); err != nil {
		t.Fatalf("decode closed: %v", err)
	}
	if closed.Reason != api.SubscribeClosedServerShutdown {
		t.Errorf("reason = %q, want %q", closed.Reason, api.SubscribeClosedServerShutdown)
	}
}
