package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const unsupportedMediaType = "request.unsupported_media_type"

func doBody(e http.Handler, method, path, contentType, origin string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec
}

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
		name, method, path, contentType, origin, body string
		wantStatus                                    int
		wantCode                                      string
	}{
		{"json", http.MethodPost, write, "application/json", "", body, 401, "auth.required"},
		{"json with charset", http.MethodPost, write, "application/json; charset=utf-8", "", body, 401, "auth.required"},
		{"json in another case", http.MethodPost, write, "Application/JSON", "", body, 401, "auth.required"},

		{"missing", http.MethodPost, write, "", "", body, 415, unsupportedMediaType},
		{"text", http.MethodPost, write, "text/plain", "", body, 415, unsupportedMediaType},
		{"form, the curl -d default", http.MethodPost, write, "application/x-www-form-urlencoded", "", body, 415, unsupportedMediaType},
		{"json as a parameter", http.MethodPost, write, "text/plain; application/json", "", body, 415, unsupportedMediaType},
		{"another json type", http.MethodPost, write, "application/problem+json", "", body, 415, unsupportedMediaType},
		{"modify", http.MethodPost, "/v1/spaces/s1/modify", "text/plain", "", body, 415, unsupportedMediaType},
		{"auth, outside the guard", http.MethodPost, "/v1/auth", "text/plain", "", `{}`, 415, unsupportedMediaType},
		{"put", http.MethodPut, "/v1/devices/me", "text/plain", "", body, 415, unsupportedMediaType},
		{"foreign origin", http.MethodPost, "/v1/spaces", "text/plain", "https://example.com", `{"name":"x"}`, 415, unsupportedMediaType},

		{"no body", http.MethodPost, write, "", "", "", 401, "auth.required"},
		{"no body, foreign type", http.MethodPost, write, "text/plain", "", "", 401, "auth.required"},
		{"read with a body", http.MethodGet, "/v1/spaces", "text/plain", "", body, 401, "auth.required"},

		{"raw: file attach", http.MethodPost, "/v1/spaces/s1/objects/o1/files", "image/jpeg", "", "bytes", 401, "auth.required"},
		{"raw: avatar", http.MethodPut, "/v1/account/icon", "image/png", "", "bytes", 401, "auth.required"},
		{"raw: local import", http.MethodPost, "/v1/local/import", "application/gzip", "", "bytes", 401, "auth.required"},

		{"unmatched route", http.MethodPost, "/v1/nope", "text/plain", "", body, 404, "request.not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doBody(e, tc.method, tc.path, tc.contentType, tc.origin, strings.NewReader(tc.body))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := errCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Fatalf("code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestRequireJSONBody_Chunked(t *testing.T) {
	e := buildEcho(&deps{})
	for _, tc := range []struct {
		name, contentType, body string
		wantStatus              int
		wantCode                string
	}{
		{"empty, no header", "", "", 401, "auth.required"},
		{"text", "text/plain", `{}`, 415, unsupportedMediaType},
		{"json", "application/json", `{}`, 401, "auth.required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doBody(e, http.MethodPost, "/v1/spaces", tc.contentType, "", chunked(tc.body))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := errCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Fatalf("code = %q, want %q", got, tc.wantCode)
			}
		})
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

	rec := doBody(e, http.MethodPost, path, "application/x-www-form-urlencoded", "", strings.NewReader(body))
	if rec.Code != http.StatusUnsupportedMediaType || errCode(t, rec.Body.Bytes()) != unsupportedMediaType {
		t.Fatalf("form body: %d %s", rec.Code, rec.Body.String())
	}

	rec = doBody(e, http.MethodPost, path, "application/json", "", strings.NewReader(`{}`))
	if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "request.missing_field" {
		t.Fatalf("missing objectId: %d %s", rec.Code, rec.Body.String())
	}

	// dataset.unknown is the write's own refusal of a body with no dataset:
	// the request decoded all the way, however its JSON type was spelled.
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		rec = doBody(e, http.MethodPost, path, contentType, "", strings.NewReader(body))
		if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "dataset.unknown" {
			t.Fatalf("%s: %d %s", contentType, rec.Code, rec.Body.String())
		}
	}
	rec = doBody(e, http.MethodPost, path, "application/json", "", chunked(body))
	if rec.Code != http.StatusBadRequest || errCode(t, rec.Body.Bytes()) != "dataset.unknown" {
		t.Fatalf("chunked: %d %s", rec.Code, rec.Body.String())
	}
}
