package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// keepaliveInterval is the gap between SSE comment-frame heartbeats.
// Idle middleboxes (proxies, load balancers) commonly close streams
// after 30–60s of silence; 25s keeps us comfortably under that. On
// loopback v1 it is belt-and-braces — cheap, future-proof. A var so
// the keepalive race tests can shrink it.
var keepaliveInterval = 25 * time.Second

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
