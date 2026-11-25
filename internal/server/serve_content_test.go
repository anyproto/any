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

// holedBody is a body whose bytes in [hole, holeEnd) cannot be read,
// like a partly fetched file whose only holder is gone; holeEnd zero
// means the hole runs to the end. With seekReads a seek into the hole
// fails too, as a reader that positions itself by reading does. reads
// counts Read calls.
type holedBody struct {
	data      []byte
	hole      int64
	holeEnd   int64
	seekReads bool
	pos       int64
	reads     int
}

func (b *holedBody) inHole(pos int64) bool {
	end := b.holeEnd
	if end == 0 {
		end = int64(len(b.data))
	}
	return pos >= b.hole && pos < end
}

func (b *holedBody) Read(p []byte) (int, error) {
	b.reads++
	if b.pos >= int64(len(b.data)) {
		return 0, io.EOF
	}
	if b.inHole(b.pos) {
		return 0, errHole
	}
	end := int64(len(b.data))
	if b.pos < b.hole {
		end = b.hole
	}
	n := copy(p, b.data[b.pos:end])
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
	// a seek to the end needs the bytes just before it
	if b.seekReads && (b.inHole(off) || (off > 0 && b.inHole(off-1))) {
		return 0, errHole
	}
	b.pos = off
	return off, nil
}

// holedRequest is one GET or HEAD against a holed 100-byte body.
type holedRequest struct {
	method    string
	rng       string
	hole      int64
	holeEnd   int64
	seekReads bool
	// noType leaves Content-Type unset, so ServeContent sniffs it.
	noType bool
}

func (hr holedRequest) serve(t *testing.T) (*httptest.ResponseRecorder, *holedBody, error) {
	t.Helper()
	body := &holedBody{data: []byte(strings.Repeat("0123456789", 10)), hole: hr.hole, holeEnd: hr.holeEnd, seekReads: hr.seekReads}
	method := hr.method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, "/", nil)
	if hr.rng != "" {
		req.Header.Set("Range", hr.rng)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if !hr.noType {
		c.Response().Header().Set(echo.HeaderContentType, "application/octet-stream")
	}
	c.Response().Header().Set("Content-Disposition", `inline; filename="a.bin"`)
	err := serveContent(c, body)
	if err != nil && c.Response().Committed {
		t.Fatalf("an error came back after the response started")
	}
	return rec, body, err
}

func serveHoled(t *testing.T, method string, hole int64, rangeHeader string) (*httptest.ResponseRecorder, *holedBody, error) {
	t.Helper()
	return holedRequest{method: method, rng: rangeHeader, hole: hole}.serve(t)
}

// unwritten fails the test unless the response is still the caller's:
// no body byte and none of the headers that described one.
func unwritten(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Body.Len() != 0 {
		t.Errorf("wrote %d bytes before the first content byte: %q", rec.Body.Len(), rec.Body.String())
	}
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Content-Type", "Content-Disposition", "X-Content-Type-Options"} {
		if v := rec.Header().Get(k); v != "" {
			t.Errorf("%s = %q left for the error reply", k, v)
		}
	}
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
			unwritten(t, rec)
		})
	}
}

// A body that positions itself by reading fails in Seek, not in Read:
// sizing it, seeking to a range, or seeking inside a multi-range reply.
// ServeContent answers those with a 500, a 416 or a preamble of its
// own; none of that may go out in place of the error.
func TestServeContent_FailedSeekIsAnError(t *testing.T) {
	for name, hr := range map[string]holedRequest{
		"size check, end missing":      {hole: 50},
		"head, end missing":            {method: http.MethodHead, hole: 50},
		"range before a missing end":   {hole: 50, rng: "bytes=0-9"},
		"range into a hole":            {hole: 40, holeEnd: 60, rng: "bytes=45-49"},
		"multi-range, first in a hole": {hole: 40, holeEnd: 60, rng: "bytes=45-49,0-4"},
	} {
		t.Run(name, func(t *testing.T) {
			hr.seekReads = true
			rec, _, err := hr.serve(t)
			if !errors.Is(err, errHole) {
				t.Fatalf("err = %v, want the seek error (reply: %d %q)", err, rec.Code, rec.Body.String())
			}
			unwritten(t, rec)
		})
	}
}

// Bytes read before the seek to the range do not count as its first
// byte: with the type sniffed from the local head, a range in the hole
// is still an error, and a HEAD is not failed by the sniff.
func TestServeContent_SniffIsNotTheFirstByte(t *testing.T) {
	rec, _, err := holedRequest{hole: 50, rng: "bytes=60-69", noType: true}.serve(t)
	if !errors.Is(err, errHole) {
		t.Fatalf("err = %v, want the read error (reply: %d, %d bytes)", err, rec.Code, rec.Body.Len())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("wrote %d bytes", rec.Body.Len())
	}

	rec, _, err = holedRequest{method: http.MethodHead, hole: 50, noType: true}.serve(t)
	if err != nil || rec.Code != http.StatusOK {
		t.Errorf("HEAD: %d, err %v", rec.Code, err)
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
