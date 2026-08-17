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

func TestServer_ProcessValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	cases := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{"register missing id", "/v1/processes", `{"kind":"k","title":"T","scope":"device"}`, 400, "request.missing_field"},
		{"register bad id", "/v1/processes", `{"id":"a/b","kind":"k","title":"T","scope":"device"}`, 400, "request.invalid_field"},
		{"register missing kind", "/v1/processes", `{"id":"p1","title":"T","scope":"device"}`, 400, "request.missing_field"},
		{"register missing title", "/v1/processes", `{"id":"p1","kind":"k","scope":"device"}`, 400, "request.missing_field"},
		{"register missing scope", "/v1/processes", `{"id":"p1","kind":"k","title":"T"}`, 400, "request.missing_field"},
		{"register bad scope", "/v1/processes", `{"id":"p1","kind":"k","title":"T","scope":"global"}`, 400, "request.invalid_field"},
		{"register space needs spaceId", "/v1/processes", `{"id":"p1","kind":"k","title":"T","scope":"space"}`, 400, "request.missing_field"},
		{"register spaceId only with space", "/v1/processes", `{"id":"p1","kind":"k","title":"T","scope":"device","spaceId":"sp1"}`, 400, "request.invalid_field"},
		{"register bad target", "/v1/processes", `{"id":"p1","kind":"k","title":"T","scope":"device","target":"a b"}`, 400, "request.invalid_field"},
		{"register unknown field", "/v1/processes", `{"id":"p1","kind":"k","title":"T","scope":"device","state":"running"}`, 400, "request.unknown_field"},
		{"progress negative done", "/v1/processes/p1/progress", `{"done":-1}`, 400, "request.invalid_field"},
		{"progress unregistered", "/v1/processes/ghost/progress", `{"done":1}`, 404, "process.not_found"},
		{"progress bad id", "/v1/processes/a%20b/progress", `{"done":1}`, 400, "request.invalid_field"},
		{"finish missing status", "/v1/processes/p1/finish", `{}`, 400, "request.missing_field"},
		{"finish bad status", "/v1/processes/p1/finish", `{"status":"crashed"}`, 400, "request.invalid_field"},
		{"finish failed needs error", "/v1/processes/p1/finish", `{"status":"failed"}`, 400, "request.missing_field"},
		{"finish error only on failed", "/v1/processes/p1/finish", `{"status":"done","error":{"message":"m"}}`, 400, "request.invalid_field"},
		{"finish unregistered", "/v1/processes/ghost/finish", `{"status":"done"}`, 404, "process.not_found"},
		{"cancel unknown", "/v1/processes/ghost/cancel", `{}`, 404, "process.not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.code) {
				t.Errorf("body %s does not carry code %q", rec.Body.String(), tc.code)
			}
		})
	}
}

// TestServer_ProcessRoundTrip drives the device-scope lifecycle:
// register → view row → progress updates the row → cancel reaches an
// SSE subscriber addressed at the owner → finish(failed) lands the
// terminal state with its error.
func TestServer_ProcessRoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	listOne := func() api.Process {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, "/v1/processes", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var out api.ProcessListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(out.Processes) != 1 {
			t.Fatalf("list = %d entries, want 1: %+v", len(out.Processes), out.Processes)
		}
		return out.Processes[0]
	}

	rec := doJSON(t, e, http.MethodPost, "/v1/processes",
		`{"id":"run1","kind":"agent.run","title":"Demo run","scope":"device","target":"obj1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	p := listOne()
	own := d.sdk.Account().Id()
	if p.State != api.ProcessStateRunning || p.Kind != "agent.run" || p.Title != "Demo run" ||
		p.Target != "obj1" || p.Identity != own || !p.Self || p.Scope != api.EventScopeDevice {
		t.Errorf("registered row = %+v", p)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/processes/run1/progress", `{"done":3,"total":9,"message":"step 3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body.String())
	}
	if p = listOne(); p.Done != 3 || p.Total != 9 || p.Message != "step 3" || p.Kind != "agent.run" {
		t.Errorf("after progress = %+v", p)
	}

	// The owner's cancel channel: an SSE subscription on process.cancel.
	frames := make(chan client.SSEFrame, 16)
	go func() {
		_ = cl.StreamEvents(ctx, url.Values{"type": {api.EventProcessCancel}, "target": {"run1"}},
			func(f client.SSEFrame) error {
				select {
				case frames <- f:
				case <-ctx.Done():
				}
				return nil
			})
	}()
	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready", got.Event)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/processes/run1/cancel", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "event" {
		t.Fatalf("frame = %q, want event (data=%s)", got.Event, got.Data)
	}
	var ev api.Event
	if err := json.Unmarshal(got.Data, &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	var cd processCancelData
	if err := json.Unmarshal(ev.Data, &cd); err != nil {
		t.Fatalf("decode cancel data: %v", err)
	}
	if ev.Type != api.EventProcessCancel || ev.Target != "run1" || cd.Identity != own {
		t.Errorf("cancel event = %+v data=%+v, want owner %s", ev, cd, own)
	}
	// Cancel changes no state — the owner does.
	if p = listOne(); p.State != api.ProcessStateRunning {
		t.Errorf("after cancel state = %q, want running", p.State)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/processes/run1/finish",
		`{"status":"failed","error":{"code":"agent.dead","message":"it broke"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
	}
	if p = listOne(); p.State != api.ProcessStateFailed || p.Error == nil ||
		p.Error.Code != "agent.dead" || p.Error.Message != "it broke" {
		t.Errorf("terminal row = %+v", p)
	}
}

// TestServer_ProcessCancelAmbiguous: same id under two identities
// needs an explicit identity pick; with one it addresses that owner.
func TestServer_ProcessCancelAmbiguous(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/processes",
		`{"id":"job","kind":"k","title":"Mine","scope":"device"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	// A remote publisher's broadcast materializes through the hub tap.
	d.eventsHub().publish(*procEvent(api.EventProcessStarted, "remoteAcct", "job", false,
		processEventData{Kind: "k", Title: "Theirs"}))

	rec = doJSON(t, e, http.MethodPost, "/v1/processes/job/cancel", `{}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "process.ambiguous") {
		t.Fatalf("ambiguous cancel: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if ids, _ := env.Error.Details["identities"].([]any); len(ids) != 2 {
		t.Errorf("details.identities = %v, want 2 entries", env.Error.Details["identities"])
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/processes/job/cancel", `{"identity":"remoteAcct"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("targeted cancel: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/processes/job/cancel", `{"identity":"nobody"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "process.not_found") {
		t.Fatalf("unknown identity cancel: %d %s", rec.Code, rec.Body.String())
	}
}
