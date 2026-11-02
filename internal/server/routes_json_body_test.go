package server

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

const unsupportedMediaType = "request.unsupported_media_type"

// chunked hides the length from httptest, as a chunked request does.
func chunked(body string) io.Reader {
	return struct{ io.Reader }{strings.NewReader(body)}
}

// An unbooted server is enough here: the check runs ahead of the auth
// guard, so 401 auth.required means the request got past it.
func TestRequireJSONBody(t *testing.T) {
	e := buildEcho(&deps{})
	const body = `{"objectId":"o1","recordIds":["r1"]}`
	const write = "/v1/spaces/s1/delete-records"

	for _, tc := range []struct {
		name, method, path, contentType, body string
		wantStatus                            int
		wantCode                              string
	}{
		{"json", http.MethodPost, write, "application/json", body, 401, "auth.required"},
		{"json with charset", http.MethodPost, write, "application/json; charset=utf-8", body, 401, "auth.required"},
		{"json in another case", http.MethodPost, write, "Application/JSON", body, 401, "auth.required"},

		{"missing", http.MethodPost, write, "", body, 415, unsupportedMediaType},
		{"text", http.MethodPost, write, "text/plain", body, 415, unsupportedMediaType},
		{"form, the curl -d default", http.MethodPost, write, "application/x-www-form-urlencoded", body, 415, unsupportedMediaType},
		{"json as a parameter", http.MethodPost, write, "text/plain; application/json", body, 415, unsupportedMediaType},
		{"another json type", http.MethodPost, write, "application/problem+json", body, 415, unsupportedMediaType},
		{"modify", http.MethodPost, "/v1/spaces/s1/modify", "text/plain", body, 415, unsupportedMediaType},
		{"auth, outside the guard", http.MethodPost, "/v1/auth", "text/plain", `{}`, 415, unsupportedMediaType},
		{"put", http.MethodPut, "/v1/devices/me", "text/plain", body, 415, unsupportedMediaType},
		{"patch", http.MethodPatch, "/v1/spaces/s1", "text/plain", body, 415, unsupportedMediaType},
		{"delete", http.MethodDelete, "/v1/push/token", "text/plain", body, 415, unsupportedMediaType},

		{"no body", http.MethodPost, write, "", "", 401, "auth.required"},
		{"no body, foreign type", http.MethodPost, write, "text/plain", "", 401, "auth.required"},
		{"read with a body", http.MethodGet, "/v1/spaces", "text/plain", body, 401, "auth.required"},

		{"raw: file attach", http.MethodPost, "/v1/spaces/s1/objects/o1/files", "image/jpeg", "bytes", 401, "auth.required"},
		{"raw: avatar", http.MethodPut, "/v1/account/icon", "image/png", "bytes", 401, "auth.required"},
		{"raw: local import", http.MethodPost, "/v1/local/import", "application/gzip", "bytes", 401, "auth.required"},

		{"unmatched route", http.MethodPost, "/v1/nope", "text/plain", body, 404, "request.not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRaw(t, e, tc.method, tc.path, tc.contentType, []byte(tc.body))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := errCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Fatalf("code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestBindBody(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		wantOK           bool
	}{
		{"value", `{"a":"1"}`, "1", true},
		{"unknown key ignored", `{"a":"1","b":2}`, "1", true},
		{"empty", "", "", true},
		{"invalid", `{`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)), rec)
			got, ok := bindBody[struct {
				A string `json:"a"`
			}](c)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v: %s", ok, tc.wantOK, rec.Body.String())
			}
			if !ok {
				if errCode(t, rec.Body.Bytes()) != "request.bad_json" {
					t.Fatalf("%d %s", rec.Code, rec.Body.String())
				}
				return
			}
			if got.A != tc.want {
				t.Fatalf("a = %q, want %q", got.A, tc.want)
			}
		})
	}
}

// Real chunked framing over a socket.
func TestRequireJSONBody_Chunked(t *testing.T) {
	srv := httptest.NewServer(buildEcho(&deps{}))
	defer srv.Close()
	const head = "POST /v1/spaces HTTP/1.1\r\nHost: any\r\nTransfer-Encoding: chunked\r\n"

	for _, tc := range []struct {
		name, request string
		wantStatus    int
	}{
		{"empty, no header", head + "\r\n0\r\n\r\n", 401},
		{"json", head + "Content-Type: application/json\r\n\r\n2\r\n{}\r\n0\r\n\r\n", 401},
		{"text", head + "Content-Type: text/plain\r\n\r\n2\r\n{}\r\n0\r\n\r\n", 415},
		{"text, malformed chunk", head + "Content-Type: text/plain\r\n\r\nzz\r\n", 415},
		{"json, malformed chunk", head + "Content-Type: application/json\r\n\r\nzz\r\n", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

// A typed request passes without the check reading its body, which would
// also send a 100 Continue to a client that asked for one.
func TestRequireJSONBody_LeavesTypedBodyUnread(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/spaces", readFunc(func([]byte) (int, error) {
		t.Error("body read")
		return 0, io.EOF
	}))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	buildEcho(&deps{}).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestHasBody_LeavesChunkedBodyWhole(t *testing.T) {
	for _, want := range []string{"{}", strings.Repeat("x", 3*4096)} {
		r := httptest.NewRequest(http.MethodPost, "/", chunked(want))
		if !hasBody(r) {
			t.Fatalf("hasBody = false for %d bytes", len(want))
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, []byte(want)) {
			t.Fatalf("body after peek: %d bytes, want %d", len(got), len(want))
		}
	}
}

func TestHasBody_NeedsOnlyTheFirstByte(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	go pw.Write([]byte("{}"))
	r := httptest.NewRequest(http.MethodPost, "/", pr)

	done := make(chan bool, 1)
	go func() { done <- hasBody(r) }()
	select {
	case got := <-done:
		if !got {
			t.Fatal("hasBody = false for a body with bytes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hasBody waited for more of the body")
	}
}

// A form body answers 415, so "objectId required" means only a missing
// field.
func TestDeleteRecords_ContentType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	spaceId, _, objectId := setupSubscribeFixture(t, e)
	path := "/v1/spaces/" + spaceId + "/delete-records"
	body := `{"objectId":"` + objectId + `","recordIds":["r1"]}`

	rec := doRaw(t, e, http.MethodPost, path, "application/x-www-form-urlencoded", []byte(body))
	if rec.Code != http.StatusUnsupportedMediaType || errCode(t, rec.Body.Bytes()) != unsupportedMediaType {
		t.Fatalf("form body: %d %s", rec.Code, rec.Body.String())
	}

	rec = doRaw(t, e, http.MethodPost, path, "application/json", []byte(`{}`))
	if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "request.missing_field" {
		t.Fatalf("missing objectId: %d %s", rec.Code, rec.Body.String())
	}

	// dataset.unknown is the write's own refusal of a body with no dataset:
	// the request decoded all the way, however its JSON type was spelled.
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		rec = doRaw(t, e, http.MethodPost, path, contentType, []byte(body))
		if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "dataset.unknown" {
			t.Fatalf("%s: %d %s", contentType, rec.Code, rec.Body.String())
		}
	}

	r := httptest.NewRequest(http.MethodPost, path, chunked(body))
	r.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "dataset.unknown" {
		t.Fatalf("chunked: %d %s", rec.Code, rec.Body.String())
	}
}
