package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

var errHole = errors.New("block not available")

// holedBody is a body whose bytes from hole on cannot be read, like a
// partly fetched file whose only holder is gone. reads counts Read
// calls.
type holedBody struct {
	data  []byte
	hole  int64
	pos   int64
	reads int
}

func (b *holedBody) Read(p []byte) (int, error) {
	b.reads++
	if b.pos >= int64(len(b.data)) {
		return 0, io.EOF
	}
	if b.pos >= b.hole {
		return 0, errHole
	}
	n := copy(p, b.data[b.pos:b.hole])
	b.pos += int64(n)
	return n, nil
}

func (b *holedBody) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		off += b.pos
	case io.SeekEnd:
		off += int64(len(b.data))
	}
	b.pos = off
	return off, nil
}

func serveHoled(t *testing.T, method string, hole int64, rangeHeader string) (*httptest.ResponseRecorder, *holedBody, error) {
	t.Helper()
	body := &holedBody{data: []byte(strings.Repeat("0123456789", 10)), hole: hole}
	req := httptest.NewRequest(method, "/", nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Response().Header().Set(echo.HeaderContentType, "application/octet-stream")
	c.Response().Header().Set("Content-Disposition", `inline; filename="a.bin"`)
	err := serveContent(c, body)
	if err != nil && c.Response().Committed {
		t.Fatalf("an error came back after the response started")
	}
	return rec, body, err
}

// A body that cannot produce the first byte of what was asked for is an
// error with nothing written: the caller still owns the response.
func TestServeContent_NoFirstByteIsAnError(t *testing.T) {
	for name, tc := range map[string]struct {
		hole int64
		rng  string
	}{
		"full":                  {0, ""},
		"range":                 {0, "bytes=10-19"},
		"suffix range":          {50, "bytes=-10"},
		"range past the hole":   {50, "bytes=60-69"},
		"multi-range, bad head": {50, "bytes=60-64,0-4"},
	} {
		t.Run(name, func(t *testing.T) {
			rec, _, err := serveHoled(t, http.MethodGet, tc.hole, tc.rng)
			if !errors.Is(err, errHole) {
				t.Fatalf("err = %v, want the read error", err)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("wrote %d bytes before the first content byte", rec.Body.Len())
			}
			for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Content-Type", "Content-Disposition"} {
				if v := rec.Header().Get(k); v != "" {
					t.Errorf("%s = %q left for the error reply", k, v)
				}
			}
		})
	}
}

// What is local stays servable: a range in front of the hole is a
// complete 206.
func TestServeContent_RangeBeforeTheHole(t *testing.T) {
	rec, _, err := serveHoled(t, http.MethodGet, 50, "bytes=10-19")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "0123456789" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 10-19/100" {
		t.Errorf("Content-Range = %q", got)
	}
}

// Once the first byte is out the status cannot change: a later hole
// cuts the body short and is not an error for the caller.
func TestServeContent_HoleAfterTheFirstByteCutsTheBody(t *testing.T) {
	rec, _, err := serveHoled(t, http.MethodGet, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "100" || rec.Body.Len() != 50 {
		t.Errorf("got %d, Content-Length %q, %d bytes", rec.Code, rec.Header().Get("Content-Length"), rec.Body.Len())
	}

	rec, _, err = serveHoled(t, http.MethodGet, 50, "bytes=0-4,60-64")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent || !strings.Contains(rec.Body.String(), "01234") {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}

// Replies that read no content pass through unchanged.
func TestServeContent_RepliesWithoutContent(t *testing.T) {
	rec, body, err := serveHoled(t, http.MethodHead, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Length") != "100" || rec.Body.Len() != 0 || body.reads != 0 {
		t.Errorf("HEAD: %d, Content-Length %q, %d bytes, %d reads", rec.Code, rec.Header().Get("Content-Length"), rec.Body.Len(), body.reads)
	}

	rec, _, err = serveHoled(t, http.MethodGet, 100, "bytes=500-")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Body.Len() == 0 {
		t.Errorf("unsatisfiable range: %d %q", rec.Code, rec.Body.String())
	}
}

// A whole body is served as before, full and multi-range.
func TestServeContent_WholeBody(t *testing.T) {
	rec, _, err := serveHoled(t, http.MethodGet, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 100 || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("got %d, %d bytes, %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Type"))
	}

	rec, _, err = serveHoled(t, http.MethodGet, 100, "bytes=0-4,60-64")
	if err != nil {
		t.Fatal(err)
	}
	out := rec.Body.String()
	if rec.Code != http.StatusPartialContent || !strings.HasPrefix(rec.Header().Get("Content-Type"), "multipart/byteranges") ||
		!strings.Contains(out, "bytes 0-4/100") || !strings.Contains(out, "bytes 60-64/100") {
		t.Errorf("got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), out)
	}
	if got := rec.Header().Get("Content-Length"); got != "" && got != strconv.Itoa(len(out)) {
		t.Errorf("Content-Length %q for %d bytes", got, len(out))
	}
}
