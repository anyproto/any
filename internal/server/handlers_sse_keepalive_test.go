package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// fencedWriter records writes and flushes that start or finish after
// the handler has returned — the moment net/http tears the real
// response down. Write is slow so a keepalive caught mid-write when the
// handler returns is observed deterministically.
type fencedWriter struct {
	*httptest.ResponseRecorder
	returned   atomic.Bool
	keepalives atomic.Int64
	late       atomic.Int64
}

func (f *fencedWriter) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte(": keepalive")) {
		f.keepalives.Add(1)
	}
	if f.returned.Load() {
		f.late.Add(1)
	}
	time.Sleep(200 * time.Microsecond)
	if f.returned.Load() {
		f.late.Add(1)
	}
	return f.ResponseRecorder.Write(p)
}

func (f *fencedWriter) Flush() {
	if f.returned.Load() {
		f.late.Add(1)
	}
	f.ResponseRecorder.Flush()
}

// TestStreamStatusSSE_NoWriteAfterReturn pins SYN-158: the keepalive
// goroutine must never touch the ResponseWriter once the handler has
// returned. The pump returns the instant a keepalive write is in
// flight, which is exactly the overdue-ticker-meets-closing-handler
// interleaving observed on iOS; the done fence makes the handler wait
// the write out and refuse every later tick.
func TestStreamStatusSSE_NoWriteAfterReturn(t *testing.T) {
	prev := keepaliveInterval
	keepaliveInterval = 50 * time.Microsecond
	defer func() { keepaliveInterval = prev }()

	d := &deps{}
	e := echo.New()
	for i := 0; i < 50; i++ {
		w := &fencedWriter{ResponseRecorder: httptest.NewRecorder()}
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), w)
		pump := func(ctx context.Context, emit func(string, any) error) error {
			deadline := time.Now().Add(time.Second)
			for w.keepalives.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Microsecond)
			}
			return nil
		}
		if err := d.streamStatusSSE(c, nil, pump); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		w.returned.Store(true)
		time.Sleep(time.Millisecond) // let a straggling tick fire
		if n := w.late.Load(); n > 0 {
			t.Fatalf("iteration %d: %d write/flush calls touched the writer after the handler returned", i, n)
		}
	}
}
