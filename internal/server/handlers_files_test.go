package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Byte prefixes long enough for the sniffer to place them. These are
// real signatures, not plausible-looking ones — a hand-drawn header
// that no detector recognises would make the table pass for the wrong
// reason.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{0}, 32)...)
	pdfBytes  = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	svgBytes  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	heicBytes = append(append([]byte{0, 0, 0, 0x18}, []byte("ftypheic")...), []byte("\x00\x00\x00\x00mif1heic")...)
	mp3Bytes  = append([]byte{0xff, 0xfb, 0x90, 0x00}, bytes.Repeat([]byte{0}, 128)...)
	movBytes  = append(append([]byte{0, 0, 0, 0x14}, []byte("ftypqt  ")...), []byte("\x00\x00\x02\x00qt  ")...)
	// PNG signature, a 13-byte IHDR chunk, then an acTL chunk — the
	// animation control chunk that makes it an APNG.
	apngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), append(bytes.Repeat([]byte{0}, 17), []byte("\x00\x00\x00\x08acTL\x00\x00\x00\x01\x00\x00\x00\x00")...)...)
	// EBML header with the matroska doctype.
	mkvBytes = append([]byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\x82\x88matroska"), bytes.Repeat([]byte{0}, 32)...)
	rarBytes = append([]byte("Rar!\x1a\x07\x01\x00"), bytes.Repeat([]byte{0}, 32)...)
)

// TestFileErrorMapping pins the SDK sentinel → wire code map
// (fileError). Sentinels are matched with errors.Is, so wrapped forms
// (how the SDK actually returns them) must map identically.
func TestFileErrorMapping(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{fmt.Errorf("files: offload X: %w", space.ErrFileNotBackedUp), http.StatusConflict, api.ErrFileNotDurable},
		{fmt.Errorf("%w: network advertises no public read base", space.ErrFileNotAvailable), http.StatusConflict, api.ErrFileNotAvailable},
		{space.ErrFileNotAvailable, http.StatusConflict, api.ErrFileNotAvailable},
		{fmt.Errorf("files: variant original A: %w", space.ErrFileVariantInvalid), http.StatusBadRequest, api.ErrFileVariantInvalid},
		{fmt.Errorf("files: attach to X: %w", space.ErrNotFound), http.StatusNotFound, api.ErrFileNotFound},
		{fmt.Errorf("payloads: derive under X: %w", space.ErrObjectDeleted), http.StatusGone, codeObjectDeleted},
		{fmt.Errorf("files: attach to X: %w", space.ErrObjectNotFound), http.StatusNotFound, "object.not_found"},
		{errors.New("something else entirely"), http.StatusInternalServerError, "internal"},
	}
	e := echo.New()
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := fileError(c, tc.err, nil); err != nil {
			t.Fatalf("fileError returned %v", err)
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
		}
		if rec.Code != tc.wantStatus || env.Error.Code != tc.wantCode {
			t.Errorf("fileError(%v) = %d %s, want %d %s", tc.err, rec.Code, env.Error.Code, tc.wantStatus, tc.wantCode)
		}
		if rec.Code == http.StatusInternalServerError && env.Error.Message != "internal error" {
			t.Errorf("fileError(%v) leaks %q", tc.err, env.Error.Message)
		}
	}
}

// doRaw sends a request with a raw (non-JSON) body and content type.
func doRaw(t *testing.T, e http.Handler, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec
}

