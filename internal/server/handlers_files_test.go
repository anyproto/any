package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

func TestAttachMime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"application/octet-stream", ""},
		{"text/plain; charset=utf-8", "text/plain"},
		{"image/jpeg", "image/jpeg"},
		{"not a mime", ""},
	}
	for _, c := range cases {
		if got := attachMime(c.in); got != c.want {
			t.Errorf("attachMime(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFileErrorMapping pins the string-matched SDK error → wire code
// map (fileError). These stay string-matched until the SDK exports
// sentinels; a wording change upstream should fail here, not silently
// turn a retryable client state back into 500 internal.
func TestFileErrorMapping(t *testing.T) {
	cases := []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{"files: offload X: not backed up — local bytes are the only copy", http.StatusConflict, api.ErrFileNotDurable},
		{"filefetch: content not available: network advertises no public read base", http.StatusConflict, api.ErrFileNotAvailable},
		{"filefetch: content not available: not durable and not local (P2P fetch lands with SYN-24)", http.StatusConflict, api.ErrFileNotAvailable},
		{"files: variant must attach to the original's object A, not B", http.StatusBadRequest, api.ErrFileVariantInvalid},
		{"something else entirely", http.StatusInternalServerError, "internal"},
	}
	e := echo.New()
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := fileError(c, errors.New(tc.err), nil); err != nil {
			t.Fatalf("fileError returned %v", err)
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
		}
		if rec.Code != tc.wantStatus || env.Error.Code != tc.wantCode {
			t.Errorf("fileError(%q) = %d %s, want %d %s", tc.err, rec.Code, env.Error.Code, tc.wantStatus, tc.wantCode)
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
	if len(list.Files) != 2 {
		t.Errorf("list = %d files, want 2", len(list.Files))
	}
	var stats api.FileStats
	rec = doJSON(t, e, http.MethodGet, filesBase+"/stats", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil || stats.Total != 2 {
		t.Errorf("stats = %+v (err %v), want total 2", stats, err)
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
	if qr.Total == nil || *qr.Total != 2 {
		t.Errorf("query total = %v, want 2", qr.Total)
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
