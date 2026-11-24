package server

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

var contentLog = logger.NewNamed("content")

// serveContent serves a seekable body through http.ServeContent (Range,
// 206, HEAD) and sends nothing until the first content byte was read. A
// body that cannot start comes back as that read's error, with the
// response still unwritten, so the caller answers with a typed error
// instead of a 2xx and an empty body. A read that fails later cuts the
// body: the status is already out.
//
// The modtime is zero: ServeContent then skips Last-Modified and
// If-Modified-Since, which a CRDT row has no meaning for.
func serveContent(c echo.Context, content io.ReadSeeker) error {
	body := &watchedBody{ReadSeeker: content}
	w := &heldWriter{ResponseWriter: c.Response(), body: body}
	http.ServeContent(w, c.Request(), "", time.Time{}, body)

	started, err := body.state()
	switch {
	case w.sent:
		if err != nil {
			contentLog.Warn("body cut after the first byte", zap.String("path", c.Path()), zap.Error(err))
		}
		return nil
	case err != nil && !started:
		// ServeContent described a body that will not follow.
		h := c.Response().Header()
		for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Content-Type", "Content-Disposition"} {
			h.Del(k)
		}
		return err
	}
	// Nothing was read: HEAD, an unsatisfiable range, an empty body.
	return w.release()
}

// watchedBody records whether the body produced a byte and the first
// read error. ServeContent reads a multi-range body on another
// goroutine, hence the lock.
type watchedBody struct {
	io.ReadSeeker
	mu      sync.Mutex
	started bool
	err     error
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadSeeker.Read(p)
	b.mu.Lock()
	if n > 0 {
		b.started = true
	}
	if err != nil && err != io.EOF && b.err == nil {
		b.err = err
	}
	b.mu.Unlock()
	return n, err
}

func (b *watchedBody) state() (started bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started, b.err
}

// heldWriter keeps the status and whatever is written before the first
// content byte — a multi-range preamble, an error text — and lets them
// out once the body started. It has no ReadFrom on purpose: the copy
// must read before it writes.
type heldWriter struct {
	http.ResponseWriter
	body *watchedBody
	code int
	held []byte
	sent bool
}

func (w *heldWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *heldWriter) Write(p []byte) (int, error) {
	if !w.sent {
		started, err := w.body.state()
		if !started {
			if err != nil {
				return 0, err
			}
			w.held = append(w.held, p...)
			return len(p), nil
		}
		if err = w.release(); err != nil {
			return 0, err
		}
	}
	return w.ResponseWriter.Write(p)
}

// release sends the held status and bytes.
func (w *heldWriter) release() error {
	w.sent = true
	if w.code != 0 {
		w.ResponseWriter.WriteHeader(w.code)
	}
	if len(w.held) == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.held)
	w.held = nil
	return err
}