// TestServer_Files_RoundTrip drives attach → get/list/stats/status →
// download (full + Range) → payload-row query → offload refusal
// against the in-process handler with a real SDK. The binary-level
// twin lives in internal/e2e/files_test.go.
func TestServer_Files_RoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _, objectId := setupSubscribeFixture(t, e)
	filesBase := "/v1/spaces/" + spaceId + "/files"
	attachBase := "/v1/spaces/" + spaceId + "/objects/" + objectId + "/files"

	// Variant pairing is validated before any space work.
	rec := doRaw(t, e, http.MethodPost, attachBase+"?variant=thumb", "", []byte("x"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("half pairing: %d %s", rec.Code, rec.Body.String())
	}

	// Inline attach.
	inlineContent := []byte("inline body")
	rec = doRaw(t, e, http.MethodPost, attachBase+"?name=a.txt", "text/plain", inlineContent)
	if rec.Code != http.StatusCreated {
		t.Fatalf("inline attach: %d %s", rec.Code, rec.Body.String())
	}
	var inlineInfo api.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &inlineInfo); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !inlineInfo.Inline || !inlineInfo.Durable || inlineInfo.Name != "a.txt" || inlineInfo.Mime != "text/plain" {
		t.Errorf("inline info = %+v", inlineInfo)
	}

	// No usable Content-Type (the curl -T / fetch default): the mime is
	// resolved from the content, so the file does not read back as
	// octet-stream for every downstream consumer. Attached without a
	// name extension too, so the stored name gains one.
	attachSniffed := func(query, contentType string, body []byte) api.FileInfo {
		t.Helper()
		rec := doRaw(t, e, http.MethodPost, attachBase+query, contentType, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("attach %s: %d %s", query, rec.Code, rec.Body.String())
		}
		var got api.FileInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}
	pngInfo := attachSniffed("?name=pasted", "application/octet-stream", pngBytes)
	if pngInfo.Mime != "image/png" || pngInfo.Name != "pasted.png" || pngInfo.Size != int64(len(pngBytes)) {
		t.Errorf("sniffed info = %+v, want image/png named pasted.png size %d", pngInfo, len(pngBytes))
	}
	rec = doRaw(t, e, http.MethodGet, filesBase+"/"+pngInfo.FileId+"/content", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Errorf("sniffed content: %d %q len %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	// SVG is the case the stdlib sniffer gets actively wrong (text/xml),
	// which a browser <img> refuses to render — end to end here.
	svgInfo := attachSniffed("?name=logo.svg", "", svgBytes)
	if svgInfo.Mime != "image/svg+xml" || svgInfo.Name != "logo.svg" {
		t.Errorf("svg info = %+v, want image/svg+xml named logo.svg", svgInfo)
	}
	rec = doRaw(t, e, http.MethodGet, filesBase+"/"+svgInfo.FileId+"/content", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("svg content: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	// Content-addressed attach (above the 4096 inline cutoff).
	big := bytes.Repeat([]byte("0123456789abcdef"), 1024) // 16KB
	rec = doRaw(t, e, http.MethodPost, attachBase+"?name=b.bin", "application/x-bin", big)
	if rec.Code != http.StatusCreated {
		t.Fatalf("big attach: %d %s", rec.Code, rec.Body.String())
	}
	var bigInfo api.FileInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &bigInfo); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bigInfo.Inline || bigInfo.RootCid == "" || !bigInfo.Cached {
		t.Errorf("big info = %+v", bigInfo)
	}

	// Get + list + stats + status.
	rec = doJSON(t, e, http.MethodGet, filesBase+"/"+bigInfo.FileId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var list api.FileListResponse
	rec = doJSON(t, e, http.MethodGet, filesBase+"?objectId="+objectId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Files) != 4 {
		t.Errorf("list = %d files, want 4", len(list.Files))
	}
	var stats api.FileStats
	rec = doJSON(t, e, http.MethodGet, filesBase+"/stats", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil || stats.Total != 4 {
		t.Errorf("stats = %+v (err %v), want total 4", stats, err)
	}
	var st api.FileStatus
	rec = doJSON(t, e, http.MethodGet, filesBase+"/"+inlineInfo.FileId+"/status", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.State != api.FileStateDurable {
		t.Errorf("inline status = %+v (err %v), want durable", st, err)
	}

	// Download full.
	rec = doJSON(t, e, http.MethodGet, filesBase+"/"+bigInfo.FileId+"/content", "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), big) {
		t.Fatalf("download: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-bin" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `inline; filename=b.bin` {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// Download a range.
	r := httptest.NewRequest(http.MethodGet, filesBase+"/"+bigInfo.FileId+"/content", nil)
	r.Header.Set("Range", "bytes=16-31")
	rangeRec := httptest.NewRecorder()
	e.ServeHTTP(rangeRec, r)
	if rangeRec.Code != http.StatusPartialContent || !bytes.Equal(rangeRec.Body.Bytes(), big[16:32]) {
		t.Fatalf("range: %d, body %q", rangeRec.Code, rangeRec.Body.String())
	}

	// Unknown fileId → 404 file.not_found.
	rec = doJSON(t, e, http.MethodGet, filesBase+"/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown file: %d %s", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != api.ErrFileNotFound {
		t.Errorf("unknown file code = %+v (err %v)", env, err)
	}

	// Payload-row query bridge.
	rec = doJSON(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/objects/"+objectId+"/files/query",
		`{"includeTotal":true,"filter":{"objectId":"`+objectId+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("files query: %d %s", rec.Code, rec.Body.String())
	}
	var qr api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if qr.Total == nil || *qr.Total != 4 {
		t.Errorf("query total = %v, want 4", qr.Total)
	}

	// No staging fileV2 nodes → the big file is not durable; offload
	// must refuse with 409 file.not_durable.
	rec = doJSON(t, e, http.MethodPost, filesBase+"/"+bigInfo.FileId+"/offload", "")
	if rec.Code == http.StatusNoContent {
		t.Logf("offload succeeded — network has fileV2 nodes; skipping refusal assertion")
	} else if rec.Code != http.StatusConflict {
		t.Fatalf("offload: %d %s", rec.Code, rec.Body.String())
	}
}
